package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// P3 — PER-GOAL wall-clock cost ceiling. The budget epoch now lives on
// goalRuntime.activatedAt (stamped at dispatch), so each in-flight goal is
// measured against the still-daemon-global maxWallClock from ITS own dispatch.
// All tests inject P2's clock seam (d.clock, read via d.now()) so
// elapsed = now()-rt.activatedAt is deterministic with zero sleeps. A budget
// breach FAILS the offending goal (GoalFailed + cascade + advanceToNextGoal),
// it does NOT deactivate the whole daemon — at MaxGoals=1 the sole goal's
// advance reaches deactivateOnCompletion so the daemon still ends idle.

// fixedClock returns a clock func pinned to t (no auto-advance), so activatedAt
// (set separately) and the tick comparison read a single controlled instant.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

// writeSettingsWithWallClock writes a minimal setting.yaml carrying an explicit
// taskvisor.max_wall_clock_sec so Run()'s settings-load maps it onto d.maxWallClock.
func writeSettingsWithWallClock(t *testing.T, dir string, wallClockSec int) {
	t.Helper()
	content := fmt.Sprintf(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
  max_workers: 4
  cycle_delay: 5
  unplanned_audit: true
plan:
  auto_approve: true
  auto_execute: true
sudo:
  timeout: 30
taskvisor:
  dispatch_timeout: 3600
  validate_timeout: 300
  poll_interval: 0
  max_wall_clock_sec: %d
`, wallClockSec)
	p := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// TestNew_SeedsMaxWallClockDefault pins the Option C fix: New() seeds the 4h
// ceiling (mirroring how progressTimeout is seeded to 5m), so an install whose
// legacy setting.yaml omits max_wall_clock_sec (loading MaxWallClockSec==0, no
// Run() override) still gets an ACTIVE ceiling instead of a silently disabled one.
func TestNew_SeedsMaxWallClockDefault(t *testing.T) {
	d := New(t.TempDir(), new(testutil.MockTmuxExecutor))
	assert.Equal(t, 4*time.Hour, d.maxWallClock,
		"New() must seed maxWallClock to the 4h default so an absent max_wall_clock_sec "+
			"key in a legacy setting.yaml still yields an active ceiling (P3 fix)")
}

// TestRun_LegacySettingMissingWallClock_CeilingActive is the acceptance test for
// the P3 legacy-backfill gap. A setting.yaml that predates the max_wall_clock_sec
// key loads MaxWallClockSec==0, so Run()'s `if >0` override is skipped — but
// Option C's New() seed of 4h must STAND, leaving the ceiling ACTIVE (not silently
// disabled to 0). We build the daemon via New() (NOT setupDaemon, which opts out by
// zeroing maxWallClock) and drive the real Run() settings-load. The single goal is
// already GoalDone so Run() activates then idles immediately (no dispatch needed,
// deterministic), and we assert the 4h seed survived the settings-load — the
// concrete proof the ceiling is active. (The per-goal HALT mechanism itself is
// exercised by the tick-level tests below, which need no fragile Run() dispatch.)
func TestRun_LegacySettingMissingWallClock_CeilingActive(t *testing.T) {
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	exec.On("ClosePipePane", mock.Anything, mock.Anything).Return(nil).Maybe()
	d := New(dir, exec)
	d.pollInterval = 10 * time.Millisecond
	d.promptSettleDelay = 0
	d.promptPollInterval = 0

	// Legacy setting.yaml: writeSettings emits NO max_wall_clock_sec /
	// progress_timeout_sec / integration_cmd keys — the exact pre-P3 file shape.
	writeSettings(t, dir, true, true)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalDone}},
	})
	writeStartSignal(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@9", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", testSession, "@9", mock.Anything).Return(nil).Maybe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, d.Run(ctx))

	assert.Equal(t, 4*time.Hour, d.maxWallClock,
		"Run()'s settings-load must leave New()'s 4h seed standing when max_wall_clock_sec "+
			"is absent — the ceiling stays ACTIVE (not disabled to 0)")
	assert.Equal(t, modeIdle, d.mode, "daemon idles after the lone done goal resolves")
}

// TestTick_WallClockBudgetExhausted_Halts: a sole in-flight goal whose PER-GOAL
// epoch is over budget is FAILED (not the daemon halted), then advance reaches
// deactivateOnCompletion so the daemon ends idle (the MaxGoals=1 end-state).
func TestTick_WallClockBudgetExhausted_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(90 * time.Minute)) // 90m elapsed >= 1h budget

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	// Per-goal budget epoch on the RUNNING goal's runtime (phaseNone ⇒ checkProgress
	// is a no-op so the gate is what fires).
	d.runtime("goal-001").activatedAt = base
	// Halt → advanceToNextGoal → deactivateOnCompletion (sole goal): teardown +
	// notifyCompletion + ensureWindow0Supervisor.
	setupDeactivateMocks(exec, testSession, "@9")

	var tickErr error
	logOut := captureLog(t, func() {
		tickErr = d.tick(context.Background(), gf)
	})
	require.NoError(t, tickErr)

	assert.Equal(t, modeIdle, d.mode, "sole over-budget goal halts → advance → daemon idle")
	// Per-goal halt: the OFFENDING goal is failed (not a daemon-level no-touch halt).
	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "the over-budget goal must be failed")
	assert.NotEmpty(t, gf.Goals[0].FinishedAt, "failed goal FinishedAt must be stamped")
	assert.Equal(t, "wall-clock-budget", gf.Goals[0].FailedBy, "failure reason must be scoped to the budget")
	// The wall-clock path no longer writes the daemon-level haltReason banner.
	assert.Empty(t, d.haltReason, "per-goal wall-clock halt must NOT set the daemon haltReason")
	// No dispatch happened — the gate preempts the dispatch loop.
	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:")
	}))
	assert.Contains(t, logOut, "ALARM: wall-clock budget exhausted", "loud alarm must be logged")
}

// TestTick_WallClockUnderBudget_NoOp: a RUNNING goal under its per-goal budget is
// left running and the daemon stays active.
func TestTick_WallClockUnderBudget_NoOp(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = 4 * time.Hour
	d.clock = fixedClock(base.Add(10 * time.Minute)) // 10m elapsed << 4h budget

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	d.runtime("goal-001").activatedAt = base

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "under budget: daemon stays active")
	assert.Empty(t, d.haltReason, "under budget: no halt")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "under budget: running goal untouched")
}

// TestTick_WallClockZeroDisabled_NoOp: maxWallClock==0 disables the gate entirely,
// so a pending goal dispatches normally even with the clock arbitrarily far.
func TestTick_WallClockZeroDisabled_NoOp(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	// setupDaemon already zeroes maxWallClock; this explicit set documents the intent
	// of THIS disabled-case test (New() otherwise seeds 4h).
	d.maxWallClock = 0                              // DISABLED
	d.clock = fixedClock(base.Add(100 * time.Hour)) // arbitrarily far

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "zero budget disables the gate — no halt")
	assert.Empty(t, d.haltReason, "zero budget: no halt")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "zero budget: dispatch proceeds byte-identically")
}

// TestTick_WallClockExactBoundary_Halts: elapsed == budget halts (>= inclusive).
func TestTick_WallClockExactBoundary_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(time.Hour)) // elapsed == budget → >= is inclusive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	d.runtime("goal-001").activatedAt = base
	setupDeactivateMocks(exec, testSession, "@9")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "elapsed == budget must halt (>= boundary inclusive)")
	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "boundary breach fails the goal")
}

// TestTick_WallClockBudgetExhausted_AllSlotsBusy_Halts: the gate precedes the
// `if free > 0` dispatch block, so a saturated daemon (free==0) still halts.
func TestTick_WallClockBudgetExhausted_AllSlotsBusy_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(90 * time.Minute)) // over budget

	// A goal already RUNNING (phase phaseNone ⇒ checkProgress is a no-op) means
	// free == maxGoals - 1 == 0. The gate precedes the `if free > 0` block, so the
	// halt must still fire even though no dispatch slot is free.
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "running", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	d.runtime("goal-001").activatedAt = base
	setupDeactivateMocks(exec, testSession, "@9")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "halt must fire even when all slots are busy (free==0)")
	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "the saturated daemon's over-budget goal is failed")
}

// TestTick_PerGoalWallClock_IndependentHalt (NEW): two in-flight goals each with
// its OWN epoch — only the over-budget one is failed; the under-budget sibling
// keeps running and the daemon stays active (the core per-goal-budget guarantee).
func TestTick_PerGoalWallClock_IndependentHalt(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeSettingsMaxGoals(t, dir, 2)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(90 * time.Minute))

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "over budget", Status: GoalRunning},
			{ID: "goal-B", Description: "under budget", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	// goal-A dispatched at base (90m elapsed >= 1h); goal-B dispatched 80m later
	// (only 10m elapsed << 1h) — each measured from ITS own epoch.
	d.runtime("goal-A").activatedAt = base
	d.runtime("goal-B").activatedAt = base.Add(80 * time.Minute)

	// notifySupervisor (GOAL-FAILED for goal-A) resolves the bare supervisor window;
	// no deactivate fires because goal-B keeps the daemon active.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@9", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", testSession, "@9", mock.Anything).Return(nil).Maybe()

	var tickErr error
	logOut := captureLog(t, func() {
		tickErr = d.tick(context.Background(), gf)
	})
	require.NoError(t, tickErr)

	gA, _ := gf.GoalByID("goal-A")
	gB, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalFailed, gA.Status, "the over-budget goal-A must be failed")
	assert.Equal(t, "wall-clock-budget", gA.FailedBy, "goal-A failure scoped to the budget")
	assert.Equal(t, GoalRunning, gB.Status, "the under-budget goal-B must keep running")
	assert.Equal(t, modeActive, d.mode, "a sibling still in flight keeps the daemon active")
	assert.Contains(t, logOut, "ALARM: wall-clock budget exhausted", "alarm logged for the offender")
	assert.Contains(t, logOut, "goal-A", "alarm names the offending goal")
}

// TestTick_SequentialGoals_FreshBudget (NEW): goal-001 already done (its runtime
// cleared); goal-002 dispatched fresh at base gets a FULL budget from ITS epoch,
// proving it is not charged for goal-001's prior wall time.
func TestTick_SequentialGoals_FreshBudget(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = 4 * time.Hour
	// Only 1h elapsed against goal-002's OWN epoch, even though wall-clock-wise a
	// prior goal-001 might have run ~3h before goal-002 was dispatched.
	d.clock = fixedClock(base.Add(1 * time.Hour))

	gf := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{ID: "goal-001", Description: "earlier, done", Status: GoalDone},
			{ID: "goal-002", Description: "dispatched fresh", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	d.runtime("goal-002").activatedAt = base // goal-001's runtime is gone (cleared on its exit)

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	g2, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalRunning, g2.Status, "goal-002 gets a full budget from its own epoch — not halted")
	assert.Equal(t, modeActive, d.mode, "daemon stays active driving goal-002")
}

// TestTick_NeverStampedEpoch_NoHalt (NEW): a running goal whose per-goal epoch was
// never stamped (rt.activatedAt zero) is skipped by the IsZero() guard and never
// halts, even with the clock far past any budget.
func TestTick_NeverStampedEpoch_NoHalt(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(100 * time.Hour)) // far past, but epoch is zero

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "no epoch", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	// Deliberately do NOT stamp d.runtime("goal-001").activatedAt — it stays zero.

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "never-stamped epoch never halts (IsZero guard)")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "running goal preserved")
}

func TestActivate_StampsActivatedAt(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeSettings(t, dir, true, true)

	fixed := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC)
	d.clock = fixedClock(fixed)
	d.haltReason = "stale-from-prior-run" // activate must clear this

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.activate(gf))

	// d.activatedAt is now ONLY the ALL-COMPLETE `wall=` run-total diagnostic stamp
	// (the budget epoch moved per-goal), but activate() still stamps it via the seam.
	assert.Equal(t, fixed, d.activatedAt, "activate() must stamp activatedAt via the clock seam")
	assert.Empty(t, d.haltReason, "activate() must clear haltReason for a clean (re)start surface")
}

// TestTick_WallClockBudget_DispatchToHalt drives the full per-goal lifecycle at the
// tick boundary (the deterministic codebase idiom, cf. DispatchTimeout_FullLifecycle):
// tick 1 dispatches goal-001 (stamping its per-goal epoch at dispatch), then with the
// clock advanced past budget tick 2 fails the goal, logs the ALARM, advances to no
// next goal → deactivateOnCompletion → idle with the guard file removed.
func TestTick_WallClockBudget_DispatchToHalt(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// Tick 1: dispatch. The per-goal epoch is stamped at dispatch via the clock seam.
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(context.Background(), gf))
	require.Equal(t, GoalRunning, gf.Goals[0].Status, "tick 1 dispatches the goal")
	require.Equal(t, base, d.runtime("goal-001").activatedAt, "dispatch stamps the per-goal budget epoch")

	// Drop the runtime to phaseNone so tick 2's checkProgress is a no-op (the
	// documented running-goal idiom, cf. AllSlotsBusy_Halts) and the wall-clock GATE
	// is unambiguously what fires — not a window-presence/dispatch-timeout failure.
	// The per-goal epoch (activatedAt=base) is preserved across the phase change.
	d.runtime("goal-001").phase = phaseNone

	// Advance the clock past budget (90m >= 1h) and re-arm the mock for the halt path.
	d.clock = fixedClock(base.Add(90 * time.Minute))
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ClosePipePane", mock.Anything, mock.Anything).Return(nil).Maybe()
	setupDeactivateMocks(exec, testSession, "@9")

	// Tick 2: the per-goal gate fires → goal failed → advance → daemon idle.
	var tickErr error
	logOut := captureLog(t, func() {
		tickErr = d.tick(context.Background(), gf)
	})
	require.NoError(t, tickErr)

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "over-budget goal is failed")
	assert.Equal(t, "wall-clock-budget", gf.Goals[0].FailedBy, "failure scoped to the budget")
	assert.NotEmpty(t, gf.Goals[0].FinishedAt, "failed goal FinishedAt stamped")
	assert.Equal(t, modeIdle, d.mode, "sole goal failed → advance → deactivateOnCompletion → idle")
	assert.Contains(t, logOut, "ALARM: wall-clock budget exhausted", "alarm line logged")

	_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-active"))
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed on idle teardown")
}

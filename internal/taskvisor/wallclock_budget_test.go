package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// P3 — wall-clock cost ceiling. All tests inject P2's clock seam (d.clock, read
// via d.now()) so elapsed = now()-activatedAt is deterministic with zero sleeps.

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

// TestRun_LegacySettingMissingWallClock_CeilingActive is the end-to-end acceptance
// test for the P3 legacy-backfill gap. A setting.yaml that predates the
// max_wall_clock_sec key loads MaxWallClockSec==0, so Run()'s `if >0` override is
// skipped — but Option C's New() seed of 4h must stand, leaving the ceiling ACTIVE.
// We deliberately build the daemon via New() (NOT setupDaemon, which opts out by
// zeroing maxWallClock) so the real seed is exercised through Run()'s settings-load.
// The injected clock jumps 5h per read (> the 4h seed), so the first post-activation
// tick is over budget and must halt — proving the ceiling is active, not disabled.
func TestRun_LegacySettingMissingWallClock_CeilingActive(t *testing.T) {
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	d := New(dir, exec)
	d.pollInterval = 10 * time.Millisecond
	d.promptSettleDelay = 0
	d.promptPollInterval = 0

	// Legacy setting.yaml: writeSettings emits NO max_wall_clock_sec /
	// progress_timeout_sec / integration_cmd keys — the exact pre-P3 file shape.
	writeSettings(t, dir, true, true)
	writeGoals(t, dir, &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	})
	writeStartSignal(t, dir)

	// Each now() jumps 5h (> the 4h seed) so the first tick after activation is over budget.
	d.clock = autoClock(5 * time.Hour)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@9", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, d.Run(ctx))

	assert.Equal(t, modeIdle, d.mode,
		"absent max_wall_clock_sec must still yield an ACTIVE 4h ceiling (Option C seed), "+
			"so the over-budget clock halts the daemon")
	assert.Contains(t, d.haltReason, "wall-clock budget")
	assert.Equal(t, 4*time.Hour, d.maxWallClock,
		"Run()'s settings-load must leave New()'s 4h seed standing when the key is absent")
}

func TestTick_WallClockBudgetExhausted_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.activatedAt = base
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(90 * time.Minute)) // 90m elapsed >= 1h budget

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	// Halt calls deactivate() directly (NOT deactivateOnCompletion): teardown +
	// ensureWindow0Supervisor (finds the bare "supervisor", no recreate).
	setupDeactivateMocks(exec, testSession, "@9")

	var tickErr error
	logOut := captureLog(t, func() {
		tickErr = d.tick(context.Background(), gf)
	})
	require.NoError(t, tickErr)

	assert.Equal(t, modeIdle, d.mode, "daemon should go idle after wall-clock halt")
	assert.Contains(t, d.haltReason, "wall-clock budget", "haltReason should explain the halt")
	// Daemon-level halt: goal status/timestamps untouched so a human can resume.
	assert.Equal(t, GoalPending, gf.Goals[0].Status, "goal status must NOT be failed on a daemon-level halt")
	assert.Empty(t, gf.Goals[0].FinishedAt, "goal FinishedAt must be untouched")
	// No dispatch happened — the guard preempts the dispatch loop.
	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
	assert.Contains(t, logOut, "ALARM: wall-clock budget exhausted", "loud alarm must be logged")
}

func TestTick_WallClockUnderBudget_NoOp(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.activatedAt = base
	d.maxWallClock = 4 * time.Hour
	d.clock = fixedClock(base.Add(10 * time.Minute)) // 10m elapsed << 4h budget

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

	assert.Equal(t, modeActive, d.mode, "under budget: daemon stays active")
	assert.Empty(t, d.haltReason, "under budget: no halt reason")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "under budget: dispatch proceeds byte-identically")
}

func TestTick_WallClockZeroDisabled_NoOp(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.activatedAt = base
	// Option C opt-out: New() now seeds maxWallClock=4h, so a test that wants the
	// ceiling DISABLED sets d.maxWallClock=0 explicitly after New() — exactly how
	// the heartbeat tests opt out of progressTimeout. (setupDaemon also zeroes it by
	// default; this explicit set documents the intent of THIS disabled-case test.)
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

	assert.Equal(t, modeActive, d.mode, "zero budget disables the guard — no halt")
	assert.Empty(t, d.haltReason, "zero budget: no halt reason")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "zero budget: dispatch proceeds byte-identically")
}

func TestTick_WallClockExactBoundary_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.activatedAt = base
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(time.Hour)) // elapsed == budget → >= is inclusive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	setupDeactivateMocks(exec, testSession, "@9")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "elapsed == budget must halt (>= boundary inclusive)")
	assert.Contains(t, d.haltReason, "wall-clock budget")
}

func TestTick_WallClockBudgetExhausted_AllSlotsBusy_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	d.activatedAt = base
	d.maxWallClock = time.Hour
	d.clock = fixedClock(base.Add(90 * time.Minute)) // over budget

	// A goal already RUNNING (phase phaseNone ⇒ checkProgress is a no-op) means
	// free == maxGoals - 1 == 0. The guard precedes the `if free > 0` block, so
	// the halt must still fire even though no dispatch slot is free.
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "running", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	setupDeactivateMocks(exec, testSession, "@9")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "halt must fire even when all slots are busy (free==0)")
	assert.Contains(t, d.haltReason, "wall-clock budget")
	// The running goal's signal was already polled this tick by checkProgress; the
	// halt is daemon-level and does NOT touch the running goal's status.
	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "running goal status untouched by daemon-level halt")
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

	assert.Equal(t, fixed, d.activatedAt, "activate() must stamp activatedAt via the clock seam")
	assert.Empty(t, d.haltReason, "activate() must clear haltReason for a clean (re)start surface")
}

// TestRun_WallClockBudget_Integration drives the full Run() loop: a tiny
// MaxWallClockSec and an injected clock that jumps past budget halts the daemon,
// logs the alarm to taskvisor.log, restores the idle supervisor window, and
// removes the guard file.
func TestRun_WallClockBudget_Integration(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.pollInterval = 10 * time.Millisecond

	// max_wall_clock_sec: 1 (1s budget); the injected clock jumps an hour per read,
	// so the very first tick after activation is already over budget.
	writeSettingsWithWallClock(t, dir, 1)
	writeGoals(t, dir, &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	})
	writeStartSignal(t, dir)

	d.clock = autoClock(time.Hour)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	// Activation window sweep + deactivate teardown + ensureWindow0Supervisor: an
	// unbounded empty list satisfies kills/awaits; the trailing return supplies the
	// bare "supervisor" so ensureWindow0Supervisor finds window-0 and does not recreate.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@9", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, d.Run(ctx))

	assert.Equal(t, modeIdle, d.mode, "daemon should halt to idle on wall-clock budget")
	assert.Contains(t, d.haltReason, "wall-clock budget")

	logData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "logs", "taskvisor.log"))
	require.NoError(t, err)
	assert.Contains(t, string(logData), "ALARM: wall-clock budget exhausted", "alarm line must reach taskvisor.log")

	_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-active"))
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed on halt")
}

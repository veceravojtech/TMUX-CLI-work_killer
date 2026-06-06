package taskvisor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeClock is a controllable time source for the P2 heartbeat tests: inject
// d.clock = clk.now and advance() past d.progressTimeout to drive the heartbeat
// deterministically without real sleeps.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// statemachine_test.go — Fix 2 (goal-061 post-mortem): salvage late-arriving
// validator verdicts after a timeout-synthesized failure.
//
// Incident shape: the daemon's validate timeout fired (rerunValidationOnly,
// valSig == nil) consuming the last ValidationRetries → goal failed + cascade,
// while the REAL pass verdict arrived minutes later via goal-validation-done
// and was silently discarded (tick only drives GoalRunning goals).
//
// The fix is two-sided:
//   - rerunValidationOnly marks a timeout-SYNTHESIZED exhaustion durably
//     (Goal.FailedBy = "validation-timeout"), but ONLY when no verdict ever
//     arrived (valSig == nil) AND the work is in the base tree (runtime
//     WorktreeDir == "" — the halt path discards worktrees, so a late pass for
//     discarded work must never flip to done).
//   - salvageLateVerdicts (top of tick) keeps polling signal.json for marked
//     goals: a late PASS flips failed→done (ReconcileBlocks then un-sticks the
//     cascade-blocked dependents the same tick); any other verdict clears the
//     marker and the failure stands.

// --- 2b: FailedBy marker at the exhausted-timeout seam ----------------------

// TestRerunValidationOnly_TimeoutExhausted_SetsFailedByMarker — the timeout
// watchdog route (valSig == nil) exhausting the last ValidationRetries with the
// work in the base tree (no worktree) marks the failure as timeout-synthesized,
// and the marker is persisted to goals.yaml.
func TestRerunValidationOnly_TimeoutExhausted_SetsFailedByMarker(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 2, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, nil))

	assert.Equal(t, GoalFailed, goal.Status, "exhausted validation budget hard-halts")
	assert.Equal(t, "validation-timeout", goal.FailedBy,
		"timeout-synthesized exhaustion must be marked for the salvage scan")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "validation-timeout", rg.FailedBy, "marker persisted to goals.yaml")
}

// TestRerunValidationOnly_TimeoutExhausted_WorktreeDiscarded_NoMarker — same
// timeout route, but the goal ran in a per-goal worktree: the halt path discards
// the worktree, so a late pass would bless work that no longer exists in base.
// The marker must NOT be set.
func TestRerunValidationOnly_TimeoutExhausted_WorktreeDiscarded_NoMarker(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").WorktreeDir = filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 2, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, nil))

	assert.Equal(t, GoalFailed, goal.Status)
	assert.Equal(t, "", goal.FailedBy,
		"worktree-discarded work must NOT be salvage-eligible — late pass would bless discarded changes")
}

// TestRerunValidationOnly_ErrorVerdictExhausted_NoMarker — the error-VERDICT
// route (valSig != nil) already has its verdict in hand; nothing late is
// pending, so exhaustion there must not be marked.
func TestRerunValidationOnly_ErrorVerdictExhausted_NoMarker(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 2, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	valSig := &ValidatorSignal{
		Verdict: VerdictError, Owner: "ops",
		Findings: []ValidationFinding{{
			Rule: "validate-script", Status: VerdictError, FailureClass: "validator-error", Owner: "ops",
			Detail: "validator crashed",
		}},
		Timestamp: "2026-06-04T12:00:00Z",
	}

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, valSig))

	assert.Equal(t, GoalFailed, goal.Status)
	assert.Equal(t, "", goal.FailedBy,
		"error-verdict exhaustion has its verdict — no late verdict to salvage, no marker")
}

// --- 2c: salvage scan at the top of tick ------------------------------------

// TestTick_SalvagesLatePassVerdict_UnblocksDependentsSameTick — a GoalFailed
// goal marked validation-timeout with a late PASS signal.json on disk flips
// failed→done in one tick: signal deleted, marker cleared, FinishedAt restamped,
// and the cascade-blocked dependent is un-stuck by ReconcileBlocks and
// dispatched the SAME tick (cascade reversal is free — no new reversal code).
func TestTick_SalvagesLatePassVerdict_UnblocksDependentsSameTick(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed blocker", Status: GoalFailed,
				FailedBy:  "validation-timeout",
				StartedAt: "2026-06-04T14:00:00Z", FinishedAt: "2026-06-04T14:27:24Z"},
			{ID: "goal-002", Description: "cascade-blocked dependent", Status: GoalBlocked,
				BlockedBy: "goal-001", DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-002")
	require.NoError(t, err)

	// The late pass verdict, exactly as goal-validation-done writes it.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass,
		Findings: []ValidationFinding{{
			Rule: "go-test", Status: VerdictPass, Detail: "all green",
		}},
		Timestamp: "2026-06-04T14:33:15Z",
	}))

	// Salvage's killWindowByName(validator) consumes one ListWindows before the
	// dispatch mock sequence for the re-pended dependent.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupDispatchMocks(exec, testSession, "@0", "supervisor-002")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.tick(context.Background(), gf))

	blocker, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, blocker.Status, "late pass verdict flips failed -> done")
	assert.Equal(t, "", blocker.FailedBy, "marker cleared after salvage")

	dep, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, dep.Status, "dependent un-blocked and dispatched the same tick")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	assert.Nil(t, sig, "salvaged signal.json deleted")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rb, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, rb.Status, "salvage persisted to goals.yaml")
	assert.Equal(t, "", rb.FailedBy, "marker clear persisted")
}

// TestTick_SalvageLateFailVerdict_FailureStands — a late non-pass verdict
// settles the question the other way: the failure stands, the marker is cleared
// (stop watching), and the stale signal is deleted.
func TestTick_SalvageLateFailVerdict_FailureStands(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed", Status: GoalFailed,
				FailedBy: "validation-timeout", FinishedAt: "2026-06-04T14:27:24Z"},
			// A live runner keeps the tick from tearing down the daemon (its
			// lazily-created runtime is phaseNone, so checkProgress no-ops).
			{ID: "goal-002", Description: "live sibling", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictFail,
		Findings: []ValidationFinding{{
			Rule: "unit-tests", Status: VerdictFail, FailureClass: "code-defect", Detail: "still red",
		}},
		Timestamp: "2026-06-04T14:33:15Z",
	}))
	noWindows(exec)

	require.NoError(t, d.tick(context.Background(), gf))

	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "late fail verdict confirms the failure")
	assert.Equal(t, "", g.FailedBy, "marker cleared — verdict arrived, stop watching")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	assert.Nil(t, sig, "stale signal deleted")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "", rg.FailedBy, "marker clear persisted")
}

// TestTick_SalvageNoSignal_KeepsWatching — no signal.json yet: the goal is left
// untouched and the marker retained, so the scan keeps polling on later ticks.
func TestTick_SalvageNoSignal_KeepsWatching(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed", Status: GoalFailed,
				FailedBy: "validation-timeout", FinishedAt: "2026-06-04T14:27:24Z"},
			{ID: "goal-002", Description: "live sibling", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)

	require.NoError(t, d.tick(context.Background(), gf))

	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "no verdict — failure unchanged")
	assert.Equal(t, "validation-timeout", g.FailedBy, "marker retained — keep watching")
}

// =============================================================================
// P2 — progress heartbeat + injectable clock seam
//
// A wedged LLM is invisible until the 1h hard timeout because checkStall
// self-disables while a worker runs. The heartbeat hashes the goal's pane each
// tick: a static digest for >= progressTimeout while the window is still the
// agent fires early recovery — supervising re-dispatches (charging the error/ops
// ValidationRetries budget, no code/spec burn), validating routes through the
// existing rerunValidationOnly. The heartbeat runs BEFORE the hard-timeout
// compare and is disabled when progressTimeout<=0 (byte-identical legacy harness).
// =============================================================================

const heartbeatWindowID = "@1"

// TestSupervisingHeartbeat_WedgedFiresAtThreshold — a supervisor window whose
// pane is static past progressTimeout (while still the agent) is recovered:
// goal windows killed, ValidationRetries decremented (NOT Code/Spec), and the
// cycle re-dispatched via dispatchRetry (ships "/tmux:supervisor <id>"). STUCK logged.
func TestSupervisingHeartbeat_WedgedFiresAtThreshold(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.dispatchTimeout = time.Hour

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	// A per-goal tasks.yaml routes the recovery through dispatchRetry (reuse), so
	// the re-dispatch ships "/tmux:supervisor <id>" rather than a full plan.
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")

	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = clk.t // recent — hard timeout will NOT fire (proves additive)
	// Pre-seed the heartbeat so a single tick fires: the captured pane hashes to
	// the stored digest (no change) and lastProgressAt is older than progressTimeout.
	rt.lastProgressHash = hashPane("WEDGED")
	rt.lastProgressAt = clk.t
	clk.advance(6 * time.Minute)

	sup := []tmux.WindowInfo{{TmuxWindowID: heartbeatWindowID, Name: "supervisor-001", CurrentCommand: "claude"}}
	empty := []tmux.WindowInfo{}
	// heartbeat find + stuck-kill prefix + stuck-kill byname (window present, killed)
	exec.On("ListWindows", testSession).Return(sup, nil).Times(3)
	// dispatchRetry's 4 kill lookups + collectManagedNames + waitWindowsGone (gone)
	exec.On("ListWindows", testSession).Return(empty, nil).Times(6)
	// waitClaudeBoot + waitForPrompt on the freshly re-created supervisor window
	exec.On("ListWindows", testSession).Return(sup, nil)
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("WEDGED", nil).Once()
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("ready ❯", nil)
	exec.On("KillWindow", testSession, heartbeatWindowID).Return(nil)
	var sent []string
	exec.On("SendMessage", testSession, heartbeatWindowID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		sent = append(sent, args.String(2))
	})
	d.SetWindowCreateFunc(mockCreateWindowFn(heartbeatWindowID))

	out := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	g := &gf.Goals[0]
	assert.Equal(t, GoalRunning, g.Status, "stuck supervisor is re-dispatched")
	assert.Equal(t, 2, g.StuckRetries, "stuck budget charged 3->2")
	assert.Equal(t, 2, g.ValidationRetries, "validation budget untouched (stuck charges stuck budget)")
	assert.Equal(t, 2, g.CodeRetries, "code budget untouched")
	assert.Equal(t, 2, g.SpecRetries, "spec budget untouched")
	assert.Contains(t, out, "STUCK", "loud STUCK line logged")
	require.Len(t, sent, 1)
	assert.Equal(t, "/tmux:supervisor goal-001", sent[0], "re-dispatch via dispatchRetry reuse path")
	exec.AssertCalled(t, "KillWindow", testSession, heartbeatWindowID)
}

// TestSupervisingHeartbeat_ResetsOnProgress — a supervisor pane whose output
// changes every tick never fires the heartbeat even past progressTimeout;
// lastProgressAt tracks the latest change.
func TestSupervisingHeartbeat_ResetsOnProgress(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.dispatchTimeout = time.Hour

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = clk.t

	sup := []tmux.WindowInfo{{TmuxWindowID: heartbeatWindowID, Name: "supervisor-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(sup, nil)
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("line-1", nil).Once()
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("line-2", nil).Once()
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("line-3", nil)

	for i := 0; i < 3; i++ {
		clk.advance(6 * time.Minute) // each gap exceeds progressTimeout
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
		assert.Equal(t, GoalRunning, gf.Goals[0].Status, "changing pane never fires the heartbeat")
	}
	assert.Equal(t, 2, gf.Goals[0].ValidationRetries, "no budget charged while making progress")
	assert.True(t, rt.lastProgressAt.Equal(clk.t), "lastProgressAt tracks the latest pane change")
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

// TestSupervisingHeartbeat_ExhaustionCascades — when the wedged supervisor's
// StuckRetries hits zero, the goal hard-fails and its dependents cascade
// blocked; an independent goal stays pending and Code/Spec/Validation budgets
// are untouched.
func TestSupervisingHeartbeat_ExhaustionCascades(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.dispatchTimeout = time.Hour

	blocker := routeGoal("goal-001", 2, 2, 1, 0)
	blocker.StuckRetries = 1 // next charge exhausts
	blocker.MaxStuckRetries = 3
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		blocker,
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = clk.t
	rt.lastProgressHash = hashPane("WEDGED")
	rt.lastProgressAt = clk.t
	clk.advance(6 * time.Minute)

	sup := []tmux.WindowInfo{{TmuxWindowID: heartbeatWindowID, Name: "supervisor-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(sup, nil)
	exec.On("CaptureWindowOutput", testSession, heartbeatWindowID).Return("WEDGED", nil)
	exec.On("KillWindow", testSession, heartbeatWindowID).Return(nil)

	out := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	g, _ := gf.GoalByID("goal-001")
	assert.Equal(t, GoalFailed, g.Status, "exhausted stuck budget hard-fails the goal")
	assert.Equal(t, 0, g.StuckRetries, "stuck budget charged 1->0")
	assert.Equal(t, 1, g.ValidationRetries, "validation budget untouched")
	assert.Equal(t, 2, g.CodeRetries, "code budget untouched on exhaustion")
	dep, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalBlocked, dep.Status, "dependent cascaded blocked")
	indep, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalPending, indep.Status, "independent goal unaffected")
	assert.Contains(t, out, "STUCK")
}

// TestValidatingHeartbeat_WedgedRoutesRerunValidationOnly — a validator pane
// static past progressTimeout (before the validate hard timeout) routes through
// rerunValidationOnly: validator re-created, ValidationRetries decremented, and
// NO Code/Spec retry charged.
func TestValidatingHeartbeat_WedgedRoutesRerunValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.validateTimeout = time.Hour // hard timeout would NOT fire — proves heartbeat is additive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 3, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	rt.phase = phaseValidating
	rt.validateTime = clk.t
	rt.lastProgressHash = hashPane("WEDGED")
	rt.lastProgressAt = clk.t
	clk.advance(6 * time.Minute)

	const valID = "@2"
	val := []tmux.WindowInfo{{TmuxWindowID: valID, Name: "validator-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(val, nil)
	exec.On("CaptureWindowOutput", testSession, valID).Return("WEDGED", nil).Once()
	exec.On("CaptureWindowOutput", testSession, valID).Return("ready ❯", nil)
	exec.On("KillWindow", testSession, valID).Return(nil)
	exec.On("SendMessage", testSession, valID, mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn(valID))

	out := captureLog(t, func() {
		require.NoError(t, d.checkValidatingPhase(&gf.Goals[0], gf))
	})

	g := &gf.Goals[0]
	assert.Equal(t, GoalRunning, g.Status, "validator re-created, goal stays running")
	assert.Equal(t, 2, g.StuckRetries, "stuck budget charged 3->2")
	assert.Equal(t, 2, g.ValidationRetries, "validation budget untouched (stuck charges stuck budget)")
	assert.Equal(t, 3, g.CodeRetries, "code budget untouched")
	assert.Equal(t, 2, g.SpecRetries, "spec budget untouched")
	assert.Equal(t, phaseValidating, rt.phase, "remains in validating after re-create")
	assert.Contains(t, out, "STUCK")
}

// TestValidatingHeartbeat_ResetsOnProgress — a validator pane that changes each
// tick never fires the heartbeat.
func TestValidatingHeartbeat_ResetsOnProgress(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.validateTimeout = time.Hour

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 3, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	rt.phase = phaseValidating
	rt.validateTime = clk.t

	const valID = "@2"
	val := []tmux.WindowInfo{{TmuxWindowID: valID, Name: "validator-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(val, nil)
	exec.On("CaptureWindowOutput", testSession, valID).Return("v-1", nil).Once()
	exec.On("CaptureWindowOutput", testSession, valID).Return("v-2", nil).Once()
	exec.On("CaptureWindowOutput", testSession, valID).Return("v-3", nil)

	for i := 0; i < 3; i++ {
		clk.advance(6 * time.Minute)
		require.NoError(t, d.checkValidatingPhase(&gf.Goals[0], gf))
		assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	}
	assert.Equal(t, 2, gf.Goals[0].ValidationRetries, "no budget charged while validator makes progress")
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

// TestHeartbeat_BackToShell_NoFire — a window back at the shell (CurrentCommand
// "zsh") is NOT a wedged agent: the heartbeat skips it WITHOUT capturing the pane,
// and the existing crash-detect branch handles it (re-pend, code-defect).
func TestHeartbeat_BackToShell_NoFire(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute
	d.dispatchTimeout = time.Hour

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = clk.t
	rt.bootConfirmedAt = clk.t // arm the crash-detect path
	clk.advance(10 * time.Second)

	shell := []tmux.WindowInfo{{TmuxWindowID: heartbeatWindowID, Name: "supervisor-001", CurrentCommand: "zsh"}}
	exec.On("ListWindows", testSession).Return(shell, nil)

	require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

	g := &gf.Goals[0]
	assert.Equal(t, GoalPending, g.Status, "crash-detect re-pends the goal")
	assert.Equal(t, 1, g.CodeRetries, "crash-detect charges a code-defect retry 2->1")
	exec.AssertNotCalled(t, "CaptureWindowOutput", mock.Anything, mock.Anything)
}

// TestHeartbeat_DisabledWhenTimeoutZero — with progressTimeout<=0 the heartbeat
// returns not-stuck WITHOUT any tmux call, so the legacy literal-Daemon tick is
// byte-identical to pre-P2.
func TestHeartbeat_DisabledWhenTimeoutZero(t *testing.T) {
	exec := new(testutil.MockTmuxExecutor)
	d := &Daemon{executor: exec, session: testSession} // progressTimeout zero, clock nil
	rt := &goalRuntime{}

	stuck, err := d.checkProgressHeartbeat(rt, "supervisor-001")

	require.NoError(t, err)
	assert.False(t, stuck, "disabled heartbeat never fires")
	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
	exec.AssertNotCalled(t, "CaptureWindowOutput", mock.Anything, mock.Anything)
}

// TestNow_NilClockFallsBackToTimeNow — d.now() is nil-safe (literal Daemon) and
// honors an injected clock.
func TestNow_NilClockFallsBackToTimeNow(t *testing.T) {
	d := &Daemon{} // clock nil
	assert.WithinDuration(t, time.Now(), d.now(), time.Second, "nil clock falls back to time.Now")

	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	d2 := &Daemon{clock: func() time.Time { return fixed }}
	assert.True(t, d2.now().Equal(fixed), "injected clock is honored")
}

// TestNew_SeedsClockAndProgressTimeout — New() seeds the heartbeat defaults.
func TestNew_SeedsClockAndProgressTimeout(t *testing.T) {
	d := New(t.TempDir(), new(testutil.MockTmuxExecutor))
	assert.Equal(t, 5*time.Minute, d.progressTimeout, "New seeds the 5m default")
	require.NotNil(t, d.clock, "New seeds a non-nil clock")
	assert.WithinDuration(t, time.Now(), d.now(), time.Second)
}

// TestProgressTimeout_SettingOverridesDefault — Run's settings-load applies
// taskvisor.progress_timeout_sec over the daemon default.
func TestProgressTimeout_SettingOverridesDefault(t *testing.T) {
	d, _, dir := setupDaemon(t) // setupDaemon zeroes progressTimeout
	writeSettingsWithProgressTimeout(t, dir, 120)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.NoError(t, d.Run(ctx))

	assert.Equal(t, 120*time.Second, d.progressTimeout, "progress_timeout_sec override applied on Run")
}

func writeSettingsWithProgressTimeout(t *testing.T, dir string, sec int) {
	t.Helper()
	s := setup.DefaultSettings()
	s.Taskvisor.ProgressTimeoutSec = sec
	require.NoError(t, setup.SaveSettings(dir, s))
}

// --- StuckRetries dedicated budget in handleStuck* --------------------------

func TestHandleStuckSupervisor_DecrementsStuckRetries(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalRunning,
		StartedAt:  "2026-06-06T10:00:00Z",
		MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5,
		SpecRetries: 3, MaxSpecRetries: 3,
		ValidationRetries: 2, MaxValidationRetries: 2,
		StuckRetries: 3, MaxStuckRetries: 3,
	}}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// handleStuckSupervisor: 2 kill lookups (execute-*, supervisor-001)
	// dispatchRetry -> dispatch: 4 kill lookups + 1 collectManagedNames + 1 waitWindowsGone
	// Then waitClaudeBoot returns supervisor-001
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(8)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckSupervisor(goal, gf))

	assert.Equal(t, 2, goal.StuckRetries, "StuckRetries must decrement by 1")
	assert.Equal(t, 2, goal.ValidationRetries, "ValidationRetries must be unchanged")
	assert.Equal(t, GoalRunning, goal.Status, "goal must stay running (re-dispatched)")
}

func TestHandleStuckSupervisor_ExhaustedStuckBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "test", Status: GoalRunning,
			StartedAt:  "2026-06-06T10:00:00Z",
			MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5,
			SpecRetries: 3, MaxSpecRetries: 3,
			ValidationRetries: 2, MaxValidationRetries: 2,
			StuckRetries: 1, MaxStuckRetries: 3,
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckSupervisor(goal, gf))

	assert.Equal(t, 0, goal.StuckRetries, "StuckRetries must be 0")
	assert.Equal(t, GoalFailed, goal.Status, "goal must be GoalFailed")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent must be cascaded to blocked")
}

func TestHandleStuckValidator_DecrementsStuckRetries(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalRunning,
		StartedAt:  "2026-06-06T10:00:00Z",
		Validate:   []string{"go test ./..."},
		MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5,
		SpecRetries: 3, MaxSpecRetries: 3,
		ValidationRetries: 2, MaxValidationRetries: 2,
		StuckRetries: 3, MaxStuckRetries: 3,
	}}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// kill old validator (empty list = no-op), then waitClaudeBoot for new validator
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(1)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckValidator(goal, gf))

	assert.Equal(t, 2, goal.StuckRetries, "StuckRetries must decrement by 1")
	assert.Equal(t, 2, goal.ValidationRetries, "ValidationRetries must be unchanged")
	assert.Equal(t, GoalRunning, goal.Status, "goal must stay running (validator re-created)")
}

func TestHandleStuckValidator_ExhaustedStuckBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "test", Status: GoalRunning,
			StartedAt:  "2026-06-06T10:00:00Z",
			Validate:   []string{"go test ./..."},
			MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5,
			SpecRetries: 3, MaxSpecRetries: 3,
			ValidationRetries: 2, MaxValidationRetries: 2,
			StuckRetries: 1, MaxStuckRetries: 3,
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckValidator(goal, gf))

	assert.Equal(t, 0, goal.StuckRetries, "StuckRetries must be 0")
	assert.Equal(t, GoalFailed, goal.Status, "goal must be GoalFailed")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent must be cascaded to blocked")
}

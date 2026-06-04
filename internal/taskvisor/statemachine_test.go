package taskvisor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	setupDispatchMocks(exec, testSession, "@0")
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

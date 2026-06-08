package taskvisor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCascadeFailure_HardFailBlocksDependents — a hard verdict class
// ("fail"/"code-defect") blocks every dependent with BlockedBy recorded.
func TestCascadeFailure_HardFailBlocksDependents(t *testing.T) {
	for _, class := range []string{"fail", "code-defect"} {
		gf := &GoalsFile{Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
		}}
		gf.CascadeFailure("goal-001", class)
		dep, _ := gf.GoalByID("goal-002")
		assert.Equal(t, GoalBlocked, dep.Status, "class %q hard-blocks direct dependent", class)
		assert.Equal(t, "goal-001", dep.BlockedBy, "class %q records BlockedBy", class)
		// BFS reaches the transitive dependent too.
		dep2, _ := gf.GoalByID("goal-003")
		assert.Equal(t, GoalBlocked, dep2.Status, "class %q hard-blocks transitive dependent", class)
	}
}

// TestCascadeFailure_SoftHoldLeavesPending — a soft verdict class
// ("blocked"/"env-config"/"infra-flake") leaves dependents GoalPending with
// BlockedBy recorded; never failed/blocked.
func TestCascadeFailure_SoftHoldLeavesPending(t *testing.T) {
	for _, class := range []string{"blocked", "env-config", "infra-flake"} {
		gf := &GoalsFile{Goals: []Goal{
			{ID: "goal-001", Status: GoalBlocked},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
		}}
		gf.CascadeFailure("goal-001", class)
		dep, _ := gf.GoalByID("goal-002")
		assert.Equal(t, GoalPending, dep.Status, "class %q keeps dependent pending", class)
		assert.NotEqual(t, GoalFailed, dep.Status, "class %q must not fail dependent", class)
		assert.NotEqual(t, GoalBlocked, dep.Status, "class %q must not block dependent", class)
		assert.Equal(t, "goal-001", dep.BlockedBy, "class %q records BlockedBy", class)
	}
}

// TestM07_BlockedUpstreamAutoResume — an env/infra-blocked upstream soft-holds
// its dependent (pending + BlockedBy), and when the upstream later completes
// resumeDownstream clears the hold without consuming any retry budget.
func TestM07_BlockedUpstreamAutoResume(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z"},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"},
			CodeRetries: 3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// (a) Upstream env-blocked: dependent stays pending with BlockedBy set.
	valSig := &ValidatorSignal{Verdict: "blocked", Class: "env-config", Owner: "ops", Remedy: "export DATABASE_URL"}
	require.NoError(t, d.haltBlockedEnv(&gf.Goals[0], gf, valSig))

	dep, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalPending, dep.Status, "soft hold leaves dependent pending (NOT failed/blocked)")
	assert.Equal(t, "goal-001", dep.BlockedBy, "dependent records its blocking upstream")
	assert.True(t, gf.Goals[0].BlockedByPrecondition, "upstream flagged for the auto-resume loop")
	beforeCode := dep.CodeRetries

	// (b) Upstream completes → resumeDownstream clears the hold, budget untouched.
	gf.Goals[0].Status = GoalDone
	d.resumeDownstream(gf, "goal-001")

	dep, _ = gf.GoalByID("goal-002")
	assert.Equal(t, "", dep.BlockedBy, "resume cleared BlockedBy")
	assert.False(t, dep.BlockedByPrecondition, "resume cleared the precondition flag")
	assert.Equal(t, GoalPending, dep.Status, "resumed goal stays pending for re-validation")
	assert.Equal(t, beforeCode, dep.CodeRetries, "resume consumed no code retry budget")
}

// TestHaltRetryCeiling_BlocksDependents — the retry-ceiling halt (no valSig in
// scope) cascades with the literal hard class "fail", blocking dependents.
func TestHaltRetryCeiling_BlocksDependents(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z"},
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.haltRetryCeiling(&gf.Goals[0], gf))

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "ceiling halts the goal")
	dep, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalBlocked, dep.Status, "ceiling hard-blocks the dependent")
	assert.Equal(t, "goal-001", dep.BlockedBy)
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advanced to the independent pending goal (no deactivate)")
}

// TestResumeDownstreamLoop_StopsOnCtxCancel — the background loop returns
// promptly when its ctx is cancelled, leaking no goroutine.
func TestResumeDownstreamLoop_StopsOnCtxCancel(t *testing.T) {
	d, _, _ := setupDaemon(t)
	d.autoResumeInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.resumeDownstreamLoop(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resumeDownstreamLoop did not exit within one interval of ctx cancel")
	}
}

// TestResumeDownstreamLoop_PreconditionClears — scanPreconditionBlocked leaves a
// goal blocked while its precondition fails, and resumes it (pending, flag
// cleared, no budget consumed) once the precondition passes.
func TestResumeDownstreamLoop_PreconditionClears(t *testing.T) {
	d, _, dir := setupDaemon(t)
	const envVar = "TMUX_CLI_TEST_PRECOND_M07"
	require.NoError(t, os.Unsetenv(envVar))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true,
			Preconditions: []Precondition{{Kind: "env", Spec: envVar, Remedy: "export it"}},
			CodeRetries:   3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	writeGoals(t, dir, gf)
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops",
		Findings:  []ValidationFinding{{Rule: "env:" + envVar, Status: "blocked", FailureClass: "env-config"}},
		Timestamp: "2026-05-20T10:00:00Z",
	}))

	// First tick: env unset → precondition still fails → stays blocked & flagged.
	d.scanPreconditionBlocked()
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status, "precondition still failing — goal stays blocked")
	assert.True(t, g.BlockedByPrecondition, "flag retained for next tick")

	// Clear the precondition and tick again.
	require.NoError(t, os.Setenv(envVar, "1"))
	defer os.Unsetenv(envVar)

	d.scanPreconditionBlocked()
	reloaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g, _ = reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalPending, g.Status, "precondition cleared — resumed to pending")
	assert.False(t, g.BlockedByPrecondition, "precondition flag cleared on resume")
	assert.Equal(t, "", g.BlockedBy, "BlockedBy cleared on resume")
	assert.Equal(t, 3, g.CodeRetries, "resume consumed no code retry budget")
}

package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidationGoalCascadeIsolation — a dedicated validation goal goal-v
// (Validates=goal-impl, depends_on goal-impl) with a downstream goal-d
// depends_on goal-v. When CascadeFailure("goal-v","fail") runs, the implementer
// it validates stays Done and the downstream impl goal stays Pending (NOT
// Blocked): a red validation goal is terminal-to-itself.
func TestValidationGoalCascadeIsolation(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-impl", Status: GoalDone},
		{ID: "goal-v", Status: GoalFailed, Validates: "goal-impl", DependsOn: []string{"goal-impl"}},
		{ID: "goal-d", Status: GoalPending, DependsOn: []string{"goal-v"}},
	}}

	gf.CascadeFailure("goal-v", "fail")

	impl, _ := gf.GoalByID("goal-impl")
	assert.Equal(t, GoalDone, impl.Status, "the validated implementer stays Done — never cascade-blocked")
	assert.Equal(t, "", impl.BlockedBy, "no BlockedBy stamped on the implementer")

	down, _ := gf.GoalByID("goal-d")
	assert.Equal(t, GoalPending, down.Status, "a downstream impl goal gating on the validation goal stays Pending (NOT Blocked)")
	assert.Equal(t, "", down.BlockedBy, "no BlockedBy stamped on the downstream goal")
}

// TestValidationGoalFailsAlone — when a validation goal's cascade fires, ONLY
// the validation goal is terminal; no other goal's Status or BlockedBy changes,
// regardless of the (hard) verdict class.
func TestValidationGoalFailsAlone(t *testing.T) {
	for _, class := range []string{"fail", "code-defect"} {
		gf := &GoalsFile{Goals: []Goal{
			{ID: "goal-impl", Status: GoalDone},
			{ID: "goal-v", Status: GoalFailed, Validates: "goal-impl", DependsOn: []string{"goal-impl"}},
			{ID: "goal-other", Status: GoalPending, DependsOn: []string{"goal-v"}},
			{ID: "goal-unrelated", Status: GoalPending},
		}}

		gf.CascadeFailure("goal-v", class)

		for _, id := range []string{"goal-impl", "goal-other", "goal-unrelated"} {
			g, _ := gf.GoalByID(id)
			assert.NotEqual(t, GoalBlocked, g.Status, "class %q: %s must not be blocked by a validation goal", class, id)
			assert.Equal(t, "", g.BlockedBy, "class %q: %s keeps empty BlockedBy", class, id)
		}
	}
}

// TestValidationIsolationPreservesOrdinaryCascade — REGRESSION: an ORDINARY
// impl goal (NOT a validation goal) that fails with class "fail" still
// hard-blocks its real dependent. The validation-goal short-circuit must not
// alter ordinary-goal cascade semantics.
func TestValidationIsolationPreservesOrdinaryCascade(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-impl", Status: GoalFailed},
		{ID: "goal-d", Status: GoalPending, DependsOn: []string{"goal-impl"}},
	}}

	gf.CascadeFailure("goal-impl", "fail")

	down, _ := gf.GoalByID("goal-d")
	assert.Equal(t, GoalBlocked, down.Status, "an ordinary failed impl goal still hard-blocks its dependent")
	assert.Equal(t, "goal-impl", down.BlockedBy, "BlockedBy records the failed ordinary upstream")
}

// TestValidationGoalOwnBudget — a validation goal carries its OWN retry budget
// (StuckRetries et al.); when it fails/cascades, the implementer's retry
// counters are untouched (independent budget, not drawn from the impl goal).
func TestValidationGoalOwnBudget(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-impl", Status: GoalDone, StuckRetries: 3, MaxStuckRetries: 3, CodeRetries: 2, MaxCodeRetries: 2},
		{ID: "goal-v", Status: GoalFailed, Validates: "goal-impl", DependsOn: []string{"goal-impl"},
			StuckRetries: 0, MaxStuckRetries: 2},
	}}

	gf.CascadeFailure("goal-v", "fail")

	impl, _ := gf.GoalByID("goal-impl")
	assert.Equal(t, 3, impl.StuckRetries, "the implementer's stuck budget is untouched by the validation goal's failure")
	assert.Equal(t, 2, impl.CodeRetries, "the implementer's code budget is untouched")
	assert.Equal(t, GoalDone, impl.Status, "the implementer stays Done")

	v, _ := gf.GoalByID("goal-v")
	assert.Equal(t, 2, v.MaxStuckRetries, "the validation goal carries its own independent stuck budget")
}

// TestValidationAsGoalDeferDoesNotFalsePass — with validation deferred
// (d.skipValidation==true): when NO validation goal exists for the impl goal,
// the supervising phase must NOT mark it Done via the skip path — it falls
// through to the (non-deterministic) validating phase where the LLM validator
// judges it (no goal-037 false-pass). Contrast: when a validation goal IS
// present, the impl goal is marked Done directly (deferred).
func TestValidationAsGoalDeferDoesNotFalsePass(t *testing.T) {
	t.Run("no validation goal -> validating phase runs (no false-pass)", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive
		d.skipValidation = true
		d.autoCommit = false
		d.validatorSendDelay = 0
		d.runtime("goal-001").phase = phaseSupervising

		gf := &GoalsFile{
			CurrentGoal: "goal-001",
			Goals:       []Goal{{ID: "goal-001", Description: "impl", Status: GoalRunning, MaxRetries: 3}},
		}
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)

		require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
			Status: "done", Timestamp: "2026-06-03T14:30:00Z",
		}))

		// Teardown kills + validator spawn (the pass path proceeds to validating).
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		setupValidatorMocks(exec, testSession, "@5")
		d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

		assert.NotEqual(t, GoalDone, gf.Goals[0].Status, "the impl goal does NOT reach Done via the skip path")
		assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "the goal advances to validating (the validator gates the done)")
	})

	t.Run("validation goal present -> deferred Done, no inline runner", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive
		d.skipValidation = true
		d.autoCommit = false
		d.runtime("goal-001").phase = phaseSupervising

		gf := &GoalsFile{
			CurrentGoal: "goal-001",
			Goals: []Goal{
				{ID: "goal-001", Description: "impl", Status: GoalRunning, MaxRetries: 3},
				{ID: "goal-002", Description: "next", Status: GoalPending},
				{ID: "goal-v01", Description: "validate impl", Status: GoalPending,
					Validates: "goal-001", DependsOn: []string{"goal-001"}},
			},
		}
		writeGoals(t, dir, gf)
		goalDir, err := EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)

		// A validate.sh exists, but the defer path must NOT invoke it inline.
		require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
		runnerCalled := false
		d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
			runnerCalled = true
			return "", "", 0, nil
		})

		require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
			Status: "done", Timestamp: "2026-06-03T14:30:00Z",
		}))

		// Only the two teardown kills — NO validator spawn on the defer path.
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()

		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

		assert.False(t, runnerCalled, "the inline runner is NOT invoked — checks are deferred to the validation goal")
		assert.Equal(t, GoalDone, gf.Goals[0].Status, "the impl goal is marked Done (deferred) with the validation goal present")
		assert.NotEqual(t, phaseValidating, d.runtime("goal-001").phase, "no inline validation phase on the defer path")
	})
}

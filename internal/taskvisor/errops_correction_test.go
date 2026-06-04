package taskvisor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errops_correction_test.go — F2/RC-C: wire the B5b mechanical-correction
// applier into the error/ops validation route (rerunValidationOnly).
//
// Before this change, every error/ops verdict re-ran the SAME broken validation
// config and charged ValidationRetries — with max_validation_retries=1 the first
// infra/config error was instantly terminal (fail + CascadeFailure), even when
// the validator produced a precise structured correction. rerunValidationOnly
// must now attempt applyStructuredCorrections BEFORE decrementing
// ValidationRetries: handled=true → corrections applied + zero-budget
// re-validation; handled=false → today's exact charging path. Boundedness is
// preserved by the applier's own contract (ineffective/no-op edits return
// handled=false → the charging path runs).

// TestRerunValidationOnly_AppliesCorrectionsZeroBudget — an error/ops signal
// whose non-pass finding carries a CorrectionEdit targeting the goal's goal.md
// (a spec artifact) is applied by the daemon: file edited on disk,
// ValidationRetries NOT decremented (still 1), goal not failed, re-validation
// queued.
func TestRerunValidationOnly_AppliesCorrectionsZeroBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	mdPath := writeGoalMd(t, dir, "goal-001", "validate: run vendor/bin/validator-wrong\n")

	valSig := &ValidatorSignal{
		Verdict: VerdictError, Owner: "ops",
		Findings: []ValidationFinding{{
			Rule: "validation-config", Status: VerdictError, FailureClass: "validator-error", Owner: "ops",
			Detail:     "goal.md names a non-existent validator command",
			Correction: "fix the validator command path in goal.md",
			CorrectionEdits: []CorrectionEdit{
				{File: ".tmux-cli/goals/goal-001/goal.md", Line: 1, Old: "validator-wrong", New: "validator"},
			},
		}},
		Timestamp: "2026-06-04T12:00:00Z",
	}

	permissiveValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, valSig))

	body, readErr := os.ReadFile(mdPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "vendor/bin/validator\n", "goal.md is corrected in place")
	assert.NotContains(t, string(body), "validator-wrong", "the wrong path is gone")

	assert.Equal(t, 1, goal.ValidationRetries, "ValidationRetries NOT charged on the applier path")
	assert.Equal(t, 2, goal.CodeRetries, "CodeRetries untouched")
	assert.Equal(t, 2, goal.SpecRetries, "SpecRetries untouched")
	assert.Equal(t, GoalRunning, goal.Status, "goal not failed — re-validation queued")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating at zero budget")
}

// TestRerunValidationOnly_NoCorrectionsChargesBudget — a signal WITHOUT
// correction edits takes today's exact charging path: ValidationRetries is
// decremented; at 0 the goal hard-fails and CascadeFailure blocks dependents.
func TestRerunValidationOnly_NoCorrectionsChargesBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 2, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	// Prose-only error finding — no structured remedy, so the applier returns
	// (false, nil) and the budget-charging path must run unchanged.
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

	assert.Equal(t, 0, goal.ValidationRetries, "validation budget decremented 1->0 exactly as today")
	assert.Equal(t, GoalFailed, goal.Status, "exhausted budget hard-halts the goal")
	dep, ok := gf.GoalByID("goal-003")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, dep.Status, "CascadeFailure blocked the dependent")
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advanceToNextGoal moved on")
}

// TestRerunValidationOnly_IneffectiveEditFallsThrough — an edit whose old-string
// doesn't match (and whose new text is not already present) produces no on-disk
// change: the applier returns handled=false and the budget is charged, so an
// edit that never fixes anything cannot re-validate for free forever.
func TestRerunValidationOnly_IneffectiveEditFallsThrough(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	original := "validate: run vendor/bin/validator\n"
	mdPath := writeGoalMd(t, dir, "goal-001", original)

	valSig := &ValidatorSignal{
		Verdict: VerdictError, Owner: "ops",
		Findings: []ValidationFinding{{
			Rule: "validation-config", Status: VerdictError, FailureClass: "validator-error", Owner: "ops",
			Detail:     "stale anchor",
			Correction: "fix the validator command path in goal.md",
			CorrectionEdits: []CorrectionEdit{
				{File: ".tmux-cli/goals/goal-001/goal.md", Line: 1, Old: "no-such-anchor", New: "still-absent"},
			},
		}},
		Timestamp: "2026-06-04T12:00:00Z",
	}

	// handled=false → the charging path re-spawns the validator (budget 2->1).
	permissiveValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, valSig))

	body, readErr := os.ReadFile(mdPath)
	require.NoError(t, readErr)
	assert.Equal(t, original, string(body), "ineffective edit leaves goal.md untouched")

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget charged 2->1 (no free loop)")
	assert.Equal(t, GoalRunning, goal.Status, "budget remains — goal keeps running")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-validation re-queued on the charged path")
}

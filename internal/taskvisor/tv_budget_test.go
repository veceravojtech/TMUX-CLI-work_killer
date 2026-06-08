package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestM02_CodeDefectRouting — fail/code-defect routes to handleFailedCycle:
// re-dispatch implementer (status pending, phase supervising), dec CodeRetries
// only (2->1); Spec/Validation/Block unchanged.
func TestM02_CodeDefectRouting(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix the off-by-one in pricing",
		Findings:  []ValidationFinding{{Rule: "unit-tests", Status: "fail", FailureClass: "code-defect", Detail: "test red"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.CodeRetries, "code budget decremented")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget untouched")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, 0, goal.Retries, "legacy Retries stays read-only")
	assert.Equal(t, GoalPending, goal.Status, "implementer re-dispatched")
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corr)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "fix the off-by-one in pricing", "correction keeps the remediation text")
}

// TestErrorVerdict_ReRunsValidationOnly — error/ops re-spawns the validator and
// does NOT re-dispatch the implementer; dec ValidationRetries only (2->1);
// Code/Spec/Block unchanged; no code-defect correction written.
func TestErrorVerdict_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.validatorSendDelay = 0
	d.createWindowFn = mockCreateWindowFn("@5")

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict:   "error",
		Findings:  []ValidationFinding{{Rule: "validate-script", Status: "error", FailureClass: "validator-error", Detail: "validator crashed"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))

	// Re-spawn path: validator window present, killed, then a fresh one created.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("ready ❯ ", nil)
	var sentCmds []string
	exec.On("SendMessage", testSession, "@5", mock.Anything).Run(func(args mock.Arguments) {
		sentCmds = append(sentCmds, args.Get(2).(string))
	}).Return(nil)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not supervising")

	for _, c := range sentCmds {
		assert.NotContains(t, c, "/tmux:supervisor", "implementer must NOT be re-dispatched on error")
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on error")
		assert.Contains(t, c, "/tmux:investigate", "error re-runs the validator only")
	}

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr), "error must not write a code-defect correction")
}

// TestBlockedEnvHold_NoBudget — blocked/ops parks the goal and writes a runbook,
// charging NO budget (all four counters unchanged); no resume loop is started.
func TestBlockedEnvHold_NoBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "ops", Remedy: "export STRIPE_KEY then resume",
		Findings:  []ValidationFinding{{Rule: "env:STRIPE_KEY", Status: "blocked", FailureClass: "env-config", Detail: "missing secret"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "no budget charged on env hold")
	assert.Equal(t, 2, goal.SpecRetries, "no budget charged on env hold")
	assert.Equal(t, 1, goal.ValidationRetries, "no budget charged on env hold")
	assert.Equal(t, 1, goal.BlockRetries, "no budget charged on env hold")
	assert.Equal(t, GoalBlocked, goal.Status, "goal parked")

	runbook := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	data, readErr := os.ReadFile(runbook)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "export STRIPE_KEY then resume")
	assert.Contains(t, string(data), "env/infra")

	// No code-defect correction is written on an env hold.
	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr))
}

// TestBlockedEnvHold_SetsPreconditionFlagsAndConcreteRemedy — WS4 regression
// backstop. Locks the two properties that no single green test pins down, so a
// future refactor cannot silently re-route an env/infra precondition (e.g. an
// unreachable test DB) back into the implementer-retry loop:
//
//	A1 (precondition-park flags): on a blocked/env-config/ops verdict,
//	    checkValidatingPhase -> haltBlockedEnv parks the goal with
//	    BlockedBy=="env_precondition" AND BlockedByPrecondition==true, and
//	    charges ZERO budget (CodeRetries stays 2) — the §5 auto-resume contract.
//	A2 (concrete remedy, not fallback): the runbook quotes the validator's
//	    concrete Remedy verbatim and does NOT fall back to the generic
//	    taskvisor.go:1641 string — proving WS3's remedy actually reaches the
//	    operator and the fallback branch was not hit.
//
// A3 (§5 blocked->pending resume with budget unchanged) is fully locked by
// TestResumeDownstreamLoop_PreconditionClears; A4 (a true code-defect still
// decrements CodeRetries, so switch precedence is intact) by
// TestM02_CodeDefectRouting. Both are green-guards — no code is duplicated here.
//
// Class:"env-config" is set BOTH at the top level AND on the blocking finding so
// ClassifyVerdict (finding-driven) and latestSignalIsPreconditionClass
// (signal-driven) both recognise the env-config class.
func TestBlockedEnvHold_SetsPreconditionFlagsAndConcreteRemedy(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	const concreteRemedy = "start the test DB per docs/architecture/test-environment.md"
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops", Remedy: concreteRemedy,
		Findings:  []ValidationFinding{{Rule: "service:db", Status: "blocked", FailureClass: "env-config", Detail: "connection refused"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	// A1 (NEW): precondition-park flags drive the §5 auto-resume loop.
	assert.Equal(t, "env_precondition", goal.BlockedBy, "BlockedBy marks the env precondition for §5 resume")
	assert.True(t, goal.BlockedByPrecondition, "BlockedByPrecondition arms scanPreconditionBlocked")

	// Cross-check: parked with NO budget charged (the regression we guard against
	// is this path burning CodeRetries like a code defect).
	assert.Equal(t, 2, goal.CodeRetries, "env hold charges no code budget")
	assert.Equal(t, GoalBlocked, goal.Status, "goal parked, not re-dispatched")

	runbook := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	data, readErr := os.ReadFile(runbook)
	require.NoError(t, readErr)

	// A2 (NEW negative): the generic fallback (taskvisor.go:1641) must NOT appear —
	// the concrete remedy reached the operator.
	assert.NotContains(t, string(data), "Resolve the missing environment/infrastructure precondition",
		"runbook must quote the concrete remedy, not the generic fallback")
	// Cross-check: the verbatim remedy is what the operator sees.
	assert.Contains(t, string(data), concreteRemedy, "runbook quotes the validator's concrete remedy verbatim")
}

// TestBudgetCountersIndependent — code=2,spec=1: fail/code-defect moves only code
// (2,1 -> 1,1), then blocked/spec-defect moves only spec (1,1 -> 1,0). Proves
// each verdict moves exactly its own counter.
func TestBudgetCountersIndependent(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 1, 1, 0),
		{ID: "goal-002", Description: "next", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]

	// Step 1: fail/code-defect -> (code 2->1, spec stays 1).
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix bug",
		Findings:  []ValidationFinding{{Rule: "r1", Status: "fail", FailureClass: "code-defect"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	require.NoError(t, d.checkValidatingPhase(goal, gf))
	assert.Equal(t, 1, goal.CodeRetries, "code 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec unchanged by a code defect")

	// Step 2: blocked/spec-defect -> (spec 1->0, code stays 1). The finding carries
	// a concrete Detail so it is a SUBSTANTIVE spec defect (HasSubstantiveSpecDefect
	// true) — without one the M2 substance guard would correctly re-route an
	// unsubstantiated verdict to rerunValidationOnly. validateFindings already
	// guarantees every real blocked finding carries non-stub content.
	goal.Status = GoalRunning
	d.runtime("goal-001").phase = phaseValidating
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "r2", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "acceptance criteria contradict"}},
		Timestamp: "2026-05-20T14:36:00Z",
	}))
	require.NoError(t, d.checkValidatingPhase(goal, gf))
	assert.Equal(t, 1, goal.CodeRetries, "code unchanged by a spec defect")
	assert.Equal(t, 0, goal.SpecRetries, "spec 1->0")
	assert.Equal(t, 1, goal.ValidationRetries, "validation never moved")
	assert.Equal(t, 0, goal.BlockRetries, "block never moved")
}

// TestCodeBudgetExhausted_HardHalt — code=1: a fail/code-defect drives CodeRetries
// to 0, hard-halts the goal (GoalFailed), cascades to dependents, and advances.
func TestCodeBudgetExhausted_HardHalt(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 1, 1, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "still broken",
		Findings:  []ValidationFinding{{Rule: "r1", Status: "fail", FailureClass: "code-defect"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 0, goal.CodeRetries, "code budget exhausted")
	assert.Equal(t, GoalFailed, goal.Status, "hard halt")
	dep, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalBlocked, dep.Status, "CascadeFailure blocked the dependent")
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advanceToNextGoal moved on")

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.NoError(t, statErr, "exhaustion still records the final correction")
}

func TestHandleFailedCycle_RetriesExhausted_LogsGoalFailed(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z", Retries: 2, MaxRetries: 3, CodeRetries: 1, MaxCodeRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.handleFailedCycle(goal, gf, "give up", "code-defect")
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> failed")
	assert.Regexp(t, `running -> failed \(\d+`, output)
}

func TestHandleFailedCycle_Retry_LogsPendingAndPhase(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.handleFailedCycle(goal, gf, "fix it", "code-defect")
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> pending (code budget left 2)")
	assert.Contains(t, output, "goal-001: phase validating -> supervising")
}

// --- dispatchRetry tests ---

// TestM03_CircuitBreakerHalt: when cycle 2 produces the SAME failure-signature
// set as cycle 1 and circuit_breaker_k=2 (default), the goal halts to
// blocked/owner=human BEFORE cycle 3 — without consuming the code budget and
// without re-dispatching. Fires on recurrence, not on budget exhaustion.
func TestM03_CircuitBreakerHalt(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	findings := []ValidationFinding{
		{Rule: "build", Status: "fail", FailureClass: "code-defect",
			FailingCommand: "go build ./...", OutputExcerpt: "undefined: Foo", Detail: "compile error"},
	}
	cycle1Sigs := ComputeSignatures(findings)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5,
				CodeRetries: 5, MaxCodeRetries: 5,
				ConvergenceSignatures: cycle1Sigs, ConvergenceStreak: 1},
			// A second pending goal so advanceToNextGoal just re-points
			// CurrentGoal (no deactivation / tmux teardown needed).
			{ID: "goal-002", Description: "next", Status: GoalPending, MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// Cycle 2 reports the SAME signature set.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", Findings: findings, NextAction: "fix build", Timestamp: "2026-06-01T15:00:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix build", "code-defect"))

	assert.Equal(t, GoalBlocked, goal.Status, "K-recurrence halts to blocked")
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.ConvergenceStreak, "streak reaches K=2")
	assert.Equal(t, 5, goal.CodeRetries, "code budget NOT decremented on circuit-break")
	assert.Less(t, goal.Retries, goal.MaxRetries, "halt is recurrence, not budget exhaustion")

	loaded, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	sig, ok := loaded.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", loaded)
	assert.Equal(t, VerdictBlocked, sig.Verdict, "persisted verdict is blocked")
	assert.Equal(t, "human", sig.Owner, "blocked verdict is owned by human")
	assert.Equal(t, cycle1Sigs, sig.Signatures, "blocked signal carries the recurrent signatures")

	// Phase NOT advanced to supervising for this goal (no re-dispatch).
	assert.NotEqual(t, phaseSupervising, d.runtime("goal-001").phase, "circuit-break does not re-dispatch")
}

// TestM03_NoHaltWhenSignaturesChange: when cycle 2's signature set differs from
// cycle 1, the streak resets to 1 and the goal follows the normal code-defect
// route (budget consumed, GoalPending re-dispatch) rather than halting.
func TestM03_NoHaltWhenSignaturesChange(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	cycle1Findings := []ValidationFinding{
		{Rule: "build", Status: "fail", FailureClass: "code-defect", Detail: "compile error in pkg a"},
	}
	cycle2Findings := []ValidationFinding{
		{Rule: "test", Status: "fail", FailureClass: "code-defect", Detail: "assertion failed in pkg b"},
	}
	cycle1Sigs := ComputeSignatures(cycle1Findings)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5,
				CodeRetries: 5, MaxCodeRetries: 5,
				ConvergenceSignatures: cycle1Sigs, ConvergenceStreak: 1},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", Findings: cycle2Findings, NextAction: "fix test", Timestamp: "2026-06-01T15:05:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix test", "code-defect"))

	assert.Equal(t, GoalPending, goal.Status, "changed signatures -> normal re-dispatch")
	assert.Equal(t, 1, goal.ConvergenceStreak, "streak resets to 1 on a new signature set")
	assert.Equal(t, ComputeSignatures(cycle2Findings), goal.ConvergenceSignatures, "baseline updated to current cycle")
	assert.Equal(t, 4, goal.CodeRetries, "code budget consumed on the normal route (5->4)")
}

// ---- §5: verdict-class-aware cascade + downstream auto-resume -------------

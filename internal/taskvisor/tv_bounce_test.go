package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBounceToGeneration_ForwardsValidatorFindings asserts that a blocked/planner
// (spec-defect) verdict writes the validator's REAL per-finding detail into
// corrections/cycle-N.md, with the SPEC-DEFECT framing header prepended ABOVE the
// rendered finding blocks (not replacing them).
func TestBounceToGeneration_ForwardsValidatorFindings(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	valSig := &ValidatorSignal{
		Verdict: "blocked",
		Findings: []ValidationFinding{
			{
				Rule: "spec-contradiction", Status: "blocked", FailureClass: "spec-defect", Owner: "planner",
				Correction: "Resolve the contradictory acceptance: spec demands both sync and async writes — pick one.",
			},
		},
	}

	require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)

	// Framing header survives AND the real per-finding detail is forwarded.
	assert.Contains(t, content, "SPEC DEFECT (owner: PLANNER)")
	assert.Contains(t, content, "### Finding: spec-contradiction")
	assert.Contains(t, content, "Resolve the contradictory acceptance")

	// The framing header is a PREFIX above the finding detail, not a replacement.
	framingIdx := strings.Index(content, "SPEC DEFECT (owner: PLANNER)")
	corrIdx := strings.Index(content, "Resolve the contradictory acceptance")
	require.NotEqual(t, -1, framingIdx)
	require.NotEqual(t, -1, corrIdx)
	assert.Less(t, framingIdx, corrIdx, "framing header must appear above the rendered finding")

	// SpecRetries-only decrement; goal bounced back to the generation phase.
	assert.Equal(t, 1, goal.SpecRetries)
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase)
}

// TestBounceToGeneration_NoFindingsKeepsFramingFallback asserts the findingless
// bounce (empty Findings, and the nil-valSig sub-case for a synthesized bounce)
// still writes a non-empty file carrying the framing header.
func TestBounceToGeneration_NoFindingsKeepsFramingFallback(t *testing.T) {
	run := func(t *testing.T, valSig *ValidatorSignal) {
		t.Helper()
		d, _, dir := setupDaemon(t)
		gf := &GoalsFile{
			CurrentGoal: "goal-001",
			Goals: []Goal{
				{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
			},
		}
		writeGoals(t, dir, gf)
		goal := &gf.Goals[0]

		require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

		data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
		require.NoError(t, err)
		content := string(data)

		assert.NotEmpty(t, strings.TrimSpace(content), "fallback file must not be empty")
		assert.Contains(t, content, "SPEC DEFECT (owner: PLANNER)")
		assert.NotContains(t, content, "### Finding:", "no findings means no structured blocks")
		assert.Equal(t, 1, goal.SpecRetries)
	}

	t.Run("EmptyFindings", func(t *testing.T) {
		run(t, &ValidatorSignal{Verdict: "blocked", Findings: []ValidationFinding{}})
	})
	t.Run("NilValSig", func(t *testing.T) {
		run(t, nil)
	})
}

// TestBounceToGeneration_ExhaustionCascades is the preservation guard: when the
// spec budget reaches 0 the goal hard-fails and dependents cascade with the hard
// class "fail", confirming the SpecRetries/exhaustion/CascadeFailure block is intact.
func TestBounceToGeneration_ExhaustionCascades(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 1, MaxSpecRetries: 2},
			{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	valSig := &ValidatorSignal{
		Verdict: "blocked",
		Findings: []ValidationFinding{
			{Rule: "spec-contradiction", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Correction: "fix the spec"},
		},
	}

	require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

	assert.Equal(t, GoalFailed, goal.Status, "spec budget exhausted -> goal failed")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent cascaded with hard class fail")
	assert.Equal(t, "goal-001", gf.Goals[1].BlockedBy)
}

// --- B10: spec-route convergence circuit-breaker ---------------------------
//
// These eight tests mirror the code-route breaker (handleFailedCycle) onto the
// spec-defect bounce path. They are built on execute-2's goal-025 repeat-signature
// fixture (newGoal025Fixture / newRecurringFindingSignal) and assert: the breaker
// halts at K identical spec-signature bounces to blocked/owner=human WITHOUT
// draining SpecRetries, never fires on an empty/changing signature set, and tracks
// streak state in DEDICATED spec-side fields so an interleaved code-defect cycle
// cannot cross-contaminate the spec streak.

// TestBounceToGeneration_IdenticalSpecSignatures_HaltsAtK: K consecutive bounces
// with an identical finding-signature set halt the goal at the Kth (K=2) bounce
// with the shared convergence-circuit-breaker sentinel.
func TestBounceToGeneration_IdenticalSpecSignatures_HaltsAtK(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Description: "non-converging spec", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	// First bounce primes the streak to 1 and drains normally — must NOT halt.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.NotEqual(t, GoalBlocked, goal.Status, "first bounce must not halt")
	assert.Equal(t, 1, goal.SpecConvergenceStreak)
	assert.True(t, equalSorted(sigs, goal.SpecConvergenceSignatures))

	// Second (Kth) identical bounce trips the breaker.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecConvergenceStreak)
}

// TestBounceToGeneration_HaltDoesNotDecrementSpecRetries: a breaker halt leaves
// SpecRetries at its pre-call value (the breaker fires on recurrence, never on
// budget drain).
func TestBounceToGeneration_HaltDoesNotDecrementSpecRetries(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 4, MaxSpecRetries: 5,
				SpecConvergenceSignatures: sigs, SpecConvergenceStreak: 1},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]
	pre := goal.SpecRetries

	require.NoError(t, d.bounceToGeneration(goal, gf, sig))

	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, pre, goal.SpecRetries, "breaker halt must not decrement SpecRetries")
}

// TestBounceToGeneration_HaltSetsOwnerHumanBlockedSignal: the persisted signal on
// a breaker halt is Verdict=blocked, Owner=human, Signatures=the current sorted set.
func TestBounceToGeneration_HaltSetsOwnerHumanBlockedSignal(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 4, MaxSpecRetries: 5,
				SpecConvergenceSignatures: sigs, SpecConvergenceStreak: 1},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	require.NoError(t, d.bounceToGeneration(goal, gf, sig))

	raw, err := LoadSignal(dir, "goal-025")
	require.NoError(t, err)
	vs, ok := raw.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", raw)
	assert.Equal(t, VerdictBlocked, vs.Verdict)
	assert.Equal(t, "human", vs.Owner)
	assert.Equal(t, sigs, vs.Signatures)
}

// TestBounceToGeneration_ChangingSpecSignatures_DrainsBudgetNotBreaker: a different
// finding signature each bounce keeps the streak pinned at 1, so the breaker never
// fires and the goal follows the normal SpecRetries-- drain.
func TestBounceToGeneration_ChangingSpecSignatures_DrainsBudgetNotBreaker(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	for i := 0; i < 3; i++ {
		sig := &ValidatorSignal{
			Verdict: VerdictBlocked,
			Findings: []ValidationFinding{
				{Rule: fmt.Sprintf("rule-%d", i), Status: VerdictFail, FailureClass: "spec-defect",
					Detail: fmt.Sprintf("distinct spec defect %d", i)},
			},
		}
		require.NoError(t, d.bounceToGeneration(goal, gf, sig))
		assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy, "changing signatures must not trip the breaker")
		assert.Equal(t, 1, goal.SpecConvergenceStreak, "streak resets to 1 on a changed signature set")
	}
	assert.Equal(t, 2, goal.SpecRetries, "3 normal bounces drained 5->2")
	assert.Equal(t, GoalPending, goal.Status)
}

// TestBounceToGeneration_EmptyFindings_NeverFires: a pass-only (empty signature)
// set never fires the breaker even when replayed past K; the budget drains normally.
func TestBounceToGeneration_EmptyFindings_NeverFires(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	passSig := &ValidatorSignal{Verdict: VerdictBlocked, Findings: []ValidationFinding{
		{Rule: "noop", Status: VerdictPass},
	}}
	for i := 0; i < 3; i++ {
		require.NoError(t, d.bounceToGeneration(goal, gf, passSig))
		assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy, "empty signature set must never fire the breaker")
		assert.Empty(t, goal.SpecConvergenceSignatures, "empty set stored, never matched")
		assert.Equal(t, 1, goal.SpecConvergenceStreak, "streak pinned at 1 for an empty set")
	}
	assert.Equal(t, 2, goal.SpecRetries, "budget drains normally 5->2")
}

// TestBounceToGeneration_NilValSig_NoPanic: a nil valSig completes without panic,
// skips the breaker, and writes the header-only framing correction.
func TestBounceToGeneration_NilValSig_NoPanic(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	require.NotPanics(t, func() {
		require.NoError(t, d.bounceToGeneration(goal, gf, nil))
	})

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "SPEC DEFECT (owner: PLANNER)")
	assert.NotContains(t, string(data), "### Finding:", "no findings means no structured blocks")
	assert.Equal(t, 1, goal.SpecRetries)
	assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy)
}

// TestBounceToGeneration_AlternatingCodeSpecCycles_NoCrossContamination: a code
// cycle interleaved between two identical spec bounces neither resets nor inflates
// the spec streak — the spec streak accumulates to K ACROSS the code cycle and
// trips, while the code streak lives only in ConvergenceStreak.
func TestBounceToGeneration_AlternatingCodeSpecCycles_NoCrossContamination(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-025").lastSupervisorStatus = "done"
	_, err := EnsureGoalDir(dir, "goal-025")
	require.NoError(t, err)

	specSig, specSigs := newRecurringFindingSignal()
	codeSig := &ValidatorSignal{
		Source: "validator", Verdict: VerdictFail,
		Findings: []ValidationFinding{
			{Rule: "deptrac", Status: VerdictFail, FailureClass: "code-defect",
				FailingCommand: "vendor/bin/deptrac", Detail: "layer violation in billing"},
		},
	}
	codeSigs := ComputeSignatures(codeSig.Findings)
	require.False(t, equalSorted(specSigs, codeSigs), "spec and code signature sets must differ")

	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning,
				SpecRetries: 5, MaxSpecRetries: 5, CodeRetries: 5, MaxCodeRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	// 1) Spec bounce S — primes spec streak; leaves code streak untouched.
	require.NoError(t, d.bounceToGeneration(goal, gf, specSig))
	assert.Equal(t, 1, goal.SpecConvergenceStreak)
	assert.True(t, equalSorted(specSigs, goal.SpecConvergenceSignatures))
	assert.Equal(t, 0, goal.ConvergenceStreak, "spec bounce must not touch the code streak")
	assert.Empty(t, goal.ConvergenceSignatures)

	// 2) Intervening code-defect cycle C — primes the code streak; spec state PRESERVED.
	writeFixtureSignal(t, dir, "goal-025", codeSig) // handleFailedCycle re-loads from disk
	require.NoError(t, d.handleFailedCycle(goal, gf, "code still failing", "code-defect"))
	assert.Equal(t, 1, goal.ConvergenceStreak)
	assert.True(t, equalSorted(codeSigs, goal.ConvergenceSignatures))
	assert.Equal(t, 1, goal.SpecConvergenceStreak, "code cycle must NOT reset the spec streak")
	assert.True(t, equalSorted(specSigs, goal.SpecConvergenceSignatures), "code cycle must NOT overwrite spec sigs")

	// 3) Spec bounce S again — spec streak reaches K across the code cycle and trips.
	require.NoError(t, d.bounceToGeneration(goal, gf, specSig))
	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecConvergenceStreak, "spec streak survived the intervening code cycle")
	assert.Equal(t, 1, goal.ConvergenceStreak, "code streak not inflated by spec bounces")
}

// TestBounceToGeneration_FixtureReplay_GoalThreeIdenticalBounces: replaying the
// goal-025 fixture's recurring signal 3 times halts at K (the 2nd bounce) with the
// sentinel BEFORE draining the spec budget to a failed goal.
func TestBounceToGeneration_FixtureReplay_GoalThreeIdenticalBounces(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf, sig := newGoal025Fixture(1, 2) // recurring-signature replay fixture
	goal, ok := gf.GoalByID("goal-025")
	require.True(t, ok)
	// The fixture stages the CODE-route streak; give the SPEC route its own budget
	// and a clean spec-side streak so only the spec breaker can halt the goal.
	goal.Status = GoalRunning
	goal.SpecRetries = 3
	goal.MaxSpecRetries = 3
	goal.SpecConvergenceSignatures = nil
	goal.SpecConvergenceStreak = 0
	writeGoals(t, dir, gf)

	// Bounce 1: streak 1, SpecRetries 3->2, no halt.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	require.NotEqual(t, GoalBlocked, goal.Status)
	require.Equal(t, 2, goal.SpecRetries)

	// Bounce 2 (Kth identical): halts at K rather than draining to failed.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.Equal(t, GoalBlocked, goal.Status, "halts at K, not a budget-exhausted failed")
	assert.NotEqual(t, GoalFailed, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecRetries, "SpecRetries preserved at its post-1st-bounce value, not drained to 0")
}

// TestM05_DispatchMdFullCorrections proves a multi-finding failure flows through
// handleFailedCycle → corrections/cycle-N.md → writeDispatchMd VERBATIM: the
// dispatch.md "## Prior Corrections" reproduces every per-finding block with all
// four fields and never collapses to the generic one-liner.
func TestM05_DispatchMdFullCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Fix pricing", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"Price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestTotal",
				OutputExcerpt:  "want 1000 got 100",
				ExpectedState:  "total in cents matches the API",
				Correction:     "multiply dollars by 100 before formatting",
			},
			{
				Rule: "currency-format", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestLocale",
				OutputExcerpt:  "want 1.000,00 got 1,000.00",
				ExpectedState:  "locale-aware currency formatting",
				Correction:     "use the locale formatter for the active request",
			},
		},
		NextAction: "fix pricing", Timestamp: "2026-05-20T14:35:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix pricing", "code-defect"))
	require.NoError(t, d.writeDispatchMd(goal))

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Prior Corrections")
	// Both per-finding blocks reproduced with all four fields.
	assert.Contains(t, content, "### Finding: price-calc")
	assert.Contains(t, content, "### Finding: currency-format")
	assert.Contains(t, content, "Command: go test ./pricing -run TestTotal")
	assert.Contains(t, content, "Output: want 1000 got 100")
	assert.Contains(t, content, "Expected: total in cents matches the API")
	assert.Contains(t, content, "Correction: multiply dollars by 100 before formatting")
	assert.Contains(t, content, "Command: go test ./pricing -run TestLocale")
	assert.Contains(t, content, "Correction: use the locale formatter for the active request")
	// No generic one-liner collapse.
	assert.NotContains(t, content, "failed acceptance criteria")
}

// TestInjectCorrections_CarriesPerFindingBlock proves injectCorrections appends
// the full structured per-finding block (Command:/Expected: lines) to each task
// context — not just a nextAction summary.
func TestInjectCorrections_CarriesPerFindingBlock(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "broken-test", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./checkout -run TestPay",
				OutputExcerpt:  "panic: nil pointer in Pay()",
				ExpectedState:  "payment succeeds without panicking",
				Correction:     "guard the nil gateway before calling Pay()",
			},
		},
		NextAction: "fix the broken test", Timestamp: "2026-05-20T14:35:00Z",
	}))

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1 context")

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix the broken test", "code-defect"))
	require.NoError(t, d.injectCorrections(goal))

	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)

	assert.Contains(t, ctxContent, "# Task 1 context")
	assert.Contains(t, ctxContent, "## Prior Corrections (Cycle 1)")
	// Full per-finding block carried, not just the nextAction summary.
	assert.Contains(t, ctxContent, "### Finding: broken-test")
	assert.Contains(t, ctxContent, "Command: go test ./checkout -run TestPay")
	assert.Contains(t, ctxContent, "Expected: payment succeeds without panicking")
	assert.Contains(t, ctxContent, "Correction: guard the nil gateway before calling Pay()")
}

// TestSpecDefectRouting_BouncesToGeneration — blocked/planner bounces to the
// generation/planner slot (not the implementer); dec SpecRetries only (2->1);
// Code/Validation/Block unchanged; CodeRetries untouched so the next dispatch
// re-plans rather than re-running the implementer.
func TestSpecDefectRouting_BouncesToGeneration(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "criteria contradict"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.SpecRetries, "spec budget decremented 2->1")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched (-> next dispatch re-plans)")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase, "marked for generation re-dispatch")

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corr)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "SPEC DEFECT")
	assert.Contains(t, string(data), "PLANNER")
}

// TestSpecDefectRouting_Unsubstantiated_ReRunsValidationOnly — a blocked/planner
// verdict whose only finding carries an empty/stub Detail AND Correction has no
// concretely-cited contradiction. The substance guard re-routes it to
// rerunValidationOnly (dec ValidationRetries 2->1) instead of bounceToGeneration,
// so the scarce single SpecRetries is preserved (no spec-retry burn + cascade).
func TestSpecDefectRouting_Unsubstantiated_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: ""}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	sentCmds := rerunValidationMocks(d, exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget UNTOUCHED — the scarce single retry is preserved")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not generation")

	for _, c := range *sentCmds {
		assert.NotContains(t, c, "/tmux:supervisor", "implementer must NOT be re-dispatched")
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on an unsubstantiated verdict")
		assert.Contains(t, c, "/tmux:investigate", "unsubstantiated blocked/planner re-runs the validator only")
	}

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr), "no code/spec correction is written on a re-validate")
}

// TestSpecDefectRouting_TopLevelFallback_NoFindings_ReRunsValidationOnly — the
// dominant previo2 vector: a top-level blocked/planner verdict with NO
// classifiable findings (the :1286 fallback). ClassifyVerdict returns pass, the
// fallback promotes it to (blocked, planner), and the guard — finding no
// substantive contradiction — re-validates rather than charging SpecRetries.
func TestSpecDefectRouting_TopLevelFallback_NoFindings_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  nil,
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	sentCmds := rerunValidationMocks(d, exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget UNTOUCHED on a contentless fallback verdict")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not generation")

	for _, c := range *sentCmds {
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on a no-finding fallback")
		assert.Contains(t, c, "/tmux:investigate", "fallback re-runs the validator only")
	}
}

// TestSpecDefectRouting_Substantive_NonStubCorrectionOnly_Bounces — predicate A
// (the zero-regression default): a blocked/planner finding with an empty Detail
// but a NON-stub Correction is substantive, so it still reaches bounceToGeneration
// and charges SpecRetries. Locks the recommended reading against a future switch
// to the stricter Detail-only predicate B.
func TestSpecDefectRouting_Substantive_NonStubCorrectionOnly_Bounces(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: "acceptance #3 requires X but precondition forbids X"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.SpecRetries, "spec budget decremented 2->1 — substantive defect still bounces")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase, "marked for generation re-dispatch")
}

// --- E1-0d: per-goal fan-out tasks.yaml relocation ---

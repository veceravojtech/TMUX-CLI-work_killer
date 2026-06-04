package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtures_test.go — FOUNDATION replay-fixture harness (goal-020).
//
// Composable, side-effect-free builders that reconstruct three real test-project
// failure states as faithful in-memory *GoalsFile / *ValidatorSignal values, so
// the A1 stale-block, A2 final-gate deadlock, and B5/B6/B10 convergence/correction
// behaviors can be TDD'd against a reproducible starting state. The builder
// signatures here are the STABLE CONTRACT consumed by execute-4/5/6/8/15 — name
// and shape must not drift.
//
// This file adds NO production symbols. It reuses the existing in-package helpers
// (writeGoals/setupDaemon from taskvisor_test.go) and production funcs
// (SaveGoals/LoadGoals/SaveValidatorSignal/LoadSignal/ComputeSignatures/
// CascadeFailure/EnsureGoalDir/handleFailedCycle/equalSorted).

// --- Builders (the stable contract) ---------------------------------------

// newGoal063Fixture reconstructs the A1 stale-block incident: goal-063 is parked
// GoalBlocked with BlockedBy naming a blocker that has since reached GoalDone,
// while ANOTHER DependsOn entry is still GoalPending. The still-pending sibling
// dep is what keeps it deliberately NON-recoverable (HasRecoverableBlock==false)
// and distinguishes it from the already-fixed recoverable_block_test case. The
// GoalDone blocker is set explicitly (CascadeFailure cannot produce a done
// blocker). Budgets are set on goal-063 both live and Max so a disk round-trip
// survives the LoadGoals zero-budget re-seed.
func newGoal063Fixture() *GoalsFile {
	return &GoalsFile{
		CurrentGoal: "goal-063",
		Goals: []Goal{
			{ID: "goal-001", Description: "recovered blocker", Status: GoalDone},
			{ID: "goal-002", Description: "still-open sibling dep", Status: GoalPending},
			{
				ID:          "goal-063",
				Description: "stale-blocked target",
				Status:      GoalBlocked,
				BlockedBy:   "goal-001",
				DependsOn:   []string{"goal-001", "goal-002"},
				CodeRetries: 3, MaxCodeRetries: 3,
			},
		},
	}
}

// newDeadlockFinalFixture reconstructs the A2 silent-deadlock incident: a
// phase:"final" goal blocked behind a GoalFailed blocker while unrelated work is
// still pending — so AnyRunning()==false, AllResolved()==false, and
// RunnableCandidates() is non-empty (the watchdog's "work ought to be moving but
// nothing is" signature). The blocked state is produced authentically via
// CascadeFailure over a pending goal-090 (the real daemon path), not hand-stamped.
func newDeadlockFinalFixture() *GoalsFile {
	gf := &GoalsFile{
		CurrentGoal: "goal-090",
		Goals: []Goal{
			{ID: "goal-001", Description: "failed upstream", Status: GoalFailed},
			{ID: "goal-002", Description: "unrelated open work", Status: GoalPending},
			{
				ID:          "goal-090",
				Description: "final gate",
				Status:      GoalPending,
				Phase:       "final",
				DependsOn:   []string{"goal-001"},
			},
		},
	}
	// Hard cascade goal-001's failure onto its dependent subtree: goal-090
	// (depends_on goal-001) becomes GoalBlocked,BlockedBy="goal-001"; goal-002 has
	// no failed dep, so it stays GoalPending.
	gf.CascadeFailure("goal-001", "fail")
	return gf
}

// newRecurringFindingSignal returns a validator signal whose non-pass findings
// produce a STABLE, non-empty signature set (the B10 non-convergence primitive),
// plus that signature set. Field values are literal constants free of timestamps/
// PIDs/hex/line-numbers so NormalizeFailureCause is a no-op and the signatures are
// reproducible across calls and processes. Findings are VerdictFail (never
// VerdictPass) because ComputeSignatures skips pass findings — a pass-only set
// would yield an empty signature set and the circuit-breaker would never fire.
func newRecurringFindingSignal() (*ValidatorSignal, []string) {
	findings := []ValidationFinding{
		{
			Rule:           "phpstan",
			Status:         VerdictFail,
			FailureClass:   "code-defect",
			FailingCommand: "vendor/bin/phpstan analyse",
			OutputExcerpt:  "Method has invalid return type",
			Detail:         "phpstan reports an invalid return type in the service layer",
		},
		{
			Rule:           "phpunit",
			Status:         VerdictFail,
			FailureClass:   "code-defect",
			FailingCommand: "vendor/bin/phpunit",
			OutputExcerpt:  "expected true but got false",
			Detail:         "assertion on the booking total failed",
		},
	}
	sig := &ValidatorSignal{
		Source:     "validator",
		Verdict:    VerdictFail,
		Findings:   findings,
		NextAction: "fix the reported defects",
		Timestamp:  "2026-06-01T12:00:00Z",
	}
	return sig, ComputeSignatures(findings)
}

// newGoal025Fixture reconstructs the B10 circuit-breaker-on-recurrence incident:
// a GoalRunning goal whose ConvergenceSignatures already hold the prior cycle's
// recurring signature set and whose ConvergenceStreak is staged at `streak`. The
// code-defect budget is set FULL (CodeRetries:3,MaxCodeRetries:3) so only the
// breaker — never budget exhaustion — can halt it; when streak==k-1 the next
// handleFailedCycle on the same signal trips the breaker. k is the caller's
// declared circuit_breaker_k threshold (the daemon reads its live value from
// settings; the default is 2). A second pending goal-026 lets advanceToNextGoal
// simply re-point CurrentGoal with no tmux teardown. Returns the goals file and
// the matching validator signal (write it to disk with writeFixtureSignal for the
// real-consumer path, since handleFailedCycle re-loads the signal from disk).
func newGoal025Fixture(streak, k int) (*GoalsFile, *ValidatorSignal) {
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{
				ID:                    "goal-025",
				Description:           "non-converging target",
				Status:                GoalRunning,
				CodeRetries:           3,
				MaxCodeRetries:        3,
				ConvergenceSignatures: sigs,
				ConvergenceStreak:     streak,
			},
			{ID: "goal-026", Description: "next pending work", Status: GoalPending},
		},
	}
	_ = k // declared threshold the consumer configures/asserts against; the daemon reads the live k from settings.
	return gf, sig
}

// newStructuredCorrectionSignal is the B5 fixture: a validator signal whose
// findings each carry FULL structured remediation (non-empty Correction, Detail,
// FailingCommand AND ExpectedState), driving the per-finding structured
// correction-rendering path.
func newStructuredCorrectionSignal() *ValidatorSignal {
	return &ValidatorSignal{
		Source:  "validator",
		Verdict: VerdictFail,
		Findings: []ValidationFinding{
			{
				Rule:           "phpstan",
				Status:         VerdictFail,
				FailureClass:   "code-defect",
				Detail:         "BookingService::total() returns float, declared int",
				Correction:     "change the return type to float and update callers",
				FailingCommand: "vendor/bin/phpstan analyse src/Booking",
				ExpectedState:  "phpstan level 8 passes with no errors",
			},
			{
				Rule:           "phpunit",
				Status:         VerdictFail,
				FailureClass:   "code-defect",
				Detail:         "BookingTest::testTotal asserts the wrong rounding",
				Correction:     "round half-up to two decimals before asserting",
				FailingCommand: "vendor/bin/phpunit --filter testTotal",
				ExpectedState:  "the booking suite is green",
			},
		},
		NextAction: "apply the per-finding corrections",
		Timestamp:  "2026-06-01T12:30:00Z",
	}
}

// newProseCorrectionSignal is the B6 fixture: a single finding carrying only flat
// prose Detail with an EMPTY Correction, driving the NextAction-fallback path that
// has no structured per-finding remediation to render.
func newProseCorrectionSignal() *ValidatorSignal {
	return &ValidatorSignal{
		Source:  "validator",
		Verdict: VerdictFail,
		Findings: []ValidationFinding{
			{
				Rule:         "acceptance",
				Status:       VerdictFail,
				FailureClass: "code-defect",
				Detail:       "the implementation does not satisfy the stated acceptance criteria",
			},
		},
		NextAction: "re-read the acceptance criteria and address the gap",
		Timestamp:  "2026-06-01T12:45:00Z",
	}
}

// writeFixtureSignal persists a fixture validator signal to
// .tmux-cli/goals/<goalID>/signal.json so on-disk consumers (and the
// handleFailedCycle re-load path) observe it. Thin require.NoError wrapper over
// the production SaveValidatorSignal.
func writeFixtureSignal(t *testing.T, dir, goalID string, sig *ValidatorSignal) {
	t.Helper()
	require.NoError(t, SaveValidatorSignal(dir, goalID, sig))
}

// --- Self-tests ------------------------------------------------------------

func TestFixtureStaleBlockedByDoneState(t *testing.T) {
	gf := newGoal063Fixture()

	assert.Equal(t, "goal-063", gf.CurrentGoal)

	g63, ok := gf.GoalByID("goal-063")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, g63.Status)
	assert.Equal(t, "goal-001", g63.BlockedBy)
	assert.Equal(t, []string{"goal-001", "goal-002"}, g63.DependsOn)
	assert.False(t, g63.BlockedByPrecondition, "stale dependency block, not a precondition park")

	g1, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, g1.Status, "the blocker has since completed")

	g2, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g2.Status, "sibling dep still open")
}

func TestFixtureStaleBlockNotRecoverable(t *testing.T) {
	gf := newGoal063Fixture()

	// The still-pending sibling dep makes the block NOT yet recoverable, so this
	// is deliberately NOT the recoverable_block_test case.
	assert.False(t, gf.HasRecoverableBlock(), "pending sibling dep ⇒ not recoverable")

	// ReconcileBlocks cannot RECOVER it (deps unsatisfied), so goal-063 stays
	// GoalBlocked. The A1 fix re-points the stale BlockedBy (which named the
	// now-done goal-001) to the first still-incomplete dep (goal-002), reporting
	// changed==true — the goal remains blocked but no longer trips checkInvariant.
	assert.True(t, gf.ReconcileBlocks(), "re-point of the stale BlockedBy is a change")
	g63, ok := gf.GoalByID("goal-063")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, g63.Status, "still blocked — the dep is still pending")
	assert.Equal(t, "goal-002", g63.BlockedBy, "re-pointed off the done blocker to the open sibling dep")
}

func TestFixtureRepeatSignatureBounce(t *testing.T) {
	gf, sig := newGoal025Fixture(1, 2)

	g, ok := gf.GoalByID("goal-025")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status)

	cur := ComputeSignatures(sig.Findings)
	assert.Greater(t, len(cur), 0, "recurring signature set is non-empty")
	assert.True(t, equalSorted(cur, g.ConvergenceSignatures), "K identical cycles staged on the goal")
	assert.Equal(t, 1, g.ConvergenceStreak)
	assert.Equal(t, 3, g.CodeRetries, "code budget full — cannot be the halt cause")
	assert.Equal(t, 3, g.MaxCodeRetries)
}

func TestFixtureRepeatSignatureTripsBreaker(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-025").lastSupervisorStatus = "done"

	gf, sig := newGoal025Fixture(1, 2)
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-025")
	require.NoError(t, err)
	// handleFailedCycle re-loads the signal from disk to compute the signatures,
	// so the recurring signal MUST be on disk or the breaker never fires.
	writeFixtureSignal(t, dir, "goal-025", sig)

	g, ok := gf.GoalByID("goal-025")
	require.True(t, ok)
	require.NoError(t, d.handleFailedCycle(g, gf, "still failing", "code-defect"))

	assert.Equal(t, GoalBlocked, g.Status, "K-recurrence halts the goal")
	assert.Equal(t, "convergence-circuit-breaker", g.BlockedBy)
	assert.Equal(t, 3, g.CodeRetries, "budget NOT decremented on circuit-break")

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	lg, ok := loaded.GoalByID("goal-025")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, lg.Status, "blocked state persisted")
	assert.Equal(t, "convergence-circuit-breaker", lg.BlockedBy)
}

func TestFixtureRecurringSignalDeterministic(t *testing.T) {
	sig, sigs := newRecurringFindingSignal()

	assert.Greater(t, len(sigs), 0, "non-empty recurrence signature")
	assert.Equal(t, sigs, ComputeSignatures(sig.Findings), "signatures are deterministic across calls")
	for _, f := range sig.Findings {
		assert.NotEqual(t, VerdictPass, f.Status, "pass findings are excluded from signatures")
	}
}

func TestFixtureFinalGateDeadlock(t *testing.T) {
	gf := newDeadlockFinalFixture()

	g90, ok := gf.GoalByID("goal-090")
	require.True(t, ok)
	assert.Equal(t, "final", g90.Phase)
	assert.Equal(t, GoalBlocked, g90.Status)
	assert.Equal(t, "goal-001", g90.BlockedBy)

	g1, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g1.Status)

	g2, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g2.Status)

	assert.False(t, gf.AnyRunning(), "no live worker")
	assert.False(t, gf.AllResolved(), "pending work remains")

	cands := gf.RunnableCandidates()
	require.Len(t, cands, 1, "only the unrelated pending goal is runnable")
	assert.Equal(t, "goal-002", cands[0].ID)
}

func TestFixtureStructuredCorrection(t *testing.T) {
	sig := newStructuredCorrectionSignal()

	require.GreaterOrEqual(t, len(sig.Findings), 1)
	for _, f := range sig.Findings {
		assert.Equal(t, VerdictFail, f.Status)
		assert.NotEmpty(t, f.Correction)
		assert.NotEmpty(t, f.Detail)
		assert.NotEmpty(t, f.FailingCommand)
		assert.NotEmpty(t, f.ExpectedState)
	}
}

func TestFixtureProseCorrection(t *testing.T) {
	sig := newProseCorrectionSignal()

	require.Len(t, sig.Findings, 1)
	assert.NotEmpty(t, sig.Findings[0].Detail)
	assert.Empty(t, sig.Findings[0].Correction, "prose findings carry no structured correction")
}

func TestFixtureRoundTripSaveLoad(t *testing.T) {
	dir := t.TempDir()
	gf := newGoal063Fixture()
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, "goal-063", loaded.CurrentGoal)

	g63, ok := loaded.GoalByID("goal-063")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, g63.Status)
	assert.Equal(t, "goal-001", g63.BlockedBy)
	assert.Equal(t, []string{"goal-001", "goal-002"}, g63.DependsOn)
	// Explicit live+Max budgets survive the LoadGoals zero-budget re-seed — the
	// trap documented for execute-4/5/6/8/15.
	assert.Equal(t, 3, g63.CodeRetries)
	assert.Equal(t, 3, g63.MaxCodeRetries)

	g1, ok := loaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, g1.Status)

	g2, ok := loaded.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g2.Status)
}

func TestFixtureGoal025RoundTrip(t *testing.T) {
	dir := t.TempDir()
	gf, sig := newGoal025Fixture(1, 2)
	writeGoals(t, dir, gf)
	writeFixtureSignal(t, dir, "goal-025", sig)

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID("goal-025")
	require.True(t, ok)
	assert.Equal(t, 1, g.ConvergenceStreak, "streak survives reload")
	assert.True(t, equalSorted(g.ConvergenceSignatures, ComputeSignatures(sig.Findings)),
		"persisted convergence signatures match the signal")

	rawSig, err := LoadSignal(dir, "goal-025")
	require.NoError(t, err)
	vs, ok := rawSig.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", rawSig)
	assert.Equal(t, ComputeSignatures(sig.Findings), ComputeSignatures(vs.Findings),
		"reloaded signal yields the same signatures")
}

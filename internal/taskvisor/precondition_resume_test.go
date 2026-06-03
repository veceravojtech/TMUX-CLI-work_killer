package taskvisor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHaltBlockedEnv_PersistsSignalJson — C-1: the env/infra validation-route
// park MUST persist a signal.json so §5's resume gate can recognize the park.
// Calling haltBlockedEnv with an EMPTY valSig.Class must stamp the resolved soft
// class (env-config default) onto the signal and write it to disk.
func TestHaltBlockedEnv_PersistsSignalJson(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z"},
	}}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// Class deliberately empty → softClass resolves to the env-config default.
	valSig := &ValidatorSignal{Verdict: "blocked", Owner: "ops", Remedy: "export DATABASE_URL"}
	require.NoError(t, d.haltBlockedEnv(&gf.Goals[0], gf, valSig))

	// signal.json must now exist on disk.
	_, statErr := os.Stat(SignalPath(dir, "goal-001"))
	require.NoError(t, statErr, "haltBlockedEnv must persist signal.json")

	loaded, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	vs, ok := loaded.(*ValidatorSignal)
	require.True(t, ok, "persisted signal must be a validator signal")
	assert.Equal(t, "env-config", vs.Class, "empty class is stamped with the resolved softClass default")
	assert.Equal(t, "blocked", vs.Verdict, "verdict preserved")

	// The persisted precondition class makes the park resume-eligible via branch (a).
	assert.True(t, d.latestSignalIsPreconditionClass("goal-001"),
		"persisted signal classifies as a precondition hold")
}

// TestScanPreconditionBlocked_ResumesParkWithResultsButNoSignal — C-2 branch (b),
// the goal-012/013 stranded repro: BlockedBy=="env_precondition", flag set,
// results.json present, NO signal.json, no preconditions. The scan must resume it
// (pending, BlockedBy cleared, flag cleared) on its own daemon flag, consuming no
// retry budget.
func TestScanPreconditionBlocked_ResumesParkWithResultsButNoSignal(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := &GoalsFile{CurrentGoal: "goal-012", Goals: []Goal{
		{ID: "goal-012", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true,
			CodeRetries: 3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-012")
	require.NoError(t, err)

	// results.json present (orchestrator ledger), but NO signal.json — the pre-fix
	// stranded state. Confirm the signal really is absent.
	require.NoError(t, SaveResults(dir, "goal-012", &Results{Results: map[string]ResultEntry{}}))
	loaded, err := LoadSignal(dir, "goal-012")
	require.NoError(t, err)
	require.Nil(t, loaded, "repro precondition: no signal.json on disk")

	d.scanPreconditionBlocked()

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, found := reloaded.GoalByID("goal-012")
	require.True(t, found)
	assert.Equal(t, GoalPending, g.Status, "stranded env park resumes to pending via branch (b)")
	assert.Equal(t, "", g.BlockedBy, "BlockedBy cleared on resume")
	assert.False(t, g.BlockedByPrecondition, "precondition flag cleared on resume")
	assert.Equal(t, 3, g.CodeRetries, "resume consumed no code retry budget")
}

// TestScanPreconditionBlocked_PreflightParkResumesViaSignalClass — branch (a):
// a preflight-gate park (BlockedBy != "env_precondition") with a readable
// env-config signal resumes when its precondition now passes. Proves the flag-only
// fallback (b) is not the sole resume path.
func TestScanPreconditionBlocked_PreflightParkResumesViaSignalClass(t *testing.T) {
	d, _, dir := setupDaemon(t)
	const envVar = "TV_PRECOND_PREFLIGHT_RESUME"
	require.NoError(t, os.Setenv(envVar, "1"))
	defer os.Unsetenv(envVar)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "", BlockedByPrecondition: true,
			Preconditions: []Precondition{{Kind: "env", Spec: envVar, Remedy: "export " + envVar}},
			CodeRetries:   3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops",
		Findings:  []ValidationFinding{{Rule: "env:" + envVar, Status: "blocked", FailureClass: "env-config"}},
		Timestamp: "2026-05-20T10:00:00Z",
	}))

	d.scanPreconditionBlocked()

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalPending, g.Status, "precondition passes → resume via branch (a) signal class")
	assert.False(t, g.BlockedByPrecondition, "flag cleared on resume")
	assert.Equal(t, "", g.BlockedBy, "BlockedBy cleared on resume")
	assert.Equal(t, 3, g.CodeRetries, "resume consumed no code retry budget")
}

// TestScanPreconditionBlocked_CircuitBreakerNeverResumed — a circuit-breaker park
// (BlockedBy=="convergence-circuit-breaker", flag false) is excluded at the flag
// skip and must never be resumed by §5.
func TestScanPreconditionBlocked_CircuitBreakerNeverResumed(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "convergence-circuit-breaker",
			BlockedByPrecondition: false},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	d.scanPreconditionBlocked()

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status, "circuit-breaker park stays blocked")
	assert.Equal(t, "convergence-circuit-breaker", g.BlockedBy, "BlockedBy untouched")
	assert.False(t, g.BlockedByPrecondition, "flag stays false")
}

// TestScanPreconditionBlocked_StillFailingPreconditionStaysParked — an env
// precondition that still fails (var unset) with a matching signal stays blocked
// and flagged: eligible at the gate, but evaluatePreconditions returns false.
func TestScanPreconditionBlocked_StillFailingPreconditionStaysParked(t *testing.T) {
	d, _, dir := setupDaemon(t)
	const envVar = "TV_PRECOND_STILL_FAILING"
	require.NoError(t, os.Unsetenv(envVar))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true,
			Preconditions: []Precondition{{Kind: "env", Spec: envVar, Remedy: "export " + envVar}}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops",
		Findings:  []ValidationFinding{{Rule: "env:" + envVar, Status: "blocked", FailureClass: "env-config"}},
		Timestamp: "2026-05-20T10:00:00Z",
	}))

	d.scanPreconditionBlocked()

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status, "precondition still failing — stays blocked")
	assert.True(t, g.BlockedByPrecondition, "flag retained for next tick")
}

// TestScanPreconditionBlocked_NonPreconditionSignalNotResumed — branch (a)
// negative: a readable signal of a NON-precondition class with BlockedBy !=
// "env_precondition" is neither (a)-eligible (non-precond class) nor (b)-eligible
// (signal readable) → no spurious resume.
func TestScanPreconditionBlocked_NonPreconditionSignalNotResumed(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "", BlockedByPrecondition: true},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	// Readable signal of a NON-precondition class (code defect).
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "code-defect", Owner: "dev",
		Findings:  []ValidationFinding{{Rule: "phpstan", Status: "blocked", FailureClass: "code-defect"}},
		Timestamp: "2026-05-20T10:00:00Z",
	}))

	d.scanPreconditionBlocked()

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status, "non-precondition signal must not resume")
	assert.True(t, g.BlockedByPrecondition, "flag retained — no spurious resume")
}

package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ScopesDisjoint ---------------------------------------------------------

func TestScopesDisjoint_BothKnownNoOverlap_True(t *testing.T) {
	a := &Goal{Scope: []string{"internal/a/**"}}
	b := &Goal{Scope: []string{"internal/b/**"}}
	assert.True(t, ScopesDisjoint(a, b))
}

func TestScopesDisjoint_OverlappingDir_False(t *testing.T) {
	a := &Goal{Scope: []string{"internal/taskvisor/**"}}
	b := &Goal{Scope: []string{"internal/taskvisor/**"}}
	assert.False(t, ScopesDisjoint(a, b))
}

func TestScopesDisjoint_EitherUnknownScope_False(t *testing.T) {
	known := &Goal{Scope: []string{"internal/a/**"}}
	unknown := &Goal{}
	assert.False(t, ScopesDisjoint(known, unknown))
	assert.False(t, ScopesDisjoint(unknown, known))
	assert.False(t, ScopesDisjoint(unknown, unknown))
}

func TestScopesDisjoint_FileWithinDeclaredDir_False(t *testing.T) {
	dir := &Goal{Scope: []string{"internal/x/**"}}
	file := &Goal{Scope: []string{"internal/x/y.go"}}
	assert.False(t, ScopesDisjoint(dir, file))
}

// --- globsOverlap -----------------------------------------------------------

func TestGlobsOverlap_SiblingDirsSharedStringPrefix_NoOverlap(t *testing.T) {
	// internal/x and internal/xy share a string prefix but are distinct dirs.
	assert.False(t, globsOverlap("internal/x", "internal/xy"))
	assert.False(t, globsOverlap("internal/xy", "internal/x"))
}

func TestGlobsOverlap_NamespacePrefix_True(t *testing.T) {
	assert.True(t, globsOverlap(`App\Billing`, `App\Billing\Invoice`))
	assert.True(t, globsOverlap(`App\Billing\Invoice`, `App\Billing`))
}

// --- DisjointReadySet -------------------------------------------------------

func TestDisjointReadySet_MaxGoals1_NoneRunning_ReturnsFirstCandidate(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(1)
	require.Len(t, got, 1)
	// Byte-identical to the head of RunnableCandidates.
	assert.Equal(t, gf.RunnableCandidates()[0].ID, got[0].ID)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_MaxGoals1_OneRunning_ReturnsNil(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	assert.Nil(t, gf.DisjointReadySet(1))
}

func TestDisjointReadySet_TwoDisjoint_BothAdmitted(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 2)
	assert.Equal(t, "goal-001", got[0].ID)
	assert.Equal(t, "goal-002", got[1].ID)
}

func TestDisjointReadySet_TwoOverlapping_OnlyFirstAdmitted(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/a/sub/**"}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_UnknownScopeNoInflight_Admitted(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending}, // unknown scope
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(2)
	// First (unknown) admitted vacuously; it then blocks the rest.
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_UnknownScopeWithRunning_Serializes(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending}, // unknown scope
	}}
	assert.Nil(t, gf.DisjointReadySet(2))
}

func TestDisjointReadySet_RunningGoalConsumesBudget(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-002", got[0].ID)
}

func TestDisjointReadySet_RunningGoalOverlapsCandidate_Rejected(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/a/calc.go"}},
	}}
	assert.Nil(t, gf.DisjointReadySet(2))
}

func TestDisjointReadySet_DelegatesRunnableFilters(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		// precondition-parked
		{ID: "goal-001", Status: GoalPending, BlockedByPrecondition: true, Scope: []string{"internal/a/**"}},
		// blocked
		{ID: "goal-002", Status: GoalBlocked, Scope: []string{"internal/b/**"}},
		// unmet deps (depends on a non-done goal)
		{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}, Scope: []string{"internal/c/**"}},
	}}
	assert.Nil(t, gf.DisjointReadySet(2))
}

// --- Migrates exclusion (E1-1b) ---------------------------------------------

func TestDisjointReadySet_MigratesCandidate_NotAdmittedWhenInflightNonEmpty(t *testing.T) {
	// goal-001 is running; goal-002 is a migrating candidate with a disjoint
	// scope. Absent the migration exclusion the disjoint-scope gate would admit
	// it — the exclusion must defer it until the running set empties.
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}, Migrates: true},
	}}
	assert.Nil(t, gf.DisjointReadySet(2), "a migrating candidate must not join a non-empty in-flight set")
}

func TestDisjointReadySet_MigratesAdmittedAlone_BlocksLaterCandidates(t *testing.T) {
	// goal-001 is a migrating candidate; goal-002 has a disjoint scope. The
	// migrating goal is admitted (in-flight empty) but must run ALONE, so the
	// disjoint goal-002 is NOT co-admitted this tick.
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}, Migrates: true},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_InflightMigrates_AdmitsNothing(t *testing.T) {
	// A migrating goal is already in flight ⇒ no candidate may co-schedule with
	// it, regardless of scope disjointness.
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}, Migrates: true},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	assert.Nil(t, gf.DisjointReadySet(2), "no candidate admitted while a migrating goal is in flight")
}

func TestDisjointReadySet_MaxGoals1_MigratesByteIdentical(t *testing.T) {
	// At MaxGoals=1 a migrating goal dispatches exactly like any other head — the
	// exclusion adds no observable change.
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}, Migrates: true},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
	}}
	got := gf.DisjointReadySet(1)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

// --- YAML round-trip --------------------------------------------------------

func TestGoal_ScopeRoundTripsYAML(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "scoped", Status: GoalPending,
			Scope: []string{"internal/x/**", `App\Billing`}},
	}}
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, []string{"internal/x/**", `App\Billing`}, loaded.Goals[0].Scope)
}

// --- DeriveScopeFromDeliverables --------------------------------------------

func TestDeriveScopeFromDeliverables_ExtractsPathTokens(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Create `internal/taskvisor/goals.go` with the gate",
		"Update internal/mcp/server.go to plumb scope",
		"Add a flag --race to the runner",
	})
	assert.Equal(t, []string{"internal/taskvisor/goals.go", "internal/mcp/server.go"}, got)
}

func TestDeriveScopeFromDeliverables_NoPathTokens_ReturnsNil(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Make the daemon faster",
		"Improve documentation and prose only",
	})
	assert.Nil(t, got)
}

// --- isStackConsuming -------------------------------------------------------

func TestIsStackConsuming_EnsureTestStack(t *testing.T) {
	g := &Goal{Validate: []string{"bash bin/ensure-test-stack.sh"}}
	assert.True(t, isStackConsuming(g))
}

func TestIsStackConsuming_NpxPlaywright(t *testing.T) {
	g := &Goal{Validate: []string{"npx playwright test tests/E2E/LoginTest.ts"}}
	assert.True(t, isStackConsuming(g))
}

func TestIsStackConsuming_DockerCompose(t *testing.T) {
	g := &Goal{Validate: []string{"docker compose config --quiet"}}
	assert.True(t, isStackConsuming(g))
}

func TestIsStackConsuming_CurlHttpProbe(t *testing.T) {
	g := &Goal{Validate: []string{"curl -sf http://localhost:8080/health"}}
	assert.True(t, isStackConsuming(g))
}

func TestIsStackConsuming_AcceptanceLine(t *testing.T) {
	g := &Goal{Acceptance: []string{"ensure-test-stack.sh runs successfully"}}
	assert.True(t, isStackConsuming(g))
}

func TestIsStackConsuming_PureUnit(t *testing.T) {
	g := &Goal{Validate: []string{"go test ./internal/...", "vendor/bin/phpstan analyse src/"}}
	assert.False(t, isStackConsuming(g))
}

func TestIsStackConsuming_EmptyGoal(t *testing.T) {
	g := &Goal{}
	assert.False(t, isStackConsuming(g))
}

func TestIsStackConsuming_NoFalseNegativeOnSubstring(t *testing.T) {
	g := &Goal{Validate: []string{"bash bin/ensure-test-stack.sh && echo done"}}
	assert.True(t, isStackConsuming(g))
}

// --- coSchedulable + stack-consuming ----------------------------------------

func TestCoSchedulable_TwoStackConsumers_Rejected(t *testing.T) {
	candidate := &Goal{
		ID:       "goal-002",
		Scope:    []string{"internal/a/**"},
		Validate: []string{"bash bin/ensure-test-stack.sh"},
	}
	inflight := []*Goal{{
		ID:       "goal-001",
		Scope:    []string{"internal/b/**"},
		Validate: []string{"bash bin/ensure-test-stack.sh"},
	}}
	assert.False(t, coSchedulable(candidate, inflight))
}

func TestCoSchedulable_StackPlusUnit_Admitted(t *testing.T) {
	candidate := &Goal{
		ID:       "goal-002",
		Scope:    []string{"internal/a/**"},
		Validate: []string{"bash bin/ensure-test-stack.sh"},
	}
	inflight := []*Goal{{
		ID:       "goal-001",
		Scope:    []string{"internal/b/**"},
		Validate: []string{"go test ./internal/..."},
	}}
	assert.True(t, coSchedulable(candidate, inflight))
}

func TestCoSchedulable_UnitPlusUnit_Admitted(t *testing.T) {
	candidate := &Goal{
		ID:       "goal-002",
		Scope:    []string{"internal/a/**"},
		Validate: []string{"go test ./internal/a/..."},
	}
	inflight := []*Goal{{
		ID:       "goal-001",
		Scope:    []string{"internal/b/**"},
		Validate: []string{"go test ./internal/b/..."},
	}}
	assert.True(t, coSchedulable(candidate, inflight))
}

func TestCoSchedulable_StackConsumerUnknownScope_AlreadyRejected(t *testing.T) {
	candidate := &Goal{
		ID:       "goal-002",
		Validate: []string{"bash bin/ensure-test-stack.sh"},
	}
	inflight := []*Goal{{
		ID:       "goal-001",
		Scope:    []string{"internal/b/**"},
		Validate: []string{"bash bin/ensure-test-stack.sh"},
	}}
	assert.False(t, coSchedulable(candidate, inflight))
}

// --- DisjointReadySet + stack-consuming -------------------------------------

func TestDisjointReadySet_TwoStackConsumers_OnlyOneAdmitted(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_StackPlusUnit_BothAdmitted(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"},
			Validate: []string{"go test ./internal/b/..."}},
	}}
	got := gf.DisjointReadySet(2)
	require.Len(t, got, 2)
	assert.Equal(t, "goal-001", got[0].ID)
	assert.Equal(t, "goal-002", got[1].ID)
}

func TestDisjointReadySet_MaxGoals1_StackConsuming_ByteIdentical(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}},
	}}
	got := gf.DisjointReadySet(1)
	require.Len(t, got, 1)
	assert.Equal(t, "goal-001", got[0].ID)
}

func TestDisjointReadySet_MigratesAndStackConsuming_MigratesWins(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"},
			Validate: []string{"bash bin/ensure-test-stack.sh"}, Migrates: true},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"},
			Validate: []string{"go test ./internal/b/..."}},
	}}
	got := gf.DisjointReadySet(2)
	assert.Nil(t, got, "migration exclusion still prevents any co-scheduling")
}

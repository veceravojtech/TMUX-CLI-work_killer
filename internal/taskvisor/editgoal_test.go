package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- EditGoal: Tier-2 elaboration write-back core ----------------------------

// ptr is a tiny test helper: &v for any value, used to build the tri-state
// GoalEdit pointer fields (nil = leave untouched, non-nil = set/clear).
func ptr[T any](v T) *T { return &v }

func seedGoals(t *testing.T, dir string, gf *GoalsFile) {
	t.Helper()
	require.NoError(t, SaveGoals(dir, gf))
}

// TestEditGoal_SetsEachField proves a single EditGoal call writes acceptance,
// validate, scope, deliverable_area, phase, and status onto an existing roadmap
// goal and that LoadGoals reads them back.
func TestEditGoal_SetsEachField(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "Skeleton", Status: GoalRoadmap, DeliverableArea: "internal/api/"},
	}})

	err := EditGoal(dir, "goal-001", GoalEdit{
		Acceptance:      ptr([]string{"Returns 200", "Validates input"}),
		Validate:        ptr([]string{"go test ./internal/api/..."}),
		Scope:           ptr([]string{"internal/api/**"}),
		DeliverableArea: ptr("internal/api/v2/"),
		Phase:           ptr("application"),
		Status:          ptr(GoalPending),
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, []string{"Returns 200", "Validates input"}, g.Acceptance)
	assert.Equal(t, []string{"go test ./internal/api/..."}, g.Validate)
	assert.Equal(t, []string{"internal/api/**"}, g.Scope)
	assert.Equal(t, "internal/api/v2/", g.DeliverableArea)
	assert.Equal(t, "application", g.Phase)
	assert.Equal(t, GoalPending, g.Status)
}

// TestEditGoal_OnlyTouchesProvidedFields proves a nil edit field leaves the
// existing value intact while a provided field is replaced — the "present vs
// untouched" tri-state contract.
func TestEditGoal_OnlyTouchesProvidedFields(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{
			ID:          "goal-001",
			Description: "Has both",
			Status:      GoalRoadmap,
			Acceptance:  []string{"keep me"},
			Validate:    []string{"keep validate"},
			Phase:       "domain",
		},
	}})

	// Edit acceptance ONLY; everything else must survive.
	err := EditGoal(dir, "goal-001", GoalEdit{Acceptance: ptr([]string{"replaced"})})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := gf.GoalByID("goal-001")
	assert.Equal(t, []string{"replaced"}, g.Acceptance)
	assert.Equal(t, []string{"keep validate"}, g.Validate, "validate must be untouched (nil edit)")
	assert.Equal(t, "domain", g.Phase, "phase must be untouched (nil edit)")
	assert.Equal(t, GoalRoadmap, g.Status, "status must be untouched (nil edit)")
}

// TestEditGoal_EmptySliceClears proves a non-nil EMPTY slice clears the field —
// the explicit clear arm of the tri-state (distinct from nil = untouched).
func TestEditGoal_EmptySliceClears(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "Has scope", Status: GoalRoadmap, Scope: []string{"internal/x/**"}},
	}})

	err := EditGoal(dir, "goal-001", GoalEdit{Scope: ptr([]string{})})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := gf.GoalByID("goal-001")
	assert.Empty(t, g.Scope, "an explicit empty slice must CLEAR scope")
}

// TestEditGoal_AbsentGoalErrors proves an unknown goal id is a clear error and
// writes nothing.
func TestEditGoal_AbsentGoalErrors(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "Exists", Status: GoalRoadmap},
	}})

	err := EditGoal(dir, "goal-404", GoalEdit{Status: ptr(GoalPending)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal not found")

	// The existing goal is untouched.
	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := gf.GoalByID("goal-001")
	assert.Equal(t, GoalRoadmap, g.Status)
}

// TestEditGoal_AbsentGoalsFileErrors proves a missing goals.yaml is a clean
// "goal not found", not a panic.
func TestEditGoal_AbsentGoalsFileErrors(t *testing.T) {
	dir := t.TempDir()
	err := EditGoal(dir, "goal-001", GoalEdit{Status: ptr(GoalPending)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal not found")
}

// TestEditGoal_RejectsDaemonOwnedStatus proves the status guard refuses
// running/done/failed — daemon-owned lifecycle states an authoring tool must
// never write — and that the rejection persists nothing.
func TestEditGoal_RejectsDaemonOwnedStatus(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "Skeleton", Status: GoalRoadmap},
	}})

	for _, bad := range []string{GoalRunning, GoalDone, GoalFailed, "bogus"} {
		err := EditGoal(dir, "goal-001", GoalEdit{Status: ptr(bad)})
		require.Error(t, err, "status %q must be rejected", bad)
		assert.Contains(t, err.Error(), "not editable")
	}

	// Nothing was persisted: status stays roadmap.
	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := gf.GoalByID("goal-001")
	assert.Equal(t, GoalRoadmap, g.Status)
}

// TestEditGoal_AllowsEachEditableStatus proves roadmap/pending/blocked are all
// accepted target statuses.
func TestEditGoal_AllowsEachEditableStatus(t *testing.T) {
	dir := t.TempDir()
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "Skeleton", Status: GoalRoadmap},
	}})

	for _, ok := range []string{GoalPending, GoalBlocked, GoalRoadmap} {
		err := EditGoal(dir, "goal-001", GoalEdit{Status: ptr(ok)})
		require.NoError(t, err, "status %q must be accepted", ok)
		gf, _ := LoadGoals(dir)
		g, _ := gf.GoalByID("goal-001")
		assert.Equal(t, ok, g.Status)
	}
}

// TestEditGoal_PreservesUntouchedDurableFields is the dual-struct silent-erase
// regression: editing one field must NOT zero the daemon-owned durable state
// (retry counters, convergence streaks, lane, depends_on, escalation count).
// EditGoal goes through the canonical taskvisor.LoadGoals/SaveGoals path (full
// Goal struct), so every field survives — this guards that it never regresses to
// a partial write.
func TestEditGoal_PreservesUntouchedDurableFields(t *testing.T) {
	dir := t.TempDir()
	original := Goal{
		ID:                        "goal-002",
		Description:               "Durable",
		Status:                    GoalPending,
		Acceptance:                []string{"old acceptance"},
		MaxRetries:                5,
		CodeRetries:               3,
		SpecRetries:               2,
		ValidationRetries:         1,
		BlockRetries:              0,
		MaxCodeRetries:            5,
		MaxSpecRetries:            3,
		MaxValidationRetries:      2,
		MaxBlockRetries:           0,
		StuckRetries:              2,
		MaxStuckRetries:           3,
		ConvergenceSignatures:     []string{"sig-a", "sig-b"},
		ConvergenceStreak:         2,
		SpecConvergenceSignatures: []string{"spec-sig"},
		SpecConvergenceStreak:     1,
		Lane:                      LaneSolo,
		DependsOn:                 []string{"goal-001"},
		EscalationCount:           1,
		Priority:                  7,
	}
	seedGoals(t, dir, &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "dep", Status: GoalDone},
		original,
	}})

	// Capture the daemon's view BEFORE the edit (LoadGoals re-seed already applied).
	before, err := LoadGoals(dir)
	require.NoError(t, err)
	wantG, _ := before.GoalByID("goal-002")
	wantCopy := *wantG

	// Edit ONLY acceptance.
	require.NoError(t, EditGoal(dir, "goal-002", GoalEdit{Acceptance: ptr([]string{"new acceptance"})}))

	after, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := after.GoalByID("goal-002")

	assert.Equal(t, []string{"new acceptance"}, g.Acceptance, "acceptance was edited")
	// Every other durable field is byte-identical to the pre-edit daemon view.
	assert.Equal(t, wantCopy.CodeRetries, g.CodeRetries)
	assert.Equal(t, wantCopy.SpecRetries, g.SpecRetries)
	assert.Equal(t, wantCopy.ValidationRetries, g.ValidationRetries)
	assert.Equal(t, wantCopy.MaxCodeRetries, g.MaxCodeRetries)
	assert.Equal(t, wantCopy.MaxSpecRetries, g.MaxSpecRetries)
	assert.Equal(t, wantCopy.ConvergenceSignatures, g.ConvergenceSignatures)
	assert.Equal(t, wantCopy.ConvergenceStreak, g.ConvergenceStreak)
	assert.Equal(t, wantCopy.SpecConvergenceSignatures, g.SpecConvergenceSignatures)
	assert.Equal(t, wantCopy.SpecConvergenceStreak, g.SpecConvergenceStreak)
	assert.Equal(t, LaneSolo, g.Lane, "lane must survive an unrelated edit")
	assert.Equal(t, []string{"goal-001"}, g.DependsOn)
	assert.Equal(t, 1, g.EscalationCount)
	assert.Equal(t, 7, g.Priority)
}

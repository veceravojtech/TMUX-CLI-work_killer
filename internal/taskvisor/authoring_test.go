package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CreateGoal: shared authoring core (F5) ----------------------------------

func TestCreateGoal_RejectsLongDescription(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{
		Description: strings.Repeat("x", 121),
		Validate:    []string{"go test ./..."},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "120")
	assert.Contains(t, err.Error(), "--acceptance")

	_, statErr := os.Stat(GoalsFilePath(dir))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be written on validation failure")
}

func TestCreateGoal_RejectsEmptyDescription(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{Validate: []string{"check"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "description cannot be empty")
}

func TestCreateGoal_RequiresValidate(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{Description: "No validate rules"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation rule")

	_, statErr := os.Stat(GoalsFilePath(dir))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be written on validation failure")
}

// TestCreateGoal_PersistsStructuredFields is the RC-A core fix: acceptance and
// validate are persisted INTO goals.yaml (the daemon reads them from there —
// EnsureInvestigationConfig, own-suite derivation), not dropped to goal.md prose.
func TestCreateGoal_PersistsStructuredFields(t *testing.T) {
	dir := t.TempDir()

	id, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Build API endpoint",
		Acceptance:  []string{"Returns 200 on success", "Validates input"},
		Validate:    []string{"go test ./...", "curl http://localhost/api"},
		Context:     "Legacy code needs cleanup",
		NotInScope:  "Performance tuning",
		Phase:       "domain",
		MaxRetries:  3,
	})

	require.NoError(t, err)
	assert.Equal(t, "goal-001", id)
	assert.False(t, derived)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	assert.Equal(t, "Build API endpoint", g.Description)
	assert.Equal(t, []string{"Returns 200 on success", "Validates input"}, g.Acceptance)
	assert.Equal(t, []string{"go test ./...", "curl http://localhost/api"}, g.Validate)
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, 3, g.MaxRetries)
	assert.Equal(t, "domain", g.Phase)

	// Context/NotInScope have no structured Goal fields (goals.go is owned by
	// sibling work this wave) — they land in goal.md prose, asserted below.
	md, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", id, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "Legacy code needs cleanup")
	assert.Contains(t, string(md), "Performance tuning")
}

// TestCreateGoal_WritesGoalMD pins byte-compatibility: the core must produce a
// goal.md identical to a direct WriteGoalMD call with the same arguments.
func TestCreateGoal_WritesGoalMD(t *testing.T) {
	dir := t.TempDir()

	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Build API endpoint",
		Acceptance:  []string{"Returns 200 on success"},
		Validate:    []string{"go test ./..."},
		Context:     "Legacy code",
		NotInScope:  "Performance",
		Phase:       "domain",
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", id, "goal.md"))
	require.NoError(t, err)

	refRoot := t.TempDir()
	refGoalDir := filepath.Join(refRoot, ".tmux-cli", "goals", id)
	require.NoError(t, os.MkdirAll(refGoalDir, 0o755))
	require.NoError(t, WriteGoalMD(refGoalDir, "Build API endpoint", "domain",
		[]string{"Returns 200 on success"}, []string{"go test ./..."}, nil,
		"Legacy code", "Performance", nil))
	want, err := os.ReadFile(filepath.Join(refGoalDir, "goal.md"))
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got), "goal.md must stay byte-compatible with WriteGoalMD")
}

func TestCreateGoal_LockedAppend(t *testing.T) {
	dir := t.TempDir()

	id1, _, err := CreateGoal(dir, GoalSpec{Description: "First", Validate: []string{"check"}})
	require.NoError(t, err)
	id2, _, err := CreateGoal(dir, GoalSpec{Description: "Second", Validate: []string{"check"}})
	require.NoError(t, err)

	assert.Equal(t, "goal-001", id1)
	assert.Equal(t, "goal-002", id2)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "goal-002", gf.Goals[1].ID)
}

func TestCreateGoal_ExplicitScopePersisted(t *testing.T) {
	dir := t.TempDir()

	scope := []string{"internal/x/**", `App\Billing`}
	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Scoped goal",
		Acceptance:  []string{"Create internal/y/file.go"}, // must NOT override explicit scope
		Validate:    []string{"check"},
		Scope:       scope,
	})
	require.NoError(t, err)
	assert.False(t, derived, "explicit scope is never reported as derived")

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, scope, gf.Goals[0].Scope)
}

func TestCreateGoal_ScopeDerivedFromAcceptance(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Derived scope goal",
		Acceptance:  []string{"Create `internal/x/file.go` with the gate", "Update internal/mcp/server.go"},
		Validate:    []string{"go test ./..."},
	})
	require.NoError(t, err)
	assert.True(t, derived)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/x/file.go", "internal/mcp/server.go"}, gf.Goals[0].Scope)
}

// TestCreateGoal_CompleteDerivationTrusted: when every non-empty acceptance
// line names a path and no explicit scope is given, the full derived scope is
// persisted and the bool return is true.
func TestCreateGoal_CompleteDerivationTrusted(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Complete derivation goal",
		Acceptance:  []string{"Edit internal/x/cors.go", "Update internal/x/ratelimit.go"},
		Validate:    []string{"go build ./..."},
	})
	require.NoError(t, err)
	assert.True(t, derived, "a complete derivation must report a derived scope")

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/x/cors.go", "internal/x/ratelimit.go"}, gf.Goals[0].Scope)
}

// TestCreateGoal_IncompleteDerivationDowngradedToUnknown: one bare acceptance
// line makes the derivation incomplete; the partial scope is DISCARDED and the
// goal persists with UNKNOWN scope so it serializes. bool return is false.
func TestCreateGoal_IncompleteDerivationDowngradedToUnknown(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Incomplete derivation goal",
		Acceptance:  []string{"Edit internal/x/cors.go", "Return the request id header"},
		Validate:    []string{"go build ./..."},
	})
	require.NoError(t, err)
	assert.False(t, derived, "an incomplete derivation must not be reported as derived")

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Empty(t, gf.Goals[0].Scope, "incomplete derivation is downgraded to UNKNOWN")
	assert.False(t, gf.Goals[0].HasKnownScope(), "UNKNOWN scope must serialize the goal")
}

// TestCreateGoal_ExplicitScopeAlwaysTrusted: an explicit scope is persisted
// verbatim even with sparse acceptance, and is never downgraded. The bool is
// false because the scope was explicit, not derived.
func TestCreateGoal_ExplicitScopeAlwaysTrusted(t *testing.T) {
	dir := t.TempDir()

	scope := []string{"internal/x"}
	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Explicit scope, sparse acceptance",
		Acceptance:  []string{"Edit internal/x/cors.go", "Return the request id header"},
		Validate:    []string{"go build ./..."},
		Scope:       scope,
	})
	require.NoError(t, err)
	assert.False(t, derived, "explicit scope is never reported as derived")

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, scope, gf.Goals[0].Scope, "explicit scope is persisted verbatim, never downgraded")
}

func TestCreateGoal_ScopeUnknownStaysNil(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Prose-only goal",
		Acceptance:  []string{"Make the daemon faster"},
		Validate:    []string{"benchmarks improve"},
	})
	require.NoError(t, err)
	assert.False(t, derived)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Nil(t, gf.Goals[0].Scope, "unknown scope stays nil so the scheduler serializes the goal")
}

func TestCreateGoal_MaxRetriesZeroCoercesToFive(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{Description: "Default budget", Validate: []string{"check"}})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestCreateGoal_DependsOnMustExist(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{
		Description: "Orphan goal",
		Validate:    []string{"check"},
		DependsOn:   []string{"goal-999"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-existent")
	assert.Contains(t, err.Error(), "goal-999")
}

func TestCreateGoal_DependsOnExistingPersisted(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{Description: "Prereq", Validate: []string{"check"}})
	require.NoError(t, err)

	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Dependent",
		Validate:    []string{"check"},
		DependsOn:   []string{"goal-001"},
	})
	require.NoError(t, err)
	assert.Equal(t, "goal-002", id)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-001"}, gf.Goals[1].DependsOn)
}

// --- StuckRetries in CreateGoal -------------------------------------------

func TestCreateGoal_SetsMaxStuckRetries(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{
		Description:     "Goal with stuck budget",
		Validate:        []string{"go test ./..."},
		MaxStuckRetries: 3,
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, 3, gf.Goals[0].MaxStuckRetries, "MaxStuckRetries must be set from GoalSpec")
	assert.Equal(t, 3, gf.Goals[0].StuckRetries, "StuckRetries must be seeded to MaxStuckRetries")
}

func TestCreateGoal_DefaultsMaxStuckRetries(t *testing.T) {
	dir := t.TempDir()

	_, _, err := CreateGoal(dir, GoalSpec{
		Description: "Goal without stuck budget",
		Validate:    []string{"go test ./..."},
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, 3, gf.Goals[0].MaxStuckRetries, "MaxStuckRetries must default to 3")
	assert.Equal(t, 3, gf.Goals[0].StuckRetries, "StuckRetries must be seeded to default MaxStuckRetries")
}

func TestCreateGoal_PreconditionsPersisted(t *testing.T) {
	dir := t.TempDir()

	pre := []Precondition{{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN"}}
	id, _, err := CreateGoal(dir, GoalSpec{
		Description:   "Setup DB",
		Validate:      []string{"check"},
		Preconditions: pre,
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, pre, gf.Goals[0].Preconditions)

	md, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", id, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "- [env] DB_DSN — export DB_DSN")
}

package taskvisor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireNoLaneOnDisk is the negative complement of requireLaneOnDisk: it
// asserts a lane-omitted goal carries NO `lane:` key in goals.yaml and NO
// `## Lane` section in goal.md (the omitempty zero-change contract), while
// LaneOrFull still resolves to full.
func requireNoLaneOnDisk(t *testing.T, dir, goalID string) {
	t.Helper()
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID(goalID)
	require.True(t, ok)
	assert.Equal(t, "", g.Lane, "goals.yaml lane for %s must stay empty", goalID)
	assert.Equal(t, LaneFull, g.LaneOrFull(), "empty lane resolves to full for %s", goalID)
	raw, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "lane:", "no lane: key may be emitted for lane-omitted %s", goalID)
}

func TestGoalCreate_AutoDerivesSoloForLocalizedPureCommandSpec(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Localized pure-command goal",
		Acceptance:  []string{"edits internal/foo/bar.go"},
		Validate:    []string{"grep -rq X internal/foo", "go test ./internal/foo/..."},
		Priority:    0,
	})
	require.NoError(t, err)

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID(id)
	require.True(t, ok)
	assert.Equal(t, LaneSolo, g.Lane, "single-dir all-pure low-priority spec must auto-derive solo")
	requireLaneOnDisk(t, dir, id, LaneSolo)
}

func TestGoalCreate_AutoDerivesFullForMultiDirSpec(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Multi-dir goal",
		Acceptance:  []string{"edits internal/a/x.go", "edits cmd/b/y.go"},
		Validate:    []string{"go build ./..."},
		Priority:    0,
	})
	require.NoError(t, err)

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID(id)
	require.True(t, ok)
	assert.Equal(t, "", g.Lane)
	assert.Equal(t, LaneFull, g.LaneOrFull())
	requireNoLaneOnDisk(t, dir, id)
}

func TestGoalCreate_AutoDerivesFullForNewFileMarker(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "New-file goal",
		Acceptance:  []string{"create new file internal/foo/new.go"},
		Validate:    []string{"go build ./..."},
		Priority:    0,
	})
	require.NoError(t, err)
	requireNoLaneOnDisk(t, dir, id)
}

func TestGoalCreate_AutoDerivesFullForSemanticValidate(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Semantic-validate goal",
		Acceptance:  []string{"edits internal/foo/bar.go"},
		Validate:    []string{"bin/console debug:router"},
		Priority:    0,
	})
	require.NoError(t, err)
	requireNoLaneOnDisk(t, dir, id)
}

func TestGoalCreate_AutoDerivesFullForCriticalPriority(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Critical-priority goal",
		Acceptance:  []string{"edits internal/foo/bar.go"},
		Validate:    []string{"grep -rq X internal/foo", "go test ./internal/foo/..."},
		Priority:    criticalPriorityTier,
	})
	require.NoError(t, err)
	requireNoLaneOnDisk(t, dir, id)
}

func TestGoalCreate_AutoDerivesFullForNoScope(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "No-scope goal",
		Acceptance:  []string{"does a thing"},
		Validate:    []string{"true"},
		Priority:    0,
	})
	require.NoError(t, err)
	requireNoLaneOnDisk(t, dir, id)
}

func TestGoalCreate_AutoDeriveNeverOverridesExplicitLane(t *testing.T) {
	// Explicit solo on a would-be-full multi-dir spec stays solo.
	dirSolo := t.TempDir()
	idSolo, _, err := CreateGoal(dirSolo, GoalSpec{
		Description: "Explicit solo, multi-dir spec",
		Acceptance:  []string{"edits internal/a/x.go", "edits cmd/b/y.go"},
		Validate:    []string{"go build ./..."},
		Lane:        LaneSolo,
	})
	require.NoError(t, err)
	requireLaneOnDisk(t, dirSolo, idSolo, LaneSolo)

	// Explicit full on a would-be-solo localized spec stays full.
	dirFull := t.TempDir()
	idFull, _, err := CreateGoal(dirFull, GoalSpec{
		Description: "Explicit full, localized spec",
		Acceptance:  []string{"edits internal/foo/bar.go"},
		Validate:    []string{"grep -rq X internal/foo", "go test ./internal/foo/..."},
		Lane:        LaneFull,
	})
	require.NoError(t, err)
	requireLaneOnDisk(t, dirFull, idFull, LaneFull)
}

// Direct-predicate coverage: AutoDeriveLane is the exported seam CreateGoal
// calls; it returns LaneSolo only when the gate holds and "" otherwise.
func TestAutoDeriveLane_SeamReturnsLane(t *testing.T) {
	dir := t.TempDir()
	solo := AutoDeriveLane(dir, GoalSpec{
		Acceptance: []string{"edits internal/foo/bar.go"},
		Validate:   []string{"grep -rq X internal/foo"},
	}, []string{"internal/foo/bar.go"})
	assert.Equal(t, LaneSolo, solo)

	full := AutoDeriveLane(dir, GoalSpec{
		Acceptance: []string{"edits internal/a/x.go"},
		Validate:   []string{"go build ./..."},
	}, []string{"internal/a/x.go", "cmd/b/y.go"})
	assert.Equal(t, "", full)
}

package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGoals_Missing(t *testing.T) {
	root := t.TempDir()
	gf, err := LoadGoals(root)
	assert.Nil(t, gf)
	assert.NoError(t, err)
}

func TestLoadGoals_InvalidYAML(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(":::invalid"), 0o644))

	gf, err := LoadGoals(root)
	assert.Nil(t, gf)
	assert.Error(t, err)
}

func TestLoadGoals_Valid(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Test goal"
    acceptance:
      - "Criterion A"
      - "Criterion B"
    validate:
      - "Run tests"
    status: pending
    retries: 0
    max_retries: 3
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, "goal-001", gf.CurrentGoal)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	assert.Equal(t, "goal-001", g.ID)
	assert.Equal(t, "Test goal", g.Description)
	assert.Equal(t, []string{"Criterion A", "Criterion B"}, g.Acceptance)
	assert.Equal(t, []string{"Run tests"}, g.Validate)
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, 0, g.Retries)
	assert.Equal(t, 3, g.MaxRetries)
}

func TestSaveGoals_CreatesDir(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Test",
			Status:      GoalPending,
			MaxRetries:  3,
		}},
	}
	err := SaveGoals(root, gf)
	require.NoError(t, err)

	_, err = os.Stat(GoalsFilePath(root))
	assert.NoError(t, err)
}

func TestSaveGoals_AtomicWrite(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Status: GoalPending}},
	}
	require.NoError(t, SaveGoals(root, gf))

	tmpPath := GoalsFilePath(root) + ".tmp"
	_, err := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain after save")
}

func TestSaveGoals_Roundtrip(t *testing.T) {
	root := t.TempDir()
	original := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{
				ID:          "goal-001",
				Description: "First goal",
				Acceptance:  []string{"A1", "A2"},
				Validate:    []string{"V1"},
				Status:      GoalDone,
				Retries:     1,
				MaxRetries:  3,
			},
			{
				ID:          "goal-002",
				Description: "Second goal",
				Acceptance:  []string{"B1"},
				Validate:    []string{"V2", "V3"},
				Status:      GoalPending,
				Retries:     0,
				MaxRetries:  5,
			},
		},
	}
	require.NoError(t, SaveGoals(root, original))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, original.CurrentGoal, loaded.CurrentGoal)
	require.Len(t, loaded.Goals, 2)
	assert.Equal(t, original.Goals[0], loaded.Goals[0])
	assert.Equal(t, original.Goals[1], loaded.Goals[1])
}

func TestNextGoalID_Empty(t *testing.T) {
	id := NextGoalID(nil)
	assert.Equal(t, "goal-001", id)
}

func TestNextGoalID_Existing(t *testing.T) {
	goals := []Goal{
		{ID: "goal-001"},
		{ID: "goal-002"},
		{ID: "goal-003"},
	}
	id := NextGoalID(goals)
	assert.Equal(t, "goal-004", id)
}

func TestNextGoalID_NonSequential(t *testing.T) {
	goals := []Goal{
		{ID: "goal-001"},
		{ID: "goal-003"},
	}
	id := NextGoalID(goals)
	assert.Equal(t, "goal-004", id)
}

func TestEnsureGoalDir_Creates(t *testing.T) {
	root := t.TempDir()
	dir, err := EnsureGoalDir(root, "goal-001")
	require.NoError(t, err)

	expected := filepath.Join(root, ".tmux-cli", "goals", "goal-001")
	assert.Equal(t, expected, dir)

	info, err := os.Stat(expected)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	corrDir := filepath.Join(expected, "corrections")
	info, err = os.Stat(corrDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureGoalDir_Idempotent(t *testing.T) {
	root := t.TempDir()
	dir1, err1 := EnsureGoalDir(root, "goal-001")
	require.NoError(t, err1)
	dir2, err2 := EnsureGoalDir(root, "goal-001")
	require.NoError(t, err2)
	assert.Equal(t, dir1, dir2)
}

func TestGoalByID_Found(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "First"},
			{ID: "goal-002", Description: "Second"},
		},
	}
	g, ok := gf.GoalByID("goal-002")
	assert.True(t, ok)
	require.NotNil(t, g)
	assert.Equal(t, "Second", g.Description)
}

func TestGoalByID_NotFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001"}},
	}
	g, ok := gf.GoalByID("goal-999")
	assert.False(t, ok)
	assert.Nil(t, g)
}

func TestNextPendingGoal_Found(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalPending},
			{ID: "goal-003", Status: GoalPending},
		},
	}
	g, ok := gf.NextPendingGoal()
	assert.True(t, ok)
	require.NotNil(t, g)
	assert.Equal(t, "goal-002", g.ID)
}

func TestNextPendingGoal_NoneLeft(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalFailed},
		},
	}
	g, ok := gf.NextPendingGoal()
	assert.False(t, ok)
	assert.Nil(t, g)
}

func TestSetStatus_Found(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalPending},
		},
	}
	ok := gf.SetStatus("goal-001", GoalRunning)
	assert.True(t, ok)
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
}

func TestSetStatus_NotFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Status: GoalPending}},
	}
	ok := gf.SetStatus("goal-999", GoalDone)
	assert.False(t, ok)
	assert.Equal(t, GoalPending, gf.Goals[0].Status)
}

func TestIncrementRetries(t *testing.T) {
	g := &Goal{Retries: 1}
	n := g.IncrementRetries()
	assert.Equal(t, 2, n)
	assert.Equal(t, 2, g.Retries)
}

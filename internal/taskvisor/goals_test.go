package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestLoadGoals_LongDescriptionReadTolerance(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	longDesc := strings.Repeat("z", 200)
	content := fmt.Sprintf(`goals:
  - id: "goal-001"
    description: "%s"
    status: pending
    max_retries: 3
`, longDesc)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, longDesc, gf.Goals[0].Description)
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

func TestDeleteGoal_Found(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "First"},
			{ID: "goal-002", Description: "Second"},
			{ID: "goal-003", Description: "Third"},
		},
	}
	removed, ok := gf.DeleteGoal("goal-002")
	assert.True(t, ok)
	require.NotNil(t, removed)
	assert.Equal(t, "goal-002", removed.ID)
	assert.Equal(t, "Second", removed.Description)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "goal-003", gf.Goals[1].ID)
}

func TestDeleteGoal_NotFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "First"},
		},
	}
	removed, ok := gf.DeleteGoal("goal-999")
	assert.False(t, ok)
	assert.Nil(t, removed)
	require.Len(t, gf.Goals, 1)
}

func TestDeleteGoal_ClearsCurrentGoal(t *testing.T) {
	gf := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{ID: "goal-001", Description: "First"},
			{ID: "goal-002", Description: "Second"},
		},
	}
	removed, ok := gf.DeleteGoal("goal-002")
	assert.True(t, ok)
	require.NotNil(t, removed)
	assert.Equal(t, "", gf.CurrentGoal)
	require.Len(t, gf.Goals, 1)
}

func TestDeleteGoal_PreservesCurrentGoal(t *testing.T) {
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First"},
			{ID: "goal-002", Description: "Second"},
		},
	}
	removed, ok := gf.DeleteGoal("goal-002")
	assert.True(t, ok)
	require.NotNil(t, removed)
	assert.Equal(t, "goal-001", gf.CurrentGoal)
	require.Len(t, gf.Goals, 1)
}

func TestResetGoal_FailedGoal(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Status:     GoalFailed,
				Retries:    2,
				FinishedAt: "2026-05-20T15:00:00Z",
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	assert.Equal(t, GoalPending, gf.Goals[0].Status)
	assert.Equal(t, 0, gf.Goals[0].Retries)
	assert.Equal(t, "", gf.Goals[0].FinishedAt)
}

func TestResetGoal_NotFailed(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone, Retries: 1},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.False(t, ok)
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, 1, gf.Goals[0].Retries)
}

func TestResetGoal_NotFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed},
		},
	}
	ok := gf.ResetGoal("goal-999")
	assert.False(t, ok)
}

func TestResetGoal_PreservesOtherFields(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:          "goal-001",
				Description: "My goal",
				Acceptance:  []string{"A1", "A2"},
				Validate:    []string{"V1"},
				Status:      GoalFailed,
				Retries:     3,
				MaxRetries:  5,
				StartedAt:   "2026-05-20T14:00:00Z",
				FinishedAt:  "2026-05-20T15:00:00Z",
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	g := gf.Goals[0]
	assert.Equal(t, "My goal", g.Description)
	assert.Equal(t, []string{"A1", "A2"}, g.Acceptance)
	assert.Equal(t, []string{"V1"}, g.Validate)
	assert.Equal(t, 5, g.MaxRetries)
	assert.Equal(t, "2026-05-20T14:00:00Z", g.StartedAt)
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, 0, g.Retries)
	assert.Equal(t, "", g.FinishedAt)
}

func TestSkipGoal_RunningGoal(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:        "goal-001",
				Status:    GoalRunning,
				StartedAt: "2026-05-20T14:00:00Z",
			},
		},
	}
	ok := gf.SkipGoal("goal-001")
	assert.True(t, ok)
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.NotEmpty(t, gf.Goals[0].FinishedAt)
}

func TestSkipGoal_NotRunning(t *testing.T) {
	for _, status := range []string{GoalPending, GoalDone, GoalFailed} {
		t.Run(status, func(t *testing.T) {
			gf := &GoalsFile{
				Goals: []Goal{
					{ID: "goal-001", Status: status},
				},
			}
			ok := gf.SkipGoal("goal-001")
			assert.False(t, ok)
			assert.Equal(t, status, gf.Goals[0].Status)
		})
	}
}

func TestSkipGoal_NotFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning},
		},
	}
	ok := gf.SkipGoal("goal-999")
	assert.False(t, ok)
}

func TestSkipGoal_SetsFinishedAt(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning},
		},
	}
	before := time.Now().UTC()
	ok := gf.SkipGoal("goal-001")
	after := time.Now().UTC()
	require.True(t, ok)

	parsed, err := time.Parse(time.RFC3339, gf.Goals[0].FinishedAt)
	require.NoError(t, err)
	assert.False(t, parsed.Before(before.Add(-2*time.Second)))
	assert.False(t, parsed.After(after.Add(2*time.Second)))
}

func TestGoalTimingFields_Roundtrip(t *testing.T) {
	root := t.TempDir()
	now := "2026-05-20T15:00:00Z"
	later := "2026-05-20T15:30:00Z"
	original := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID:         "goal-001",
			Status:     GoalDone,
			StartedAt:  now,
			FinishedAt: later,
		}},
	}
	require.NoError(t, SaveGoals(root, original))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, now, loaded.Goals[0].StartedAt)
	assert.Equal(t, later, loaded.Goals[0].FinishedAt)
}

func TestWriteGoalMD_AllSections(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Fix prices", []string{"Price matches API", "No rounding errors"}, []string{"go test ./...", "curl check"}, "We need accurate pricing", "UI redesign")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Fix prices")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "- Price matches API")
	assert.Contains(t, content, "- No rounding errors")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "- go test ./...")
	assert.Contains(t, content, "- curl check")
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "We need accurate pricing")
	assert.Contains(t, content, "## Not In Scope")
	assert.Contains(t, content, "UI redesign")
}

func TestWriteGoalMD_AcceptanceOnly(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Build API", []string{"Returns 200"}, nil, "", "")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Build API")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "- Returns 200")
	assert.NotContains(t, content, "## Validation Rules")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_NoCriteria(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Simple goal", nil, nil, "", "")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Simple goal")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.NotContains(t, content, "## Validation Rules")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_ContextAndNotInScope(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Refactor module", nil, nil, "Legacy code needs cleanup", "Performance tuning")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "Legacy code needs cleanup")
	assert.Contains(t, content, "## Not In Scope")
	assert.Contains(t, content, "Performance tuning")
	assert.NotContains(t, content, "## Validation Rules")
}

func TestWriteGoalMD_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Test atomic", []string{"A1"}, nil, "", "")
	require.NoError(t, err)

	tmpPath := filepath.Join(dir, "goal.md.tmp")
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain after write")
}

func TestWriteGoalMD_MarkdownFormat(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Format check", []string{"Criterion A", "Criterion B"}, []string{"validate cmd"}, "Some context", "Out of scope")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")

	assert.Equal(t, "# Format check", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "## Acceptance Criteria", lines[2])
	assert.Equal(t, "", lines[3])
	assert.Equal(t, "- Criterion A", lines[4])
	assert.Equal(t, "- Criterion B", lines[5])
	assert.Equal(t, "", lines[6])
	assert.Equal(t, "## Validation Rules", lines[7])
	assert.Equal(t, "", lines[8])
	assert.Equal(t, "- validate cmd", lines[9])
	assert.Equal(t, "", lines[10])
	assert.Equal(t, "## Context", lines[11])
	assert.Equal(t, "", lines[12])
	assert.Equal(t, "Some context", lines[13])
	assert.Equal(t, "", lines[14])
	assert.Equal(t, "## Not In Scope", lines[15])
	assert.Equal(t, "", lines[16])
	assert.Equal(t, "Out of scope", lines[17])
}

func TestGoalTimingFields_OmitEmpty(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID:     "goal-001",
			Status: GoalPending,
		}},
	}
	require.NoError(t, SaveGoals(root, gf))

	data, err := os.ReadFile(GoalsFilePath(root))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "started_at")
	assert.NotContains(t, string(data), "finished_at")
}

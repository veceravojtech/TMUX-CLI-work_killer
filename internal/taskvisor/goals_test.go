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

func TestHasResumablePark(t *testing.T) {
	tests := []struct {
		name  string
		goals []Goal
		want  bool
	}{
		{
			name:  "empty",
			goals: nil,
			want:  false,
		},
		{
			name: "no resumable park",
			goals: []Goal{
				{ID: "goal-1", Status: GoalDone},
				{ID: "goal-2", Status: GoalBlocked, BlockedBy: "external"},
				{ID: "goal-3", Status: GoalPending},
			},
			want: false,
		},
		{
			name: "one resumable park",
			goals: []Goal{
				{ID: "goal-1", Status: GoalDone},
				{ID: "goal-2", Status: GoalBlocked, BlockedByPrecondition: true},
			},
			want: true,
		},
		{
			name: "park flag set without explicit BlockedBy",
			goals: []Goal{
				{ID: "goal-1", Status: GoalBlocked, BlockedBy: "", BlockedByPrecondition: true},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gf := &GoalsFile{Goals: tt.goals}
			assert.Equal(t, tt.want, gf.HasResumablePark())
		})
	}
}

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
				// Per-class live counters decrement toward zero: live = REMAINING
				// budget, Max… = configured start. LoadGoals seeds an unstarted
				// goal's live counters to the full Max… budget. Pre-populating both
				// live and Max to that budget (live == Max) keeps the save→load
				// roundtrip exact: the non-zero live guard then skips migration.
				CodeRetries:          3,
				SpecRetries:          1,
				ValidationRetries:    1,
				MaxCodeRetries:       3,
				MaxSpecRetries:       1,
				MaxValidationRetries: 1,
			},
			{
				ID:                   "goal-002",
				Description:          "Second goal",
				Acceptance:           []string{"B1"},
				Validate:             []string{"V2", "V3"},
				Status:               GoalPending,
				Retries:              0,
				MaxRetries:           5,
				CodeRetries:          5,
				SpecRetries:          2,
				ValidationRetries:    1,
				MaxCodeRetries:       5,
				MaxSpecRetries:       2,
				MaxValidationRetries: 1,
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
	assert.Equal(t, "", g.StartedAt)
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
	err := WriteGoalMD(dir, "Fix prices", "", []string{"Price matches API", "No rounding errors"}, []string{"go test ./...", "curl check"}, nil, "We need accurate pricing", "UI redesign", nil)
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
	assert.NotContains(t, content, "## Phase")
}

func TestWriteGoalMD_WithPhase(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Setup DB", "infrastructure", []string{"Tables exist"}, []string{"check"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Phase")
	assert.Contains(t, content, "infrastructure")
	lines := strings.Split(content, "\n")
	phaseIdx := -1
	acIdx := -1
	for i, l := range lines {
		if l == "## Phase" {
			phaseIdx = i
		}
		if l == "## Acceptance Criteria" {
			acIdx = i
		}
	}
	assert.Greater(t, phaseIdx, 0, "Phase section should exist")
	assert.Greater(t, acIdx, phaseIdx, "Phase must appear before Acceptance Criteria")
}

func TestWriteGoalMD_EmptyPhaseOmitted(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "No phase goal", "", []string{"AC1"}, []string{"check"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "## Phase")
}

func TestWriteGoalMD_AcceptanceOnly(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Build API", "", []string{"Returns 200"}, nil, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Build API")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "- Returns 200")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_NoCriteria(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Simple goal", "", nil, nil, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Simple goal")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_ContextAndNotInScope(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Refactor module", "", nil, nil, nil, "Legacy code needs cleanup", "Performance tuning", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "Legacy code needs cleanup")
	assert.Contains(t, content, "## Not In Scope")
	assert.Contains(t, content, "Performance tuning")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
}

func TestWriteGoalMD_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Test atomic", "", []string{"A1"}, nil, nil, "", "", nil)
	require.NoError(t, err)

	tmpPath := filepath.Join(dir, "goal.md.tmp")
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain after write")
}

func TestWriteGoalMD_MarkdownFormat(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Format check", "", []string{"Criterion A", "Criterion B"}, []string{"validate cmd"}, nil, "Some context", "Out of scope", nil)
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

func TestWriteGoalMD_MarkdownFormatWithPhase(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Phase check", "domain", []string{"Criterion A"}, []string{"validate cmd"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")

	assert.Equal(t, "# Phase check", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "## Phase", lines[2])
	assert.Equal(t, "", lines[3])
	assert.Equal(t, "domain", lines[4])
	assert.Equal(t, "", lines[5])
	assert.Equal(t, "## Acceptance Criteria", lines[6])
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

func TestMigrateRetries_LegacyGoal(t *testing.T) {
	// old=4 → Spec=max(2,(4+1)/2)=max(2,2)=2, Val=2.
	b := MigrateRetries(4)
	assert.Equal(t, 4, b.CodeRetries)
	assert.Equal(t, 2, b.SpecRetries)
	assert.Equal(t, 2, b.ValidationRetries)
	assert.Equal(t, 0, b.BlockRetries)
}

func TestMigrateRetries_CeilBoundary(t *testing.T) {
	// old=3 → Spec=max(2,(3+1)/2)=max(2,2)=2 (Spec floor of 2 means no instant fail).
	assert.Equal(t, 2, MigrateRetries(3).SpecRetries)

	z := MigrateRetries(0)
	assert.Equal(t, 0, z.CodeRetries)
	assert.Equal(t, 2, z.SpecRetries)
	assert.Equal(t, 2, z.ValidationRetries)
	assert.Equal(t, 0, z.BlockRetries)
}

func TestMigrateRetries_NegativeClampsToZero(t *testing.T) {
	b := MigrateRetries(-2)
	assert.Equal(t, 0, b.CodeRetries)
	assert.Equal(t, 2, b.SpecRetries)
	assert.Equal(t, 2, b.ValidationRetries)
	assert.Equal(t, 0, b.BlockRetries)
}

// TestMigrateRetries_DefaultFiveGivesCode5Spec3Val2 pins the new default:
// old=5 → Code 5 / Spec max(2,3)=3 / Val 2 / Block 0.
func TestMigrateRetries_DefaultFiveGivesCode5Spec3Val2(t *testing.T) {
	b := MigrateRetries(5)
	assert.Equal(t, 5, b.CodeRetries)
	assert.Equal(t, 3, b.SpecRetries)
	assert.Equal(t, 2, b.ValidationRetries)
	assert.Equal(t, 0, b.BlockRetries)
}

// TestMigrateRetries_SpecFloorIsTwo proves even tiny legacy budgets never
// produce a Spec=0/1 (the previo2 instant spec-fail bug); Val is always 2.
func TestMigrateRetries_SpecFloorIsTwo(t *testing.T) {
	for _, old := range []int{0, 1} {
		b := MigrateRetries(old)
		assert.Equalf(t, 2, b.SpecRetries, "old=%d Spec should floor at 2", old)
		assert.Equalf(t, 2, b.ValidationRetries, "old=%d Val should be 2", old)
	}
}

// TestMigrateRetries_LegacyThree: old=3 → {3,2,2,0}; no instant spec-fail.
func TestMigrateRetries_LegacyThree(t *testing.T) {
	b := MigrateRetries(3)
	assert.Equal(t, 3, b.CodeRetries)
	assert.Equal(t, 2, b.SpecRetries)
	assert.Equal(t, 2, b.ValidationRetries)
	assert.Equal(t, 0, b.BlockRetries)
}

// TestMigrateRetries_NegativeClamped: old=-2 → {0,2,2,0}.
func TestMigrateRetries_NegativeClamped(t *testing.T) {
	b := MigrateRetries(-2)
	assert.Equal(t, 0, b.CodeRetries)
	assert.Equal(t, 2, b.SpecRetries)
	assert.Equal(t, 2, b.ValidationRetries)
	assert.Equal(t, 0, b.BlockRetries)
}

func TestLoadGoals_MigratesLegacyRetries(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Legacy goal"
    status: pending
    retries: 4
    max_retries: 5
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	// Unstarted legacy goal: live counters seed to the FULL Max… budget
	// (decrement-toward-zero), NOT to the legacy used-count of 4. The Max…
	// budget itself derives from max_retries (5).
	assert.Equal(t, 5, g.CodeRetries)
	assert.Equal(t, 3, g.SpecRetries)
	assert.Equal(t, 2, g.ValidationRetries)
	assert.Equal(t, 0, g.BlockRetries)
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
	// Live == Max: full remaining budget for a goal that has not run yet.
	assert.Equal(t, g.MaxCodeRetries, g.CodeRetries)
	assert.Equal(t, g.MaxSpecRetries, g.SpecRetries)
	assert.Equal(t, g.MaxValidationRetries, g.ValidationRetries)
}

func TestLoadGoals_AlreadyMigratedNotReSplit(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Already migrated goal"
    status: pending
    retries: 4
    code_retries: 7
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	// A goal already carrying a non-zero live counter is mid-flight: the
	// all-zero guard gates the WHOLE live block, so nothing is re-seeded —
	// CodeRetries stays 7 and the other live counters stay at their loaded
	// (zero) values rather than being topped up to Max. Idempotent on reload.
	assert.Equal(t, 7, g.CodeRetries)
	assert.Equal(t, 0, g.SpecRetries)
	assert.Equal(t, 0, g.ValidationRetries)
	assert.Equal(t, 0, g.BlockRetries)
}

func TestLoadGoals_SeedsFreshGoalToFullBudget(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Fresh goal"
    status: pending
    retries: 0
    max_retries: 5
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	// Fresh goal (no per-class keys): live counters seed to the FULL Max…
	// budget so the first code defect does NOT hard-halt the goal.
	assert.Equal(t, 5, g.CodeRetries)
	assert.Equal(t, 3, g.SpecRetries)
	assert.Equal(t, 2, g.ValidationRetries)
	assert.Equal(t, 0, g.BlockRetries)
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
}

func TestLoadGoals_DoesNotResurrectFailedGoal(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Exhausted goal"
    status: failed
    retries: 0
    max_retries: 5
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	// A terminal (failed) goal with all-zero live counters must NOT be
	// re-seeded — an exhausted goal is never resurrected to full budget.
	assert.Equal(t, GoalFailed, g.Status)
	assert.Equal(t, 0, g.CodeRetries)
	assert.Equal(t, 0, g.SpecRetries)
	assert.Equal(t, 0, g.ValidationRetries)
	assert.Equal(t, 0, g.BlockRetries)
	// The Max… budget is still derived (Max seeding is not status-gated); only
	// the live seed is withheld for terminal goals.
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
}

func TestLoadGoals_LegacyStillParses(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Legacy goal"
    status: pending
    retries: 4
    max_retries: 5
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, 4, gf.Goals[0].Retries)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestLoadGoalLegacyNoPreconditions(t *testing.T) {
	root := t.TempDir()
	content := `current_goal: goal-001
goals:
  - id: goal-001
    description: legacy goal
    status: pending
    retries: 0
    max_retries: 3
`
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)

	assert.Empty(t, gf.Goals[0].Preconditions, "legacy goal must have no preconditions")

	d := New(root, nil)
	ok, class, remedy := d.evaluatePreconditions(&gf.Goals[0])
	assert.True(t, ok, "legacy goal with no preconditions must never block")
	assert.Empty(t, class)
	assert.Empty(t, remedy)
}

func TestLoadGoalEmptyPreconditionsSection(t *testing.T) {
	root := t.TempDir()
	content := `current_goal: goal-001
goals:
  - id: goal-001
    description: empty preconditions goal
    status: pending
    retries: 0
    max_retries: 3
    preconditions: []
`
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)

	assert.Empty(t, gf.Goals[0].Preconditions, "empty preconditions section yields empty slice")

	d := New(root, nil)
	ok, class, remedy := d.evaluatePreconditions(&gf.Goals[0])
	assert.True(t, ok, "empty preconditions behaves identically to absent key")
	assert.Empty(t, class)
	assert.Empty(t, remedy)
}

func TestWriteGoalMD_PreconditionsSection(t *testing.T) {
	dir := t.TempDir()
	preconds := []Precondition{
		{Kind: "env", Spec: "DB_USER", Remedy: "export DB_USER"},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	err := WriteGoalMD(dir, "With preconds", "", []string{"AC1"}, []string{"check"}, preconds, "ctx", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Preconditions")
	assert.Contains(t, content, "- [env] DB_USER — export DB_USER")
	assert.Contains(t, content, "- [service] localhost:5432 — start postgres")

	// ## Preconditions must sit between ## Validation Rules and ## Context.
	valIdx := strings.Index(content, "## Validation Rules")
	preIdx := strings.Index(content, "## Preconditions")
	ctxIdx := strings.Index(content, "## Context")
	require.NotEqual(t, -1, valIdx)
	require.NotEqual(t, -1, preIdx)
	require.NotEqual(t, -1, ctxIdx)
	assert.Greater(t, preIdx, valIdx, "Preconditions must come after Validation Rules")
	assert.Greater(t, ctxIdx, preIdx, "Context must come after Preconditions")

	// Empty slice => section omitted (legacy goal.md byte-unchanged).
	dir2 := t.TempDir()
	require.NoError(t, WriteGoalMD(dir2, "No preconds", "", []string{"AC1"}, []string{"check"}, nil, "ctx", "", nil))
	data2, err := os.ReadFile(filepath.Join(dir2, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data2), "## Preconditions")
}

// TestCurrentCycle verifies the one-indexed dispatch-attempt counter is derived
// from CONSUMED retry budget (Max… − live counters), NOT the live remaining
// sum. The live per-class counters decrement toward zero (seeded to Max… by
// LoadGoals), so a fresh full-budget goal is cycle 1 and the number rises
// monotonically as any class consumes budget. goal.Retries must never be read.
func TestCurrentCycle(t *testing.T) {
	cases := []struct {
		name                                      string
		maxCode, code, maxSpec, spec, maxVal, val int
		want                                      int
	}{
		{"full budget => cycle 1", 3, 3, 2, 2, 1, 1, 1},
		{"one code consumed => 2", 3, 2, 2, 2, 1, 1, 2},
		{"code+spec consumed => 3", 3, 2, 2, 1, 1, 1, 3},
		{"code2 spec3 val1 => 7", 2, 0, 3, 0, 1, 0, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Goal{
				Retries:              99, // legacy counter MUST be ignored
				MaxCodeRetries:       tc.maxCode,
				CodeRetries:          tc.code,
				MaxSpecRetries:       tc.maxSpec,
				SpecRetries:          tc.spec,
				MaxValidationRetries: tc.maxVal,
				ValidationRetries:    tc.val,
			}
			assert.Equal(t, tc.want, CurrentCycle(g))
		})
	}
}

// TestCycleResearchDir asserts the exact joined per-cycle research path beneath
// the goal-scoped research root.
func TestCycleResearchDir(t *testing.T) {
	root := "/tmp/proj"
	g := &Goal{ID: "goal-007", MaxCodeRetries: 3, CodeRetries: 2} // consumed code 1 => cycle 2
	want := filepath.Join(root, ".tmux-cli", "goals", "goal-007", "research", "cycle-2")
	assert.Equal(t, want, CycleResearchDir(root, g))

	// EnsureCycleResearchDir mkdir -p's it idempotently and returns the dir.
	realRoot := t.TempDir()
	g2 := &Goal{ID: "goal-007", MaxCodeRetries: 3, CodeRetries: 2}
	dir, err := EnsureCycleResearchDir(realRoot, g2)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(dir, filepath.Join("goals", "goal-007", "research", "cycle-2")))
	info, statErr := os.Stat(dir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
	// idempotent second call
	_, err = EnsureCycleResearchDir(realRoot, g2)
	require.NoError(t, err)
}

// --- Investigation Config rendering (M1) ---

func TestWriteGoalMD_RendersProvidedInvestigators(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "Quality gate", Type: "quality-gate", Commands: []string{"phpstan analyse"}, Pass: "exit 0", Fail: "errors"},
		{Name: "Test execution", Type: "test-execution", Commands: []string{"phpunit"}, Pass: "green", Fail: "red"},
		{Name: "Architecture check", Type: "architecture-check", Commands: []string{"deptrac"}, Pass: "no violations", Fail: "violation"},
	}
	require.NoError(t, WriteGoalMD(dir, "Provided", "", []string{"AC1"}, []string{"x"}, nil, "", "", invs))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Equal(t, 1, strings.Count(content, "## Investigation Config"))
	assert.Contains(t, content, "### Investigator 1: Quality gate")
	assert.Contains(t, content, "### Investigator 2: Test execution")
	assert.Contains(t, content, "### Investigator 3: Architecture check")
	assert.Contains(t, content, "- type: quality-gate")
	assert.Contains(t, content, "- command: phpstan analyse")
	assert.Contains(t, content, "- Pass: exit 0")
	assert.Contains(t, content, "- Fail: errors")

	cfgIdx := strings.Index(content, "## Investigation Config")
	revalIdx := strings.Index(content, "## Re-validation")
	assert.Greater(t, cfgIdx, 0)
	assert.Greater(t, revalIdx, cfgIdx, "Investigation Config must come before Re-validation")

	// In-order rendering
	assert.Less(t, strings.Index(content, "Investigator 1:"), strings.Index(content, "Investigator 2:"))
	assert.Less(t, strings.Index(content, "Investigator 2:"), strings.Index(content, "Investigator 3:"))
}

func TestWriteGoalMD_DerivesFallbackFromValidate(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse --level=9",
		"php bin/phpunit --testsuite=unit",
		"vendor/bin/deptrac analyse",
	}
	require.NoError(t, WriteGoalMD(dir, "Fallback", "", []string{"AC1"}, validate, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	qg := strings.Index(content, "- type: quality-gate")
	te := strings.Index(content, "- type: test-execution")
	ac := strings.Index(content, "- type: architecture-check")
	assert.Greater(t, qg, 0, "quality-gate present")
	assert.Greater(t, te, 0, "test-execution present")
	assert.Greater(t, ac, 0, "architecture-check present")
	assert.Less(t, qg, te, "quality-gate before test-execution")
	assert.Less(t, te, ac, "test-execution before architecture-check")
}

func TestWriteGoalMD_FallbackGuaranteesAtLeastTwo(t *testing.T) {
	for _, validate := range [][]string{
		{"vendor/bin/phpstan analyse"},
		{},
	} {
		dir := t.TempDir()
		require.NoError(t, WriteGoalMD(dir, "Few", "", []string{"AC1"}, validate, nil, "", "", nil))
		data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, strings.Count(string(data), "### Investigator "), 2,
			"validate=%v should yield >=2 investigators", validate)
	}
}

func TestWriteGoalMD_CapsAtFourInvestigators(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
		"npx eslint .",
		"npx playwright test",
	}
	require.NoError(t, WriteGoalMD(dir, "Many", "", []string{"AC1"}, validate, nil, "", "", nil))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(string(data), "### Investigator "))
}

func TestWriteGoalMD_SingleInvestigationConfigSection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Single", "", []string{"AC1"}, []string{"go test ./..."}, nil, "", "", nil))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "## Investigation Config"))
}

func TestWriteGoalMD_OptionalConditionRendered(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "With cond", Type: "static-analysis", Pass: "p", Fail: "f", Condition: "only when X"},
		{Name: "No cond", Type: "static-analysis", Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Cond", "", []string{"AC1"}, []string{"x"}, nil, "", "", invs))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "- condition: only when X")
	assert.Equal(t, 1, strings.Count(content, "- condition:"), "exactly one condition line (the empty one omitted)")
}

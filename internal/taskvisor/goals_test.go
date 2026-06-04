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
				ID:                   "goal-001",
				Description:          "My goal",
				Acceptance:           []string{"A1", "A2"},
				Validate:             []string{"V1"},
				DependsOn:            []string{"goal-000"},
				Status:               GoalFailed,
				Retries:              3,
				MaxRetries:           5,
				MaxCodeRetries:       5,
				MaxSpecRetries:       3,
				MaxValidationRetries: 2,
				MaxBlockRetries:      0,
				StartedAt:            "2026-05-20T14:00:00Z",
				FinishedAt:           "2026-05-20T15:00:00Z",
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	g := gf.Goals[0]
	assert.Equal(t, "My goal", g.Description)
	assert.Equal(t, []string{"A1", "A2"}, g.Acceptance)
	assert.Equal(t, []string{"V1"}, g.Validate)
	assert.Equal(t, []string{"goal-000"}, g.DependsOn)
	assert.Equal(t, 5, g.MaxRetries)
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
	assert.Equal(t, "", g.StartedAt)
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, 0, g.Retries)
	assert.Equal(t, "", g.FinishedAt)
}

// TestResetGoal_ZeroesAllFourLiveCounters asserts that resetting a failed goal
// zeroes ALL FOUR live per-class retry counters (not just the legacy Retries),
// so the LoadGoals re-seed guard (all-four-zero + non-terminal) fires on the
// next load and restores each from its Max… value (the W1 zero+re-seed idiom).
func TestResetGoal_ZeroesAllFourLiveCounters(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:                "goal-001",
				Status:            GoalFailed,
				Retries:           4,
				CodeRetries:       0,
				SpecRetries:       1,
				ValidationRetries: 2,
				BlockRetries:      0,
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	g := gf.Goals[0]
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, 0, g.CodeRetries)
	assert.Equal(t, 0, g.SpecRetries)
	assert.Equal(t, 0, g.ValidationRetries)
	assert.Equal(t, 0, g.BlockRetries)
}

// TestResetGoal_PreservesMaxCounters asserts ResetGoal never mutates the Max…
// budget fields — they are the source the LoadGoals guard re-seeds from.
func TestResetGoal_PreservesMaxCounters(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:                   "goal-001",
				Status:               GoalFailed,
				CodeRetries:          0,
				SpecRetries:          1,
				MaxCodeRetries:       5,
				MaxSpecRetries:       3,
				MaxValidationRetries: 2,
				MaxBlockRetries:      0,
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	g := gf.Goals[0]
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
}

// TestResetGoal_NonFailedLeavesLiveCountersUntouched asserts the failed-only
// guard: a non-failed goal returns false and its live counters are unchanged.
func TestResetGoal_NonFailedLeavesLiveCountersUntouched(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:                "goal-001",
				Status:            GoalDone,
				CodeRetries:       3,
				SpecRetries:       2,
				ValidationRetries: 1,
				BlockRetries:      1,
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.False(t, ok)
	g := gf.Goals[0]
	assert.Equal(t, GoalDone, g.Status)
	assert.Equal(t, 3, g.CodeRetries)
	assert.Equal(t, 2, g.SpecRetries)
	assert.Equal(t, 1, g.ValidationRetries)
	assert.Equal(t, 1, g.BlockRetries)
}

// TestLoadGoals_AfterReset_ReseedsFullBudget asserts the full round-trip: the
// post-reset on-disk state (status pending, all four live counters 0, Max… set)
// re-seeds the live counters to their Max… values on the next LoadGoals.
func TestLoadGoals_AfterReset_ReseedsFullBudget(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{
				ID:                   "goal-001",
				Description:          "Reset goal",
				Status:               GoalPending,
				CodeRetries:          0,
				SpecRetries:          0,
				ValidationRetries:    0,
				BlockRetries:         0,
				MaxCodeRetries:       5,
				MaxSpecRetries:       3,
				MaxValidationRetries: 2,
				MaxBlockRetries:      2,
			},
		},
	}
	require.NoError(t, SaveGoals(root, gf))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Goals, 1)
	g := loaded.Goals[0]
	assert.Equal(t, 5, g.CodeRetries)
	assert.Equal(t, 3, g.SpecRetries)
	assert.Equal(t, 2, g.ValidationRetries)
	assert.Equal(t, 2, g.BlockRetries)
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

// --- consumedRetries / global retry ceiling accounting (B6) ---

// consumedRetries returns the budget a single goal has burned across the three
// budgeted classes (code/spec/validation), clamped to >= 0 per goal. It is the
// single source of "budget consumed" reused by CurrentCycle and the global
// retry-ceiling sum.

func TestConsumedRetries_FullBudgetGoalIsZero(t *testing.T) {
	g := &Goal{
		Retries:        99, // legacy scalar MUST be ignored
		MaxCodeRetries: 5, CodeRetries: 5,
		MaxSpecRetries: 3, SpecRetries: 3,
		MaxValidationRetries: 2, ValidationRetries: 2,
	}
	assert.Equal(t, 0, consumedRetries(g), "live==max for every class => nothing consumed")
}

func TestConsumedRetries_SumsAllThreeClasses(t *testing.T) {
	// code consumed 3, spec consumed 2, validation consumed 0 => 5.
	g := &Goal{
		MaxCodeRetries: 5, CodeRetries: 2,
		MaxSpecRetries: 3, SpecRetries: 1,
		MaxValidationRetries: 2, ValidationRetries: 2,
		MaxBlockRetries: 0, BlockRetries: 0, // BlockRetries excluded from the sum
	}
	assert.Equal(t, 5, consumedRetries(g))
}

func TestConsumedRetries_ClampsNegativePerGoal(t *testing.T) {
	// Corrupt live > max (Code7 with MaxCode5) must never contribute negative.
	g := &Goal{
		MaxCodeRetries: 5, CodeRetries: 7,
	}
	assert.GreaterOrEqual(t, consumedRetries(g), 0, "corrupt live>max clamps to >= 0")
	assert.Equal(t, 0, consumedRetries(g))
}

func TestTotalRetries_IgnoresLegacyScalar(t *testing.T) {
	// Two goals each consuming 2 per-class; legacy Retries:99 must never be summed.
	gf := &GoalsFile{
		GlobalMaxRetries: 100, // large => never clamps the sum
		Goals: []Goal{
			{ID: "goal-001", Retries: 99, MaxCodeRetries: 5, CodeRetries: 3},
			{ID: "goal-002", Retries: 99, MaxCodeRetries: 5, CodeRetries: 3},
		},
	}
	assert.Equal(t, 4, gf.TotalRetries(), "sum of consumed (2+2), legacy scalar ignored")
}

func TestRetryCeilingReached_TripsOnConsumedSum(t *testing.T) {
	// Two goals each consuming 2 => sum 4 == GlobalMaxRetries 4 => reached (>=).
	gf := &GoalsFile{
		GlobalMaxRetries: 4,
		Goals: []Goal{
			{ID: "goal-001", MaxCodeRetries: 5, CodeRetries: 3},
			{ID: "goal-002", MaxCodeRetries: 5, CodeRetries: 3},
		},
	}
	assert.True(t, gf.RetryCeilingReached())
}

func TestRetryCeilingReached_FalseBelowCeiling(t *testing.T) {
	// consumed sum 3 < GlobalMaxRetries 4 => not reached.
	gf := &GoalsFile{
		GlobalMaxRetries: 4,
		Goals: []Goal{
			{ID: "goal-001", MaxCodeRetries: 5, CodeRetries: 2}, // consumed 3
		},
	}
	assert.False(t, gf.RetryCeilingReached())
}

func TestRetryCeilingReached_LegacyScalarAloneDoesNotTrip(t *testing.T) {
	// One full-budget goal carrying a huge legacy scalar, default ceiling.
	// Proves the dead g.Retries counter no longer arms the kill-switch.
	gf := &GoalsFile{
		GlobalMaxRetries: 0, // default => max(60, N*3) = 60
		Goals: []Goal{
			{ID: "goal-001", Retries: 100,
				MaxCodeRetries: 5, CodeRetries: 5,
				MaxSpecRetries: 3, SpecRetries: 3,
				MaxValidationRetries: 2, ValidationRetries: 2},
		},
	}
	assert.False(t, gf.RetryCeilingReached(), "legacy scalar must not trip the ceiling")
}

func TestRetrySumAndCeiling_DefaultCeilingFormula(t *testing.T) {
	// GlobalMaxRetries<=0 => ceiling == max(60, len(goals)*3).
	mkGoals := func(n int) []Goal {
		gs := make([]Goal, n)
		for i := range gs {
			gs[i] = Goal{ID: fmt.Sprintf("goal-%03d", i+1)}
		}
		return gs
	}
	t.Run("small plan hits the floor of 60", func(t *testing.T) {
		gf := &GoalsFile{GlobalMaxRetries: 0, Goals: mkGoals(3)}
		_, ceiling := gf.retrySumAndCeiling()
		assert.Equal(t, 60, ceiling, "max(60, 3*3) == 60")
	})
	t.Run("large plan scales with N*3", func(t *testing.T) {
		gf := &GoalsFile{GlobalMaxRetries: 0, Goals: mkGoals(30)}
		_, ceiling := gf.retrySumAndCeiling()
		assert.Equal(t, 90, ceiling, "max(60, 30*3) == 90")
	})
}

func TestCurrentCycle_UnchangedAfterRefactor(t *testing.T) {
	// CurrentCycle must remain consumedRetries(g)+1; MaxCode5/Code3 => cycle 3.
	g := &Goal{Retries: 99, MaxCodeRetries: 5, CodeRetries: 3}
	assert.Equal(t, 3, CurrentCycle(g))
	assert.Equal(t, consumedRetries(g)+1, CurrentCycle(g), "CurrentCycle == consumed + 1")
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

// --- B3: emission / dead-choreography investigator -------------------------

func TestDeriveEmissionInvestigator_PhaseEvent_AddsInvestigator(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"event",
		"Catalog reserves stock",
		[]string{`src/Catalog/ constructs App\Share\Event\StockReserved`},
		[]string{"go build ./..."},
	)
	require.True(t, ok)
	assert.Equal(t, "emission-check", inv.Type)
	assert.Equal(t, "Event emission", inv.Name)
	require.Len(t, inv.Commands, 1)
	assert.Contains(t, inv.Commands[0], "StockReserved")
	assert.Contains(t, inv.Commands[0], "src/Catalog/")
	assert.Equal(t, []string{"src/Catalog/"}, inv.Paths)
}

func TestDeriveEmissionInvestigator_EventFQCNInAcceptance_Detected(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"",
		"Pricing recalculates",
		[]string{`emits App\Share\Event\PriceChanged from src/Pricing/`},
		nil,
	)
	require.True(t, ok)
	assert.Contains(t, inv.Pass, "PriceChanged")
	assert.Contains(t, inv.Fail, "PriceChanged")
	assert.Contains(t, inv.Commands[0], "src/Pricing/")
}

func TestDeriveEmissionInvestigator_NonEventGoal_NotAdded(t *testing.T) {
	_, ok := deriveEmissionInvestigator(
		"domain",
		"Add tax calculation",
		[]string{"compute totals in src/Pricing/"},
		[]string{"vendor/bin/phpstan analyse"},
	)
	assert.False(t, ok)
}

func TestDeriveEmissionInvestigator_GrepRootExcludesTests(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"event",
		"reserve",
		[]string{`src/Catalog/ emits App\Share\Event\StockReserved`},
		nil,
	)
	require.True(t, ok)
	assert.Contains(t, inv.Commands[0], "src/Catalog/")
	assert.NotContains(t, inv.Commands[0], "tests/")
	assert.NotContains(t, strings.Join(inv.Paths, ","), "tests/")
}

func TestDeriveEmissionInvestigator_EmptyCondition(t *testing.T) {
	inv, ok := deriveEmissionInvestigator(
		"choreography",
		`x emits App\Share\Event\StockReserved`,
		nil,
		nil,
	)
	require.True(t, ok)
	assert.Equal(t, "", inv.Condition)
}

func TestWriteGoalMD_EventGoal_RendersEmissionInvestigator(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Catalog reserves stock", "event",
		[]string{`src/Catalog/ constructs App\Share\Event\StockReserved`},
		[]string{"go build ./..."}, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Event emission")
	assert.Contains(t, content, "- type: emission-check")
}

func TestWriteGoalMD_EventGoal_EmissionSurvivesCap(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
	}
	require.NoError(t, WriteGoalMD(dir, `Catalog emits App\Share\Event\StockReserved`, "event",
		[]string{"AC1"}, validate, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.LessOrEqual(t, strings.Count(content, "### Investigator "), 4)
	assert.Contains(t, content, "- type: emission-check")
}

func TestWriteGoalMD_ExplicitInvestigators_NoEmissionAppended(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "Q", Type: "quality-gate", Pass: "p", Fail: "f"},
		{Name: "T", Type: "test-execution", Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, `Catalog emits App\Share\Event\StockReserved`, "event",
		[]string{"AC1"}, []string{"x"}, nil, "", "", invs))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "emission-check")
}

func TestCapInvestigators_PreservesUnder4(t *testing.T) {
	in := []Investigator{
		{Name: "A", Type: "static-analysis"},
		{Name: "B", Type: "test-execution"},
		{Name: "C", Type: "quality-gate"},
	}
	out := capInvestigators(in)
	require.Len(t, out, 3)
	assert.Equal(t, "A", out[0].Name)
	assert.Equal(t, "B", out[1].Name)
	assert.Equal(t, "C", out[2].Name)
}

func TestDeriveInvestigators_StillCapsAt4(t *testing.T) {
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
		"npx eslint .",
	}
	out := deriveInvestigators(validate)
	assert.Len(t, out, 4)
}

// --- B2b: own-suite-green mandatory gate -----------------------------------

// newOwnSuiteGoalDir lays out <root>/.tmux-cli/goals/goal-001 (the canonical
// goalDir shape ownSuiteFSRoot climbs back from) plus any given test-suite dirs
// under root. The selector resolves existence against root, so the suites listed
// here are exactly the ones the gate's phpunit scope will reference.
func newOwnSuiteGoalDir(t *testing.T, suites ...string) string {
	t.Helper()
	root := t.TempDir()
	goalDir := filepath.Join(root, ".tmux-cli", "goals", "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	for _, s := range suites {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(s)), 0o755))
	}
	return goalDir
}

func TestProducesAppCode_SrcToken(t *testing.T) {
	got := producesAppCode("domain", []string{"AC1"}, []string{"phpunit covers `src/Catalog`"}, "")
	assert.True(t, got, "a validate rule citing src/Catalog produces app code")
}

func TestProducesAppCode_NoSrcToken(t *testing.T) {
	got := producesAppCode("domain",
		[]string{"docs updated", "the source of truth is the README"},
		[]string{"markdownlint docs/"}, "purely a documentation goal, app behaviour unchanged")
	assert.False(t, got, "prose mentioning 'source'/'app' without a src/|app/ path token is not app code")
}

func TestProducesAppCode_GatePhase(t *testing.T) {
	got := producesAppCode("gate", []string{"build green"}, []string{"compile app/Kernel.php"}, "touches app/")
	assert.False(t, got, "a gate-phase goal never produces app code even with an app/ token")
}

func TestWriteGoalMD_SrcDeliverableGetsOwnSuiteGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "Own-suite green")
	assert.Contains(t, content, "- type: own-suite-green")
}

func TestWriteGoalMD_OwnSuiteGateUsesSelectorScope(t *testing.T) {
	// Only the Integration suite exists, so the selector scope is exactly it.
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "- type: own-suite-green")
	assert.Contains(t, content, "vendor/bin/phpunit tests/Integration/Catalog")
	assert.NotContains(t, content, "--filter")
}

func TestWriteGoalMD_OwnSuiteGateNeverUsesUnitFilter(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	cmd := ownSuiteGateCommand(t, readGoalMD(t, dir))
	assert.NotContains(t, cmd, "--filter", "the gate must not run the unit --filter slice")
	assert.NotContains(t, cmd, "Domain", "the gate must not target the unit \\Domain regex")
	assert.Contains(t, cmd, "vendor/bin/phpunit tests/")
}

func TestWriteGoalMD_DocsGoalNoOwnSuiteGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t)
	require.NoError(t, WriteGoalMD(dir, "Update docs", "domain",
		[]string{"README explains the flow"}, []string{"markdownlint docs/"}, nil, "", "", nil))

	assert.NotContains(t, readGoalMD(t, dir), "own-suite-green",
		"a docs-only goal (no src/app deliverable) gets no own-suite gate")
}

func TestWriteGoalMD_ExplicitConfigStillGetsMandatoryGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "Stan", Type: "quality-gate", Commands: []string{"phpstan"}, Pass: "p", Fail: "f"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"phpunit"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "- type: own-suite-green",
		"the gate is appended even when the planner supplied an explicit config")
}

func TestWriteGoalMD_OwnSuiteGateNotDuplicated(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "Own", Type: "own-suite-green", Commands: []string{"vendor/bin/phpunit tests/Integration/Catalog"}, Pass: "p", Fail: "f"},
		{Name: "Stan", Type: "quality-gate", Commands: []string{"phpstan"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Equal(t, 1, strings.Count(content, "- type: own-suite-green"),
		"an explicit own-suite-green config must not be duplicated by the auto-append")
}

func TestWriteGoalMD_GatePinnedWhenCapExceeded(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "A", Type: "test-execution", Commands: []string{"a"}, Pass: "p", Fail: "f"},
		{Name: "B", Type: "quality-gate", Commands: []string{"b"}, Pass: "p", Fail: "f"},
		{Name: "C", Type: "architecture-check", Commands: []string{"c"}, Pass: "p", Fail: "f"},
		{Name: "D", Type: "static-analysis", Commands: []string{"d"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Equal(t, 4, strings.Count(content, "### Investigator "),
		"section must hold exactly 4 investigators after the cap")
	assert.Contains(t, content, "- type: own-suite-green",
		"the mandatory gate survives the cap (lowest-priority explicit dropped)")
}

func TestOwnSuiteGateInvestigator_FailIsCodeDefect(t *testing.T) {
	inv := ownSuiteGateInvestigator([]string{"tests/Integration/Catalog", "tests/Functional/Catalog"})
	assert.Equal(t, "own-suite-green", inv.Type)
	assert.Contains(t, inv.Fail, "code-defect")
	assert.Contains(t, inv.Fail, "implementer")
	require.Len(t, inv.Commands, 1)
	assert.Equal(t, "vendor/bin/phpunit tests/Integration/Catalog tests/Functional/Catalog", inv.Commands[0])
	assert.NotContains(t, inv.Commands[0], "--filter")
}

// --- IsPureCommand / isExitOnlyPass (B9a) ----------------------------------

func TestIsPureCommand_ExplicitCommandType(t *testing.T) {
	inv := Investigator{Type: "command", Commands: []string{"test -f composer.json"}, Pass: "anything"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExplicitCommandTypeNoCommands(t *testing.T) {
	inv := Investigator{Type: "command", Commands: nil, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyStaticAnalysis(t *testing.T) {
	inv := Investigator{Type: "static-analysis", Commands: []string{"vendor/bin/phpstan analyse"}, Pass: "exit 0, no errors"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyTestExecution(t *testing.T) {
	inv := Investigator{Type: "test-execution", Commands: []string{"php bin/phpunit"}, Pass: "all green (exit 0)"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyArchitectureCheck(t *testing.T) {
	inv := Investigator{Type: "architecture-check", Commands: []string{"vendor/bin/deptrac analyse"}, Pass: "exit 0, no layer violations"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_SemanticPassVetoesExitType(t *testing.T) {
	inv := Investigator{Type: "static-analysis", Commands: []string{"grep -r Foo src/"}, Pass: "matches expected"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_CodeReviewRejected(t *testing.T) {
	inv := Investigator{Type: "code-review", Commands: []string{"true"}, Pass: "design correct"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ConventionAuditRejected(t *testing.T) {
	inv := Investigator{Type: "convention-audit", Commands: []string{"true"}, Pass: "DDD compliance holds"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_E2ETestRejected(t *testing.T) {
	inv := Investigator{Type: "e2e-test", Commands: []string{"npx playwright test"}, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitTypeNoCommandRejected(t *testing.T) {
	inv := Investigator{Type: "quality-gate", Commands: nil, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_DerivedExitInvestigatorsAreClassified(t *testing.T) {
	derived := deriveInvestigators([]string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
	})
	require.Len(t, derived, 3)
	for _, inv := range derived {
		assert.Truef(t, IsPureCommand(inv), "derived %s/%q should be pure-command", inv.Type, inv.Pass)
	}
}

func TestIsPureCommand_DerivedGrepInvestigatorNotPure(t *testing.T) {
	derived := deriveInvestigators([]string{"grep -r Foo src/"})
	require.NotEmpty(t, derived)
	grep := derived[0]
	require.Equal(t, "static-analysis", grep.Type)
	require.Equal(t, "matches expected", grep.Pass)
	assert.False(t, IsPureCommand(grep))
}

func TestIsExitOnlyPass_MarkerTable(t *testing.T) {
	cases := []struct {
		pass string
		want bool
	}{
		{"exit 0", true},
		{"command succeeds", true},
		{"matches expected", false},
		{"", false},
		{"review passes", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isExitOnlyPass(c.pass), "isExitOnlyPass(%q)", c.pass)
	}
}

// readGoalMD reads the rendered goal.md from a goalDir.
func readGoalMD(t *testing.T, goalDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	return string(data)
}

// ownSuiteGateCommand extracts the own-suite-green investigator's command line
// from a rendered goal.md (the first `- command:` line following the
// `- type: own-suite-green` marker).
func ownSuiteGateCommand(t *testing.T, content string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "- type: own-suite-green" {
			continue
		}
		for _, after := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(after), "- command:") {
				return after
			}
			if strings.HasPrefix(after, "### Investigator ") {
				break
			}
		}
	}
	t.Fatalf("no own-suite-green command found in:\n%s", content)
	return ""
}

// TestNextPendingGoal_PrerequisiteAppendedAfterDependentRunsFirst proves that an
// escalation prerequisite appended LAST in file order still runs before its
// earlier-indexed dependent — so GoalAddPrerequisite's append (never a reorder)
// is harmless. Dependent A (goal-001, earlier index) is wired to depend on the
// appended prerequisite P (goal-003, last index); NextPendingGoal must pick P.
func TestNextPendingGoal_PrerequisiteAppendedAfterDependentRunsFirst(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-002", Status: GoalDone},                                     // scaffold anchor
			{ID: "goal-001", Status: GoalPending, DependsOn: []string{"goal-003"}}, // dependent A (earlier index)
			{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}}, // appended prerequisite P (last index)
		},
	}
	g, ok := gf.NextPendingGoal()
	require.True(t, ok)
	require.NotNil(t, g)
	assert.Equal(t, "goal-003", g.ID, "appended prerequisite must dispatch before its earlier-indexed dependent")
}

// TestGoal_EscalationCountSurvivesSaveGoalsRoundTrip guards the dual-struct
// field: the daemon (de)serializes goals via taskvisor.Goal, so escalation_count
// MUST persist across a LoadGoals/SaveGoals round-trip — otherwise the daemon's
// first save silently erases the counter and the escalation cap leaks.
func TestGoal_EscalationCountSurvivesSaveGoalsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "A", Status: GoalPending, EscalationCount: 1},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, 1, loaded.Goals[0].EscalationCount)

	// Re-save (the daemon round-trip) and confirm the counter is not dropped.
	require.NoError(t, SaveGoals(dir, loaded))
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, reloaded.Goals, 1)
	assert.Equal(t, 1, reloaded.Goals[0].EscalationCount, "escalation_count must survive SaveGoals round-trip")
}

// --- B4: repair-at-dispatch Investigation Config guard ---------------------

// strippedGoalMD is a goal.md whose `## Investigation Config` section was
// removed post-creation (the planner-re-write failure mode B4 repairs).
const strippedGoalMD = `# Reserve stock

## Acceptance Criteria

- Stock decrements on reserve
- No oversell

## Validation Rules

- vendor/bin/phpstan analyse
- vendor/bin/phpunit

## Not In Scope

UI redesign

## Re-validation

Incremental: only failed checks and checks whose inputs changed are re-run on retry.
`

// countInvestigatorsLikeParser mirrors parseGoalFindings (cmd/tmux-cli/session.go,
// package main — not importable here): it counts `### Investigator ` headings
// scoped to the `## Investigation Config` section, the same round-trip the
// validator relies on. Used to prove a repaired goal.md parses to >=2 findings.
func countInvestigatorsLikeParser(md string) int {
	n := 0
	section := ""
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if section == "Investigation Config" && strings.HasPrefix(line, "### ") {
			n++
		}
	}
	return n
}

func writeGoalMDRaw(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "goal.md"), []byte(content), 0o644))
}

func TestEnsureInvestigationConfig_NoopWhenValidSectionPresent(t *testing.T) {
	dir := t.TempDir()
	// A creation-time goal.md always carries a valid (>=2) section.
	require.NoError(t, WriteGoalMD(dir, "Valid goal", "", []string{"AC1"},
		[]string{"go test ./...", "go vet ./..."}, nil, "", "", nil))
	before, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)

	repaired, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired, "valid section must be a no-op")

	after, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "file bytes must be unchanged")
}

func TestEnsureInvestigationConfig_RepairsWhenSectionMissing(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit"})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "## Investigation Config")
	hasSection, n := countInvestigators(string(out))
	assert.True(t, hasSection)
	assert.GreaterOrEqual(t, n, 2, "must end with >=2 investigators")
}

func TestEnsureInvestigationConfig_ReplacesMalformedSingleInvestigator(t *testing.T) {
	dir := t.TempDir()
	malformed := `# Goal

## Acceptance Criteria

- AC1

## Investigation Config

### Investigator 1: Lonely
- type: static-analysis
- command: go build ./...
- Pass: ok
- Fail: no

## Re-validation

Incremental.
`
	writeGoalMDRaw(t, dir, malformed)

	repaired, err := EnsureInvestigationConfig(dir, []string{"go test ./...", "go vet ./..."})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	hasSection, n := countInvestigators(string(out))
	assert.True(t, hasSection)
	assert.GreaterOrEqual(t, n, 2)
	assert.Equal(t, 1, strings.Count(string(out), "## Investigation Config"),
		"exactly one Investigation Config heading after replace")
	assert.NotContains(t, string(out), "Lonely", "the malformed section must be replaced, not kept")
}

func TestEnsureInvestigationConfig_DoesNotDuplicateSection(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out), "## Investigation Config"))

	// Idempotent: a second run is a no-op and still leaves exactly one.
	repaired2, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired2)
	out2, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out2), "## Investigation Config"))
}

func TestEnsureInvestigationConfig_PreservesSurroundingSections(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "# Reserve stock")
	assert.Contains(t, s, "- Stock decrements on reserve")
	assert.Contains(t, s, "- No oversell")
	assert.Contains(t, s, "## Not In Scope")
	assert.Contains(t, s, "UI redesign")
	assert.Contains(t, s, "## Re-validation")
}

func TestEnsureInvestigationConfig_InsertsBeforeReValidation(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	s := string(out)
	icIdx := strings.Index(s, "## Investigation Config")
	rvIdx := strings.Index(s, "## Re-validation")
	require.GreaterOrEqual(t, icIdx, 0)
	require.GreaterOrEqual(t, rvIdx, 0)
	assert.Less(t, icIdx, rvIdx, "section must be spliced BEFORE Re-validation")
}

func TestEnsureInvestigationConfig_EmptyValidateStillYieldsTwo(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, nil)
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	_, n := countInvestigators(string(out))
	assert.GreaterOrEqual(t, n, 2, "Build-sanity padding guarantees >=2")
}

func TestEnsureInvestigationConfig_MissingFileIsNoop(t *testing.T) {
	dir := t.TempDir() // no goal.md written
	repaired, err := EnsureInvestigationConfig(dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired)
	_, statErr := os.Stat(filepath.Join(dir, "goal.md"))
	assert.True(t, os.IsNotExist(statErr), "must not create a goal.md when none existed")
}

func TestEnsureInvestigationConfig_RepairSurvivesParseGoalFindings(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit"})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, countInvestigatorsLikeParser(string(out)), 2,
		"the goal.md parser must see >=2 findings after repair")
}

func TestRenderInvestigationConfig_MatchesWriteGoalMDOutput(t *testing.T) {
	invs := []Investigator{
		{Name: "Quality gate", Type: "quality-gate", Paths: []string{"src/Foo"},
			Commands: []string{"phpstan analyse"}, Pass: "exit 0", Fail: "errors"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"phpunit"},
			Pass: "all green", Fail: "red", Condition: "when changed"},
	}
	var b strings.Builder
	renderInvestigationConfig(&b, invs)
	section := b.String()

	// WriteGoalMD with these explicit investigators (no src/ deliverable, no
	// event phase) embeds exactly this list — its file must CONTAIN the helper's
	// byte-for-byte output, proving the extraction introduced no drift.
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Parity", "", []string{"AC1"},
		[]string{"x"}, nil, "", "", invs))
	full, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(full), section,
		"WriteGoalMD must embed renderInvestigationConfig output verbatim")
}

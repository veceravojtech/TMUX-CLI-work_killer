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
				StuckRetries:         3,
				MaxStuckRetries:      3,
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
				StuckRetries:         3,
				MaxStuckRetries:      3,
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

func TestResetGoal_NotFailedOrDone(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning, Retries: 1},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.False(t, ok)
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
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

// TestResetGoal_ClearsFailedBy asserts ResetGoal clears the timeout-salvage
// marker: a reset goal starts fresh, so a stale "validation-timeout" must not
// make the salvage scan watch (or flip) the re-pended goal.
func TestResetGoal_ClearsFailedBy(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed, FailedBy: "validation-timeout"},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	assert.Equal(t, "", gf.Goals[0].FailedBy, "ResetGoal must clear the FailedBy marker")
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

// TestResetGoal_DoneGoal asserts that a done goal can be reset back to pending,
// the same as a failed goal — zeroing counters and clearing markers.
func TestResetGoal_DoneGoal(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:                "goal-001",
				Status:            GoalDone,
				CodeRetries:       3,
				SpecRetries:       2,
				ValidationRetries: 1,
				BlockRetries:      1,
				StuckRetries:      1,
				Retries:           2,
				NextDispatch:      "implementer",
				FailedBy:          "",
				StartedAt:         "2026-06-01T10:00:00Z",
				FinishedAt:        "2026-06-01T12:00:00Z",
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
	assert.Equal(t, 0, g.StuckRetries)
	assert.Equal(t, 0, g.Retries)
	assert.Equal(t, "", g.NextDispatch)
	assert.Equal(t, "", g.FailedBy)
	assert.Equal(t, "", g.StartedAt)
	assert.Equal(t, "", g.FinishedAt)
}

// TestResetGoal_RunningLeavesLiveCountersUntouched asserts the guard rejects
// non-terminal statuses: a running goal returns false and is unchanged.
func TestResetGoal_RunningLeavesLiveCountersUntouched(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:                "goal-001",
				Status:            GoalRunning,
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
	assert.Equal(t, GoalRunning, g.Status)
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

// TestGoal_FailedBySurvivesSaveGoalsRoundTrip guards the dual-struct field:
// the daemon (de)serializes goals via taskvisor.Goal, so failed_by MUST persist
// across a LoadGoals/SaveGoals round-trip — otherwise the daemon's first save
// erases the timeout-salvage marker and the late-verdict scan never fires.
func TestGoal_FailedBySurvivesSaveGoalsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "A", Status: GoalFailed, FailedBy: "validation-timeout"},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, "validation-timeout", loaded.Goals[0].FailedBy)

	// Re-save (the daemon round-trip) and confirm the marker is not dropped.
	require.NoError(t, SaveGoals(dir, loaded))
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, reloaded.Goals, 1)
	assert.Equal(t, "validation-timeout", reloaded.Goals[0].FailedBy, "failed_by must survive SaveGoals round-trip")
}

// TestGoal_PriorityRoundTrip guards the persisted Priority field (goal-007 M1):
// a goal saved with priority: 7 survives a LoadGoals/SaveGoals round-trip, and a
// legacy goals.yaml WITHOUT a priority key loads as the default 0 (omitempty).
func TestGoal_PriorityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "A", Status: GoalPending, Priority: 7},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, loaded.Goals, 1)
	assert.Equal(t, 7, loaded.Goals[0].Priority)

	// Re-save (the daemon round-trip) and confirm the value is not dropped.
	require.NoError(t, SaveGoals(dir, loaded))
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, reloaded.Goals, 1)
	assert.Equal(t, 7, reloaded.Goals[0].Priority, "priority must survive SaveGoals round-trip")

	// Legacy file with no priority key loads as the default 0 (omitempty).
	legacy := t.TempDir()
	p := GoalsFilePath(legacy)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Legacy goal, no priority key"
    status: pending
    retries: 0
    max_retries: 3
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	lg, err := LoadGoals(legacy)
	require.NoError(t, err)
	require.Len(t, lg.Goals, 1)
	assert.Equal(t, 0, lg.Goals[0].Priority, "absent priority key must default to 0")
}

// --- StuckRetries dedicated budget -------------------------------------------

func TestLoadGoals_BackfillsMaxStuckRetries(t *testing.T) {
	root := t.TempDir()
	p := GoalsFilePath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	content := `goals:
  - id: "goal-001"
    description: "Legacy goal without stuck fields"
    status: pending
    max_retries: 5
    code_retries: 5
    max_code_retries: 5
    spec_retries: 3
    max_spec_retries: 3
    validation_retries: 2
    max_validation_retries: 2
`
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	gf, err := LoadGoals(root)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, 3, gf.Goals[0].MaxStuckRetries, "MaxStuckRetries must be backfilled to 3")
}

func TestLoadGoals_ReseedsStuckRetries(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalRunning,
		MaxRetries:  5,
		CodeRetries: 3, MaxCodeRetries: 5,
		SpecRetries: 2, MaxSpecRetries: 3,
		ValidationRetries: 1, MaxValidationRetries: 2,
		StuckRetries: 0, MaxStuckRetries: 3,
	}}}
	require.NoError(t, SaveGoals(root, gf))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	assert.Equal(t, 3, loaded.Goals[0].StuckRetries,
		"StuckRetries==0 with MaxStuckRetries>0 and non-terminal must re-seed")
}

func TestLoadGoals_SkipsStuckReseedForTerminal(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalFailed,
		MaxRetries:  5,
		CodeRetries: 0, MaxCodeRetries: 5,
		SpecRetries: 0, MaxSpecRetries: 3,
		ValidationRetries: 0, MaxValidationRetries: 2,
		StuckRetries: 0, MaxStuckRetries: 3,
	}}}
	require.NoError(t, SaveGoals(root, gf))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.Goals[0].StuckRetries,
		"terminal goals must NOT re-seed StuckRetries")
}

func TestResetGoal_ZeroesStuckRetries(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalFailed,
		StuckRetries: 2, MaxStuckRetries: 3,
	}}}

	ok := gf.ResetGoal("goal-001")
	require.True(t, ok)
	assert.Equal(t, 0, gf.Goals[0].StuckRetries, "ResetGoal must zero StuckRetries")
}

func TestResetGoal_PreservesMaxStuckRetries(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalFailed,
		StuckRetries: 1, MaxStuckRetries: 3,
	}}}

	ok := gf.ResetGoal("goal-001")
	require.True(t, ok)
	assert.Equal(t, 3, gf.Goals[0].MaxStuckRetries, "ResetGoal must preserve MaxStuckRetries")
}

func TestLoadGoals_AfterReset_ReseedsStuckRetries(t *testing.T) {
	root := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-001", Description: "test", Status: GoalPending,
		MaxRetries:  5,
		CodeRetries: 5, MaxCodeRetries: 5,
		SpecRetries: 3, MaxSpecRetries: 3,
		ValidationRetries: 2, MaxValidationRetries: 2,
		StuckRetries: 0, MaxStuckRetries: 3,
	}}}
	require.NoError(t, SaveGoals(root, gf))

	loaded, err := LoadGoals(root)
	require.NoError(t, err)
	assert.Equal(t, 3, loaded.Goals[0].StuckRetries,
		"after reset (StuckRetries=0, pending), LoadGoals must re-seed to MaxStuckRetries")
}

func TestConsumedRetries_ExcludesStuckRetries(t *testing.T) {
	g := &Goal{
		MaxCodeRetries: 5, CodeRetries: 5,
		MaxSpecRetries: 3, SpecRetries: 3,
		MaxValidationRetries: 2, ValidationRetries: 2,
		MaxStuckRetries: 3, StuckRetries: 1,
	}
	assert.Equal(t, 0, consumedRetries(g),
		"consumedRetries must NOT count consumed StuckRetries")
}

// --- B4: repair-at-dispatch Investigation Config guard ---------------------

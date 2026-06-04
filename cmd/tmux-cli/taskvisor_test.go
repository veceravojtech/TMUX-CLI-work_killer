package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	return string(out)
}

func withTempCwd(t *testing.T, fn func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(orig)) }()
	fn(dir)
}

func TestTaskvisorCommandHierarchy(t *testing.T) {
	tests := []struct {
		name string
		args []string
		use  string
	}{
		{"taskvisor", []string{"taskvisor"}, "taskvisor"},
		{"taskvisor start", []string{"taskvisor", "start"}, "start"},
		{"taskvisor goal", []string{"taskvisor", "goal"}, "goal"},
		{"taskvisor goal add", []string{"taskvisor", "goal", "add"}, "add"},
		{"taskvisor goal list", []string{"taskvisor", "goal", "list"}, "list"},
		{"taskvisor goal delete", []string{"taskvisor", "goal", "delete"}, "delete [goal-id]"},
		{"taskvisor goal reset", []string{"taskvisor", "goal", "reset"}, "reset [goal-id]"},
		{"taskvisor goal skip", []string{"taskvisor", "goal", "skip"}, "skip [goal-id]"},
		{"taskvisor goal stop", []string{"taskvisor", "goal", "stop"}, "stop"},
		{"taskvisor goal prune", []string{"taskvisor", "goal", "prune"}, "prune"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.use, cmd.Use)
		})
	}
}

func TestTaskvisorRunFlag_Hidden(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor"})
	require.NoError(t, err)

	flag := cmd.Flags().Lookup("run")
	require.NotNil(t, flag, "--run flag should exist on taskvisor command")
	assert.True(t, flag.Hidden, "--run flag should be hidden")

	help := cmd.UsageString()
	assert.NotContains(t, help, "--run")
}

func TestGoalAddCmd_RequiresDescription(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "goal", "add"})
	require.NoError(t, err)

	flag := cmd.Flags().Lookup("description")
	require.NotNil(t, flag, "--description flag should exist")

	err = cmd.ValidateRequiredFlags()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description")
}

// TestGoalAddDefaultMaxRetriesIsFive pins the `goal add --max-retries` flag
// default at 5 (bumped from 3 so MigrateRetries yields Spec≥2, never an
// instant spec-fail).
func TestGoalAddDefaultMaxRetriesIsFive(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "goal", "add"})
	require.NoError(t, err)

	flag := cmd.Flags().Lookup("max-retries")
	require.NotNil(t, flag, "--max-retries flag should exist")
	assert.Equal(t, "5", flag.DefValue)
}

func TestGoalAddCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries := goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries = oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries
		}()

		goalDescription = "Implement feature X"
		goalAcceptance = nil
		goalValidate = []string{"check"}
		goalContext = ""
		goalNotInScope = ""
		goalMaxRetries = 3

		output := captureStdout(t, func() {
			err := runTaskvisorGoalAdd(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "goal-001")

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.NotNil(t, gf)
		require.Len(t, gf.Goals, 1)
		assert.Equal(t, "goal-001", gf.Goals[0].ID)
		assert.Equal(t, "Implement feature X", gf.Goals[0].Description)
		assert.Equal(t, taskvisor.GoalPending, gf.Goals[0].Status)
		assert.Equal(t, 3, gf.Goals[0].MaxRetries)
	})
}

func TestGoalAddCmd_WithAllFlags(t *testing.T) {
	withTempCwd(t, func(dir string) {
		oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries := goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries = oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries
		}()

		goalDescription = "Build API endpoint"
		goalAcceptance = []string{"Returns 200 on success", "Validates input"}
		goalValidate = []string{"go test ./...", "curl http://localhost/api"}
		goalContext = ""
		goalNotInScope = ""
		goalMaxRetries = 5

		err := runTaskvisorGoalAdd(nil, nil)
		require.NoError(t, err)

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.NotNil(t, gf)
		require.Len(t, gf.Goals, 1)

		g := gf.Goals[0]
		assert.Equal(t, "Build API endpoint", g.Description)
		// Inverted per supervisor AMEND (F5/RC-A): acceptance/validate are now
		// persisted as structured Goal fields — the daemon reads them from
		// goals.yaml (EnsureInvestigationConfig, own-suite derivation).
		assert.Equal(t, []string{"Returns 200 on success", "Validates input"}, g.Acceptance, "acceptance must persist to goals.yaml")
		assert.Equal(t, []string{"go test ./...", "curl http://localhost/api"}, g.Validate, "validate must persist to goals.yaml")
		assert.Equal(t, 5, g.MaxRetries)

		mdData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md"))
		require.NoError(t, err)
		mdContent := string(mdData)
		assert.Contains(t, mdContent, "# Build API endpoint")
		assert.Contains(t, mdContent, "- Returns 200 on success")
		assert.Contains(t, mdContent, "- Validates input")
		assert.Contains(t, mdContent, "- go test ./...")
		assert.Contains(t, mdContent, "- curl http://localhost/api")
	})
}

func TestGoalAddCmd_WithContextFlags(t *testing.T) {
	withTempCwd(t, func(dir string) {
		oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries := goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries = oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries
		}()

		goalDescription = "Refactor auth"
		goalAcceptance = []string{"Tests pass"}
		goalValidate = []string{"check"}
		goalContext = "Legacy code needs cleanup"
		goalNotInScope = "Performance tuning"
		goalMaxRetries = 3

		err := runTaskvisorGoalAdd(nil, nil)
		require.NoError(t, err)

		mdData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md"))
		require.NoError(t, err)
		mdContent := string(mdData)
		assert.Contains(t, mdContent, "## Context")
		assert.Contains(t, mdContent, "Legacy code needs cleanup")
		assert.Contains(t, mdContent, "## Not In Scope")
		assert.Contains(t, mdContent, "Performance tuning")
	})
}

func TestGoalAddCmd_DescriptionTooLong(t *testing.T) {
	withTempCwd(t, func(dir string) {
		oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries := goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalContext, goalNotInScope, goalMaxRetries = oldDesc, oldAcc, oldVal, oldCtx, oldNIS, oldRetries
		}()

		goalDescription = strings.Repeat("x", 121)
		goalAcceptance = nil
		goalValidate = nil
		goalContext = ""
		goalNotInScope = ""
		goalMaxRetries = 3

		err := runTaskvisorGoalAdd(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "120")
		assert.Contains(t, err.Error(), "--acceptance")
	})
}

func TestGoalListCmd_PrintsTable(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "First goal", Status: taskvisor.GoalPending, MaxRetries: 3},
				{ID: "goal-002", Description: "Second goal", Status: taskvisor.GoalDone, Retries: 1, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalList(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "goal-001")
		assert.Contains(t, output, "goal-002")
		assert.Contains(t, output, "First goal")
		assert.Contains(t, output, "Second goal")
		assert.Contains(t, output, "pending")
		assert.Contains(t, output, "done")
	})
}

func TestGoalListCmd_Empty(t *testing.T) {
	withTempCwd(t, func(dir string) {
		output := captureStdout(t, func() {
			err := runTaskvisorGoalList(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "No goals")
	})
}

func TestTaskvisorStartCmd_WritesSignalFile(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Test goal", Status: taskvisor.GoalPending, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorStart(nil, nil)
		require.NoError(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, err = os.Stat(signalPath)
		assert.NoError(t, err, "signal file should exist")
	})
}

func TestTaskvisorStartCmd_NoPendingGoals(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Done goal", Status: taskvisor.GoalDone, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorStart(nil, nil)
		assert.Error(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.True(t, os.IsNotExist(statErr), "signal file should not exist")
	})
}

func TestGoalDeleteCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "First", Status: taskvisor.GoalPending, MaxRetries: 3},
				{ID: "goal-002", Description: "Second", Status: taskvisor.GoalDone, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))
		goalDir, err := taskvisor.EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)

		output := captureStdout(t, func() {
			err := runTaskvisorGoalDelete(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		assert.Contains(t, output, "goal-001")
		assert.Contains(t, output, "deleted")

		loaded, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, loaded.Goals, 1)
		assert.Equal(t, "goal-002", loaded.Goals[0].ID)

		_, statErr := os.Stat(goalDir)
		assert.True(t, os.IsNotExist(statErr), "goal dir should be removed")
	})
}

func TestGoalDeleteCmd_RunningGoal(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Running", Status: taskvisor.GoalRunning, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalDelete(nil, []string{"goal-001"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "currently running")

		loaded, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, loaded.Goals, 1)
	})
}

func TestGoalDeleteCmd_NotFound(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "First", Status: taskvisor.GoalPending},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalDelete(nil, []string{"goal-999"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGoalDeleteCmd_MissingDir(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "First", Status: taskvisor.GoalPending},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalDelete(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		assert.Contains(t, output, "deleted")

		loaded, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, loaded.Goals, 0)
	})
}

func TestGoalResetCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Failed goal", Status: taskvisor.GoalFailed, Retries: 2, MaxRetries: 3, FinishedAt: "2026-05-20T15:00:00Z"},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalReset(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		assert.Contains(t, output, "goal-001")
		assert.Contains(t, output, "reset")

		loaded, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, loaded.Goals, 1)
		assert.Equal(t, taskvisor.GoalPending, loaded.Goals[0].Status)
		assert.Equal(t, 0, loaded.Goals[0].Retries)
		assert.Equal(t, "", loaded.Goals[0].FinishedAt)
	})
}

func TestGoalResetCmd_NotFailed(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Pending goal", Status: taskvisor.GoalPending},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalReset(nil, []string{"goal-001"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in failed status")
	})
}

func TestGoalResetCmd_NotFound(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Status: taskvisor.GoalFailed},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalReset(nil, []string{"goal-999"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGoalStopCmd_FilesPresent(t *testing.T) {
	withTempCwd(t, func(dir string) {
		tmuxDir := filepath.Join(dir, ".tmux-cli")
		require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-active"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-start"), nil, 0o644))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalStop(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "stop signal sent")

		_, err := os.Stat(filepath.Join(tmuxDir, "taskvisor-active"))
		assert.True(t, os.IsNotExist(err))
		_, err = os.Stat(filepath.Join(tmuxDir, "taskvisor-start"))
		assert.True(t, os.IsNotExist(err))
	})
}

func TestGoalStopCmd_Idempotent(t *testing.T) {
	withTempCwd(t, func(dir string) {
		output := captureStdout(t, func() {
			err := runTaskvisorGoalStop(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "stop signal sent")
	})
}

// --- GoalPrune CLI tests ---

func TestGoalPruneCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "First", Status: taskvisor.GoalDone, MaxRetries: 3},
				{ID: "goal-002", Description: "Second", Status: taskvisor.GoalPending, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))
		_, err := taskvisor.EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)
		_, err = taskvisor.EnsureGoalDir(dir, "goal-002")
		require.NoError(t, err)

		output := captureStdout(t, func() {
			err := runTaskvisorGoalPrune(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "Pruned 2")

		_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "goals.yaml"))
		assert.True(t, os.IsNotExist(statErr), "goals.yaml should be removed")

		_, statErr = os.Stat(filepath.Join(dir, ".tmux-cli", "goals"))
		assert.True(t, os.IsNotExist(statErr), "goals/ dir should be removed")
	})
}

func TestGoalPruneCmd_Idempotent(t *testing.T) {
	withTempCwd(t, func(dir string) {
		output := captureStdout(t, func() {
			err := runTaskvisorGoalPrune(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "Pruned 0")
	})
}

func TestGoalPruneCmd_DaemonActive(t *testing.T) {
	withTempCwd(t, func(dir string) {
		tmuxDir := filepath.Join(dir, ".tmux-cli")
		require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-active"), nil, 0o644))

		err := runTaskvisorGoalPrune(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "active")
	})
}

func TestGoalPruneCmd_CleansSignalFiles(t *testing.T) {
	withTempCwd(t, func(dir string) {
		tmuxDir := filepath.Join(dir, ".tmux-cli")
		require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-current-goal"), []byte("goal-001"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-start"), nil, 0o644))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalPrune(nil, nil)
			require.NoError(t, err)
		})

		assert.Contains(t, output, "Pruned 0")

		_, statErr := os.Stat(filepath.Join(tmuxDir, "taskvisor-current-goal"))
		assert.True(t, os.IsNotExist(statErr), "taskvisor-current-goal should be removed")

		_, statErr = os.Stat(filepath.Join(tmuxDir, "taskvisor-start"))
		assert.True(t, os.IsNotExist(statErr), "taskvisor-start should be removed")
	})
}

// --- GoalSkip CLI tests ---

func TestGoalSkipCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			CurrentGoal: "goal-001",
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Running goal", Status: taskvisor.GoalRunning, MaxRetries: 3, StartedAt: "2026-05-20T14:00:00Z"},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		output := captureStdout(t, func() {
			err := runTaskvisorGoalSkip(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		assert.Contains(t, output, "skipped")
		assert.Contains(t, output, "goal-001")

		loaded, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, loaded.Goals, 1)
		assert.Equal(t, taskvisor.GoalDone, loaded.Goals[0].Status)
		assert.NotEmpty(t, loaded.Goals[0].FinishedAt)

		skippedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "skipped.md")
		data, err := os.ReadFile(skippedPath)
		require.NoError(t, err)
		assert.Equal(t, "manually skipped", string(data))
	})
}

func TestGoalSkipCmd_NotRunning(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Pending goal", Status: taskvisor.GoalPending},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalSkip(nil, []string{"goal-001"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not running")
	})
}

func TestGoalSkipCmd_NotFound(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Status: taskvisor.GoalRunning},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorGoalSkip(nil, []string{"goal-999"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGoalSkipCmd_WritesSkippedMd(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Running goal", Status: taskvisor.GoalRunning, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		oldReason := skipReason
		defer func() { skipReason = oldReason }()
		skipReason = "blocked by infra"

		output := captureStdout(t, func() {
			err := runTaskvisorGoalSkip(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		assert.Contains(t, output, "skipped")

		skippedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "skipped.md")
		data, err := os.ReadFile(skippedPath)
		require.NoError(t, err)
		assert.Equal(t, "blocked by infra", string(data))
	})
}

func TestGoalSkipCmd_CustomReason(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Running goal", Status: taskvisor.GoalRunning, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		oldReason := skipReason
		defer func() { skipReason = oldReason }()
		skipReason = "no longer relevant"

		captureStdout(t, func() {
			err := runTaskvisorGoalSkip(nil, []string{"goal-001"})
			require.NoError(t, err)
		})

		skippedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "skipped.md")
		data, err := os.ReadFile(skippedPath)
		require.NoError(t, err)
		assert.Equal(t, "no longer relevant", string(data))
	})
}

// TestRunTaskvisorStart_WritesSignalForRecoverableBlock guards the activation-side
// recoverable-block fix: a graph whose only outstanding work is a GoalBlocked goal
// blocked_by a now-Done goal (deps satisfied) has 0 pending goals, yet must still
// activate so activate()'s reconcile can un-stick the frontier. Fails pre-fix
// (errored "no pending goals"), passes after.
func TestRunTaskvisorStart_WritesSignalForRecoverableBlock(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Done blocker", Status: taskvisor.GoalDone, MaxRetries: 3},
				{ID: "goal-002", Description: "Recoverable block", Status: taskvisor.GoalBlocked, BlockedBy: "goal-001", MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorStart(nil, nil)
		require.NoError(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.NoError(t, statErr, "signal file should exist for a recoverable-only graph")
	})
}

// TestRunTaskvisorStart_WritesSignalForPending is a regression guard: a graph with
// at least one pending goal keeps the original behavior (signal written, no error).
func TestRunTaskvisorStart_WritesSignalForPending(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Pending goal", Status: taskvisor.GoalPending, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorStart(nil, nil)
		require.NoError(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.NoError(t, statErr, "signal file should exist when a pending goal exists")
	})
}

// TestRunTaskvisorStart_RefusesTerminalGraph asserts the fix does NOT widen
// activation to genuinely-terminal graphs: a GoalBlocked goal whose blocker is
// GoalFailed (a hard block, not recoverable) plus only Done goals must still be
// refused with no signal written.
func TestRunTaskvisorStart_RefusesTerminalGraph(t *testing.T) {
	withTempCwd(t, func(dir string) {
		gf := &taskvisor.GoalsFile{
			Goals: []taskvisor.Goal{
				{ID: "goal-001", Description: "Done goal", Status: taskvisor.GoalDone, MaxRetries: 3},
				{ID: "goal-002", Description: "Hard-blocked goal", Status: taskvisor.GoalBlocked, BlockedBy: "goal-003", MaxRetries: 3},
				{ID: "goal-003", Description: "Failed blocker", Status: taskvisor.GoalFailed, MaxRetries: 3},
			},
		}
		require.NoError(t, taskvisor.SaveGoals(dir, gf))

		err := runTaskvisorStart(nil, nil)
		require.Error(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.True(t, os.IsNotExist(statErr), "signal file should not exist for a terminal graph")
	})
}

func TestTaskvisorStartCmd_NoGoalsFile(t *testing.T) {
	withTempCwd(t, func(dir string) {
		err := runTaskvisorStart(nil, nil)
		assert.Error(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.True(t, os.IsNotExist(statErr), "signal file should not exist")
	})
}

func TestInvestigateWorkerTemplate_ContainsRetryLogic(t *testing.T) {
	xmlData, err := embeddedCommands.ReadFile("embedded/commands/tmux/investigate-worker.xml")
	require.NoError(t, err)
	xmlContent := string(xmlData)

	assert.Contains(t, xmlContent, "<retry-logic>")
	assert.Contains(t, xmlContent, "max_attempts")
	assert.Contains(t, xmlContent, "re-run ONLY the failing command")
	assert.Contains(t, xmlContent, "Pass on first success")
	assert.Contains(t, xmlContent, "At max_attempts with no success")
	assert.Contains(t, xmlContent, "Log each attempt in FINDINGS")

	mdData, err := embeddedCommands.ReadFile("embedded/commands/tmux/investigate-worker.md")
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "Retry flaky commands per retry config")
	assert.Contains(t, mdContent, "default: 1 attempt, no retry")
}

// TestParseGoalFindings_ReadsGeneratedConfig guards the bullet-tolerance fix:
// a goal.md rendered by WriteGoalMD (with Paths) must parse into one finding per
// investigator, Rule = the name after the colon, Scope populated from `- paths:`.
func TestParseGoalFindings_ReadsGeneratedConfig(t *testing.T) {
	dir := t.TempDir()
	invs := []taskvisor.Investigator{
		{Name: "Quality gate", Type: "quality-gate", Paths: []string{"src/Pricing.php", "src/Tax.php"}, Commands: []string{"phpstan analyse"}, Pass: "exit 0", Fail: "errors"},
		{Name: "Test execution", Type: "test-execution", Paths: []string{"tests/PricingTest.php"}, Commands: []string{"phpunit"}, Pass: "green", Fail: "red"},
	}
	require.NoError(t, taskvisor.WriteGoalMD(dir, "Parse roundtrip", "", []string{"AC1"}, []string{"x"}, nil, "", "", invs))

	findings, err := parseGoalFindings(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	require.Len(t, findings, 2)

	assert.Equal(t, "Quality gate", findings[0].Rule)
	assert.Equal(t, []string{"src/Pricing.php", "src/Tax.php"}, findings[0].Scope)
	assert.Equal(t, "Test execution", findings[1].Rule)
	assert.Equal(t, []string{"tests/PricingTest.php"}, findings[1].Scope)
}

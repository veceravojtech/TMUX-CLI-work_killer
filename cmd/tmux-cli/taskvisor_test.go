package main

import (
	"io"
	"os"
	"path/filepath"
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

func TestGoalAddCmd_Success(t *testing.T) {
	withTempCwd(t, func(dir string) {
		oldDesc, oldAcc, oldVal, oldRetries := goalDescription, goalAcceptance, goalValidate, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalMaxRetries = oldDesc, oldAcc, oldVal, oldRetries
		}()

		goalDescription = "Implement feature X"
		goalAcceptance = nil
		goalValidate = nil
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
		oldDesc, oldAcc, oldVal, oldRetries := goalDescription, goalAcceptance, goalValidate, goalMaxRetries
		defer func() {
			goalDescription, goalAcceptance, goalValidate, goalMaxRetries = oldDesc, oldAcc, oldVal, oldRetries
		}()

		goalDescription = "Build API endpoint"
		goalAcceptance = []string{"Returns 200 on success", "Validates input"}
		goalValidate = []string{"go test ./...", "curl http://localhost/api"}
		goalMaxRetries = 5

		err := runTaskvisorGoalAdd(nil, nil)
		require.NoError(t, err)

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.NotNil(t, gf)
		require.Len(t, gf.Goals, 1)

		g := gf.Goals[0]
		assert.Equal(t, "Build API endpoint", g.Description)
		assert.Equal(t, []string{"Returns 200 on success", "Validates input"}, g.Acceptance)
		assert.Equal(t, []string{"go test ./...", "curl http://localhost/api"}, g.Validate)
		assert.Equal(t, 5, g.MaxRetries)
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

func TestTaskvisorStartCmd_NoGoalsFile(t *testing.T) {
	withTempCwd(t, func(dir string) {
		err := runTaskvisorStart(nil, nil)
		assert.Error(t, err)

		signalPath := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
		_, statErr := os.Stat(signalPath)
		assert.True(t, os.IsNotExist(statErr), "signal file should not exist")
	})
}

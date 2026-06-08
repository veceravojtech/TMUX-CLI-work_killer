package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

func TestGoalPrune_WithGoals(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: done
  max_retries: 3
- id: goal-002
  description: Second
  status: pending
  max_retries: 3
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "corrections"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-002", "corrections"), 0o755))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)
	assert.Equal(t, 2, output.GoalsRemoved)

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals.yaml"))
	assert.True(t, os.IsNotExist(statErr), "goals.yaml should be removed")

	_, statErr = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "goals/ dir should be removed")
}

func TestGoalPrune_NoGoals(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)
	assert.Equal(t, 0, output.GoalsRemoved)
}

func TestGoalPrune_DaemonActive(t *testing.T) {
	tmpDir := t.TempDir()
	tmuxDir := filepath.Join(tmpDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-active"), nil, 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalPrune()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "active")
}

func TestGoalPrune_CleansSignalFiles(t *testing.T) {
	tmpDir := t.TempDir()
	tmuxDir := filepath.Join(tmpDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-current-goal"), []byte("goal-001"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-start"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-current-cycle"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-current-worktree"), nil, 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)

	_, statErr := os.Stat(filepath.Join(tmuxDir, "taskvisor-current-goal"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-current-goal should be removed")

	_, statErr = os.Stat(filepath.Join(tmuxDir, "taskvisor-start"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-start should be removed")

	_, statErr = os.Stat(filepath.Join(tmuxDir, "taskvisor-current-cycle"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-current-cycle should be removed")

	_, statErr = os.Stat(filepath.Join(tmuxDir, "taskvisor-current-worktree"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-current-worktree should be removed")
}

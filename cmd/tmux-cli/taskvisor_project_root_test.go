package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCwd chdirs into dir for the duration of fn, restoring the original cwd.
func withCwd(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(orig)) }()
	fn()
}

// TestTaskvisorProjectRoot_FromWorktreeCwd verifies that goal commands invoked
// from a per-goal worktree cwd resolve the BASE project root, so LoadGoals
// finds the base goals.yaml instead of a nested empty one ("goal not found").
func TestTaskvisorProjectRoot_FromWorktreeCwd(t *testing.T) {
	base := t.TempDir()
	// Goals live in the BASE control plane only — worktrees carry no .tmux-cli.
	gf := &taskvisor.GoalsFile{
		Goals: []taskvisor.Goal{
			{ID: "goal-001", Description: "Base goal", Status: taskvisor.GoalPending, MaxRetries: 3},
		},
	}
	require.NoError(t, taskvisor.SaveGoals(base, gf))

	worktree := filepath.Join(base, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(worktree, 0o755))

	withCwd(t, worktree, func() {
		root, err := taskvisorProjectRoot()
		require.NoError(t, err)

		// os.Getwd may return a symlink-resolved path (e.g. /tmp on tmpfs), so
		// compare against the same resolution of base.
		resolvedBase, err := filepath.EvalSymlinks(base)
		require.NoError(t, err)
		assert.Equal(t, resolvedBase, root)

		loaded, err := taskvisor.LoadGoals(root)
		require.NoError(t, err)
		require.NotNil(t, loaded, "LoadGoals from the resolved root must find the base goals.yaml")
		g, ok := loaded.GoalByID("goal-001")
		require.True(t, ok, "base goal must be visible from a worktree cwd")
		assert.Equal(t, "Base goal", g.Description)
	})
}

// TestTaskvisorProjectRoot_NonWorktreePassthrough verifies that a normal
// project cwd passes through unchanged.
func TestTaskvisorProjectRoot_NonWorktreePassthrough(t *testing.T) {
	base := t.TempDir()
	withCwd(t, base, func() {
		root, err := taskvisorProjectRoot()
		require.NoError(t, err)

		resolvedBase, err := filepath.EvalSymlinks(base)
		require.NoError(t, err)
		assert.Equal(t, resolvedBase, root)
	})
}

// TestGoalListCmd_FromWorktreeCwd is the end-to-end regression for the
// goal-061 incident: `goal list` run from a worktree cwd must list the BASE
// goals instead of reporting an empty nested store.
func TestGoalListCmd_FromWorktreeCwd(t *testing.T) {
	base := t.TempDir()
	gf := &taskvisor.GoalsFile{
		Goals: []taskvisor.Goal{
			{ID: "goal-061", Description: "Incident goal", Status: taskvisor.GoalFailed, MaxRetries: 3},
		},
	}
	require.NoError(t, taskvisor.SaveGoals(base, gf))

	worktree := filepath.Join(base, ".tmux-cli", "worktrees", "goal-061")
	require.NoError(t, os.MkdirAll(worktree, 0o755))

	withCwd(t, worktree, func() {
		output := captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalList(nil, nil))
		})
		assert.Contains(t, output, "goal-061")
		assert.Contains(t, output, "Incident goal")
	})
}

// TestGoalResetCmd_FromWorktreeCwd verifies `goal reset` from a worktree cwd
// resolves the base store: the failed goal is found and re-pended there.
func TestGoalResetCmd_FromWorktreeCwd(t *testing.T) {
	base := t.TempDir()
	gf := &taskvisor.GoalsFile{
		Goals: []taskvisor.Goal{
			{ID: "goal-061", Description: "Incident goal", Status: taskvisor.GoalFailed, MaxRetries: 3},
		},
	}
	require.NoError(t, taskvisor.SaveGoals(base, gf))

	worktree := filepath.Join(base, ".tmux-cli", "worktrees", "goal-061")
	require.NoError(t, os.MkdirAll(worktree, 0o755))

	withCwd(t, worktree, func() {
		require.NoError(t, runTaskvisorGoalReset(nil, []string{"goal-061"}))
	})

	loaded, err := taskvisor.LoadGoals(base)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	g, ok := loaded.GoalByID("goal-061")
	require.True(t, ok)
	assert.NotEqual(t, taskvisor.GoalFailed, g.Status, "reset must re-pend the goal in the BASE store")

	// The worktree must NOT have grown a nested control plane.
	_, statErr := os.Stat(filepath.Join(worktree, ".tmux-cli", "goals.yaml"))
	assert.True(t, os.IsNotExist(statErr), "no nested goals.yaml may be created inside the worktree")
}

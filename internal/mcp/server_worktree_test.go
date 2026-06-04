package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

// TestNewServer_NormalizesWorktreeCwd verifies that a per-goal worktree cwd
// is normalized back to the base project path at construction time.
func TestNewServer_NormalizesWorktreeCwd(t *testing.T) {
	server := NewServer("/base/.tmux-cli/worktrees/goal-001")

	assert.Equal(t, "/base", server.workingDir)
}

// TestNewServer_NormalizesNestedWorktreePath verifies that a cwd nested
// deeper inside a worktree still normalizes to the base project path.
func TestNewServer_NormalizesNestedWorktreePath(t *testing.T) {
	server := NewServer("/base/.tmux-cli/worktrees/goal-001/internal/sub")

	assert.Equal(t, "/base", server.workingDir)
}

// TestNewServer_NonWorktreeUnchanged verifies that non-worktree paths are
// preserved exactly, including paths under .tmux-cli that are not worktrees.
func TestNewServer_NonWorktreeUnchanged(t *testing.T) {
	assert.Equal(t, "/base", NewServer("/base").workingDir)
	assert.Equal(t, "/base/.tmux-cli/goals/goal-001",
		NewServer("/base/.tmux-cli/goals/goal-001").workingDir)
}

// TestDiscoverSession_FromWorktreeCwd verifies that session discovery from a
// worktree cwd matches TMUX_CLI_PROJECT_PATH against the BASE project path.
func TestDiscoverSession_FromWorktreeCwd(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/base").Return("base-session", nil)

	server := NewServerWithExecutor(mockExec, "/base/.tmux-cli/worktrees/goal-004")
	sessionID, err := server.discoverSession()

	require.NoError(t, err)
	assert.Equal(t, "base-session", sessionID)
	mockExec.AssertExpectations(t)
}

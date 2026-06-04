package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeProjectDir_WorktreeSuffix verifies that a per-goal worktree
// root (<base>/.tmux-cli/worktrees/<id>) maps back to <base>.
func TestNormalizeProjectDir_WorktreeSuffix(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees/goal-001"))
}

// TestNormalizeProjectDir_NestedSubdir verifies that a cwd nested deeper
// inside a worktree still normalizes to the base project path.
func TestNormalizeProjectDir_NestedSubdir(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees/goal-001/internal/sub"))
}

// TestNormalizeProjectDir_WorktreesDirExact verifies the bare worktrees
// directory itself (no trailing separator) also maps back to <base>.
func TestNormalizeProjectDir_WorktreesDirExact(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees"))
}

// TestNormalizeProjectDir_NonWorktreePassthrough verifies that non-worktree
// paths pass through unchanged, including paths under .tmux-cli that are not
// worktrees.
func TestNormalizeProjectDir_NonWorktreePassthrough(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base"))
	assert.Equal(t, "/base/.tmux-cli/goals/goal-001", NormalizeProjectDir("/base/.tmux-cli/goals/goal-001"))
	assert.Equal(t, "/base/worktrees/goal-001", NormalizeProjectDir("/base/worktrees/goal-001"))
}

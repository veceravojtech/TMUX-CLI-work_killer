package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestNormalizeProjectDir_SiblingSuffix verifies that the relocated per-goal
// worktree path (<base>/.tmux-cli-worktrees/<id>) and a cwd nested deeper inside
// it both normalize back to <base> via pure-string suffix stripping (no FS).
func TestNormalizeProjectDir_SiblingSuffix(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli-worktrees/goal-001"))
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli-worktrees/goal-001/internal/sub"))
	// Bare sibling dir (no trailing separator) also maps back to base.
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli-worktrees"))
}

// TestNormalizeProjectDir_LegacySuffixStillWorks pins the one-release back-compat
// guarantee: a pre-upgrade worktree still under <base>/.tmux-cli/worktrees/<id>
// must continue to normalize to <base>.
func TestNormalizeProjectDir_LegacySuffixStillWorks(t *testing.T) {
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees/goal-001"))
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees/goal-001/internal/sub"))
	assert.Equal(t, "/base", NormalizeProjectDir("/base/.tmux-cli/worktrees"))
}

// TestNormalizeProjectDir_SymlinkResolve verifies location-independent discovery:
// when the cwd is a worktree root carrying a <cwd>/.tmux-cli symlink back to the
// base control plane, NormalizeProjectDir resolves the symlink and returns the
// base project dir — even when the worktree path itself carries no marker, so the
// symlink branch is the only resolver that can succeed.
func TestNormalizeProjectDir_SymlinkResolve(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, ".tmux-cli"), 0o755))

	// Worktree at an unmarked location: only the symlink branch can resolve it.
	wtParent := t.TempDir()
	wt := filepath.Join(wtParent, "isolated-wt")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(base, ".tmux-cli"), filepath.Join(wt, ".tmux-cli")))

	assert.Equal(t, base, NormalizeProjectDir(wt),
		"a <cwd>/.tmux-cli symlink must resolve to the base project dir")
}

// TestNormalizeProjectDir_SymlinkResolve_RelativeTarget verifies a relative
// symlink target is joined against the worktree dir before resolving the base.
func TestNormalizeProjectDir_SymlinkResolve_RelativeTarget(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	require.NoError(t, os.MkdirAll(filepath.Join(base, ".tmux-cli"), 0o755))
	wt := filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	// Relative target: ../base/.tmux-cli
	require.NoError(t, os.Symlink(filepath.Join("..", "base", ".tmux-cli"), filepath.Join(wt, ".tmux-cli")))

	assert.Equal(t, base, NormalizeProjectDir(wt),
		"a relative <cwd>/.tmux-cli symlink target must resolve to the base project dir")
}

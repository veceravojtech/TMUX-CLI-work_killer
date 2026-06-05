package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
)

// NormalizeProjectDir maps a per-goal worktree cwd back to the BASE project dir.
// Non-worktree paths are returned unchanged.
//
// The taskvisor daemon dispatches goal supervisors into git worktrees under the
// in-repo sibling .tmux-cli-worktrees/; both the MCP server and the goal CLI
// commands inherit that cwd, but session discovery matches TMUX_CLI_PROJECT_PATH
// by exact equality against the BASE project path, and goal/tasks/research
// routing must hit the base .tmux-cli control plane (worktrees do not contain a
// real .tmux-cli — only a back-symlink to base, which is git-excluded).
//
// Resolution order:
//  1. Symlink-resolve (location-independent): if <dir>/.tmux-cli is a symlink, the
//     cwd is a worktree root and the symlink points at <base>/.tmux-cli, so the
//     base is the symlink target's parent. This works regardless of where the
//     worktree lives.
//  2. Sibling suffix strip: a <base>/.tmux-cli-worktrees/<id>[/...] path (pure
//     string, no FS) maps back to <base>.
//  3. Legacy suffix strip (kept one release for back-compat): a pre-upgrade
//     <base>/.tmux-cli/worktrees/<id>[/...] path maps back to <base>.
//  4. Otherwise the path is returned unchanged.
//
// Shared by the MCP server delegate and the CLI taskvisorProjectRoot so both
// resolve the base control plane identically.
func NormalizeProjectDir(dir string) string {
	sep := string(os.PathSeparator)

	// (1) Symlink-resolve: <dir>/.tmux-cli is the worktree's back-symlink to base.
	ctl := filepath.Join(dir, ".tmux-cli")
	if fi, err := os.Lstat(ctl); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(ctl); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return filepath.Dir(filepath.Clean(target))
		}
	}

	// (2) Sibling worktrees dir: <base>/.tmux-cli-worktrees/<id>[/...].
	if marker := sep + worktreesDirName + sep; strings.Contains(dir, marker) {
		return dir[:strings.Index(dir, marker)]
	}
	if suffix := sep + worktreesDirName; strings.HasSuffix(dir, suffix) {
		return strings.TrimSuffix(dir, suffix)
	}

	// (3) Legacy nested worktrees dir (one-release back-compat).
	legacyMarker := sep + ".tmux-cli" + sep + "worktrees" + sep
	if i := strings.Index(dir, legacyMarker); i >= 0 {
		return dir[:i]
	}
	legacySuffix := sep + ".tmux-cli" + sep + "worktrees"
	if strings.HasSuffix(dir, legacySuffix) {
		return strings.TrimSuffix(dir, legacySuffix)
	}

	// (4) Non-worktree passthrough.
	return dir
}

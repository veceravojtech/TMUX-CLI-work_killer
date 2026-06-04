package taskvisor

import (
	"os"
	"strings"
)

// NormalizeProjectDir maps a per-goal worktree cwd (<base>/.tmux-cli/worktrees/<id>[/...])
// back to <base>. Non-worktree paths are returned unchanged.
//
// The taskvisor daemon dispatches goal supervisors into git worktrees under
// .tmux-cli/worktrees/; both the MCP server and the goal CLI commands inherit
// that cwd, but session discovery matches TMUX_CLI_PROJECT_PATH by exact
// equality against the BASE project path, and goal/tasks/research routing must
// hit the base .tmux-cli control plane (worktrees do not contain .tmux-cli —
// it is git-excluded). Moved verbatim from internal/mcp/server.go so the CLI
// and MCP share one implementation.
func NormalizeProjectDir(dir string) string {
	marker := string(os.PathSeparator) + ".tmux-cli" + string(os.PathSeparator) + "worktrees" + string(os.PathSeparator)
	if i := strings.Index(dir, marker); i >= 0 {
		return dir[:i]
	}
	// Also handle dir ending exactly at .../.tmux-cli/worktrees (no trailing separator).
	suffix := string(os.PathSeparator) + ".tmux-cli" + string(os.PathSeparator) + "worktrees"
	if strings.HasSuffix(dir, suffix) {
		return strings.TrimSuffix(dir, suffix)
	}
	return dir
}

package setup

import (
	"os"
	"path/filepath"
	"strings"
)

var gitExcludeEntries = []string{
	// Exclude .tmux-cli as a NAME (no trailing slash) so the pattern matches BOTH
	// the real control-plane directory AND the per-goal worktree's .tmux-cli
	// back-symlink (git mode 120000). A directory-only "/.tmux-cli/" let that
	// back-symlink slip past `git add -A` in a parallel-mode worktree, get
	// committed, and fast-forward-merge into base — which replaced base's real
	// .tmux-cli directory with a self-referential symlink (ELOOP) and destroyed
	// the control plane. See taskvisor.symlinkControlPlane / mergeWorktreeBack.
	"/.tmux-cli",
	"/.tmux-cli-worktrees/",
	"/.tmux-cli/logs/",
	"/.claude/settings.json",
	"/.claude/commands/tmux/",
}

const managedHeader = "# tmux-cli managed"

func EnsureGitExclude(projectRoot string) error {
	infoDir := filepath.Join(projectRoot, ".git", "info")
	if _, err := os.Stat(infoDir); os.IsNotExist(err) {
		return nil
	}

	excludePath := filepath.Join(infoDir, "exclude")

	existing, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	lines := strings.Split(content, "\n")

	present := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			present[trimmed] = true
		}
	}

	var missing []string
	for _, entry := range gitExcludeEntries {
		if !present[entry] {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var buf strings.Builder
	buf.WriteString(content)

	if !present[managedHeader] {
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			buf.WriteByte('\n')
		}
		buf.WriteString(managedHeader)
		buf.WriteByte('\n')
	}

	for _, entry := range missing {
		buf.WriteString(entry)
		buf.WriteByte('\n')
	}

	return os.WriteFile(excludePath, []byte(buf.String()), 0o644)
}

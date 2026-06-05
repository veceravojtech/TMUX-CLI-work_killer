package setup

import (
	"os"
	"path/filepath"
	"strings"
)

var gitExcludeEntries = []string{
	"/.tmux-cli/",
	"/.tmux-cli-worktrees/",
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

package setup

import (
	"os"
	"path/filepath"
)

func WriteCommands(projectRoot string, templates map[string]string) error {
	tmuxDir := filepath.Join(projectRoot, ".claude", "commands", "tmux")

	if err := os.RemoveAll(tmuxDir); err != nil {
		return err
	}

	if err := os.MkdirAll(tmuxDir, 0755); err != nil {
		return err
	}

	for relPath, content := range templates {
		absPath := filepath.Join(tmuxDir, relPath)

		if dir := filepath.Dir(absPath); dir != tmuxDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}

		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}

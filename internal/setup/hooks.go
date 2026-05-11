package setup

import (
	"os"
	"path/filepath"
)

func WriteHookScripts(projectRoot string, scripts map[string]string) error {
	hooksDir := filepath.Join(projectRoot, ".tmux-cli", "hooks")
	logsDir := filepath.Join(projectRoot, ".tmux-cli", "logs")

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return err
	}

	for name, content := range scripts {
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			return err
		}
	}

	return nil
}

package setup

import (
	"os"
	"path/filepath"
)

func WriteTemplates(projectRoot string, templates map[string]string) error {
	tplDir := filepath.Join(projectRoot, ".tmux-cli", "templates")

	if err := os.RemoveAll(tplDir); err != nil {
		return err
	}

	if err := os.MkdirAll(tplDir, 0755); err != nil {
		return err
	}

	for relPath, content := range templates {
		absPath := filepath.Join(tplDir, relPath)

		if dir := filepath.Dir(absPath); dir != tplDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}

		tmpPath := absPath + ".tmp"
		if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
			return err
		}
		if err := os.Rename(tmpPath, absPath); err != nil {
			return err
		}
	}

	return nil
}

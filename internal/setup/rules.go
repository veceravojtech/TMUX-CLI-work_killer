package setup

import (
	"os"
	"path/filepath"
)

// WriteRules materializes the embedded rule catalogue into
// .tmux-cli/rules/. Embedded packs are clean-slated like WriteTemplates, but
// the local/ subtree is user-owned (project-specific rules, e.g. ingested
// from MR feedback) and must survive every re-setup — only non-local entries
// are removed.
func WriteRules(projectRoot string, rules map[string]string) error {
	rulesDir := filepath.Join(projectRoot, ".tmux-cli", "rules")

	entries, err := os.ReadDir(rulesDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.Name() == "local" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(rulesDir, e.Name())); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return err
	}

	for relPath, content := range rules {
		absPath := filepath.Join(rulesDir, relPath)

		if dir := filepath.Dir(absPath); dir != rulesDir {
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

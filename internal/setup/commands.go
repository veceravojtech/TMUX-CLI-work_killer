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

// PurgeUserCommandShadow resolves the user's home directory and removes the
// unmanaged global command copy at ~/.claude/commands/tmux, so a stale global
// shadow can never serve in place of the fresh project-local copy. A failure
// to resolve HOME is returned to the caller.
func PurgeUserCommandShadow() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return purgeUserCommandShadowAt(home)
}

// purgeUserCommandShadowAt removes <home>/.claude/commands/tmux. Sibling
// commands outside tmux/ are untouched. Idempotent: RemoveAll returns nil
// when the tree is absent.
func purgeUserCommandShadowAt(home string) error {
	return os.RemoveAll(filepath.Join(home, ".claude", "commands", "tmux"))
}

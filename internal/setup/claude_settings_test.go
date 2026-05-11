package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateClaudeSettings_AllEnabled(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
	}

	cs := GenerateClaudeSettings(s)

	require.Len(t, cs.Hooks.SessionStart, 1)
	require.Len(t, cs.Hooks.SessionStart[0].Hooks, 1)
	assert.Equal(t, "command", cs.Hooks.SessionStart[0].Hooks[0].Type)
	assert.Equal(t, 10, cs.Hooks.SessionStart[0].Hooks[0].Timeout)

	require.Len(t, cs.Hooks.SessionEnd, 1)
	require.Len(t, cs.Hooks.SessionEnd[0].Hooks, 1)
	assert.Equal(t, "command", cs.Hooks.SessionEnd[0].Hooks[0].Type)
	assert.Equal(t, 10, cs.Hooks.SessionEnd[0].Hooks[0].Timeout)

	require.Len(t, cs.Hooks.Stop, 1)
	require.Len(t, cs.Hooks.Stop[0].Hooks, 1)
	assert.Equal(t, "command", cs.Hooks.Stop[0].Hooks[0].Type)
	assert.Equal(t, 10, cs.Hooks.Stop[0].Hooks[0].Timeout)

	require.Len(t, cs.Hooks.PreToolUse, 1)
	assert.Equal(t, "AskUserQuestion", cs.Hooks.PreToolUse[0].Matcher)
	require.Len(t, cs.Hooks.PreToolUse[0].Hooks, 1)
	assert.Equal(t, "command", cs.Hooks.PreToolUse[0].Hooks[0].Type)
	assert.Equal(t, 5, cs.Hooks.PreToolUse[0].Hooks[0].Timeout)

	data, err := json.Marshal(cs)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"SessionStart"`)
	assert.Contains(t, string(data), `"SessionEnd"`)
	assert.Contains(t, string(data), `"Stop"`)
	assert.Contains(t, string(data), `"PreToolUse"`)
}

func TestGenerateClaudeSettings_NotifyDisabled(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
		},
	}

	cs := GenerateClaudeSettings(s)

	assert.Empty(t, cs.Hooks.SessionStart)
	assert.Empty(t, cs.Hooks.SessionEnd)
	assert.Empty(t, cs.Hooks.Stop)
	require.Len(t, cs.Hooks.PreToolUse, 1)
}

func TestGenerateClaudeSettings_BlockInteractiveDisabled(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: false,
		},
	}

	cs := GenerateClaudeSettings(s)

	require.Len(t, cs.Hooks.SessionStart, 1)
	require.Len(t, cs.Hooks.SessionEnd, 1)
	require.Len(t, cs.Hooks.Stop, 1)
	assert.Empty(t, cs.Hooks.PreToolUse)
}

func TestGenerateClaudeSettings_CustomHooks(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: false,
			Custom: []CustomHook{
				{
					Event:   "PreToolUse",
					Matcher: "Bash",
					Command: "validate-bash.sh",
					Timeout: 15,
				},
			},
		},
	}

	cs := GenerateClaudeSettings(s)

	require.Len(t, cs.Hooks.PreToolUse, 1)
	assert.Equal(t, "Bash", cs.Hooks.PreToolUse[0].Matcher)
	require.Len(t, cs.Hooks.PreToolUse[0].Hooks, 1)
	assert.Equal(t, "command", cs.Hooks.PreToolUse[0].Hooks[0].Type)
	assert.Equal(t, "validate-bash.sh", cs.Hooks.PreToolUse[0].Hooks[0].Command)
	assert.Equal(t, 15, cs.Hooks.PreToolUse[0].Hooks[0].Timeout)
}

func TestGenerateClaudeSettings_CustomHooksMultipleEvents(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
			Custom: []CustomHook{
				{
					Event:   "SessionStart",
					Command: "custom-start.sh",
					Timeout: 8,
				},
				{
					Event:   "PreToolUse",
					Matcher: "Edit",
					Command: "validate-edit.sh",
					Timeout: 12,
				},
			},
		},
	}

	cs := GenerateClaudeSettings(s)

	require.Len(t, cs.Hooks.SessionStart, 2)
	assert.Equal(t, "custom-start.sh", cs.Hooks.SessionStart[1].Hooks[0].Command)
	assert.Equal(t, 8, cs.Hooks.SessionStart[1].Hooks[0].Timeout)

	require.Len(t, cs.Hooks.PreToolUse, 2)
	assert.Equal(t, "AskUserQuestion", cs.Hooks.PreToolUse[0].Matcher)
	assert.Equal(t, "Edit", cs.Hooks.PreToolUse[1].Matcher)
	assert.Equal(t, "validate-edit.sh", cs.Hooks.PreToolUse[1].Hooks[0].Command)
}

func TestGenerateClaudeSettings_HookPaths(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
	}

	cs := GenerateClaudeSettings(s)

	assert.Contains(t, cs.Hooks.SessionStart[0].Hooks[0].Command, ".tmux-cli/hooks/")
	assert.Contains(t, cs.Hooks.SessionEnd[0].Hooks[0].Command, ".tmux-cli/hooks/")
	assert.Contains(t, cs.Hooks.Stop[0].Hooks[0].Command, ".tmux-cli/hooks/")
	assert.Contains(t, cs.Hooks.PreToolUse[0].Hooks[0].Command, ".tmux-cli/hooks/")
}

func TestGenerateClaudeSettings_SessionNotifyArgs(t *testing.T) {
	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify: true,
		},
	}

	cs := GenerateClaudeSettings(s)

	assert.Contains(t, cs.Hooks.SessionStart[0].Hooks[0].Command, " start")
	assert.Contains(t, cs.Hooks.SessionEnd[0].Hooks[0].Command, " end")
	assert.Contains(t, cs.Hooks.Stop[0].Hooks[0].Command, " stop")
}

func TestWriteClaudeSettings_CreatesFile(t *testing.T) {
	root := t.TempDir()

	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
	}

	err := WriteClaudeSettings(root, s)
	require.NoError(t, err)

	settingsPath := filepath.Join(root, ".claude", "settings.json")
	assert.FileExists(t, settingsPath)

	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	var cs ClaudeSettings
	err = json.Unmarshal(data, &cs)
	require.NoError(t, err, "output should be valid JSON")

	assert.Len(t, cs.Hooks.SessionStart, 1)
	assert.Len(t, cs.Hooks.PreToolUse, 1)
}

func TestWriteClaudeSettings_OverwritesSilently(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))

	oldContent := `{"hooks":{"SessionStart":[]}}`
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(oldContent), 0o644))

	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
	}

	err := WriteClaudeSettings(root, s)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	require.NoError(t, err)

	var cs ClaudeSettings
	require.NoError(t, json.Unmarshal(data, &cs))
	assert.Len(t, cs.Hooks.SessionStart, 1, "should contain new data, not old")
}

func TestWriteClaudeSettings_IndentedJSON(t *testing.T) {
	root := t.TempDir()

	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify: true,
		},
	}

	err := WriteClaudeSettings(root, s)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	require.NoError(t, err)

	assert.Contains(t, string(data), "  ", "JSON should be indented")
	assert.Contains(t, string(data), "\n", "JSON should have newlines")
}

func TestWriteClaudeSettings_FilePermissions(t *testing.T) {
	root := t.TempDir()

	s := &Settings{
		Hooks: HooksSettings{
			SessionNotify: true,
		},
	}

	err := WriteClaudeSettings(root, s)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(root, ".claude", "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

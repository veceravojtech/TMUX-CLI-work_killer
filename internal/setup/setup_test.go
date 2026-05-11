package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))
	return dir
}

func testConfig() *SetupConfig {
	return &SetupConfig{
		HookScripts: map[string]string{
			"tmux-session-notify.sh":      "#!/bin/bash\necho notify",
			"no-interactive-questions.sh": "#!/bin/bash\nexit 2",
		},
		CommandTemplates: map[string]string{
			"supervisor.md": "---\ndescription: test\n---\ncontent",
			"worker.md":     "---\ndescription: worker\n---\nworker content",
		},
	}
}

func TestRun_FullSetup(t *testing.T) {
	dir := setupTestProject(t)
	cfg := testConfig()
	cfg.ProjectRoot = dir

	err := Run(cfg)
	require.NoError(t, err)

	// setting.yaml created
	settingsData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"))
	require.NoError(t, err)
	var s Settings
	require.NoError(t, yaml.Unmarshal(settingsData, &s))
	assert.False(t, s.Hooks.SessionNotify)
	assert.True(t, s.Commands.Enabled)

	// hook scripts created
	for name := range cfg.HookScripts {
		path := filepath.Join(dir, ".tmux-cli", "hooks", name)
		assert.FileExists(t, path)
	}

	// .claude/settings.json created
	csPath := filepath.Join(dir, ".claude", "settings.json")
	assert.FileExists(t, csPath)
	csData, err := os.ReadFile(csPath)
	require.NoError(t, err)
	var cs ClaudeSettings
	require.NoError(t, json.Unmarshal(csData, &cs))
	assert.Empty(t, cs.Hooks.SessionStart)

	// commands created (commands enabled by default)
	for name := range cfg.CommandTemplates {
		path := filepath.Join(dir, ".claude", "commands", "tmux", name)
		assert.FileExists(t, path)
	}

	// git exclude updated
	excludeData, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	require.NoError(t, err)
	assert.Contains(t, string(excludeData), "/.tmux-cli/")
	assert.Contains(t, string(excludeData), "/.claude/settings.json")
}

func TestRun_CommandsDisabled(t *testing.T) {
	dir := setupTestProject(t)

	// Pre-create setting.yaml with commands disabled
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	settingsData, err := yaml.Marshal(&Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
		Commands: CommandsSettings{Enabled: false},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".tmux-cli", "setting.yaml"),
		settingsData, 0o644,
	))

	cfg := testConfig()
	cfg.ProjectRoot = dir

	err = Run(cfg)
	require.NoError(t, err)

	// commands dir must NOT exist
	tmuxCmdDir := filepath.Join(dir, ".claude", "commands", "tmux")
	assert.NoDirExists(t, tmuxCmdDir)

	// other artifacts still created
	assert.FileExists(t, filepath.Join(dir, ".claude", "settings.json"))
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "hooks", "tmux-session-notify.sh"))
}

func TestRun_Idempotent(t *testing.T) {
	dir := setupTestProject(t)
	cfg := testConfig()
	cfg.ProjectRoot = dir

	require.NoError(t, Run(cfg))
	require.NoError(t, Run(cfg))

	// Verify artifacts still correct after second run
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "setting.yaml"))
	assert.FileExists(t, filepath.Join(dir, ".claude", "settings.json"))

	for name := range cfg.HookScripts {
		assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "hooks", name))
	}
	for name := range cfg.CommandTemplates {
		assert.FileExists(t, filepath.Join(dir, ".claude", "commands", "tmux", name))
	}

	excludeData, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	require.NoError(t, err)
	content := string(excludeData)
	// managed header should appear exactly once
	assert.Equal(t, 1, countOccurrences(content, managedHeader))
}

func TestRun_EmptyHookScripts(t *testing.T) {
	dir := setupTestProject(t)
	cfg := &SetupConfig{
		ProjectRoot:      dir,
		HookScripts:      map[string]string{},
		CommandTemplates: map[string]string{"test.md": "content"},
	}

	err := Run(cfg)
	require.NoError(t, err)

	// hooks dir created even with no scripts
	assert.DirExists(t, filepath.Join(dir, ".tmux-cli", "hooks"))
	// logs dir created
	assert.DirExists(t, filepath.Join(dir, ".tmux-cli", "logs"))
}

func TestRun_NoGitDir(t *testing.T) {
	dir := t.TempDir() // no .git/info/ created
	cfg := testConfig()
	cfg.ProjectRoot = dir

	err := Run(cfg)
	require.NoError(t, err)

	// all other artifacts still created
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "setting.yaml"))
	assert.FileExists(t, filepath.Join(dir, ".claude", "settings.json"))

	// .git/info/exclude should NOT exist
	assert.NoFileExists(t, filepath.Join(dir, ".git", "info", "exclude"))
}

func countOccurrences(s, sub string) int {
	count := 0
	idx := 0
	for {
		i := indexOf(s[idx:], sub)
		if i < 0 {
			break
		}
		count++
		idx += i + len(sub)
	}
	return count
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

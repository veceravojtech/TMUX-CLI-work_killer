package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Install Project Files Command Tests
// ============================================================================

// TestInstallCmd_Exists verifies the install command is registered
func TestInstallCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install"})
	assert.NoError(t, err, "install command should be registered")
	assert.NotNil(t, cmd, "install command should exist")
	assert.Equal(t, "install", cmd.Use, "command name should be 'install'")
}

// TestInstallCmd_HasForceFlag verifies --force flag exists
func TestInstallCmd_HasForceFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	forceFlag := cmd.Flags().Lookup("force")
	assert.NotNil(t, forceFlag, "--force flag should exist")
	assert.Equal(t, "bool", forceFlag.Value.Type(), "--force should be a boolean flag")
}

// TestRunInstall_CreatesSettingsJSON verifies settings.json is created
func TestRunInstall_CreatesSettingsJSON(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Change to test directory
	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag to avoid interactive prompt
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstall(nil, []string{})
	assert.NoError(t, err, "install should succeed")

	// Verify .claude/settings.json was created
	settingsFile := filepath.Join(tmpDir, ".claude", "settings.json")
	_, err = os.Stat(settingsFile)
	assert.NoError(t, err, "settings.json should be created")

	// Verify settings.json content
	data, err := os.ReadFile(settingsFile)
	require.NoError(t, err)

	var settings ClaudeSettings
	err = json.Unmarshal(data, &settings)
	require.NoError(t, err, "settings.json should be valid JSON")

	// Verify hooks structure
	assert.NotNil(t, settings.Hooks.SessionStart, "SessionStart hooks should exist")
	assert.NotNil(t, settings.Hooks.SessionEnd, "SessionEnd hooks should exist")
	assert.NotNil(t, settings.Hooks.Stop, "Stop hooks should exist")

	// Verify SessionStart hook command
	require.Len(t, settings.Hooks.SessionStart, 1)
	require.Len(t, settings.Hooks.SessionStart[0].Hooks, 1)
	hook := settings.Hooks.SessionStart[0].Hooks[0]
	assert.Equal(t, "command", hook.Type)
	assert.Contains(t, hook.Command, "tmux-session-notify.sh start")
	assert.Equal(t, 10, hook.Timeout)
}

// TestRunInstall_CreatesHooksDirectory verifies scripts/hooks/ is created and populated
func TestRunInstall_CreatesHooksDirectory(t *testing.T) {
	// Create temporary test directory without scripts/hooks
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag to avoid interactive prompt
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command - should succeed and create hooks directory
	err = runInstall(nil, []string{})
	assert.NoError(t, err, "install should succeed and create scripts/hooks/")

	// Verify scripts/hooks/ directory was created
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	info, err := os.Stat(hooksDir)
	assert.NoError(t, err, "scripts/hooks/ should be created")
	assert.True(t, info.IsDir(), "scripts/hooks/ should be a directory")

	// Verify hook scripts were created
	notifyScript := filepath.Join(hooksDir, "tmux-session-notify.sh")
	validateScript := filepath.Join(hooksDir, "tmux-validate-session.sh")

	_, err = os.Stat(notifyScript)
	assert.NoError(t, err, "tmux-session-notify.sh should be created")

	_, err = os.Stat(validateScript)
	assert.NoError(t, err, "tmux-validate-session.sh should be created")
}

// TestRunInstall_SetsScriptPermissions verifies hook scripts get executable permissions
func TestRunInstall_SetsScriptPermissions(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Change to test directory
	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstall(nil, []string{})
	assert.NoError(t, err)

	// Verify scripts were created with executable permissions
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	scripts := []string{"tmux-session-notify.sh", "tmux-validate-session.sh"}
	for _, script := range scripts {
		scriptPath := filepath.Join(hooksDir, script)
		info, err := os.Stat(scriptPath)
		require.NoError(t, err)

		mode := info.Mode()
		assert.True(t, mode&0111 != 0, "script %s should be executable", script)
	}
}

// TestRunInstall_CreatesLogsDirectory verifies .tmux-cli/logs/ is created
func TestRunInstall_CreatesLogsDirectory(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Change to test directory
	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstall(nil, []string{})
	assert.NoError(t, err)

	// Verify .tmux-cli/logs/ directory was created
	logsDir := filepath.Join(tmpDir, ".tmux-cli", "logs")
	info, err := os.Stat(logsDir)
	assert.NoError(t, err, ".tmux-cli/logs/ should be created")
	assert.True(t, info.IsDir(), ".tmux-cli/logs/ should be a directory")
}

// TestRunInstall_OverwriteExisting verifies --force flag behavior
func TestRunInstall_OverwriteExisting(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create existing .claude/settings.json with different content
	claudeDir := filepath.Join(tmpDir, ".claude")
	err := os.MkdirAll(claudeDir, 0755)
	require.NoError(t, err)

	existingSettings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"existing": "data",
		},
	}
	existingData, _ := json.Marshal(existingSettings)
	settingsFile := filepath.Join(claudeDir, "settings.json")
	err = os.WriteFile(settingsFile, existingData, 0644)
	require.NoError(t, err)

	// Change to test directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag to overwrite
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstall(nil, []string{})
	assert.NoError(t, err)

	// Verify settings.json was overwritten
	data, err := os.ReadFile(settingsFile)
	require.NoError(t, err)

	var settings ClaudeSettings
	err = json.Unmarshal(data, &settings)
	require.NoError(t, err)

	// Verify it has our hook structure, not the old one
	assert.NotNil(t, settings.Hooks.SessionStart)
	assert.Len(t, settings.Hooks.SessionStart, 1)
}

// TestRunInstall_CreatesNoInteractiveQuestionsHook verifies hook script is created
func TestRunInstall_CreatesNoInteractiveQuestionsHook(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	require.NoError(t, os.Chdir(tmpDir))
	forceInstall = true
	defer func() { forceInstall = false }()

	err := runInstall(nil, []string{})
	assert.NoError(t, err)

	hookPath := filepath.Join(tmpDir, ".claude", "hooks", "no-interactive-questions.sh")
	info, err := os.Stat(hookPath)
	assert.NoError(t, err, "no-interactive-questions.sh should be created at .claude/hooks/")
	assert.True(t, info.Mode()&0111 != 0, "hook script should be executable")
}

// TestRunInstall_AddsPreToolUseHook verifies PreToolUse entry is written to settings.json
func TestRunInstall_AddsPreToolUseHook(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	require.NoError(t, os.Chdir(tmpDir))
	forceInstall = true
	defer func() { forceInstall = false }()

	err := runInstall(nil, []string{})
	require.NoError(t, err)

	settingsFile := filepath.Join(tmpDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsFile)
	require.NoError(t, err)

	var settings ClaudeSettings
	require.NoError(t, json.Unmarshal(data, &settings))

	require.Len(t, settings.Hooks.PreToolUse, 1)
	assert.Equal(t, "AskUserQuestion", settings.Hooks.PreToolUse[0].Matcher)
	require.Len(t, settings.Hooks.PreToolUse[0].Hooks, 1)
	assert.Contains(t, settings.Hooks.PreToolUse[0].Hooks[0].Command, "no-interactive-questions.sh")
	assert.Equal(t, 5, settings.Hooks.PreToolUse[0].Hooks[0].Timeout)
}

// TestRunInstall_PreToolUseIdempotent verifies no duplicate entries on re-run
func TestRunInstall_PreToolUseIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	require.NoError(t, os.Chdir(tmpDir))
	forceInstall = true
	defer func() { forceInstall = false }()

	require.NoError(t, runInstall(nil, []string{}))
	require.NoError(t, runInstall(nil, []string{}))

	settingsFile := filepath.Join(tmpDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsFile)
	require.NoError(t, err)

	var settings ClaudeSettings
	require.NoError(t, json.Unmarshal(data, &settings))

	assert.Len(t, settings.Hooks.PreToolUse, 1, "running install twice must not duplicate PreToolUse entry")
}

// TestRunInstall_MergePreservesExistingPreToolUse verifies existing entries are preserved
func TestRunInstall_MergePreservesExistingPreToolUse(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	require.NoError(t, os.Chdir(tmpDir))
	forceInstall = true
	defer func() { forceInstall = false }()

	// Pre-create settings.json with an existing PreToolUse entry for a different matcher
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0755))
	existing := ClaudeSettings{
		Hooks: HooksConfig{
			PreToolUse: []PreToolUseHookGroup{
				{
					Matcher: "Bash",
					Hooks:   []Hook{{Type: "command", Command: "existing-bash-hook.sh", Timeout: 10}},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644))

	err := runInstall(nil, []string{})
	require.NoError(t, err)

	result, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	require.NoError(t, err)
	var settings ClaudeSettings
	require.NoError(t, json.Unmarshal(result, &settings))

	// Both entries should be present
	assert.Len(t, settings.Hooks.PreToolUse, 2, "existing Bash matcher should be preserved")
	matchers := make([]string, len(settings.Hooks.PreToolUse))
	for i, g := range settings.Hooks.PreToolUse {
		matchers[i] = g.Matcher
	}
	assert.Contains(t, matchers, "Bash")
	assert.Contains(t, matchers, "AskUserQuestion")
}

// TestClaudeSettings_JSONMarshaling verifies JSON marshaling produces correct format
func TestClaudeSettings_JSONMarshaling(t *testing.T) {
	settings := ClaudeSettings{
		Hooks: HooksConfig{
			SessionStart: []HookGroup{
				{
					Hooks: []Hook{
						{
							Type:    "command",
							Command: "test-command",
							Timeout: 10,
						},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)

	// Verify it can be unmarshaled back
	var unmarshaled ClaudeSettings
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, settings.Hooks.SessionStart[0].Hooks[0].Type, unmarshaled.Hooks.SessionStart[0].Hooks[0].Type)
	assert.Equal(t, settings.Hooks.SessionStart[0].Hooks[0].Command, unmarshaled.Hooks.SessionStart[0].Hooks[0].Command)
	assert.Equal(t, settings.Hooks.SessionStart[0].Hooks[0].Timeout, unmarshaled.Hooks.SessionStart[0].Hooks[0].Timeout)
}

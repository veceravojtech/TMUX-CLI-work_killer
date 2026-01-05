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

// TestInstallProjectFilesCmd_Exists verifies the install-project-files command is registered
func TestInstallProjectFilesCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install-project-files"})
	assert.NoError(t, err, "install-project-files command should be registered")
	assert.NotNil(t, cmd, "install-project-files command should exist")
	assert.Equal(t, "install-project-files", cmd.Use, "command name should be 'install-project-files'")
}

// TestInstallProjectFilesCmd_HasForceFlag verifies --force flag exists
func TestInstallProjectFilesCmd_HasForceFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"install-project-files"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	forceFlag := cmd.Flags().Lookup("force")
	assert.NotNil(t, forceFlag, "--force flag should exist")
	assert.Equal(t, "bool", forceFlag.Value.Type(), "--force should be a boolean flag")
}

// TestRunInstallProjectFiles_CreatesSettingsJSON verifies settings.json is created
func TestRunInstallProjectFiles_CreatesSettingsJSON(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create scripts/hooks directory
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	err := os.MkdirAll(hooksDir, 0755)
	require.NoError(t, err)

	// Create a dummy hook script
	hookScript := filepath.Join(hooksDir, "tmux-session-notify.sh")
	err = os.WriteFile(hookScript, []byte("#!/bin/bash\necho test"), 0644)
	require.NoError(t, err)

	// Change to test directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag to avoid interactive prompt
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstallProjectFiles(nil, []string{})
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

// TestRunInstallProjectFiles_MissingHooksDir verifies error when scripts/hooks/ missing
func TestRunInstallProjectFiles_MissingHooksDir(t *testing.T) {
	// Create temporary test directory without scripts/hooks
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	// Run the install command - should fail
	err = runInstallProjectFiles(nil, []string{})
	assert.Error(t, err, "install should fail when scripts/hooks/ missing")
	assert.Contains(t, err.Error(), "scripts/hooks/ directory not found")
}

// TestRunInstallProjectFiles_SetsScriptPermissions verifies hook scripts get executable permissions
func TestRunInstallProjectFiles_SetsScriptPermissions(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create scripts/hooks directory
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	err := os.MkdirAll(hooksDir, 0755)
	require.NoError(t, err)

	// Create hook scripts with non-executable permissions
	scripts := []string{"tmux-session-notify.sh", "tmux-validate-session.sh"}
	for _, script := range scripts {
		scriptPath := filepath.Join(hooksDir, script)
		err = os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0644)
		require.NoError(t, err)
	}

	// Change to test directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstallProjectFiles(nil, []string{})
	assert.NoError(t, err)

	// Verify scripts have executable permissions
	for _, script := range scripts {
		scriptPath := filepath.Join(hooksDir, script)
		info, err := os.Stat(scriptPath)
		require.NoError(t, err)

		mode := info.Mode()
		assert.True(t, mode&0111 != 0, "script %s should be executable", script)
	}
}

// TestRunInstallProjectFiles_CreatesLogsDirectory verifies .tmux-cli/logs/ is created
func TestRunInstallProjectFiles_CreatesLogsDirectory(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create scripts/hooks directory
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	err := os.MkdirAll(hooksDir, 0755)
	require.NoError(t, err)

	// Create a dummy hook script
	hookScript := filepath.Join(hooksDir, "tmux-session-notify.sh")
	err = os.WriteFile(hookScript, []byte("#!/bin/bash\necho test"), 0644)
	require.NoError(t, err)

	// Change to test directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set force flag
	forceInstall = true
	defer func() { forceInstall = false }()

	// Run the install command
	err = runInstallProjectFiles(nil, []string{})
	assert.NoError(t, err)

	// Verify .tmux-cli/logs/ directory was created
	logsDir := filepath.Join(tmpDir, ".tmux-cli", "logs")
	info, err := os.Stat(logsDir)
	assert.NoError(t, err, ".tmux-cli/logs/ should be created")
	assert.True(t, info.IsDir(), ".tmux-cli/logs/ should be a directory")
}

// TestRunInstallProjectFiles_OverwriteExisting verifies --force flag behavior
func TestRunInstallProjectFiles_OverwriteExisting(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	// Create scripts/hooks directory
	hooksDir := filepath.Join(tmpDir, "scripts", "hooks")
	err := os.MkdirAll(hooksDir, 0755)
	require.NoError(t, err)

	// Create a dummy hook script
	hookScript := filepath.Join(hooksDir, "tmux-session-notify.sh")
	err = os.WriteFile(hookScript, []byte("#!/bin/bash\necho test"), 0644)
	require.NoError(t, err)

	// Create existing .claude/settings.json with different content
	claudeDir := filepath.Join(tmpDir, ".claude")
	err = os.MkdirAll(claudeDir, 0755)
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
	err = runInstallProjectFiles(nil, []string{})
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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartCmd_Exists verifies the start command is registered
func TestStartCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	assert.NoError(t, err, "start command should be registered")
	assert.NotNil(t, cmd, "start command should exist")
	assert.Equal(t, "start", cmd.Use, "command name should be 'start'")
}

// TestStartCmd_NoPathFlag verifies --path flag no longer exists
func TestStartCmd_NoPathFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	pathFlag := cmd.Flags().Lookup("path")
	assert.Nil(t, pathFlag, "--path flag should not exist (uses current directory)")

	idFlag := cmd.Flags().Lookup("id")
	assert.Nil(t, idFlag, "--id flag should not exist (sessions auto-detected)")
}

// TestStartCmd_UsesCurrentDirectory verifies start command uses current working directory
func TestStartCmd_UsesCurrentDirectory(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	assert.NotNil(t, cmd.RunE, "start command should have RunE function")
}

// TestKillCmd_Exists verifies the kill command is registered
func TestKillCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"kill"})
	assert.NoError(t, err, "kill command should be registered")
	assert.NotNil(t, cmd, "kill command should exist")
}

// TestKillCmd_RequiresSessionIDArg verifies kill requires session ID argument
func TestKillCmd_RequiresSessionIDArg(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"kill"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	// Kill command should require exactly 1 argument
	assert.Contains(t, cmd.Use, "kill [session-id]")
}

// ============================================================================
// Window ID Validation Tests
// ============================================================================

// TestValidateWindowID_ValidFormats tests window ID validation with valid formats
func TestValidateWindowID_ValidFormats(t *testing.T) {
	tests := []struct {
		name     string
		windowID string
	}{
		{"single digit", "@0"},
		{"double digit", "@12"},
		{"triple digit", "@123"},
		{"large number", "@9999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWindowID(tt.windowID)
			assert.NoError(t, err, "Expected %s to be valid", tt.windowID)
		})
	}
}

// TestValidateWindowID_InvalidFormats tests window ID validation with invalid formats
func TestValidateWindowID_InvalidFormats(t *testing.T) {
	tests := []struct {
		name     string
		windowID string
		errMsg   string
	}{
		{"missing @", "0", "must start with @"},
		{"@ only", "@", "must have a number"},
		{"non-numeric", "@abc", "must be @ followed by digits"},
		{"mixed", "@1a", "must be @ followed by digits"},
		{"space", "@ 1", "must be @ followed by digits"},
		{"negative", "@-1", "must be @ followed by digits"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWindowID(tt.windowID)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

// TestWindowsKillCmd_Exists verifies the windows-kill command is registered
func TestWindowsKillCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"windows-kill"})
	assert.NoError(t, err, "windows-kill command should be registered")
	assert.NotNil(t, cmd, "windows-kill command should exist")
	assert.Equal(t, "windows-kill", cmd.Use, "command name should be 'windows-kill'")
}

// TestWindowsKillCmd_RequiredFlags verifies required flags exist
func TestWindowsKillCmd_RequiredFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"windows-kill"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	idFlag := cmd.Flags().Lookup("id")
	assert.Nil(t, idFlag, "--id flag should not exist (sessions auto-detected)")

	windowIDFlagCmd := cmd.Flags().Lookup("window-id")
	assert.NotNil(t, windowIDFlagCmd, "--window-id flag should exist")
}

// TestStartAttachCmd_Exists verifies the start-attach command is registered
func TestStartAttachCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	assert.NoError(t, err, "start-attach command should be registered")
	assert.NotNil(t, cmd, "start-attach command should exist")
	assert.Equal(t, "start-attach", cmd.Use, "command name should be 'start-attach'")
}

// TestStartAttachCmd_HasRunE verifies start-attach has a RunE handler
func TestStartAttachCmd_HasRunE(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)
	assert.NotNil(t, cmd.RunE, "start-attach command should have RunE function")
}

// TestEndCmd_Removed verifies end command no longer exists
func TestEndCmd_Removed(t *testing.T) {
	// The end command has been removed
	cmd, _, err := rootCmd.Find([]string{"end"})
	// With the end command removed, Find should not find it
	// or it should not match as an exact command
	if err == nil && cmd != nil {
		assert.NotEqual(t, "end", cmd.Use, "end command should have been removed")
	}
}

// ============================================================================
// status --json Tests
// ============================================================================

// TestStatusCmd_HasJSONFlag verifies the --json flag is registered on status.
func TestStatusCmd_HasJSONFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"status"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	jsonFlag := cmd.Flags().Lookup("json")
	require.NotNil(t, jsonFlag, "--json flag should exist on status command")
	assert.Equal(t, "false", jsonFlag.DefValue, "--json should default to false")
}

// TestStatusReport_JSONShapeAndKeys verifies the marshaled report has exactly the
// documented keys and the contracted types (laneNew is JSON null when nil).
func TestStatusReport_JSONShapeAndKeys(t *testing.T) {
	rep := statusJSONReport{
		Project:         "cli",
		SessionUp:       true,
		TaskvisorActive: false,
		RuntimeState:    "idle",
		Activity:        "",
		LaneNew:         nil,
	}

	data, err := json.Marshal(rep)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	wantKeys := []string{"project", "sessionUp", "taskvisorActive", "runtimeState", "activity", "laneNew"}
	assert.Len(t, m, len(wantKeys), "object must have exactly the documented keys")
	for _, k := range wantKeys {
		_, ok := m[k]
		assert.True(t, ok, "missing key %q", k)
	}
	assert.IsType(t, true, m["sessionUp"])
	assert.IsType(t, true, m["taskvisorActive"])
	assert.IsType(t, "", m["runtimeState"])
	assert.Nil(t, m["laneNew"], "laneNew must marshal to JSON null when nil")
}

// TestStatusReport_RuntimeStateDown verifies an empty dir (no session/markers)
// yields runtimeState=down and does not error.
func TestStatusReport_RuntimeStateDown(t *testing.T) {
	tmpDir := t.TempDir()

	rep := buildStatusReport(tmpDir)

	assert.Equal(t, "down", rep.RuntimeState)
	assert.False(t, rep.SessionUp)
	assert.False(t, rep.TaskvisorActive)
}

// TestStatusReport_PausedMarkerWins verifies the PAUSED marker overrides other states.
func TestStatusReport_PausedMarkerWins(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux-cli", "PAUSED"), []byte(""), 0o644))

	rep := buildStatusReport(tmpDir)

	assert.Equal(t, "paused", rep.RuntimeState)
}

// TestStatusReport_TaskvisorActiveConsuming verifies the taskvisor-active marker
// sets taskvisorActive and runtimeState=consuming.
func TestStatusReport_TaskvisorActiveConsuming(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux-cli", "taskvisor-active"), []byte(""), 0o644))

	rep := buildStatusReport(tmpDir)

	assert.True(t, rep.TaskvisorActive)
	assert.Equal(t, "consuming", rep.RuntimeState)
}

// TestStatusReport_ActivityFromGoals verifies activity is "<id>: <description>"
// from goals.yaml, and empty when no goals.yaml exists.
func TestStatusReport_ActivityFromGoals(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli"), 0o755))

	// No goals.yaml → empty activity.
	repNone := buildStatusReport(tmpDir)
	assert.Equal(t, "", repNone.Activity)

	goalsYAML := "current_goal: goal-003\n" +
		"goals:\n" +
		"  - id: goal-003\n" +
		"    description: add status json\n" +
		"    status: running\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux-cli", "goals.yaml"), []byte(goalsYAML), 0o644))

	rep := buildStatusReport(tmpDir)
	assert.Equal(t, "goal-003: add status json", rep.Activity)
}

// TestStatusCmd_HumanOutputUnchanged verifies the no-flag path still returns the
// existing "no tmux-cli session found" error for a dir with no session, and that
// the human path is untouched (no JSON branch taken).
func TestStatusCmd_HumanOutputUnchanged(t *testing.T) {
	origJSON := statusJSON
	statusJSON = false
	defer func() { statusJSON = origJSON }()

	origWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origWd) }()

	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

	err = runSessionStatus(statusCmd, nil)
	require.Error(t, err, "no-flag path must still error when no session exists")
	assert.Contains(t, err.Error(), "no tmux-cli session found for this directory")
}

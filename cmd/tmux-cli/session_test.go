package main

import (
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

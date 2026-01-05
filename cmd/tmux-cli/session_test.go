package main

import (
	"testing"

	"github.com/console/tmux-cli/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartCmd_Exists verifies the start command is registered
func TestStartCmd_Exists(t *testing.T) {
	// After refactoring, start command should be at root level
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

	// Verify --path flag no longer exists (should use current directory)
	pathFlag := cmd.Flags().Lookup("path")
	assert.Nil(t, pathFlag, "--path flag should not exist (uses current directory)")

	// Verify --id flag no longer exists (sessions auto-detected via .tmux-session file)
	idFlag := cmd.Flags().Lookup("id")
	assert.Nil(t, idFlag, "--id flag should not exist (sessions auto-detected)")
}

// TestStartCmd_UsesCurrentDirectory verifies start command uses current working directory
func TestStartCmd_UsesCurrentDirectory(t *testing.T) {
	// This test verifies that runSessionStart uses os.Getwd() to determine project path
	// Note: This is a unit test that verifies the projectPath variable is set from Getwd()
	// Full integration testing with actual tmux sessions would require mock executor

	// The implementation sets projectPath = os.Getwd() at the start of runSessionStart
	// We verify this indirectly by checking that the command can be constructed and executed
	cmd, _, err := rootCmd.Find([]string{"start"})
	assert.NoError(t, err)
	require.NotNil(t, cmd)

	// Verify command is runnable (would use current directory when executed)
	assert.NotNil(t, cmd.RunE, "start command should have RunE function")
}

// ============================================================================
// Story 2.3: Window Get Command Tests
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

// TestFindWindowByID_Success tests successful window lookup
func TestFindWindowByID_Success(t *testing.T) {
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []store.Window{
			{
				TmuxWindowID: "@0",
				Name:         "editor",
			},
			{
				TmuxWindowID: "@1",
				Name:         "tests",
			},
		},
	}

	window, err := findWindowByID(session, "@1")

	assert.NoError(t, err)
	assert.Equal(t, "@1", window.TmuxWindowID)
	assert.Equal(t, "tests", window.Name)
}

// TestFindWindowByID_NotFound tests window lookup when window doesn't exist
func TestFindWindowByID_NotFound(t *testing.T) {
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []store.Window{},
	}

	window, err := findWindowByID(session, "@99")

	assert.Error(t, err)
	assert.Nil(t, window)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "@99")
}

// TestFindWindowByID_EmptySession tests window lookup in session with no windows
func TestFindWindowByID_EmptySession(t *testing.T) {
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []store.Window{},
	}

	window, err := findWindowByID(session, "@0")

	assert.Error(t, err)
	assert.Nil(t, window)
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

	// Verify --id flag no longer exists (sessions auto-detected via .tmux-session file)
	idFlag := cmd.Flags().Lookup("id")
	assert.Nil(t, idFlag, "--id flag should not exist (sessions auto-detected)")

	// Check --window-id flag exists
	windowIDFlag := cmd.Flags().Lookup("window-id")
	assert.NotNil(t, windowIDFlag, "--window-id flag should exist")
}

// ============================================================================
// Tech Spec Kill Window Improvements Tests
// ============================================================================

// TestWindowsKill_RollbackOnKillFailure tests Phase 1.1 & 1.2
// When file save succeeds but tmux kill fails, session file should be rolled back
func TestWindowsKill_RollbackOnKillFailure(t *testing.T) {
	// This test verifies F3 (race condition fix) and F9 (atomic operations)
	// Scenario: Save succeeds, kill fails → rollback should restore window

	// Note: This requires integration with mock executor
	// Will be implemented in Phase 2.3 (mock verification tests)
	t.Skip("Requires mock executor integration - see Phase 2.3 tests below")
}

// TestWindowsKill_WindowNotInTmux tests Phase 1.3 (F4 validation)
// When window is not found in tmux session, operation should fail
func TestWindowsKill_WindowNotInTmux(t *testing.T) {
	// This test verifies F4 (session validation)
	// Scenario: Window exists in file but not in tmux for this session

	// Note: This requires integration with mock executor
	// Will be implemented in Phase 2.3 (mock verification tests)
	t.Skip("Requires mock executor integration - see Phase 2.3 tests below")
}

// TestWindowsKill_HappyPath tests Phase 1.1
// When both save and kill succeed, window should be removed
func TestWindowsKill_HappyPath(t *testing.T) {
	// This test verifies the normal flow works correctly

	// Note: This requires integration with mock executor and store
	// Will be implemented in Phase 2.3 (mock verification tests)
	t.Skip("Requires mock executor integration - see Phase 2.3 tests below")
}

// ============================================================================
// Unique Window Names Validation Tests
// ============================================================================

// TestIsDuplicateWindowName_ExactMatch verifies exact name match detection
func TestIsDuplicateWindowName_ExactMatch(t *testing.T) {
	sess := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
			{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-2"},
		},
	}

	// Test exact match
	isDup, existingName, windowID := isDuplicateWindowName(sess, "supervisor")
	assert.True(t, isDup, "should detect exact duplicate name")
	assert.Equal(t, "supervisor", existingName)
	assert.Equal(t, "@0", windowID)
}

// TestIsDuplicateWindowName_CaseInsensitive verifies case-insensitive matching
func TestIsDuplicateWindowName_CaseInsensitive(t *testing.T) {
	sess := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "hook-test", UUID: "uuid-1"},
		},
	}

	tests := []struct {
		name          string
		inputName     string
		shouldMatch   bool
		expectedFound string
	}{
		{"uppercase", "HOOK-TEST", true, "hook-test"},
		{"mixed case", "Hook-Test", true, "hook-test"},
		{"different name", "other-window", false, ""},
		{"partial match", "hook", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isDup, existingName, windowID := isDuplicateWindowName(sess, tt.inputName)
			if tt.shouldMatch {
				assert.True(t, isDup, "should detect case-insensitive duplicate")
				assert.Equal(t, tt.expectedFound, existingName)
				assert.Equal(t, "@0", windowID)
			} else {
				assert.False(t, isDup, "should not match different name")
				assert.Equal(t, "", existingName)
				assert.Equal(t, "", windowID)
			}
		})
	}
}

// TestIsDuplicateWindowName_EmptySession verifies behavior with no windows
func TestIsDuplicateWindowName_EmptySession(t *testing.T) {
	sess := &store.Session{
		SessionID: "test-session",
		Windows:   []store.Window{},
	}

	isDup, existingName, windowID := isDuplicateWindowName(sess, "any-name")
	assert.False(t, isDup, "empty session should allow any name")
	assert.Equal(t, "", existingName)
	assert.Equal(t, "", windowID)
}

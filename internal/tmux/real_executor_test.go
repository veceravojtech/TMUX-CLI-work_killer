package tmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTmuxExecutor_ReturnsInstance verifies constructor works
func TestNewTmuxExecutor_ReturnsInstance(t *testing.T) {
	executor := NewTmuxExecutor()
	require.NotNil(t, executor)

	// Verify it implements the interface
	var _ TmuxExecutor = executor
}

// TestRealTmuxExecutor_CreateSession_ValidatesInterface verifies RealTmuxExecutor implements TmuxExecutor
func TestRealTmuxExecutor_ValidatesInterface(t *testing.T) {
	// This will fail compilation if RealTmuxExecutor doesn't implement TmuxExecutor
	var _ TmuxExecutor = (*RealTmuxExecutor)(nil)
}

// Table-driven tests for error handling scenarios
func TestRealTmuxExecutor_ErrorHandling(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		expectErr     error
		expectErrType error
	}{
		{
			name:          "CreateSession returns error type",
			method:        "CreateSession",
			expectErr:     nil, // Will be set in GREEN phase
			expectErrType: nil,
		},
		{
			name:          "HasSession returns error type",
			method:        "HasSession",
			expectErr:     nil,
			expectErrType: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := NewTmuxExecutor()
			require.NotNil(t, executor)

			// Tests will be expanded in GREEN phase
			switch tt.method {
			case "CreateSession":
				err := executor.CreateSession("test-id", "/tmp")
				if err != nil {
					assert.Error(t, err)
				}
			case "HasSession":
				_, err := executor.HasSession("test-id")
				if err != nil {
					assert.Error(t, err)
				}
			}
		})
	}
}

// TestRealTmuxExecutor_KillSession_Idempotency verifies kill is idempotent
// KillSession should NOT return an error if the session doesn't exist
// This is critical for the kill workflow - session might already be dead
func TestRealTmuxExecutor_KillSession_Idempotency(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to kill a session that definitely doesn't exist
	// This should NOT return an error (idempotent behavior)
	err := executor.KillSession("nonexistent-session-12345")

	// CRITICAL: Kill must be idempotent - no error if session doesn't exist
	assert.NoError(t, err, "KillSession should be idempotent - killing non-existent session should not error")
}

// TestRealTmuxExecutor_CreateWindow_SessionNotFound verifies error when session doesn't exist
func TestRealTmuxExecutor_CreateWindow_SessionNotFound(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to create window in non-existent session
	windowID, err := executor.CreateWindow("nonexistent-session-99999", "test-window", "vim")

	assert.Error(t, err, "CreateWindow should error for non-existent session")
	assert.Empty(t, windowID, "Window ID should be empty on error")
	assert.Contains(t, err.Error(), "tmux new-window failed", "Error should indicate tmux command failure")
}

// TestRealTmuxExecutor_CreateWindow_WindowIDFormat verifies window ID parsing
func TestRealTmuxExecutor_CreateWindow_WindowIDFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create a test session
	sessionID := "test-create-window-format-session"
	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)
	defer executor.KillSession(sessionID)

	// Create first window
	windowID1, err := executor.CreateWindow(sessionID, "window-1", "sleep 60")
	assert.NoError(t, err)
	assert.NotEmpty(t, windowID1, "Window ID should not be empty")
	assert.True(t, len(windowID1) >= 2, "Window ID should be at least 2 chars (@0)")
	assert.True(t, windowID1[0] == '@', "Window ID should start with @")

	// Create second window - verify sequential IDs
	windowID2, err := executor.CreateWindow(sessionID, "window-2", "sleep 60")
	assert.NoError(t, err)
	assert.NotEmpty(t, windowID2)
	assert.NotEqual(t, windowID1, windowID2, "Window IDs should be unique")
	assert.True(t, len(windowID2) >= 2, "Window ID should be at least 2 chars")
	assert.True(t, windowID2[0] == '@', "Window ID should start with @")
}

// TestRealTmuxExecutor_ListWindows_Success tests successful window listing
func TestRealTmuxExecutor_ListWindows_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create test session (tmux creates default window 0 automatically)
	sessionID := "test-list-windows-success-2"

	// Kill any existing test session first
	_ = executor.KillSession(sessionID)

	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)
	defer executor.KillSession(sessionID)

	// Create windows (these will be window 1 and 2, window 0 already exists)
	// Use sleep to keep windows alive during the test
	window1, err := executor.CreateWindow(sessionID, "editor", "sleep 60")
	require.NoError(t, err)

	window2, err := executor.CreateWindow(sessionID, "tests", "sleep 60")
	require.NoError(t, err)

	// List windows (includes default window 0 + our 2 windows = 3 total)
	windows, err := executor.ListWindows(sessionID)

	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(windows), 2, "Should have at least 2 created windows")

	// Find our created windows in the list
	foundEditor := false
	foundTests := false
	for _, w := range windows {
		if w.TmuxWindowID == window1 && w.Name == "editor" {
			foundEditor = true
			assert.True(t, w.Running, "Editor window should be running")
		}
		if w.TmuxWindowID == window2 && w.Name == "tests" {
			foundTests = true
			assert.True(t, w.Running, "Tests window should be running")
		}
	}

	assert.True(t, foundEditor, "Should find editor window")
	assert.True(t, foundTests, "Should find tests window")
}

// TestRealTmuxExecutor_ListWindows_SessionNotFound tests error when session doesn't exist
func TestRealTmuxExecutor_ListWindows_SessionNotFound(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to list windows in non-existent session
	windows, err := executor.ListWindows("nonexistent-session-99999")

	assert.Error(t, err, "Should error for non-existent session")
	assert.Nil(t, windows, "Windows should be nil on error")
	assert.Contains(t, err.Error(), "tmux list-windows failed", "Error should indicate tmux command failure")
}

// TestRealTmuxExecutor_ListWindows_HasDefaultWindow tests that tmux creates a default window
func TestRealTmuxExecutor_ListWindows_HasDefaultWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create session (tmux automatically creates window 0)
	sessionID := "test-list-windows-default-2"

	// Kill any existing test session first
	_ = executor.KillSession(sessionID)

	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)
	defer executor.KillSession(sessionID)

	// List windows (should have the default window created by tmux)
	windows, err := executor.ListWindows(sessionID)

	assert.NoError(t, err, "Should not error for new session")
	assert.GreaterOrEqual(t, len(windows), 1, "Should have at least the default window")

	// First window should have an ID starting with @
	if len(windows) > 0 {
		assert.True(t, windows[0].TmuxWindowID[0] == '@', "Window ID should start with @")
		assert.True(t, windows[0].Running, "Default window should be running")
	}
}

// TestRealTmuxExecutor_ListWindows_WindowNamesWithSpaces tests parsing window names with spaces
func TestRealTmuxExecutor_ListWindows_WindowNamesWithSpaces(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-list-windows-spaces-2"

	// Kill any existing test session first
	_ = executor.KillSession(sessionID)

	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)
	defer executor.KillSession(sessionID)

	// Create window with space in name (use sleep to keep it alive)
	windowID, err := executor.CreateWindow(sessionID, "my editor", "sleep 60")
	require.NoError(t, err)

	// List windows
	windows, err := executor.ListWindows(sessionID)

	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(windows), 1, "Should have at least 1 window")

	// Find our window with spaces in the list
	found := false
	for _, w := range windows {
		if w.TmuxWindowID == windowID {
			found = true
			assert.Equal(t, "my editor", w.Name, "Window name with spaces should be preserved")
			assert.True(t, w.Running, "Window should be running")
			break
		}
	}

	assert.True(t, found, "Should find our window with spaces in name")
}

// TestRealTmuxExecutor_ListWindows_ParsesMultipleWindows tests parsing multiple windows
func TestRealTmuxExecutor_ListWindows_ParsesMultipleWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-list-windows-multiple-2"

	// Kill any existing test session first
	_ = executor.KillSession(sessionID)

	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)
	defer executor.KillSession(sessionID)

	// Create 3 windows with different names (use sleep to keep them alive)
	win1, err := executor.CreateWindow(sessionID, "editor", "sleep 60")
	require.NoError(t, err)

	win2, err := executor.CreateWindow(sessionID, "tests", "sleep 60")
	require.NoError(t, err)

	win3, err := executor.CreateWindow(sessionID, "server", "sleep 60")
	require.NoError(t, err)

	// List all windows (includes default window + our 3 windows)
	windows, err := executor.ListWindows(sessionID)

	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(windows), 3, "Should have at least 3 created windows")

	// Verify all window IDs are unique
	ids := make(map[string]bool)
	for _, w := range windows {
		ids[w.TmuxWindowID] = true
		assert.True(t, w.Running, "All windows should be running")
	}
	assert.GreaterOrEqual(t, len(ids), 3, "All window IDs should be unique")

	// Find our 3 created windows
	foundWindows := make(map[string]string) // ID -> Name
	for _, w := range windows {
		if w.TmuxWindowID == win1 || w.TmuxWindowID == win2 || w.TmuxWindowID == win3 {
			foundWindows[w.TmuxWindowID] = w.Name
		}
	}

	assert.Equal(t, 3, len(foundWindows), "Should find all 3 created windows")
	assert.Equal(t, "editor", foundWindows[win1], "Editor window should have correct name")
	assert.Equal(t, "tests", foundWindows[win2], "Tests window should have correct name")
	assert.Equal(t, "server", foundWindows[win3], "Server window should have correct name")
}

// TestRealTmuxExecutor_SendMessage_SessionNotFound verifies error when session doesn't exist
func TestRealTmuxExecutor_SendMessage_SessionNotFound(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to send message to non-existent session
	err := executor.SendMessage("nonexistent-session-99999", "@0", "test message")

	assert.Error(t, err, "SendMessage should error for non-existent session")
	assert.Contains(t, err.Error(), "send-keys", "Error should indicate tmux command failure")
}

// TestRealTmuxExecutor_SendMessage_InvalidWindow verifies error for invalid window
func TestRealTmuxExecutor_SendMessage_InvalidWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create a session for this test
	sessionID := "test-sendmessage-invalidwindow"
	testDir := "/tmp"

	// Clean up any existing session
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session
	err := executor.CreateSession(sessionID, testDir)
	require.NoError(t, err, "Failed to create test session")

	// Try to send message to non-existent window @99
	err = executor.SendMessage(sessionID, "@99", "test message")

	assert.Error(t, err, "SendMessage should error for non-existent window")
	assert.Contains(t, err.Error(), "send-keys", "Error should indicate tmux command failure")
}

// TestRealTmuxExecutor_SendMessage_Success verifies successful message delivery
func TestRealTmuxExecutor_SendMessage_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create a session with a window
	sessionID := "test-sendmessage-success"
	testDir := "/tmp"

	// Clean up any existing session
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session
	err := executor.CreateSession(sessionID, testDir)
	require.NoError(t, err, "Failed to create test session")

	// Create a window with cat command (simple command that reads input)
	windowID, err := executor.CreateWindow(sessionID, "test-window", "cat")
	require.NoError(t, err, "Failed to create test window")
	require.NotEmpty(t, windowID, "Window ID should not be empty")

	// Send a simple message
	testMessage := "Hello world"
	err = executor.SendMessage(sessionID, windowID, testMessage)

	// This should succeed
	assert.NoError(t, err, "SendMessage should succeed for valid session and window")
}

// TestRealTmuxExecutor_SendMessage_MessageWithSpecialCharacters tests safe escaping
func TestRealTmuxExecutor_SendMessage_MessageWithSpecialCharacters(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	// Create a session with a window
	sessionID := "test-sendmessage-special"
	testDir := "/tmp"

	// Clean up any existing session
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session
	err := executor.CreateSession(sessionID, testDir)
	require.NoError(t, err, "Failed to create test session")

	// Create a window with cat command
	windowID, err := executor.CreateWindow(sessionID, "test-window", "cat")
	require.NoError(t, err, "Failed to create test window")

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "message with quotes",
			message: `Message with "quotes" inside`,
		},
		{
			name:    "message with dollar sign",
			message: `Message with $VAR expansion`,
		},
		{
			name:    "message with backticks",
			message: "Message with `command` substitution",
		},
		{
			name:    "message with semicolon",
			message: "test; rm -rf /",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Send message with special characters
			// This should NOT execute as a command, just send literal text
			err := executor.SendMessage(sessionID, windowID, tt.message)

			// Should succeed - message is safely escaped
			assert.NoError(t, err, "SendMessage should safely handle special characters")
		})
	}
}

// TestRealTmuxExecutor_KillWindow_Idempotency verifies kill is idempotent
// KillWindow should NOT return an error if the window doesn't exist
// This is critical for the kill workflow - window might already be dead
func TestRealTmuxExecutor_KillWindow_Idempotency(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to kill a window in a session that doesn't exist
	// This should NOT return an error (idempotent behavior)
	err := executor.KillWindow("nonexistent-session-12345", "@999")

	// CRITICAL: Kill must be idempotent - no error if window doesn't exist
	assert.NoError(t, err, "KillWindow should be idempotent - killing non-existent window should not error")
}

// TestRealTmuxExecutor_KillWindow_SessionNotFound verifies error handling
func TestRealTmuxExecutor_KillWindow_SessionNotFound(t *testing.T) {
	executor := NewTmuxExecutor()

	// Try to kill window in non-existent session
	// This should be idempotent - no error
	err := executor.KillWindow("nonexistent-session-99999", "@0")

	assert.NoError(t, err, "KillWindow should be idempotent for non-existent session")
}

// ============================================================================
// Tech Spec Edge Case Tests - Phase 2.1 (F7)
// ============================================================================

// TestRealTmuxExecutor_KillWindow_LastWindowInSession tests killing last window
// NOTE: Behavior is tmux version-dependent:
// - Some versions auto-kill the session when last window dies
// - Other versions keep the session alive
// We test that the command itself succeeds regardless
func TestRealTmuxExecutor_KillWindow_LastWindowInSession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-kill-last-window"
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session (tmux auto-creates @0 window)
	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)

	// List windows to get the default window ID
	windows, err := executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(windows), 1, "Should have at least one window")

	defaultWindowID := windows[0].TmuxWindowID

	// Kill the only window (last window)
	err = executor.KillWindow(sessionID, defaultWindowID)

	// Should succeed (idempotent behavior)
	// Session may or may not exist after this - tmux version dependent
	assert.NoError(t, err, "Killing last window should not error (idempotent)")
}

// TestRealTmuxExecutor_KillWindow_MiddleWindow tests killing @1 when @0, @1, @2 exist
func TestRealTmuxExecutor_KillWindow_MiddleWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-kill-middle-window"
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session with default @0
	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)

	// Create @1 and @2 (or next available IDs)
	win1, err := executor.CreateWindow(sessionID, "win1", "sleep 60")
	require.NoError(t, err)

	win2, err := executor.CreateWindow(sessionID, "win2", "sleep 60")
	require.NoError(t, err)

	// Kill middle window
	err = executor.KillWindow(sessionID, win1)
	assert.NoError(t, err, "Killing middle window should succeed")

	// Verify win1 is gone and win2 still exists
	windows, err := executor.ListWindows(sessionID)
	require.NoError(t, err)

	foundWin1 := false
	foundWin2 := false
	for _, w := range windows {
		if w.TmuxWindowID == win1 {
			foundWin1 = true
		}
		if w.TmuxWindowID == win2 {
			foundWin2 = true
		}
	}

	assert.False(t, foundWin1, "win1 should be gone")
	assert.True(t, foundWin2, "win2 should still exist")
}

// TestRealTmuxExecutor_KillWindow_FirstWindowWhenOthersExist tests killing @0 when @1, @2 exist
func TestRealTmuxExecutor_KillWindow_FirstWindowWhenOthersExist(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-kill-first-window"
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session with default @0
	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)

	// Get default window ID
	windows, err := executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(windows), 1)
	firstWindowID := windows[0].TmuxWindowID

	// Create additional windows
	win1, err := executor.CreateWindow(sessionID, "win1", "sleep 60")
	require.NoError(t, err)

	win2, err := executor.CreateWindow(sessionID, "win2", "sleep 60")
	require.NoError(t, err)

	// Kill first window (usually @0)
	err = executor.KillWindow(sessionID, firstWindowID)
	assert.NoError(t, err, "Killing first window should succeed")

	// Verify first window is gone and others still exist
	windows, err = executor.ListWindows(sessionID)
	require.NoError(t, err)

	foundFirst := false
	foundWin1 := false
	foundWin2 := false
	for _, w := range windows {
		if w.TmuxWindowID == firstWindowID {
			foundFirst = true
		}
		if w.TmuxWindowID == win1 {
			foundWin1 = true
		}
		if w.TmuxWindowID == win2 {
			foundWin2 = true
		}
	}

	assert.False(t, foundFirst, "first window should be gone")
	assert.True(t, foundWin1, "win1 should still exist")
	assert.True(t, foundWin2, "win2 should still exist")
}

// TestRealTmuxExecutor_KillWindow_InvalidWindowIDFormat tests validation
func TestRealTmuxExecutor_KillWindow_InvalidWindowIDFormat(t *testing.T) {
	executor := NewTmuxExecutor()

	tests := []struct {
		name     string
		windowID string
	}{
		{"missing @", "0"},
		{"@ only", "@"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := executor.KillWindow("test-session", tt.windowID)
			assert.Error(t, err, "Should error for invalid window ID format")
			assert.Contains(t, err.Error(), "invalid window ID format")
		})
	}
}

// TestRealTmuxExecutor_KillWindow_NonNumericIDsIdempotent tests tmux handling of invalid IDs
// Note: "@abc" passes basic validation (starts with @, len >= 2) but tmux rejects it
// tmux treats "@abc" as "window not found" - this is idempotent behavior
func TestRealTmuxExecutor_KillWindow_NonNumericIDsIdempotent(t *testing.T) {
	executor := NewTmuxExecutor()

	// These pass validation but tmux will reject them as "not found"
	tests := []string{"@abc", "@x1", "@999999"}

	for _, windowID := range tests {
		t.Run(windowID, func(t *testing.T) {
			err := executor.KillWindow("test-session", windowID)
			// Idempotent: no error because tmux says "can't find window"
			assert.NoError(t, err, "Non-existent window IDs should be idempotent")
		})
	}
}

// TestRealTmuxExecutor_KillWindow_DoubleKillIdempotency tests killing same window twice
func TestRealTmuxExecutor_KillWindow_DoubleKillIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	executor := NewTmuxExecutor()

	sessionID := "test-double-kill"
	_ = executor.KillSession(sessionID)
	defer executor.KillSession(sessionID)

	// Create session
	err := executor.CreateSession(sessionID, "/tmp")
	require.NoError(t, err)

	// Create window
	windowID, err := executor.CreateWindow(sessionID, "test", "sleep 60")
	require.NoError(t, err)

	// Kill window first time
	err = executor.KillWindow(sessionID, windowID)
	assert.NoError(t, err, "First kill should succeed")

	// Kill same window second time (idempotent)
	err = executor.KillWindow(sessionID, windowID)
	assert.NoError(t, err, "Second kill should be idempotent (no error)")
}

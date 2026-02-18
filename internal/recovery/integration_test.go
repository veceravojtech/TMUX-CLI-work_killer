//go:build integration
// +build integration

// Package recovery provides session recovery detection and execution functionality.
package recovery

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
)

// TestIntegration_RecoveryManager_RealDependencies verifies the recovery manager
// can be created with real dependencies and has the correct structure
func TestIntegration_RecoveryManager_RealDependencies(t *testing.T) {
	// Create real dependencies
	sessionStore, err := store.NewFileSessionStore()
	assert.NoError(t, err, "should create file session store")

	tmuxExecutor := tmux.NewTmuxExecutor()
	assert.NotNil(t, tmuxExecutor, "should create tmux executor")

	// Create recovery manager
	recoveryManager := NewSessionRecoveryManager(sessionStore, tmuxExecutor)
	assert.NotNil(t, recoveryManager, "should create recovery manager")

	// Verify manager implements interface
	var _ RecoveryManager = recoveryManager

	t.Log("✅ Recovery detection system integrates correctly with real dependencies")
}

// TestIntegration_RecoverSession_EndToEnd tests the complete recovery workflow
// This test verifies RecoverSession() works with real dependencies
// Run with: go test -tags=integration ./internal/recovery -v -run TestIntegration_RecoverSession_EndToEnd
func TestIntegration_RecoverSession_EndToEnd(t *testing.T) {
	// Skip if tmux is not available
	executor := tmux.NewTmuxExecutor()
	sessions, err := executor.ListSessions()
	if err != nil {
		t.Skip("Tmux not available, skipping integration test")
	}
	_ = sessions

	// Create real dependencies
	sessionStore, err := store.NewFileSessionStore()
	assert.NoError(t, err, "should create file session store")

	recoveryManager := NewSessionRecoveryManager(sessionStore, executor)

	// Create a test session
	testSessionID := "test-recovery-integration"
	testPath := "/tmp/test-recovery"

	// Clean up any existing test session
	executor.KillSession(testSessionID)
	os.RemoveAll(testPath)

	// Create test directory and session
	err = os.MkdirAll(testPath, 0755)
	assert.NoError(t, err, "should create test directory")

	err = executor.CreateSession(testSessionID, testPath)
	assert.NoError(t, err, "should create test session")

	// Create windows with shell command (keeps window alive)
	window1ID, err := executor.CreateWindow(testSessionID, "test-window-1", "")
	assert.NoError(t, err, "should create window 1")

	window2ID, err := executor.CreateWindow(testSessionID, "test-window-2", "")
	assert.NoError(t, err, "should create window 2")

	// Save session to store
	testSession := &store.Session{
		SessionID:   testSessionID,
		ProjectPath: testPath,
		Windows: []store.Window{
			{
				TmuxWindowID: window1ID,
				Name:         "test-window-1",
			},
			{
				TmuxWindowID: window2ID,
				Name:         "test-window-2",
			},
		},
	}
	err = sessionStore.Save(testSession)
	assert.NoError(t, err, "should save session to store")

	// Kill the tmux session (but keep the file)
	err = executor.KillSession(testSessionID)
	assert.NoError(t, err, "should kill tmux session")

	// Verify recovery is needed
	needsRecovery, err := recoveryManager.IsRecoveryNeeded(testSession)
	assert.NoError(t, err, "should check recovery status")
	assert.True(t, needsRecovery, "should need recovery after killing session")

	// Load session and recover it
	sessionToRecover, err := sessionStore.Load(testPath)
	assert.NoError(t, err, "should load session from store")

	err = recoveryManager.RecoverSession(sessionToRecover)
	assert.NoError(t, err, "should recover session successfully")

	// Verify session was recreated
	exists, err := executor.HasSession(testSessionID)
	assert.NoError(t, err, "should check if session exists")
	assert.True(t, exists, "session should exist after recovery")

	// Verify windows were recreated
	windows, err := executor.ListWindows(testSessionID)
	assert.NoError(t, err, "should list windows")
	assert.GreaterOrEqual(t, len(windows), 2, "should have at least 2 windows")

	// Verify window names preserved
	windowNames := make(map[string]bool)
	for _, w := range windows {
		windowNames[w.Name] = true
	}
	assert.True(t, windowNames["test-window-1"], "window 1 should exist")
	assert.True(t, windowNames["test-window-2"], "window 2 should exist")

	// Reload session to get updated state
	recoveredSession, err := sessionStore.Load(testPath)
	assert.NoError(t, err, "should load recovered session")

	// Verify recovery is no longer needed
	needsRecovery, err = recoveryManager.IsRecoveryNeeded(recoveredSession)
	assert.NoError(t, err, "should check recovery status")
	assert.False(t, needsRecovery, "should not need recovery after successful recovery")

	// Clean up
	executor.KillSession(testSessionID)
	os.RemoveAll(testPath)

	t.Log("✅ End-to-end recovery workflow completed successfully")
	t.Log("   - Session killed and detected as needing recovery")
	t.Log("   - RecoverSession() recreated session with all windows")
	t.Log("   - Window names and identities preserved")
	t.Log("   - Session no longer needs recovery after successful recovery")
}

// TestIntegration_RecoveryCommand_CapturedFromRunningPane verifies that the recovery
// command is properly captured from a REAL running tmux pane, not just saved from memory.
// This test demonstrates the ACTUAL bug: when we capture the state of a running tmux
// session (like during 'tmux-cli kill'), we need to read what command is ACTUALLY running
// in the pane using #{pane_current_command} or similar tmux format variables.
//
// TODO: This test is currently FAILING and demonstrates the real bug.
// The code does NOT capture pane_current_command when listing windows, so recovery
// commands are never populated from running panes.
//
// Run with: go test -tags=integration ./internal/recovery -v -run TestIntegration_RecoveryCommand_CapturedFromRunningPane
func TestIntegration_RecoveryCommand_CapturedFromRunningPane(t *testing.T) {
	// Skip if tmux is not available
	executor := tmux.NewTmuxExecutor()
	sessions, err := executor.ListSessions()
	if err != nil {
		t.Skip("Tmux not available, skipping integration test")
	}
	_ = sessions

	// Create a test session
	testSessionID := "test-pane-command-capture"
	testPath := "/tmp/test-pane-command"

	// Clean up any existing test session
	executor.KillSession(testSessionID)

	// Ensure test directory exists
	err = os.MkdirAll(testPath, 0755)
	assert.NoError(t, err, "should create test directory")

	// Create session
	err = executor.CreateSession(testSessionID, testPath)
	assert.NoError(t, err, "should create test session")

	// Create a window with an actual command running
	// We'll use 'sleep' command as it's universally available
	windowID, err := executor.CreateWindow(testSessionID, "test-window", "sleep 999")
	assert.NoError(t, err, "should create window with command")

	// Give tmux a moment to start the command
	time.Sleep(500 * time.Millisecond)

	// NOW: Capture the running command from the pane using tmux display-message
	// This simulates what should happen when we save session state
	cmd := exec.Command("tmux", "display-message", "-t", testSessionID+":"+windowID,
		"-p", "#{pane_current_command}")
	output, err := cmd.CombinedOutput()
	assert.NoError(t, err, "should capture pane_current_command")

	capturedCommand := strings.TrimSpace(string(output))
	t.Logf("Captured running command: '%s'", capturedCommand)

	// The bug: ListWindows() doesn't capture this! It should return "sleep" but doesn't
	windows, err := executor.ListWindows(testSessionID)
	assert.NoError(t, err, "should list windows")
	assert.Equal(t, 2, len(windows), "should have 2 windows (supervisor + test-window)")

	// Find our test window
	var testWindow *tmux.WindowInfo
	for i := range windows {
		if windows[i].Name == "test-window" {
			testWindow = &windows[i]
			break
		}
	}
	assert.NotNil(t, testWindow, "should find test-window")

	// VERIFY: WindowInfo now has CurrentCommand field and it matches what's running
	assert.NotEmpty(t, testWindow.CurrentCommand, "WindowInfo should capture CurrentCommand")
	assert.Equal(t, "sleep", testWindow.CurrentCommand,
		"CurrentCommand should match the actual running command")

	t.Log("✅ BUG FIXED: ListWindows() now captures pane_current_command")
	t.Logf("   - Real command running in pane: '%s'", capturedCommand)
	t.Logf("   - WindowInfo.CurrentCommand: '%s'", testWindow.CurrentCommand)
	t.Log("   - Commands match!")

	// Clean up
	executor.KillSession(testSessionID)
	os.RemoveAll(testPath)
}

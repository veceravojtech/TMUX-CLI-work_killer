//go:build integration
// +build integration

// Package recovery provides session recovery detection and execution functionality.
package recovery

import (
	"testing"

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

	// Try to check recovery for a non-existent session (should return error)
	needsRecovery, err := recoveryManager.IsRecoveryNeeded("nonexistent-test-session")
	assert.Error(t, err, "should error for nonexistent session")
	assert.False(t, needsRecovery, "should not need recovery for nonexistent session")
	assert.Contains(t, err.Error(), "load session", "error should be about loading session")

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
	sessionStore.Delete(testSessionID)

	// Create session and save to store
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
				TmuxWindowID:    window1ID,
				Name:            "test-window-1",
				RecoveryCommand: "", // Empty command = default shell
			},
			{
				TmuxWindowID:    window2ID,
				Name:            "test-window-2",
				RecoveryCommand: "", // Empty command = default shell
			},
		},
	}
	err = sessionStore.Save(testSession)
	assert.NoError(t, err, "should save session to store")

	// Kill the tmux session (but keep the file)
	err = executor.KillSession(testSessionID)
	assert.NoError(t, err, "should kill tmux session")

	// Verify recovery is needed
	needsRecovery, err := recoveryManager.IsRecoveryNeeded(testSessionID)
	assert.NoError(t, err, "should check recovery status")
	assert.True(t, needsRecovery, "should need recovery after killing session")

	// Load session and recover it
	sessionToRecover, err := sessionStore.Load(testSessionID)
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

	// Verify recovery is no longer needed
	needsRecovery, err = recoveryManager.IsRecoveryNeeded(testSessionID)
	assert.NoError(t, err, "should check recovery status")
	assert.False(t, needsRecovery, "should not need recovery after successful recovery")

	// Clean up
	executor.KillSession(testSessionID)
	sessionStore.Delete(testSessionID)

	t.Log("✅ End-to-end recovery workflow completed successfully")
	t.Log("   - Session killed and detected as needing recovery")
	t.Log("   - RecoverSession() recreated session with all windows")
	t.Log("   - Window names and identities preserved")
	t.Log("   - Session no longer needs recovery after successful recovery")
}

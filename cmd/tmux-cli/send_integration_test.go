//go:build integration
// +build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/recovery"
	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterWindowCommunication_EndToEnd(t *testing.T) {
	// Setup real dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	require.NoError(t, err, "Failed to initialize session store")
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)

	sessionID := uuid.New().String()
	testDir := filepath.Join(os.TempDir(), "tmux-cli-test-"+sessionID)

	// Cleanup function
	defer func() {
		_ = executor.KillSession(sessionID)
		_ = os.RemoveAll(testDir)
		homeDir, _ := os.UserHomeDir()
		sessionFile := filepath.Join(homeDir, ".tmux-cli", "sessions", sessionID+".json")
		_ = os.Remove(sessionFile)
	}()

	// Create test directory
	err = os.MkdirAll(testDir, 0755)
	require.NoError(t, err, "Failed to create test directory")

	// 1. Create session with 2 windows
	err = executor.CreateSession(sessionID, testDir)
	require.NoError(t, err, "Failed to create session")

	session := &store.Session{
		SessionID:   sessionID,
		ProjectPath: testDir,
		Windows:     []store.Window{},
	}

	// Create supervisor window
	windowID0, err := executor.CreateWindow(sessionID, "supervisor", "cat")
	require.NoError(t, err, "Failed to create supervisor window")
	session.Windows = append(session.Windows, store.Window{
		TmuxWindowID: windowID0,
		Name:         "supervisor",
	})

	// Create worker window
	windowID1, err := executor.CreateWindow(sessionID, "worker", "cat")
	require.NoError(t, err, "Failed to create worker window")
	session.Windows = append(session.Windows, store.Window{
		TmuxWindowID: windowID1,
		Name:         "worker",
	})

	// Save session
	err = fileStore.Save(session)
	require.NoError(t, err, "Failed to save session")

	// 2. Send message from worker (@1) to supervisor (@0)
	testMessage := "Worker task complete: test successful"
	err = executor.SendMessage(sessionID, windowID0, testMessage)
	require.NoError(t, err, "Failed to send message")

	// Wait briefly for message to be delivered
	time.Sleep(100 * time.Millisecond)

	// 3. Verify message received in supervisor window
	captureCmd := exec.Command("tmux", "capture-pane", "-t", sessionID+":"+windowID0, "-p")
	paneContent, err := captureCmd.CombinedOutput()
	require.NoError(t, err, "Failed to capture pane content")

	assert.Contains(t, string(paneContent), testMessage,
		"Message not found in supervisor window pane")

	// 4. Test recovery scenario: kill session, send message
	err = executor.KillSession(sessionID)
	require.NoError(t, err, "Failed to kill session")

	// Verify session is killed
	exists, _ := executor.HasSession(sessionID)
	assert.False(t, exists, "Session should be killed")

	// Trigger recovery via send command (uses MaybeRecoverSession internally)
	recoveryNeeded, err := recoveryManager.IsRecoveryNeeded(session)
	require.NoError(t, err)
	assert.True(t, recoveryNeeded, "Recovery should be needed")

	// Recover session
	err = recoveryManager.RecoverSession(session)
	require.NoError(t, err, "Recovery failed")

	err = recoveryManager.VerifyRecovery(session)
	require.NoError(t, err, "Recovery verification failed")

	// Send message after recovery
	afterRecoveryMessage := "Message after recovery"
	// Note: Window IDs may have changed after recovery, reload session
	recoveredSession, err := fileStore.Load(sessionID)
	require.NoError(t, err)

	err = executor.SendMessage(sessionID, recoveredSession.Windows[0].TmuxWindowID, afterRecoveryMessage)
	require.NoError(t, err, "Failed to send message after recovery")

	// Verify message delivered
	time.Sleep(100 * time.Millisecond)
	captureCmd = exec.Command("tmux", "capture-pane", "-t",
		sessionID+":"+recoveredSession.Windows[0].TmuxWindowID, "-p")
	paneContent, err = captureCmd.CombinedOutput()
	require.NoError(t, err)

	assert.Contains(t, string(paneContent), afterRecoveryMessage,
		"Message not found after recovery")

	t.Log("✅ Inter-window communication test passed: messages delivered before and after recovery")
}

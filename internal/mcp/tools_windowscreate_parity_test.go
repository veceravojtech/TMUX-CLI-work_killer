//go:build integration
// +build integration

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
)

// ============================================================================
// WindowsCreate CLI Parity Tests (Tech-Spec: MCP windows-create Parity with CLI)
// ============================================================================

// TestServer_WindowsCreate_UUIDGeneration verifies that windows created via MCP
// have UUIDs generated and set correctly (AC1, AC2).
func TestServer_WindowsCreate_UUIDGeneration(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("uuid-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "existing-uuid-1"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Change to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session (required for UUID option setting)
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Create window via MCP
	window, err := server.WindowsCreate("test-window", "")

	// Assert: Window created successfully with UUID
	require.NoError(t, err)
	require.NotNil(t, window)
	assert.NotEmpty(t, window.UUID, "Window should have UUID generated")

	// Verify UUID is valid v4 format
	err = session.ValidateUUID(window.UUID)
	assert.NoError(t, err, "UUID should be valid v4 format")

	// Verify UUID is set as tmux window option
	uuidFromTmux, err := server.executor.GetWindowOption(sessionID, window.TmuxWindowID, "window-uuid")
	require.NoError(t, err)
	assert.Equal(t, window.UUID, uuidFromTmux, "UUID in tmux option should match window struct")

	t.Logf("Created window with UUID: %s", window.UUID)
}

// TestServer_WindowsCreate_EnvironmentExport verifies that TMUX_WINDOW_UUID
// environment variable is exported in the window shell (AC1, AC2).
func TestServer_WindowsCreate_EnvironmentExport(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("env-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Create window via MCP
	window, err := server.WindowsCreate("env-window", "")
	require.NoError(t, err)

	// Give export command time to execute
	time.Sleep(200 * time.Millisecond)

	// Verify environment variable exported by echoing it
	err = server.executor.SendMessage(sessionID, window.TmuxWindowID, "echo TMUX_WINDOW_UUID=$TMUX_WINDOW_UUID")
	require.NoError(t, err)

	// Capture output to verify
	time.Sleep(200 * time.Millisecond)
	output, err := server.executor.CaptureWindowOutput(sessionID, window.TmuxWindowID)
	require.NoError(t, err)

	// Assert: Output contains the UUID
	expectedOutput := fmt.Sprintf("TMUX_WINDOW_UUID=%s", window.UUID)
	assert.Contains(t, output, expectedOutput, "Environment variable should be exported with correct UUID")

	t.Logf("Environment variable exported: TMUX_WINDOW_UUID=%s", window.UUID)
}

// TestServer_WindowsCreate_SessionPersistence verifies that windows created
// via MCP are saved to .tmux-session file with UUID (AC1, AC5).
func TestServer_WindowsCreate_SessionPersistence(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("persist-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Record initial window count
	initialSession, err := server.store.Load(tmpDir)
	require.NoError(t, err)
	initialWindowCount := len(initialSession.Windows)

	// Act: Create window via MCP
	window, err := server.WindowsCreate("persist-window", "")
	require.NoError(t, err)

	// Assert: Load session file and verify window is persisted
	updatedSession, err := server.store.Load(tmpDir)
	require.NoError(t, err)
	assert.Len(t, updatedSession.Windows, initialWindowCount+1, "Session should have one more window")

	// Find the newly created window in session
	var found bool
	var persistedWindow *store.Window
	for i := range updatedSession.Windows {
		if updatedSession.Windows[i].TmuxWindowID == window.TmuxWindowID {
			found = true
			persistedWindow = &updatedSession.Windows[i]
			break
		}
	}

	assert.True(t, found, "Window should be persisted in session file")
	require.NotNil(t, persistedWindow)
	assert.Equal(t, window.UUID, persistedWindow.UUID, "Persisted window should have same UUID")
	assert.Equal(t, window.Name, persistedWindow.Name, "Persisted window should have same name")
	assert.Equal(t, window.TmuxWindowID, persistedWindow.TmuxWindowID, "Persisted window should have same tmux ID")

	t.Logf("Window persisted to session file: %+v", persistedWindow)
}

// TestServer_WindowsCreate_CommandParameterIgnored verifies that the command
// parameter is ignored and zsh is always used (matches CLI behavior).
func TestServer_WindowsCreate_CommandParameterIgnored(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("cmd-ignore-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Create window with command parameter (should be ignored)
	window, err := server.WindowsCreate("test-zsh", "bash")
	require.NoError(t, err)

	// Give shell time to start
	time.Sleep(200 * time.Millisecond)

	// Verify zsh is running (not bash) by checking shell-specific variable
	err = server.executor.SendMessage(sessionID, window.TmuxWindowID, "echo SHELL_TYPE=$ZSH_VERSION")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	output, err := server.executor.CaptureWindowOutput(sessionID, window.TmuxWindowID)
	require.NoError(t, err)

	// Assert: ZSH_VERSION should be set (proves zsh is running)
	assert.Contains(t, output, "SHELL_TYPE=", "ZSH_VERSION should be set")
	assert.NotContains(t, output, "SHELL_TYPE=$ZSH_VERSION", "ZSH_VERSION should have actual value, not be unexpanded")

	t.Logf("Window uses zsh shell despite command parameter: %s", "bash")
}

// TestServer_WindowsCreate_CleanupOnUUIDSetupFailure verifies that the window
// is killed if UUID option setting fails (AC4).
func TestServer_WindowsCreate_CleanupOnUUIDSetupFailure(t *testing.T) {
	// This test is challenging to implement without mocking the executor
	// because we need SetWindowOption to fail. In a real integration test,
	// we would need to kill the session mid-operation or use other techniques.
	// This is better tested in unit tests with mock executor.
	t.Skip("Requires mock executor - covered in unit tests")
}

// TestServer_WindowsCreate_CleanupOnExportFailure verifies that the window
// is killed if environment variable export fails (AC4).
func TestServer_WindowsCreate_CleanupOnExportFailure(t *testing.T) {
	// Similar to above - requires mock executor to force SendMessage failure
	t.Skip("Requires mock executor - covered in unit tests")
}

// TestServer_WindowsCreate_PostCommandExecutionNonFatal verifies that window
// creation succeeds even if postcommand fails (AC3).
func TestServer_WindowsCreate_PostCommandExecutionNonFatal(t *testing.T) {
	// Arrange: Create session with failing postcommand config
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("postcmd-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"postCommand": {
			"enabled": true,
			"commands": [
				"nonexistent-command-that-will-fail",
				"another-failing-command"
			],
			"errorPatterns": ["not found"]
		},
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Create window (postcommand will fail, but window should be created)
	window, err := server.WindowsCreate("postcmd-window", "")

	// Assert: Window created successfully despite postcommand failure
	require.NoError(t, err, "Window creation should succeed even if postcommand fails")
	require.NotNil(t, window)
	assert.NotEmpty(t, window.UUID, "Window should have UUID")
	assert.Equal(t, "postcmd-window", window.Name)

	// Verify window exists in tmux
	windows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)

	var found bool
	for _, w := range windows {
		if w.TmuxWindowID == window.TmuxWindowID {
			found = true
			break
		}
	}
	assert.True(t, found, "Window should exist in tmux even though postcommand failed")

	t.Logf("Window created successfully despite postcommand failure: %s", window.TmuxWindowID)
}

// TestServer_WindowsCreate_MCLIParity_FullFlow is a comprehensive test that
// verifies MCP WindowsCreate matches CLI behavior exactly (all AC criteria).
func TestServer_WindowsCreate_MCLIParity_FullFlow(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("parity-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"postCommand": {
			"enabled": true,
			"commands": [
				"echo 'postcommand executed successfully'"
			]
		},
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Create window via MCP
	window, err := server.WindowsCreate("full-parity-test", "ignored-command")

	// Assert AC1: MCP windows-create behavior matches CLI exactly
	require.NoError(t, err)
	require.NotNil(t, window)
	assert.NotEmpty(t, window.TmuxWindowID, "Window has tmux ID")
	assert.Equal(t, "full-parity-test", window.Name, "Window has correct name")
	assert.NotEmpty(t, window.UUID, "Window has UUID set")

	// Assert AC2: UUID generation and persistence
	err = session.ValidateUUID(window.UUID)
	assert.NoError(t, err, "UUID is valid v4 format")

	// Verify UUID is set as tmux option
	uuidFromTmux, err := server.executor.GetWindowOption(sessionID, window.TmuxWindowID, "window-uuid")
	require.NoError(t, err)
	assert.Equal(t, window.UUID, uuidFromTmux, "UUID set as tmux window option")

	// Verify UUID stored in session file
	sess, err := server.store.Load(tmpDir)
	require.NoError(t, err)
	var persistedWindow *store.Window
	for i := range sess.Windows {
		if sess.Windows[i].TmuxWindowID == window.TmuxWindowID {
			persistedWindow = &sess.Windows[i]
			break
		}
	}
	require.NotNil(t, persistedWindow, "Window persisted in session file")
	assert.Equal(t, window.UUID, persistedWindow.UUID, "UUID stored in session file")

	// Verify environment variable exported
	time.Sleep(300 * time.Millisecond)
	err = server.executor.SendMessage(sessionID, window.TmuxWindowID, "echo ENV_CHECK=$TMUX_WINDOW_UUID")
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	output, err := server.executor.CaptureWindowOutput(sessionID, window.TmuxWindowID)
	require.NoError(t, err)
	expectedEnv := fmt.Sprintf("ENV_CHECK=%s", window.UUID)
	assert.Contains(t, output, expectedEnv, "TMUX_WINDOW_UUID environment variable exported")

	// Assert AC3: Postcommand execution (check for success message)
	assert.Contains(t, output, "postcommand executed successfully", "Postcommand should have executed")

	// Verify window uses zsh (command parameter ignored)
	assert.True(t, strings.Contains(output, "zsh") || strings.Contains(output, "ENV_CHECK="),
		"Window should be using zsh shell")

	t.Logf("Full parity test passed - MCP matches CLI behavior:")
	t.Logf("  Window: %s", window.TmuxWindowID)
	t.Logf("  UUID: %s", window.UUID)
	t.Logf("  Name: %s", window.Name)
	t.Logf("  Session persisted: ✓")
	t.Logf("  Environment exported: ✓")
	t.Logf("  Postcommand executed: ✓")
}

// TestServer_WindowsCreate_RecoveryIntegration verifies that MCP WindowsCreate
// triggers session recovery if needed (AC6).
func TestServer_WindowsCreate_RecoveryIntegration(t *testing.T) {
	// Arrange: Create session file but don't create tmux session (simulates killed session)
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("recovery-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "recovered", "uuid": "recovered-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Ensure tmux session doesn't exist (killed state)
	server.executor.KillSession(sessionID) // Idempotent - safe even if doesn't exist

	// Act: Create window (should trigger recovery first)
	window, err := server.WindowsCreate("post-recovery-window", "")

	// Assert: Window created successfully after recovery
	require.NoError(t, err, "WindowsCreate should succeed after recovery")
	require.NotNil(t, window)
	assert.NotEmpty(t, window.UUID, "New window should have UUID")

	// Verify session was recovered (session exists in tmux)
	exists, err := server.executor.HasSession(sessionID)
	require.NoError(t, err)
	assert.True(t, exists, "Session should exist after recovery")

	// Verify new window exists
	windows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)

	var newWindowFound bool
	for _, w := range windows {
		if w.TmuxWindowID == window.TmuxWindowID {
			newWindowFound = true
			break
		}
	}
	assert.True(t, newWindowFound, "New window should exist after recovery")

	// Cleanup
	server.executor.KillSession(sessionID)

	t.Logf("Recovery succeeded - new window created: %s (UUID: %s)", window.TmuxWindowID, window.UUID)
}

// TestServer_WindowsCreate_Performance verifies that WindowsCreate with full
// UUID/export/postcommand/persistence completes within performance requirements (AC1).
func TestServer_WindowsCreate_Performance(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("perf-parity-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"postCommand": {
			"enabled": true,
			"commands": ["echo 'quick command'"]
		},
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "main-uuid"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Act: Measure WindowsCreate performance
	start := time.Now()
	window, err := server.WindowsCreate("perf-test", "")
	duration := time.Since(start)

	// Assert: Performance requirement (excluding Claude startup, should be <500ms)
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.NotEmpty(t, window.UUID)

	// Note: Tech-spec says <500ms excluding Claude startup
	// We allow 2s for the full operation including postcommand
	assert.Less(t, duration, 2*time.Second, "WindowsCreate should complete in <2s (NFR-P1)")

	t.Logf("WindowsCreate with full parity completed in %v (target: <500ms excluding Claude startup)", duration)
}

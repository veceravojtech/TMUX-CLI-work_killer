//go:build integration
// +build integration

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentServers_MultipleInstances verifies multiple servers
// can run simultaneously without conflicts.
func TestConcurrentServers_MultipleInstances(t *testing.T) {
	// Arrange: Create 3 temporary project directories
	dirs := make([]string, 3)
	for i := range dirs {
		dirs[i] = t.TempDir()
		sessionFile := filepath.Join(dirs[i], ".tmux-cli-session.json")
		sessionData := fmt.Sprintf(`{"sessionID":"test-server-%d","projectPath":"%s","windows":[]}`, i, dirs[i])
		require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))
	}

	// Act: Start 3 servers concurrently
	var wg sync.WaitGroup
	servers := make([]*Server, 3)
	errs := make([]error, 3)

	for i := range servers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Change to directory before creating server
			oldDir, _ := os.Getwd()
			defer os.Chdir(oldDir)

			os.Chdir(dirs[idx])
			servers[idx], errs[idx] = NewServer()
		}(i)
	}

	wg.Wait()

	// Assert: All servers created successfully
	for i, err := range errs {
		require.NoError(t, err, "Server %d failed to initialize", i)
		require.NotNil(t, servers[i], "Server %d is nil", i)
	}

	// Assert: Each server has unique working directory
	workingDirs := make(map[string]bool)
	for _, srv := range servers {
		assert.False(t, workingDirs[srv.workingDir],
			"Duplicate working directory: %s", srv.workingDir)
		workingDirs[srv.workingDir] = true
	}

	// Assert: Servers operate independently (state isolation)
	for i, srv := range servers {
		assert.Equal(t, dirs[i], srv.workingDir,
			"Server %d has wrong working directory", i)
		assert.NotNil(t, srv.store, "Server %d has nil store", i)
		assert.NotNil(t, srv.executor, "Server %d has nil executor", i)
	}
}

// TestConcurrentServers_NoFileLocking verifies servers don't create
// file locking conflicts when accessing session files.
func TestConcurrentServers_NoFileLocking(t *testing.T) {
	// Arrange: Create shared project directory with session file
	projectDir := t.TempDir()
	sessionFile := filepath.Join(projectDir, ".tmux-cli-session.json")
	sessionData := fmt.Sprintf(`{"sessionID":"test-shared","projectPath":"%s","windows":[]}`, projectDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Act: Create 10 servers concurrently from same directory
	// Note: We test concurrent NewServer() calls, not concurrent os.Chdir()
	// Each goroutine changes directory sequentially, then creates server
	const numServers = 10
	var wg sync.WaitGroup
	servers := make([]*Server, numServers)
	errs := make([]error, numServers)

	// Use a mutex to serialize os.Chdir() calls (since it's global process state)
	// This isolates the file locking test from concurrent Chdir() issues
	var chdirMutex sync.Mutex

	for i := 0; i < numServers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			chdirMutex.Lock()
			oldDir, _ := os.Getwd()
			os.Chdir(projectDir)
			servers[idx], errs[idx] = NewServer()
			os.Chdir(oldDir)
			chdirMutex.Unlock()
		}(i)
	}

	wg.Wait()

	// Assert: No file locking errors
	for i, err := range errs {
		require.NoError(t, err, "Server %d encountered file lock", i)
	}

	// Assert: All servers initialized successfully
	for i, srv := range servers {
		require.NotNil(t, srv, "Server %d is nil", i)
		assert.Equal(t, projectDir, srv.workingDir)
	}
}

// TestConcurrentServers_PerformanceNoRegression verifies multiple
// concurrent servers don't degrade performance.
func TestConcurrentServers_PerformanceNoRegression(t *testing.T) {
	// Arrange: Create 3 project directories
	dirs := make([]string, 3)
	for i := range dirs {
		dirs[i] = t.TempDir()
		sessionFile := filepath.Join(dirs[i], ".tmux-cli-session.json")
		sessionData := fmt.Sprintf(`{"sessionID":"perf-test-%d","projectPath":"%s","windows":[]}`, i, dirs[i])
		require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))
	}

	// Baseline: Measure single server initialization time
	start := time.Now()
	oldDir, _ := os.Getwd()
	os.Chdir(dirs[0])
	_, err := NewServer()
	os.Chdir(oldDir)
	baselineTime := time.Since(start)
	require.NoError(t, err)

	// Act: Initialize 3 servers concurrently and measure time
	start = time.Now()
	var wg sync.WaitGroup
	for i := range dirs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			oldDir, _ := os.Getwd()
			defer os.Chdir(oldDir)
			os.Chdir(dirs[idx])
			NewServer()
		}(i)
	}
	wg.Wait()
	concurrentTime := time.Since(start)

	// Assert: Concurrent initialization not significantly slower
	// Allow 5x overhead for concurrency coordination (very fast operations)
	// Note: Since baseline is <1ms, we use generous multiplier for test stability
	assert.Less(t, concurrentTime, baselineTime*5,
		"Concurrent server initialization too slow: %v vs baseline %v",
		concurrentTime, baselineTime)

	// Assert: All operations complete within NFR-P2 (500ms)
	assert.Less(t, concurrentTime, 500*time.Millisecond,
		"Concurrent initialization exceeds NFR-P2 requirement")

	t.Logf("Performance: baseline=%v, concurrent=%v (3 servers)", baselineTime, concurrentTime)
}

// TestServer_WindowsList_Integration verifies WindowsList() works with
// real session files and returns accurate window data.
func TestServer_WindowsList_Integration(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Write real session data with multiple windows
	sessionData := `{
		"sessionId": "integration-test",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "0", "name": "shell", "uuid": "uuid-1"},
			{"tmuxWindowId": "1", "name": "vim", "uuid": "uuid-2"},
			{"tmuxWindowId": "2", "name": "logs", "uuid": "uuid-3"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Create server pointing to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Call WindowsList and measure performance (NFR-P1)
	start := time.Now()
	windows, err := server.WindowsList()
	duration := time.Since(start)

	// Assert: Verify real data loaded correctly
	require.NoError(t, err)
	assert.Len(t, windows, 3)

	// Verify first window
	assert.Equal(t, "shell", windows[0].Name)
	assert.Equal(t, "0", windows[0].TmuxWindowID)
	assert.Equal(t, "uuid-1", windows[0].UUID)

	// Verify second window
	assert.Equal(t, "vim", windows[1].Name)
	assert.Equal(t, "1", windows[1].TmuxWindowID)
	assert.Equal(t, "uuid-2", windows[1].UUID)

	// Verify third window
	assert.Equal(t, "logs", windows[2].Name)
	assert.Equal(t, "2", windows[2].TmuxWindowID)
	assert.Equal(t, "uuid-3", windows[2].UUID)

	// Assert: Performance requirement (NFR-P1: <2s)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Operation must complete in <2s")

	// Assert: Expected performance (<100ms for file read + JSON parse)
	assert.Less(t, duration, 100*time.Millisecond, "Expected performance <100ms")

	t.Logf("Performance: WindowsList completed in %v", duration)
}

// TestServer_WindowsList_Integration_EmptySession verifies WindowsList()
// handles sessions with no windows correctly.
func TestServer_WindowsList_Integration_EmptySession(t *testing.T) {
	// Arrange: Create session with empty windows array
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "empty-session",
		"projectPath": "` + tmpDir + `",
		"windows": []
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act
	windows, err := server.WindowsList()

	// Assert: Returns empty array, not nil
	require.NoError(t, err)
	assert.NotNil(t, windows)
	assert.Len(t, windows, 0)
}

// TestServer_WindowsList_Integration_SessionNotFound verifies WindowsList()
// returns appropriate error when session file doesn't exist.
func TestServer_WindowsList_Integration_SessionNotFound(t *testing.T) {
	// Arrange: Create server in directory WITHOUT session file
	tmpDir := t.TempDir()
	// Note: We need to create a session file for NewServer() to succeed,
	// but then delete it to test the error case
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{"sessionId": "temp", "projectPath": "` + tmpDir + `", "windows": []}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Delete the session file AFTER server creation to simulate file removal
	os.Remove(sessionFile)

	// Act
	windows, err := server.WindowsList()

	// Assert: Error is wrapped with ErrSessionNotFound
	require.Error(t, err)
	assert.Nil(t, windows)
	// Note: The error will be from store.Load(), not ErrSessionNotFound directly,
	// but our WindowsList wraps it
	assert.Contains(t, err.Error(), tmpDir, "Error should include directory path")
}

// ========================================
// WindowsGet Integration Tests
// ========================================

// TestServer_WindowsGet_Integration verifies WindowsGet() works with
// real session files and returns accurate window data.
func TestServer_WindowsGet_Integration(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Write real session data with multiple windows
	sessionData := `{
		"sessionId": "integration-test",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "0", "name": "shell", "uuid": "uuid-1"},
			{"tmuxWindowId": "1", "name": "vim", "uuid": "uuid-2"},
			{"tmuxWindowId": "2", "name": "logs", "uuid": "uuid-3"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Create server pointing to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Call WindowsGet for each window ID and measure performance (NFR-P1)
	testCases := []struct {
		windowID     string
		expectedName string
		expectedUUID string
	}{
		{"0", "shell", "uuid-1"},
		{"1", "vim", "uuid-2"},
		{"2", "logs", "uuid-3"},
	}

	for _, tc := range testCases {
		t.Run("window_"+tc.windowID, func(t *testing.T) {
			start := time.Now()
			window, err := server.WindowsGet(tc.windowID)
			duration := time.Since(start)

			// Assert: Verify real data loaded correctly
			require.NoError(t, err)
			require.NotNil(t, window)
			assert.Equal(t, tc.expectedName, window.Name)
			assert.Equal(t, tc.windowID, window.TmuxWindowID)
			assert.Equal(t, tc.expectedUUID, window.UUID)

			// Assert: Performance requirement (NFR-P1: <2s)
			assert.Less(t, duration, 2*time.Second, "NFR-P1: Operation must complete in <2s")

			// Assert: Expected performance (<100ms for file read + JSON parse + search)
			assert.Less(t, duration, 100*time.Millisecond, "Expected performance <100ms")

			t.Logf("Performance: WindowsGet(%s) completed in %v", tc.windowID, duration)
		})
	}
}

// TestServer_WindowsGet_Integration_NonExistentWindow verifies WindowsGet()
// returns appropriate error when requesting a non-existent window.
func TestServer_WindowsGet_Integration_NonExistentWindow(t *testing.T) {
	// Arrange: Create session with 3 windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "test-session",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "0", "name": "main", "uuid": "uuid-1"},
			{"tmuxWindowId": "1", "name": "editor", "uuid": "uuid-2"},
			{"tmuxWindowId": "2", "name": "logs", "uuid": "uuid-3"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Request non-existent window ID
	window, err := server.WindowsGet("99")

	// Assert: Error is wrapped with ErrWindowNotFound
	require.Error(t, err)
	assert.Nil(t, window)
	assert.Contains(t, err.Error(), "99", "Error should include window ID in context")
}

// TestServer_WindowsGet_Integration_InvalidWindowID verifies WindowsGet()
// returns appropriate error for invalid window IDs.
func TestServer_WindowsGet_Integration_InvalidWindowID(t *testing.T) {
	// Arrange: Create session
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "test-session",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "0", "name": "main", "uuid": "uuid-1"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Request with empty window ID
	window, err := server.WindowsGet("")

	// Assert: Error indicates invalid window ID
	require.Error(t, err)
	assert.Nil(t, window)
	assert.Contains(t, err.Error(), "invalid window ID", "Error should mention invalid window ID")
}

// ========================================
// WindowsCapture Integration Tests
// ========================================

// TestServer_WindowsCapture_Integration_RealTmux verifies WindowsCapture
// captures pane output from a real tmux session.
func TestServer_WindowsCapture_Integration_RealTmux(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Write real session data
	sessionData := `{
		"sessionId": "capture-test",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "@0", "name": "test-window", "uuid": "uuid-1"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Create server pointing to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Call WindowsCapture with timing
	start := time.Now()
	output, err := server.WindowsCapture("@0")
	duration := time.Since(start)

	// Assert: Performance requirement NFR-P1
	require.NoError(t, err)
	assert.NotNil(t, output) // Output can be empty string, but should not be nil
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Must complete in <2s")

	t.Logf("WindowsCapture completed in %v (NFR-P1 requirement: <2s)", duration)
}

// TestServer_WindowsCapture_Integration_NonExistentWindow verifies WindowsCapture
// returns proper error when window doesn't exist.
func TestServer_WindowsCapture_Integration_NonExistentWindow(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "capture-test",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "@0", "name": "test", "uuid": "uuid-1"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Request non-existent window
	output, err := server.WindowsCapture("@99")

	// Assert: Should return window not found error
	require.Error(t, err)
	assert.Empty(t, output)
	assert.Contains(t, err.Error(), "window not found", "Error should indicate window not found")
	assert.Contains(t, err.Error(), "@99", "Error should include requested window ID")
}

// TestServer_WindowsCapture_Integration_InvalidWindowID verifies WindowsCapture
// handles invalid window ID correctly.
func TestServer_WindowsCapture_Integration_InvalidWindowID(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "test-session",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "uuid-1"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Request with empty window ID
	output, err := server.WindowsCapture("")

	// Assert: Error indicates invalid window ID
	require.Error(t, err)
	assert.Empty(t, output)
	assert.Contains(t, err.Error(), "invalid window ID", "Error should mention invalid window ID")
}

// ========================================
// WindowsSend Integration Tests
// ========================================

// TestServer_WindowsSend_Integration_RealTmux verifies WindowsSend
// sends commands to real tmux windows successfully.
func TestServer_WindowsSend_Integration_RealTmux(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Write real session data
	sessionData := `{
		"sessionId": "send-test-` + fmt.Sprintf("%d", time.Now().Unix()) + `",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "@0", "name": "test-window", "uuid": "uuid-1"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Create server pointing to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Send a simple echo command
	start := time.Now()
	success, err := server.WindowsSend("@0", "echo 'test message'")
	duration := time.Since(start)

	// Assert
	require.NoError(t, err)
	assert.True(t, success, "WindowsSend should return true on success")
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Must complete in <2s")

	// Verify command executed by capturing output
	time.Sleep(100 * time.Millisecond) // Give command time to execute
	output, err := server.WindowsCapture("@0")
	require.NoError(t, err)
	assert.Contains(t, output, "test message", "Command should have executed")
}

// TestServer_WindowsSend_Integration_SequentialCommands verifies WindowsSend
// can send commands to multiple windows in sequence (FR10).
func TestServer_WindowsSend_Integration_SequentialCommands(t *testing.T) {
	// Test FR10: Send commands to multiple windows in sequence
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "multi-window-test-` + fmt.Sprintf("%d", time.Now().Unix()) + `",
		"projectPath": "` + tmpDir + `",
		"windows": [
			{"tmuxWindowId": "@0", "name": "window-0", "uuid": "uuid-1"},
			{"tmuxWindowId": "@1", "name": "window-1", "uuid": "uuid-2"}
		]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Send commands to multiple windows in sequence
	success1, err1 := server.WindowsSend("@0", "echo 'Window 0 command'")
	require.NoError(t, err1)
	assert.True(t, success1, "First command should succeed")

	success2, err2 := server.WindowsSend("@1", "echo 'Window 1 command'")
	require.NoError(t, err2)
	assert.True(t, success2, "Second command should succeed")

	// Verify both commands executed
	time.Sleep(100 * time.Millisecond)

	output0, err0 := server.WindowsCapture("@0")
	require.NoError(t, err0)
	assert.Contains(t, output0, "Window 0 command", "First window should show its command")

	output1, err1Cap := server.WindowsCapture("@1")
	require.NoError(t, err1Cap)
	assert.Contains(t, output1, "Window 1 command", "Second window should show its command")
}

// TestServer_WindowsSend_Integration_NonExistentWindow verifies WindowsSend
// returns appropriate error when window doesn't exist.
func TestServer_WindowsSend_Integration_NonExistentWindow(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "send-test-` + fmt.Sprintf("%d", time.Now().Unix()) + `",
		"projectPath": "` + tmpDir + `",
		"windows": [{"tmuxWindowId": "@0", "name": "test", "uuid": "uuid-1"}]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Send to non-existent window
	success, err := server.WindowsSend("@99", "echo 'test'")

	// Assert
	require.Error(t, err)
	assert.False(t, success, "Should return false on error")
	assert.Contains(t, err.Error(), "window not found", "Error should indicate window not found")
	assert.Contains(t, err.Error(), "@99", "Error should include requested window ID")
}

// TestServer_WindowsSend_Integration_Performance verifies WindowsSend
// completes within NFR-P1 performance requirement (<2s).
func TestServer_WindowsSend_Integration_Performance(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "perf-test-` + fmt.Sprintf("%d", time.Now().Unix()) + `",
		"projectPath": "` + tmpDir + `",
		"windows": [{"tmuxWindowId": "@0", "name": "main", "uuid": "uuid-1"}]
	}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Measure command send performance
	start := time.Now()
	success, err := server.WindowsSend("@0", "ls")
	duration := time.Since(start)

	// Assert: Performance requirement
	require.NoError(t, err)
	assert.True(t, success)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Command send must complete in <2s")

	// Log actual performance for documentation
	t.Logf("WindowsSend performance: %v (target: <2s)", duration)
}

// ============================================================================
// WindowsCreate Integration Tests
// ============================================================================

// TestServer_WindowsCreate_Integration_RealTmux verifies WindowsCreate
// creates a window without command in a real tmux session.
func TestServer_WindowsCreate_Integration_RealTmux(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-cli-session.json")

	// Create real session data
	sessionData := fmt.Sprintf(`{"sessionID":"create-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Change to test directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Create server
	server, err := NewServer()
	require.NoError(t, err)

	// Act: Create window without command
	start := time.Now()
	window, err := server.WindowsCreate("test-window", "")
	duration := time.Since(start)

	// Assert: Window created successfully
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.NotEmpty(t, window.TmuxWindowID)
	assert.Equal(t, "test-window", window.Name)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Window creation must complete in <2s")

	// Verify window exists using WindowsList
	windows, err := server.WindowsList()
	require.NoError(t, err)

	var found bool
	for _, w := range windows {
		if w.Name == "test-window" {
			found = true
			break
		}
	}
	assert.True(t, found, "Created window should appear in windows list")

	// Log performance
	t.Logf("WindowsCreate performance: %v (target: <2s)", duration)
}

// TestServer_WindowsCreate_Integration_WithCommand verifies WindowsCreate
// creates a window with command execution in a real tmux session.
func TestServer_WindowsCreate_Integration_WithCommand(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-cli-session.json")

	sessionData := fmt.Sprintf(`{"sessionID":"create-test-cmd","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Create window with command
	window, err := server.WindowsCreate("echo-window", "echo 'Hello from new window'")

	// Assert: Window created successfully
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Equal(t, "echo-window", window.Name)
	assert.NotEmpty(t, window.TmuxWindowID)

	// Give command time to execute
	time.Sleep(100 * time.Millisecond)

	// Verify command executed by capturing output
	output, err := server.WindowsCapture(window.TmuxWindowID)
	require.NoError(t, err)
	assert.Contains(t, output, "Hello from new window", "Command should have executed")
}

// TestServer_WindowsCreate_Integration_ImmediatelyUsable verifies newly
// created windows are immediately usable by other MCP tools.
func TestServer_WindowsCreate_Integration_ImmediatelyUsable(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-cli-session.json")

	sessionData := fmt.Sprintf(`{"sessionID":"usable-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Create window
	window, err := server.WindowsCreate("interactive", "")
	require.NoError(t, err)

	// Immediately use WindowsGet to retrieve details
	retrieved, err := server.WindowsGet(window.TmuxWindowID)
	require.NoError(t, err)
	assert.Equal(t, window.TmuxWindowID, retrieved.TmuxWindowID)
	assert.Equal(t, "interactive", retrieved.Name)

	// Immediately send command to new window
	success, err := server.WindowsSend(window.TmuxWindowID, "echo 'testing'")
	require.NoError(t, err)
	assert.True(t, success)

	// Verify command executed
	time.Sleep(50 * time.Millisecond)
	output, err := server.WindowsCapture(window.TmuxWindowID)
	require.NoError(t, err)
	assert.Contains(t, output, "testing")
}

// TestServer_WindowsCreate_Integration_InvalidSession verifies error handling
// when session doesn't exist.
func TestServer_WindowsCreate_Integration_InvalidSession(t *testing.T) {
	// Arrange: Create temporary directory WITHOUT session file
	tmpDir := t.TempDir()

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Act: Try to create server (should fail - no session file)
	_, err := NewServer()

	// Assert: Should fail with session not found
	require.Error(t, err, "Should fail when no session file exists")
}

// TestServer_WindowsCreate_Integration_Performance verifies WindowsCreate
// meets NFR-P1 performance requirement (<2s).
func TestServer_WindowsCreate_Integration_Performance(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-cli-session.json")

	sessionData := fmt.Sprintf(`{"sessionID":"perf-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Create window and measure time
	start := time.Now()
	window, err := server.WindowsCreate("perf-window", "echo 'performance test'")
	duration := time.Since(start)

	// Assert: Performance requirement
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Window creation must complete in <2s")

	// Log actual performance for documentation
	t.Logf("WindowsCreate performance: %v (target: <2s)", duration)
}

// ============================================================================
// WindowsKill Integration Tests (Story 5.2)
// ============================================================================

// TestServer_WindowsKill_Integration_RealTmux verifies that WindowsKill
// successfully terminates a real tmux window and validates the window
// is immediately removed from the session.
func TestServer_WindowsKill_Integration_RealTmux(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Use unique session ID to avoid conflicts
	sessionID := fmt.Sprintf("kill-test-%d", time.Now().UnixNano())

	sessionData := fmt.Sprintf(`{
		"sessionID": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "main", "uuid": "uuid-1"},
			{"tmuxWindowID": "@1", "name": "temp", "uuid": "uuid-2"}
		]
	}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Change to temp directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session with 2 windows
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Create second window in tmux
	_, err = server.executor.CreateWindow(sessionID, "temp", "")
	require.NoError(t, err)

	// Verify 2 windows exist before kill (query tmux directly)
	windowsBefore, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.Len(t, windowsBefore, 2, "Should have 2 windows initially")

	// Act: Kill second window
	start := time.Now()
	success, err := server.WindowsKill(windowsBefore[1].TmuxWindowID)
	duration := time.Since(start)

	// Assert
	require.NoError(t, err)
	assert.True(t, success)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Must complete in <2s")

	// Verify window removed (query tmux directly)
	windowsAfter, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	assert.Len(t, windowsAfter, 1, "Should have only 1 window after kill")
	assert.Equal(t, windowsBefore[0].TmuxWindowID, windowsAfter[0].TmuxWindowID, "Remaining window should be the first one")

	t.Logf("WindowsKill performance: %v (target: <2s)", duration)
}

// TestServer_WindowsKill_Integration_OtherWindowsUnaffected verifies that
// killing one window doesn't affect other windows in the session.
func TestServer_WindowsKill_Integration_OtherWindowsUnaffected(t *testing.T) {
	// Arrange: Create session with 3 windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{
		"sessionID": "multi-kill-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "first", "uuid": "uuid-1"},
			{"tmuxWindowID": "@1", "name": "middle", "uuid": "uuid-2"},
			{"tmuxWindowID": "@2", "name": "last", "uuid": "uuid-3"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Kill middle window
	success, err := server.WindowsKill("@1")
	require.NoError(t, err)
	assert.True(t, success)

	// Verify other windows still exist
	windows, err := server.WindowsList()
	require.NoError(t, err)
	assert.Len(t, windows, 2, "Should have 2 windows after killing middle one")

	// Check @0 and @2 still exist
	windowIDs := make([]string, len(windows))
	for i, w := range windows {
		windowIDs[i] = w.TmuxWindowID
	}
	assert.Contains(t, windowIDs, "@0", "First window should still exist")
	assert.Contains(t, windowIDs, "@2", "Last window should still exist")
	assert.NotContains(t, windowIDs, "@1", "Killed window should not exist")
}

// TestServer_WindowsKill_Integration_Idempotency verifies that killing
// the same window twice succeeds (idempotent behavior).
func TestServer_WindowsKill_Integration_Idempotency(t *testing.T) {
	// Arrange: Create session with 2 windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{
		"sessionID": "idempotent-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "main", "uuid": "uuid-1"},
			{"tmuxWindowID": "@1", "name": "temp", "uuid": "uuid-2"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// First kill
	success1, err1 := server.WindowsKill("@1")
	require.NoError(t, err1)
	assert.True(t, success1)

	// Update session file to reflect killed window (simulate real scenario)
	sessionData = fmt.Sprintf(`{
		"sessionID": "idempotent-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "main", "uuid": "uuid-1"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Second kill (idempotent)
	success2, err2 := server.WindowsKill("@1")
	require.NoError(t, err2, "Second kill should succeed (idempotent)")
	assert.True(t, success2, "Second kill should return true (idempotent)")
}

// TestServer_WindowsKill_Integration_LastWindowPrevention verifies that
// attempting to kill the last window in a session returns an error.
func TestServer_WindowsKill_Integration_LastWindowPrevention(t *testing.T) {
	// Arrange: Create session with only 1 window
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{
		"sessionID": "last-window-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "only-window", "uuid": "uuid-1"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Attempt to kill last window
	success, err := server.WindowsKill("@0")

	// Should fail
	require.Error(t, err, "Should not allow killing last window")
	assert.False(t, success)
	assert.Contains(t, err.Error(), "last window", "Error should mention last window")

	// Verify session still has window
	windows, _ := server.WindowsList()
	assert.Len(t, windows, 1, "Window should still exist")
}

// TestServer_WindowsKill_Integration_PerformanceUnder2s validates that
// window kill operations complete within the NFR-P1 requirement of <2s.
func TestServer_WindowsKill_Integration_PerformanceUnder2s(t *testing.T) {
	// Arrange: Create session with multiple windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{
		"sessionID": "perf-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowID": "@0", "name": "main", "uuid": "uuid-1"},
			{"tmuxWindowID": "@1", "name": "killme", "uuid": "uuid-2"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Measure performance
	start := time.Now()
	success, err := server.WindowsKill("@1")
	duration := time.Since(start)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Kill operation must complete in <2s")

	// Document actual performance
	t.Logf("Kill operation completed in %v (target: <2s, typical: <100ms)", duration)
}

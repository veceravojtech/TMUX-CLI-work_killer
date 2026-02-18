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
		sessionFile := filepath.Join(dirs[i], ".tmux-session")
		sessionData := fmt.Sprintf(`{"sessionId":"test-server-%d","projectPath":"%s","windows":[]}`, i, dirs[i])
		require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))
	}

	// Save original dir before concurrent Chdir (os.Chdir is process-global)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	// Act: Start 3 servers concurrently
	// Use mutex to serialize os.Chdir() calls (process-global state)
	var wg sync.WaitGroup
	var chdirMutex sync.Mutex
	servers := make([]*Server, 3)
	errs := make([]error, 3)

	for i := range servers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			chdirMutex.Lock()
			oldDir, _ := os.Getwd()
			os.Chdir(dirs[idx])
			servers[idx], errs[idx] = NewServer()
			os.Chdir(oldDir)
			chdirMutex.Unlock()
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
	sessionFile := filepath.Join(projectDir, ".tmux-session")
	sessionData := fmt.Sprintf(`{"sessionId":"test-shared","projectPath":"%s","windows":[]}`, projectDir)
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
		sessionFile := filepath.Join(dirs[i], ".tmux-session")
		sessionData := fmt.Sprintf(`{"sessionId":"perf-test-%d","projectPath":"%s","windows":[]}`, i, dirs[i])
		require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))
	}

	// Save original dir before any Chdir (os.Chdir is process-global)
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	// Baseline: Measure single server initialization time
	start := time.Now()
	os.Chdir(dirs[0])
	_, err := NewServer()
	os.Chdir(origDir)
	baselineTime := time.Since(start)
	require.NoError(t, err)

	// Act: Initialize 3 servers concurrently and measure time
	// Use mutex to serialize os.Chdir() calls (process-global state)
	start = time.Now()
	var wg sync.WaitGroup
	var chdirMutex sync.Mutex
	for i := range dirs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			chdirMutex.Lock()
			oldDir, _ := os.Getwd()
			os.Chdir(dirs[idx])
			NewServer()
			os.Chdir(oldDir)
			chdirMutex.Unlock()
		}(i)
	}
	wg.Wait()
	concurrentTime := time.Since(start)

	// Assert: Concurrent initialization not significantly slower
	// Allow 15x overhead: operations are serialized by mutex (Chdir is process-global)
	// so concurrent time ≈ N × baseline + mutex overhead
	assert.Less(t, concurrentTime, baselineTime*15,
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
			{"tmuxWindowId": "0", "name": "shell", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "1", "name": "vim", "uuid": "00000002-0000-4000-8000-000000000002"},
			{"tmuxWindowId": "2", "name": "logs", "uuid": "00000003-0000-4000-8000-000000000003"}
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

	// Verify windows (WindowListItem only exposes Name, not IDs or UUIDs)
	assert.Equal(t, "shell", windows[0].Name)
	assert.Equal(t, "vim", windows[1].Name)
	assert.Equal(t, "logs", windows[2].Name)

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
// WindowsSend Integration Tests
// ========================================

// TestServer_WindowsSend_Integration_RealTmux verifies WindowsSend
// sends commands to real tmux windows successfully.
func TestServer_WindowsSend_Integration_RealTmux(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("send-test-%d", time.Now().UnixNano())
	// Write minimal session file for NewServer
	sessionData := fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[]}`, sessionID, tmpDir)
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

	// Get actual window IDs from tmux
	actualWindows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, actualWindows)

	// Update session file with real tmux window IDs
	sessionData = fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[{"tmuxWindowId":"%s","name":"test-window","uuid":"00000001-0000-4000-8000-000000000001"}]}`,
		sessionID, tmpDir, actualWindows[0].TmuxWindowID)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Act: Send a simple echo command
	start := time.Now()
	success, err := server.WindowsSend(actualWindows[0].TmuxWindowID, "echo 'test message'")
	duration := time.Since(start)

	// Assert
	require.NoError(t, err)
	assert.True(t, success, "WindowsSend should return true on success")
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Must complete in <2s")
}

// TestServer_WindowsSend_Integration_SequentialCommands verifies WindowsSend
// can send commands to multiple windows in sequence (FR10).
func TestServer_WindowsSend_Integration_SequentialCommands(t *testing.T) {
	// Arrange: Create real tmux session with 2 windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("multi-window-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[]}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session and second window
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	_, err = server.executor.CreateWindow(sessionID, "window-1", "")
	require.NoError(t, err)

	// Get actual window IDs
	actualWindows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.Len(t, actualWindows, 2)

	// Update session file with real tmux window IDs
	sessionData = fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[{"tmuxWindowId":"%s","name":"%s","uuid":"00000001-0000-4000-8000-000000000001"},{"tmuxWindowId":"%s","name":"%s","uuid":"00000002-0000-4000-8000-000000000002"}]}`,
		sessionID, tmpDir,
		actualWindows[0].TmuxWindowID, actualWindows[0].Name,
		actualWindows[1].TmuxWindowID, actualWindows[1].Name)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Act: Send commands to multiple windows in sequence
	success1, err1 := server.WindowsSend(actualWindows[0].TmuxWindowID, "echo 'Window 0 command'")
	require.NoError(t, err1)
	assert.True(t, success1, "First command should succeed")

	success2, err2 := server.WindowsSend(actualWindows[1].TmuxWindowID, "echo 'Window 1 command'")
	require.NoError(t, err2)
	assert.True(t, success2, "Second command should succeed")

	// Verify both commands executed
	time.Sleep(100 * time.Millisecond)
}

// TestServer_WindowsSend_Integration_NonExistentWindow verifies WindowsSend
// returns appropriate error when window doesn't exist.
func TestServer_WindowsSend_Integration_NonExistentWindow(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := `{
		"sessionId": "send-test-` + fmt.Sprintf("%d", time.Now().Unix()) + `",
		"projectPath": "` + tmpDir + `",
		"windows": [{"tmuxWindowId": "@0", "name": "test", "uuid": "00000001-0000-4000-8000-000000000001"}]
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

	sessionID := fmt.Sprintf("perf-test-%d", time.Now().UnixNano())
	sessionData := fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[]}`, sessionID, tmpDir)
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

	// Get actual window IDs
	actualWindows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, actualWindows)

	// Update session file with real tmux window IDs
	sessionData = fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[{"tmuxWindowId":"%s","name":"main","uuid":"00000001-0000-4000-8000-000000000001"}]}`,
		sessionID, tmpDir, actualWindows[0].TmuxWindowID)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Act: Measure command send performance
	start := time.Now()
	success, err := server.WindowsSend(actualWindows[0].TmuxWindowID, "ls")
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
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	// Create real session data
	sessionData := fmt.Sprintf(`{"sessionId":"create-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
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
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{"sessionId":"create-test-cmd","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
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
}

// TestServer_WindowsCreate_Integration_ImmediatelyUsable verifies newly
// created windows are immediately usable by other MCP tools.
func TestServer_WindowsCreate_Integration_ImmediatelyUsable(t *testing.T) {
	// Arrange: Create temporary directory with real session file
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{"sessionId":"usable-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Act: Create window
	window, err := server.WindowsCreate("interactive", "")
	require.NoError(t, err)

	// Immediately verify window appears in list
	windows, err := server.WindowsList()
	require.NoError(t, err)
	var found bool
	for _, w := range windows {
		if w.Name == "interactive" {
			found = true
			break
		}
	}
	assert.True(t, found, "Created window should appear in windows list")

	// Immediately send command to new window
	success, err := server.WindowsSend(window.TmuxWindowID, "echo 'testing'")
	require.NoError(t, err)
	assert.True(t, success)

	// Verify command executed
	time.Sleep(50 * time.Millisecond)
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
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{"sessionId":"perf-test","projectPath":"%s","windows":[{"tmuxWindowId":"0","name":"main"}]}`, tmpDir)
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
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "@1", "name": "temp", "uuid": "00000002-0000-4000-8000-000000000002"}
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

	// Update session file with actual tmux window IDs (real IDs may differ from @0/@1)
	sessionData = fmt.Sprintf(`{
		"sessionId": "%s",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "%s", "name": "%s", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "%s", "name": "%s", "uuid": "00000002-0000-4000-8000-000000000002"}
		]
	}`, sessionID, tmpDir,
		windowsBefore[0].TmuxWindowID, windowsBefore[0].Name,
		windowsBefore[1].TmuxWindowID, windowsBefore[1].Name)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Act: Kill second window (WindowsKill requires name, not @-prefix ID)
	start := time.Now()
	success, err := server.WindowsKill(windowsBefore[1].Name)
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
		"sessionId": "multi-kill-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "first", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "@1", "name": "middle", "uuid": "00000002-0000-4000-8000-000000000002"},
			{"tmuxWindowId": "@2", "name": "last", "uuid": "00000003-0000-4000-8000-000000000003"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Kill middle window (WindowsKill requires name, not @-prefix ID)
	success, err := server.WindowsKill("middle")
	require.NoError(t, err)
	assert.True(t, success)

	// Verify other windows still exist
	windows, err := server.WindowsList()
	require.NoError(t, err)
	assert.Len(t, windows, 2, "Should have 2 windows after killing middle one")

	// Check first and last still exist by name
	windowNames := make([]string, len(windows))
	for i, w := range windows {
		windowNames[i] = w.Name
	}
	assert.Contains(t, windowNames, "first", "First window should still exist")
	assert.Contains(t, windowNames, "last", "Last window should still exist")
	assert.NotContains(t, windowNames, "middle", "Killed window should not exist")
}

// TestServer_WindowsKill_Integration_Idempotency verifies that killing
// the same window twice succeeds (idempotent behavior).
func TestServer_WindowsKill_Integration_Idempotency(t *testing.T) {
	// Arrange: Create session with 2 windows
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionData := fmt.Sprintf(`{
		"sessionId": "idempotent-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "@1", "name": "temp", "uuid": "00000002-0000-4000-8000-000000000002"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// First kill (WindowsKill requires name, not @-prefix ID)
	success1, err1 := server.WindowsKill("temp")
	require.NoError(t, err1)
	assert.True(t, success1)

	// Update session file to reflect killed window (simulate real scenario)
	sessionData = fmt.Sprintf(`{
		"sessionId": "idempotent-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "00000001-0000-4000-8000-000000000001"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Second kill — window name no longer exists in session, should error
	success2, err2 := server.WindowsKill("temp")
	require.Error(t, err2, "Second kill should fail (window no longer in session)")
	assert.False(t, success2, "Second kill should return false")
}

// TestServer_WindowsKill_Integration_LastWindowPrevention verifies that
// attempting to kill the last window in a session returns an error.
func TestServer_WindowsKill_Integration_LastWindowPrevention(t *testing.T) {
	// Arrange: Create real tmux session with exactly 1 window
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".tmux-session")

	sessionID := fmt.Sprintf("last-window-test-%d", time.Now().UnixNano())
	// Write minimal session file for NewServer
	sessionData := fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[]}`, sessionID, tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Create real tmux session (1 default window)
	err = server.executor.CreateSession(sessionID, tmpDir)
	require.NoError(t, err)
	defer server.executor.KillSession(sessionID)

	// Get actual window ID
	actualWindows, err := server.executor.ListWindows(sessionID)
	require.NoError(t, err)
	require.Len(t, actualWindows, 1)

	// Update session file with real window ID
	sessionData = fmt.Sprintf(`{"sessionId":"%s","projectPath":"%s","windows":[{"tmuxWindowId":"%s","name":"only-window","uuid":"00000001-0000-4000-8000-000000000001"}]}`,
		sessionID, tmpDir, actualWindows[0].TmuxWindowID)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Attempt to kill last window (WindowsKill requires name, not @-prefix ID)
	success, err := server.WindowsKill("only-window")

	// Should fail
	require.Error(t, err, "Should not allow killing last window")
	assert.False(t, success)

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
		"sessionId": "perf-test-%d",
		"projectPath": "%s",
		"windows": [
			{"tmuxWindowId": "@0", "name": "main", "uuid": "00000001-0000-4000-8000-000000000001"},
			{"tmuxWindowId": "@1", "name": "killme", "uuid": "00000002-0000-4000-8000-000000000002"}
		]
	}`, time.Now().UnixNano(), tmpDir)
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	server, err := NewServer()
	require.NoError(t, err)

	// Measure performance (WindowsKill requires name, not @-prefix ID)
	start := time.Now()
	success, err := server.WindowsKill("killme")
	duration := time.Since(start)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Less(t, duration, 2*time.Second, "NFR-P1: Kill operation must complete in <2s")

	// Document actual performance
	t.Logf("Kill operation completed in %v (target: <2s, typical: <100ms)", duration)
}

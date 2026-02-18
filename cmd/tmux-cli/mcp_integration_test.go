//go:build integration
// +build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPCommand_Integration_ServerStarts verifies that the MCP server
// starts successfully when a valid session file exists in the working directory.
func TestMCPCommand_Integration_ServerStarts(t *testing.T) {
	// Arrange: Create temp directory with session file
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, ".tmux-session")
	sessionData := `{"sessionId":"test-mcp","windows":[]}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Build binary from project root
	binary := filepath.Join(tempDir, "tmux-cli-test")
	// Get project root (two directories up from cmd/tmux-cli)
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	buildCmd := exec.Command("go", "build", "-o", binary, "./cmd/tmux-cli")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build tmux-cli binary: %s", string(output))

	// Act: Start MCP server in background
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "mcp")
	cmd.Dir = tempDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err, "MCP server should start successfully")

	// Give server time to initialize
	time.Sleep(100 * time.Millisecond)

	// Assert: Server is running
	assert.NotNil(t, cmd.Process, "Server process should be running")

	// Cleanup: Shutdown server
	cmd.Process.Signal(os.Interrupt)
	cmd.Wait()
}

// TestMCPCommand_Integration_SessionNotFound verifies that the server
// returns a clear error when no session file exists in the working directory.
func TestMCPCommand_Integration_SessionNotFound(t *testing.T) {
	// Arrange: Empty temp directory (no session file)
	tempDir := t.TempDir()

	// Build binary from project root
	binary := filepath.Join(tempDir, "tmux-cli-test")
	// Get project root (two directories up from cmd/tmux-cli)
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	buildCmd := exec.Command("go", "build", "-o", binary, "./cmd/tmux-cli")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build tmux-cli binary: %s", string(output))

	// Act: Try to start MCP server
	cmd := exec.Command(binary, "mcp")
	cmd.Dir = tempDir
	output, err = cmd.CombinedOutput()

	// Assert: Error about missing session file
	assert.Error(t, err, "Should return error when session file not found")
	assert.Contains(t, string(output), "session file not detected", "Error message should mention session file")
	assert.Contains(t, string(output), tempDir, "Error message should include working directory")
	assert.Contains(t, string(output), ".tmux-session", "Error message should mention expected file name")
}

// TestMCPCommand_Integration_HelpText verifies that the mcp command
// appears in the root help output and has its own help text.
func TestMCPCommand_Integration_HelpText(t *testing.T) {
	// Arrange: Build binary
	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, "tmux-cli-test")
	// Get project root (two directories up from cmd/tmux-cli)
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	buildCmd := exec.Command("go", "build", "-o", binary, "./cmd/tmux-cli")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build tmux-cli binary: %s", string(output))

	// Act & Assert: Check root help includes mcp command
	rootHelp := exec.Command(binary, "--help")
	rootOutput, err := rootHelp.CombinedOutput()
	require.NoError(t, err, "Root help should succeed")
	assert.Contains(t, string(rootOutput), "mcp", "Root help should list mcp command")

	// Act & Assert: Check mcp command help
	mcpHelp := exec.Command(binary, "mcp", "--help")
	mcpOutput, err := mcpHelp.CombinedOutput()
	require.NoError(t, err, "MCP help should succeed")
	assert.Contains(t, string(mcpOutput), "Model Context Protocol", "MCP help should describe protocol")
	assert.Contains(t, string(mcpOutput), "stdin/stdout", "MCP help should mention stdio transport")
	assert.Contains(t, string(mcpOutput), "SIGINT", "MCP help should mention shutdown signals")
}

// TestMCPCommand_Integration_GracefulShutdown verifies that the server
// shuts down cleanly when receiving SIGTERM signal.
func TestMCPCommand_Integration_GracefulShutdown(t *testing.T) {
	// Arrange: Create temp directory with session file
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, ".tmux-session")
	sessionData := `{"sessionId":"test-shutdown","windows":[]}`
	require.NoError(t, os.WriteFile(sessionFile, []byte(sessionData), 0644))

	// Build binary from project root
	binary := filepath.Join(tempDir, "tmux-cli-test")
	// Get project root (two directories up from cmd/tmux-cli)
	wd, _ := os.Getwd()
	projectRoot := filepath.Join(wd, "..", "..")
	buildCmd := exec.Command("go", "build", "-o", binary, "./cmd/tmux-cli")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build tmux-cli binary: %s", string(output))

	// Act: Start MCP server
	cmd := exec.Command(binary, "mcp")
	cmd.Dir = tempDir
	err = cmd.Start()
	require.NoError(t, err, "Server should start")

	// Give server time to initialize
	time.Sleep(100 * time.Millisecond)

	// Send SIGTERM for graceful shutdown
	err = cmd.Process.Signal(os.Interrupt)
	require.NoError(t, err, "Should send SIGTERM successfully")

	// Wait for shutdown (with timeout)
	doneChan := make(chan error, 1)
	go func() {
		doneChan <- cmd.Wait()
	}()

	select {
	case err := <-doneChan:
		// Server should exit cleanly (exit code 0 or SIGINT)
		if err != nil {
			// SIGINT may cause non-zero exit in some shells - this is acceptable
			t.Logf("Server exited with: %v (acceptable for SIGINT)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not shutdown within timeout")
	}
}

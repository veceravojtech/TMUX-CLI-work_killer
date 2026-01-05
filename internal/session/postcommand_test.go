package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockExecutorWithFeedback is a test mock for tmux.TmuxExecutor
type MockExecutorWithFeedback struct {
	SendMessageWithFeedbackFunc func(sessionID, windowID, message string) (string, error)
}

func (m *MockExecutorWithFeedback) SendMessageWithFeedback(sessionID, windowID, message string) (string, error) {
	if m.SendMessageWithFeedbackFunc != nil {
		return m.SendMessageWithFeedbackFunc(sessionID, windowID, message)
	}
	return "", nil
}

// Implement other required interface methods (stubs)
func (m *MockExecutorWithFeedback) SendMessage(sessionID, windowID, message string) error {
	return nil
}

func (m *MockExecutorWithFeedback) CaptureWindowOutput(sessionID, windowID string) (string, error) {
	return "", nil
}

func (m *MockExecutorWithFeedback) CreateSession(sessionID, workingDir string) error {
	return nil
}

func (m *MockExecutorWithFeedback) KillSession(sessionID string) error {
	return nil
}

func (m *MockExecutorWithFeedback) SessionExists(sessionID string) (bool, error) {
	return false, nil
}

func (m *MockExecutorWithFeedback) WindowExists(sessionID, windowID string) (bool, error) {
	return false, nil
}

func (m *MockExecutorWithFeedback) CreateWindow(sessionID, name, command string) (string, error) {
	return "@0", nil
}

func (m *MockExecutorWithFeedback) KillWindow(sessionID, windowID string) error {
	return nil
}

func (m *MockExecutorWithFeedback) RenameWindow(sessionID, windowID, newName string) error {
	return nil
}

func (m *MockExecutorWithFeedback) ListWindows(sessionID string) ([]tmux.WindowInfo, error) {
	return nil, nil
}

func (m *MockExecutorWithFeedback) SetWindowOption(sessionID, windowID, optionName, value string) error {
	return nil
}

func (m *MockExecutorWithFeedback) GetWindowOption(sessionID, windowID, optionName string) (string, error) {
	return "", nil
}

func (m *MockExecutorWithFeedback) SendMessageWithDelay(sessionID, windowID, message string) error {
	return nil
}

func (m *MockExecutorWithFeedback) HasSession(id string) (bool, error) {
	return false, nil
}

func (m *MockExecutorWithFeedback) ListSessions() ([]string, error) {
	return nil, nil
}

// setupTestLogDir creates a temporary test directory and changes to it
// Returns cleanup function
func setupTestLogDir(t *testing.T) func() {
	t.Helper()

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "tmux-cli-test-*")
	require.NoError(t, err)

	// Save original working directory
	origDir, err := os.Getwd()
	require.NoError(t, err)

	// Change to temp directory
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Return cleanup function
	return func() {
		os.Chdir(origDir)
		os.RemoveAll(tempDir)
	}
}

// TestLogPostCommand_FileCreation tests that log file and directory are created
func TestLogPostCommand_FileCreation(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Call logPostCommand
	logPostCommand("test-session", "@1", 1, 3, "test command", "test output", "Testing", nil)

	// Verify log directory was created
	_, err := os.Stat(".tmux-cli/logs")
	assert.NoError(t, err, "Log directory should be created")

	// Verify log file was created
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	_, err = os.Stat(logFile)
	assert.NoError(t, err, "Log file should be created")
}

// TestLogPostCommand_FileContent tests that log entries contain expected fields
func TestLogPostCommand_FileContent(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Call logPostCommand with specific data
	testSessionID := "test-session-123"
	testWindowID := "@5"
	testCommand := "claude --test"
	testOutput := "Test output from command"
	testDecision := "SUCCESS"

	logPostCommand(testSessionID, testWindowID, 2, 3, testCommand, testOutput, testDecision, nil)

	// Read log file
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	logContent := string(content)

	// Verify all expected fields are present
	assert.Contains(t, logContent, testWindowID, "Log should contain window ID")
	assert.Contains(t, logContent, testSessionID, "Log should contain session ID")
	assert.Contains(t, logContent, "Cmd=2/3", "Log should contain command index")
	assert.Contains(t, logContent, testDecision, "Log should contain decision")
	assert.Contains(t, logContent, testCommand, "Log should contain command")
	assert.Contains(t, logContent, testOutput, "Log should contain output")
}

// TestLogPostCommand_OutputTruncation tests that long output is truncated
func TestLogPostCommand_OutputTruncation(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Create output longer than 200 characters
	longOutput := strings.Repeat("x", 250)

	logPostCommand("test-session", "@1", 1, 1, "test", longOutput, "Testing", nil)

	// Read log file
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	logContent := string(content)

	// Verify output was truncated with ellipsis
	assert.Contains(t, logContent, "...", "Long output should be truncated with ellipsis")
	assert.NotContains(t, logContent, strings.Repeat("x", 250), "Full long output should not be present")
}

// TestLogPostCommand_ErrorLogging tests that errors are included in logs
func TestLogPostCommand_ErrorLogging(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	testError := fmt.Errorf("test error message")

	logPostCommand("test-session", "@1", 1, 1, "test", "", "Failed", testError)

	// Read log file
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	logContent := string(content)

	// Verify error is logged
	assert.Contains(t, logContent, "test error message", "Error message should be in log")
	assert.Contains(t, logContent, "Error:", "Error label should be present")
}

// TestLogPostCommand_NonFatalErrors tests that logging errors don't cause panics
func TestLogPostCommand_NonFatalErrors(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Make log directory read-only to trigger write error
	os.MkdirAll(".tmux-cli/logs", 0755)
	os.Chmod(".tmux-cli/logs", 0444)
	defer os.Chmod(".tmux-cli/logs", 0755) // Cleanup

	// This should not panic even though write will fail
	assert.NotPanics(t, func() {
		logPostCommand("test-session", "@1", 1, 1, "test", "output", "Testing", nil)
	}, "Logging errors should be silently suppressed")
}

// TestExecutePostCommandWithFallback_LoggingIntegration tests that ExecutePostCommandWithFallback logs correctly
func TestExecutePostCommandWithFallback_LoggingIntegration(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Create mock executor that simulates fallback scenario
	callCount := 0
	mockExecutor := &MockExecutorWithFeedback{
		SendMessageWithFeedbackFunc: func(sessionID, windowID, message string) (string, error) {
			callCount++
			if callCount == 1 {
				// First command fails with "already in use" error
				return "Error: Session ID is already in use", nil
			}
			if callCount == 2 {
				// Second command fails with "No conversation found" error
				return "Error: No conversation found", nil
			}
			// Third command succeeds
			return "Claude Code started successfully", nil
		},
	}

	// Create config with 3 fallback commands
	config := &store.PostCommandConfig{
		Enabled: true,
		Commands: []string{
			"claude --session-id=\"test\"",
			"claude --resume \"test\"",
			"claude",
		},
		ErrorPatterns: []string{
			"already in use",
			"No conversation found",
		},
	}

	// Execute with fallback
	err := ExecutePostCommandWithFallback(mockExecutor, "test-session", "@1", config)
	assert.NoError(t, err, "Should succeed on third fallback")

	// Read log file
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	logContent := string(content)

	// Verify log contains entries for all commands
	assert.Contains(t, logContent, "Starting PostCommand fallback chain", "Should log chain start")
	assert.Contains(t, logContent, "Cmd=1/3", "Should log first command")
	assert.Contains(t, logContent, "Cmd=2/3", "Should log second command")
	assert.Contains(t, logContent, "Cmd=3/3", "Should log third command")
	assert.Contains(t, logContent, "SUCCESS", "Should log success")
	assert.Contains(t, logContent, "PostCommand chain completed successfully", "Should log completion")

	// Verify pattern matching is logged
	assert.Contains(t, logContent, "Pattern \"already in use\"", "Should log pattern check")
	assert.Contains(t, logContent, "Pattern \"No conversation found\"", "Should log pattern check")
}

// TestExecutePostCommandWithFallback_AllFailures tests logging when all fallbacks fail
func TestExecutePostCommandWithFallback_AllFailures(t *testing.T) {
	cleanup := setupTestLogDir(t)
	defer cleanup()

	// Create mock executor that fails all commands
	mockExecutor := &MockExecutorWithFeedback{
		SendMessageWithFeedbackFunc: func(sessionID, windowID, message string) (string, error) {
			return "Error: Command failed", nil
		},
	}

	config := &store.PostCommandConfig{
		Enabled: true,
		Commands: []string{
			"claude --session-id=\"test\"",
			"claude --resume \"test\"",
		},
		ErrorPatterns: []string{
			"Command failed",
			"Command failed",
		},
	}

	// Execute with fallback - should fail
	err := ExecutePostCommandWithFallback(mockExecutor, "test-session", "@1", config)
	assert.Error(t, err, "Should fail when all fallbacks fail")

	// Read log file
	logFile := filepath.Join(".tmux-cli/logs", "postcommand.log")
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	logContent := string(content)

	// Verify failure is logged
	assert.Contains(t, logContent, "All fallbacks exhausted → FAILURE", "Should log final failure")
}

// TestContainsPattern tests the pattern matching function
func TestContainsPattern(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		pattern  string
		expected bool
	}{
		{
			name:     "exact match",
			errMsg:   "already in use",
			pattern:  "already in use",
			expected: true,
		},
		{
			name:     "substring match",
			errMsg:   "Error: Session ID is already in use by another process",
			pattern:  "already in use",
			expected: true,
		},
		{
			name:     "no match",
			errMsg:   "Some other error",
			pattern:  "already in use",
			expected: false,
		},
		{
			name:     "empty pattern",
			errMsg:   "Some error",
			pattern:  "",
			expected: false,
		},
		{
			name:     "empty error message",
			errMsg:   "",
			pattern:  "error",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsPattern(tt.errMsg, tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

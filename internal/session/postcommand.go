package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
)

// PostCommandConfig defines the configuration for commands to execute
// after window initialization with fallback support.
type PostCommandConfig struct {
	Enabled       bool     `json:"enabled"`
	Commands      []string `json:"commands,omitempty"`
	ErrorPatterns []string `json:"errorPatterns,omitempty"`
}

// DefaultPostCommandConfig returns the default post-command configuration
// with Claude CLI launch and fallback handling.
func DefaultPostCommandConfig() *PostCommandConfig {
	return &PostCommandConfig{
		Enabled: true,
		Commands: []string{
			`claude --dangerously-skip-permissions --session-id="$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions --resume "$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions`,
		},
		ErrorPatterns: []string{
			"already in use",
			"No conversation found",
		},
	}
}

// logMutex protects concurrent writes to postcommand.log
// Multiple windows may execute PostCommand chains simultaneously
var logMutex sync.Mutex

// ExecutePostCommandWithFallback executes post-command with fallback mechanism.
// This is a shared function used by both SessionManager and RecoveryManager.
// Returns nil if:
// - config is nil or disabled
// - any command in the fallback chain succeeds
// Returns error only if all fallback commands fail
func ExecutePostCommandWithFallback(executor tmux.TmuxExecutor, sessionID, windowID string, config *PostCommandConfig) error {
	// Skip if config is nil or disabled
	if config == nil || !config.Enabled {
		return nil
	}

	// Skip if no commands configured
	if len(config.Commands) == 0 {
		return nil
	}

	// Log start of fallback chain
	totalCmds := len(config.Commands)
	logPostCommand(sessionID, windowID, 0, totalCmds, "", "", "Starting PostCommand fallback chain", nil)

	// Try each command in sequence
	var lastErr error
	for i, cmd := range config.Commands {
		cmdIndex := i + 1 // 1-based for human readability

		// Log command attempt
		logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", "Attempting", nil)

		// Send command and capture output to detect errors
		output, err := executor.SendMessageWithFeedback(sessionID, windowID, cmd)

		// Log captured output
		logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, output, "Output captured", err)

		if err != nil {
			// Tmux command itself failed (session/window doesn't exist)
			lastErr = err
			logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", "Tmux command failed → trying next fallback", err)
			if i < len(config.Commands)-1 {
				continue // Try next fallback
			}
			break
		}

		// Check if command output contains error patterns
		hasError := false
		if i < len(config.ErrorPatterns) {
			pattern := config.ErrorPatterns[i]
			logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", fmt.Sprintf("Checking pattern \"%s\"", pattern), nil)

			if containsPattern(output, pattern) {
				// Output contains error pattern - command failed
				hasError = true
				lastErr = fmt.Errorf("command failed with error: %s", pattern)
				logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", fmt.Sprintf("Pattern \"%s\" → MATCH", pattern), nil)
			} else {
				logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", fmt.Sprintf("Pattern \"%s\" → no match", pattern), nil)
			}
		} else {
			// No error pattern to check (final fallback)
			logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", "No error pattern to check (final fallback)", nil)
		}

		if !hasError {
			// Command succeeded - no error pattern in output
			logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", "SUCCESS", nil)
			logPostCommand(sessionID, windowID, 0, totalCmds, "", "", "PostCommand chain completed successfully", nil)
			return nil
		}

		// Command failed - try next fallback if available
		logPostCommand(sessionID, windowID, cmdIndex, totalCmds, cmd, "", "Failed → trying next fallback", lastErr)
		if i < len(config.Commands)-1 {
			continue
		}
	}

	// All commands failed
	logPostCommand(sessionID, windowID, 0, totalCmds, "", "", "All fallbacks exhausted → FAILURE", lastErr)
	return fmt.Errorf("all post-command fallbacks failed: %w", lastErr)
}

// containsPattern checks if the error message contains the pattern
func containsPattern(errMsg, pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	return errMsg == pattern || strings.Contains(errMsg, pattern)
}

// logPostCommand writes PostCommand execution details to .tmux-cli/logs/postcommand.log
// This is a best-effort logging function - errors are silently suppressed to never block window creation
// Log location: .tmux-cli/logs/postcommand.log (relative to current working directory)
// Thread-safe: Protected by package-level logMutex for concurrent window creation
func logPostCommand(sessionID, windowID string, cmdIndex, totalCmds int, command, output, decision string, logErr error) {
	// Serialize log writes to prevent race conditions during concurrent window creation
	logMutex.Lock()
	defer logMutex.Unlock()

	// Get absolute path for log directory (security: prevent directory traversal)
	cwd, err := os.Getwd()
	if err != nil {
		return // Can't determine working directory - logging not possible
	}
	logDir := filepath.Join(cwd, ".tmux-cli", "logs")

	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return // Silently fail - logging errors should never block window creation
	}

	// Open log file in append mode
	logFile := filepath.Join(logDir, "postcommand.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return // Silently fail
	}
	defer f.Close()

	// Format timestamp
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// Truncate output to first 200 characters for readability
	truncatedOutput := output
	if len(truncatedOutput) > 200 {
		truncatedOutput = truncatedOutput[:200] + "..."
	}

	// Build log entry
	var logEntry strings.Builder
	logEntry.WriteString(fmt.Sprintf("[%s] ", timestamp))
	logEntry.WriteString(fmt.Sprintf("Window=%s SessionID=%s ", windowID, sessionID))
	logEntry.WriteString(fmt.Sprintf("Cmd=%d/%d: %s", cmdIndex, totalCmds, decision))

	if command != "" {
		logEntry.WriteString(fmt.Sprintf(" | Command: %s", command))
	}

	if truncatedOutput != "" {
		// Clean up output (remove newlines for single-line log)
		cleanOutput := strings.ReplaceAll(truncatedOutput, "\n", " ")
		logEntry.WriteString(fmt.Sprintf(" | Output: %s", cleanOutput))
	}

	if logErr != nil {
		logEntry.WriteString(fmt.Sprintf(" | Error: %v", logErr))
	}

	logEntry.WriteString("\n")

	// Write to file (ignore errors)
	f.WriteString(logEntry.String())
}

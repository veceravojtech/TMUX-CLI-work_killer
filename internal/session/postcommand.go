package session

import (
	"fmt"
	"strings"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// ExecutePostCommandWithFallback executes post-command with fallback mechanism.
// This is a shared function used by both SessionManager and RecoveryManager.
// Returns nil if:
// - config is nil or disabled
// - any command in the fallback chain succeeds
// Returns error only if all fallback commands fail
func ExecutePostCommandWithFallback(executor tmux.TmuxExecutor, sessionID, windowID string, config *store.PostCommandConfig) error {
	// Skip if config is nil or disabled
	if config == nil || !config.Enabled {
		return nil
	}

	// Skip if no commands configured
	if len(config.Commands) == 0 {
		return nil
	}

	// Try each command in sequence
	var lastErr error
	for i, cmd := range config.Commands {
		// Send command and capture output to detect errors
		output, err := executor.SendMessageWithFeedback(sessionID, windowID, cmd)
		if err != nil {
			// Tmux command itself failed (session/window doesn't exist)
			lastErr = err
			if i < len(config.Commands)-1 {
				continue // Try next fallback
			}
			break
		}

		// Check if command output contains error patterns
		hasError := false
		if i < len(config.ErrorPatterns) {
			pattern := config.ErrorPatterns[i]
			if containsPattern(output, pattern) {
				// Output contains error pattern - command failed
				hasError = true
				lastErr = fmt.Errorf("command failed with error: %s", pattern)
			}
		}

		if !hasError {
			// Command succeeded - no error pattern in output
			return nil
		}

		// Command failed - try next fallback if available
		if i < len(config.Commands)-1 {
			continue
		}
	}

	// All commands failed
	return fmt.Errorf("all post-command fallbacks failed: %w", lastErr)
}

// containsPattern checks if the error message contains the pattern
func containsPattern(errMsg, pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	return errMsg == pattern || strings.Contains(errMsg, pattern)
}

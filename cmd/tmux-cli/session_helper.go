package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
)

// GetSessionIDFromFile reads the session from .tmux-session in the current working directory.
// Returns descriptive errors for missing or invalid session files.
func GetSessionIDFromFile(fileStore *store.FileSessionStore) (string, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}

	// Load session from .tmux-session file
	sess, err := fileStore.Load(cwd)
	if err != nil {
		// Return user-friendly errors directly from store layer
		return "", err
	}

	// Validate UUID format
	if err := session.ValidateUUID(sess.SessionID); err != nil {
		return "", store.ErrInvalidSession
	}

	return sess.SessionID, nil
}

// ResolveWindowIdentifier resolves a window identifier (ID or name) to a window ID.
// If the identifier starts with "@", it's treated as a window ID and returned as-is.
// Otherwise, it's treated as a window name and resolved by searching session.Windows.
//
// This is the CLI equivalent of internal/mcp/tools.go:resolveWindowIdentifier.
//
// Parameters:
//   - sess: The session containing windows to search
//   - identifier: Either a window ID (starts with "@") or window name (exact match)
//
// Returns:
//   - string: The resolved window ID
//   - error: Descriptive error if name doesn't match any window (includes available names)
func ResolveWindowIdentifier(sess *store.Session, identifier string) (string, error) {
	// Validate identifier is non-empty
	if identifier == "" {
		return "", fmt.Errorf("window identifier cannot be empty")
	}

	// If identifier starts with "@", treat as window ID (backward compatible)
	if strings.HasPrefix(identifier, "@") {
		return identifier, nil
	}

	// Otherwise, treat as window name - search for exact case-sensitive match
	for i := range sess.Windows {
		if sess.Windows[i].Name == identifier {
			return sess.Windows[i].TmuxWindowID, nil
		}
	}

	// Name not found - build helpful error message with available window names
	availableNames := make([]string, len(sess.Windows))
	for i := range sess.Windows {
		availableNames[i] = sess.Windows[i].Name
	}
	return "", fmt.Errorf("window name %q not found (available: %v)", identifier, availableNames)
}

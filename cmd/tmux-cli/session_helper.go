package main

import (
	"fmt"
	"strings"

	"github.com/console/tmux-cli/internal/tmux"
)

// ResolveWindowIdentifier resolves a window identifier (ID or name) to a window ID.
// If the identifier starts with "@", it's treated as a window ID and returned as-is.
// Otherwise, it's treated as a window name and resolved by searching the window list.
func ResolveWindowIdentifier(windows []tmux.WindowInfo, identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("window identifier cannot be empty")
	}

	// If identifier starts with "@", treat as window ID
	if strings.HasPrefix(identifier, "@") {
		return identifier, nil
	}

	// Otherwise, treat as window name - search for exact case-sensitive match
	for i := range windows {
		if windows[i].Name == identifier {
			return windows[i].TmuxWindowID, nil
		}
	}

	// Name not found - build helpful error message
	availableNames := make([]string, len(windows))
	for i := range windows {
		availableNames[i] = windows[i].Name
	}
	return "", fmt.Errorf("window name %q not found (available: %v)", identifier, availableNames)
}

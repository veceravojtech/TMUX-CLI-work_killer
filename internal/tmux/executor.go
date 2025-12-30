// Package tmux provides tmux command execution functionality.
// It handles direct interaction with tmux binary for session and window management.
package tmux

// TmuxExecutor defines the interface for executing tmux commands.
// Implementations wrap os/exec to enable testing via mocking.
type TmuxExecutor interface {
	// CreateSession creates a new tmux session with the given ID and working directory
	CreateSession(id, path string) error

	// KillSession terminates a tmux session by ID
	KillSession(id string) error

	// HasSession checks if a session with the given ID exists
	HasSession(id string) (bool, error)

	// ListSessions returns all active tmux session IDs
	ListSessions() ([]string, error)

	// CreateWindow creates a new window in the specified session with name and command.
	// Returns the tmux-assigned window ID (e.g., "@0", "@1") or error.
	CreateWindow(sessionID, name, command string) (windowID string, err error)

	// ListWindows returns all windows in a session with their metadata
	ListWindows(sessionID string) ([]WindowInfo, error)

	// SendMessage sends a text message to a specific window in a session
	// The message is delivered to the first pane of the target window
	// An Enter key is automatically appended to the message
	// Implements FR35, FR37
	SendMessage(sessionID, windowID, message string) error

	// KillWindow kills a window in the specified session
	// Returns nil if window doesn't exist (idempotent)
	KillWindow(sessionID, windowID string) error
}

// WindowInfo contains metadata about a tmux window
type WindowInfo struct {
	TmuxWindowID string // @0, @1, @2...
	Name         string
	Running      bool
}

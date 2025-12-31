// Package store provides session state persistence functionality.
// It handles JSON-based storage of tmux session metadata with atomic file operations.
package store

// SessionStore defines the interface for persisting session state to disk.
// Implementations must provide atomic file operations to prevent data corruption.
type SessionStore interface {
	// Save persists session data to storage using atomic file operations
	// Writes full session JSON to {session.ProjectPath}/.tmux-session
	Save(session *Session) error

	// Load retrieves session data from project path
	// Reads full session JSON from {projectPath}/.tmux-session
	Load(projectPath string) (*Session, error)
}

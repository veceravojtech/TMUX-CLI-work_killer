// Package store provides session state persistence functionality.
// It handles JSON-based storage of tmux session metadata with atomic file operations.
package store

// SessionStore defines the interface for persisting session state to disk.
// Implementations must provide atomic file operations to prevent data corruption.
type SessionStore interface {
	// Save persists session data to storage using atomic file operations
	Save(session *Session) error

	// Load retrieves session data from storage by session ID
	Load(id string) (*Session, error)

	// Delete removes session data from storage by session ID
	Delete(id string) error

	// List returns all stored sessions
	List() ([]*Session, error)

	// Move relocates a session file to a destination directory (e.g., "ended" for archival)
	Move(id string, destination string) error
}

package session

import (
	"fmt"
	"os"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// SessionManager orchestrates session operations across tmux and storage layers
type SessionManager struct {
	executor tmux.TmuxExecutor
	store    store.SessionStore
}

// NewSessionManager creates a new SessionManager with the given dependencies
func NewSessionManager(executor tmux.TmuxExecutor, store store.SessionStore) *SessionManager {
	return &SessionManager{
		executor: executor,
		store:    store,
	}
}

// CreateSession creates a new tmux session and persists it to storage
// Returns error if:
// - path does not exist
// - session already exists in tmux
// - tmux command fails
// - storage operation fails
func (m *SessionManager) CreateSession(id, path string) error {
	// 1. Validate path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("path does not exist: %s", path)
	}

	// 2. Check if session already exists in tmux
	exists, err := m.executor.HasSession(id)
	if err != nil {
		return fmt.Errorf("check session exists: %w", err)
	}
	if exists {
		return tmux.ErrSessionAlreadyExists
	}

	// 3. Create tmux session
	if err := m.executor.CreateSession(id, path); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	// 4. Create session object
	session := &store.Session{
		SessionID:   id,
		ProjectPath: path,
		Windows:     []store.Window{}, // Empty initially
	}

	// 5. Save to store
	if err := m.store.Save(session); err != nil {
		// Cleanup: kill the tmux session if store fails
		// This prevents orphaned tmux sessions
		_ = m.executor.KillSession(id) // Best effort cleanup
		return fmt.Errorf("save session to store: %w", err)
	}

	return nil
}

// KillSession kills a tmux session while preserving the session file for recovery
// This is an idempotent operation - killing an already-dead session is not an error
// Returns error if:
// - session ID is invalid
// - session file doesn't exist in store
func (m *SessionManager) KillSession(id string) error {
	// 1. Validate UUID format
	if err := ValidateUUID(id); err != nil {
		return err
	}

	// 2. Load session to verify it exists in store
	_, err := m.store.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 3. Kill tmux session (ignore error if already dead - idempotent)
	// The session file remains in active directory for recovery
	_ = m.executor.KillSession(id)

	return nil
}

// EndSession kills a tmux session and archives its file to ended/ directory
// This signals permanent completion - session will NOT be recovered
// Returns error if:
// - session ID is invalid
// - session file doesn't exist in store
// - file move operation fails
func (m *SessionManager) EndSession(id string) error {
	// 1. Validate UUID format
	if err := ValidateUUID(id); err != nil {
		return err
	}

	// 2. Load session to verify it exists
	_, err := m.store.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 3. Kill tmux session if running (ignore error if already dead)
	_ = m.executor.KillSession(id)

	// 4. Move session file to ended/ directory
	if err := m.store.Move(id, "ended"); err != nil {
		return fmt.Errorf("move session to ended: %w", err)
	}

	return nil
}

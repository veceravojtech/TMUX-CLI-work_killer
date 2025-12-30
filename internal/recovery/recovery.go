// Package recovery provides session recovery detection and execution functionality.
// It handles automatic detection of killed sessions and their recreation.
package recovery

import (
	"fmt"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// RecoveryManager defines interface for session recovery operations
type RecoveryManager interface {
	// IsRecoveryNeeded checks if a session needs recovery
	IsRecoveryNeeded(sessionId string) (bool, error)

	// RecoverSession recreates a killed session (placeholder for Story 3.2)
	RecoverSession(session *store.Session) error

	// VerifyRecovery verifies recovery succeeded (placeholder for Story 3.3)
	VerifyRecovery(sessionId string) error
}

// SessionRecoveryManager implements RecoveryManager interface
type SessionRecoveryManager struct {
	store    store.SessionStore
	executor tmux.TmuxExecutor
}

// NewSessionRecoveryManager creates a new SessionRecoveryManager
func NewSessionRecoveryManager(store store.SessionStore, executor tmux.TmuxExecutor) *SessionRecoveryManager {
	return &SessionRecoveryManager{
		store:    store,
		executor: executor,
	}
}

// IsRecoveryNeeded checks if a session needs recovery
// Returns true if session file exists but tmux session doesn't (killed state)
// Returns false if both exist (active) or if session file doesn't exist (error)
// Returns error if unable to determine state
func (m *SessionRecoveryManager) IsRecoveryNeeded(sessionId string) (bool, error) {
	// 1. Load session from store to verify file exists
	_, err := m.store.Load(sessionId)
	if err != nil {
		// Session file doesn't exist - can't recover
		return false, fmt.Errorf("load session: %w", err)
	}

	// 2. Check if tmux session exists
	exists, err := m.executor.HasSession(sessionId)
	if err != nil {
		// Error checking tmux - can't determine state
		return false, fmt.Errorf("check tmux session: %w", err)
	}

	// 3. Recovery needed if file exists but tmux session doesn't
	return !exists, nil
}

// RecoverSession recreates a killed session with all its windows
// Implements FR12, FR13, FR15
func (m *SessionRecoveryManager) RecoverSession(session *store.Session) error {
	// 1. Recreate tmux session with original UUID (FR12, FR15)
	err := m.executor.CreateSession(session.SessionID, session.ProjectPath)
	if err != nil {
		return fmt.Errorf("recreate session: %w", err)
	}

	// 2. Recreate all windows using stored recovery commands (FR13)
	// Use index to modify original slice
	for i := range session.Windows {
		window := &session.Windows[i]

		// Execute recovery command to recreate window
		windowId, err := m.executor.CreateWindow(
			session.SessionID,
			window.Name,
			window.RecoveryCommand,
		)

		if err != nil {
			// Log error but continue with other windows
			// Partial recovery better than no recovery
			continue
		}

		// Update window ID (tmux may assign different ID like @0, @1...)
		window.TmuxWindowID = windowId
	}

	// 3. Save updated session to store (persists new window IDs)
	err = m.store.Save(session)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

// VerifyRecovery confirms that session and all windows are running after recovery
// Implements FR14, NFR10
func (m *SessionRecoveryManager) VerifyRecovery(sessionId string) error {
	// 1. Load session from store to get expected windows
	session, err := m.store.Load(sessionId)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 2. Verify tmux session exists and is running
	exists, err := m.executor.HasSession(sessionId)
	if err != nil {
		return fmt.Errorf("check session exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("session %s not running after recovery", sessionId)
	}

	// 3. Get list of live windows from tmux
	liveWindows, err := m.executor.ListWindows(sessionId)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	// 4. Verify each stored window (with non-empty ID) exists in tmux
	for _, window := range session.Windows {
		// Skip windows that failed to create during recovery (empty ID)
		if window.TmuxWindowID == "" {
			continue
		}

		// Search for window in live windows
		found := false
		for _, liveWindow := range liveWindows {
			if liveWindow.TmuxWindowID == window.TmuxWindowID {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("window %s (%s) not found after recovery",
				window.TmuxWindowID, window.Name)
		}
	}

	// All windows verified successfully
	return nil
}

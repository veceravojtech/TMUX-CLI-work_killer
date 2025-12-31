// Package recovery provides session recovery detection and execution functionality.
// It handles automatic detection of killed sessions and their recreation.
package recovery

import (
	"fmt"
	"time"

	sessionpkg "github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// RecoveryManager defines interface for session recovery operations
type RecoveryManager interface {
	// IsRecoveryNeeded checks if a session needs recovery
	// Takes session object directly to avoid API mismatch with store.Load
	IsRecoveryNeeded(session *store.Session) (bool, error)

	// RecoverSession recreates a killed session (placeholder for Story 3.2)
	RecoverSession(session *store.Session) error

	// VerifyRecovery verifies recovery succeeded (placeholder for Story 3.3)
	// Takes session object directly to avoid API mismatch with store.Load
	VerifyRecovery(session *store.Session) error
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

// isShellCommand returns true if the command is a common shell (zsh, bash, sh, fish, etc.)
// or empty (default shell). These are the typical commands running in a supervisor window.
func isShellCommand(cmd string) bool {
	if cmd == "" {
		return true // Empty = default shell
	}
	// Common shells - expand as needed
	shells := []string{"zsh", "bash", "sh", "fish", "ksh", "tcsh", "csh", "dash"}
	for _, shell := range shells {
		if cmd == shell {
			return true
		}
	}
	return false
}

// IsRecoveryNeeded checks if a session needs recovery
// Returns true if session object provided but tmux session doesn't exist (killed state)
// Returns false if tmux session exists (active)
// Returns error if unable to determine state
func (m *SessionRecoveryManager) IsRecoveryNeeded(session *store.Session) (bool, error) {
	if session == nil {
		return false, fmt.Errorf("session is required")
	}

	// 1. Check if tmux session exists
	exists, err := m.executor.HasSession(session.SessionID)
	if err != nil {
		// Error checking tmux - can't determine state
		return false, fmt.Errorf("check tmux session: %w", err)
	}

	// 2. Recovery needed if tmux session doesn't exist
	return !exists, nil
}

// RecoverSession recreates a killed session with all its windows
// Implements FR12, FR13, FR15
func (m *SessionRecoveryManager) RecoverSession(session *store.Session) error {
	// 1. Recreate tmux session with original UUID (FR12, FR15)
	// NOTE: CreateSession automatically creates a "supervisor" window
	err := m.executor.CreateSession(session.SessionID, session.ProjectPath)
	if err != nil {
		return fmt.Errorf("recreate session: %w", err)
	}

	// 2. Get the auto-created supervisor window ID
	// CreateSession creates a window named "supervisor", but we need to find its actual ID
	liveWindows, err := m.executor.ListWindows(session.SessionID)
	if err != nil {
		return fmt.Errorf("list windows after session creation: %w", err)
	}

	// Find the supervisor window created by CreateSession
	var supervisorWindowID string
	for _, w := range liveWindows {
		if w.Name == "supervisor" {
			supervisorWindowID = w.TmuxWindowID
			break
		}
	}

	// 3. Recreate all windows using zsh as default shell (FR13)
	// IMPORTANT: CreateSession already created a "supervisor" window
	// If first window is supervisor, reuse it instead of duplicating
	startIndex := 0
	if len(session.Windows) > 0 {
		firstWindow := &session.Windows[0]
		if firstWindow.Name == "supervisor" && supervisorWindowID != "" {
			// Reuse the auto-created supervisor window
			firstWindow.TmuxWindowID = supervisorWindowID

			// Restore UUID for supervisor window if it exists
			err = m.restoreWindowUUID(session, firstWindow, supervisorWindowID)
			if err != nil {
				return fmt.Errorf("restore supervisor window UUID: %w", err)
			}

			startIndex = 1 // Skip this window in the loop
		}
	}

	// Use index to modify original slice
	for i := startIndex; i < len(session.Windows); i++ {
		window := &session.Windows[i]

		// Execute recovery command to recreate window (always uses zsh)
		windowId, err := m.executor.CreateWindow(
			session.SessionID,
			window.Name,
			"zsh", // Always use zsh as default shell
		)

		if err != nil {
			// Log error but continue with other windows
			// Partial recovery better than no recovery
			continue
		}

		// Update window ID (tmux may assign different ID like @0, @1...)
		window.TmuxWindowID = windowId

		// Restore UUID to tmux user-option if it exists
		err = m.restoreWindowUUID(session, window, windowId)
		if err != nil {
			return fmt.Errorf("restore window %s UUID: %w", windowId, err)
		}
	}

	// 4. Update LastRecoveryAt timestamp
	session.LastRecoveryAt = time.Now().Format(time.RFC3339)

	// 5. Save updated session to store (persists new window IDs and timestamp)
	err = m.store.Save(session)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

// restoreWindowUUID restores a window's UUID to tmux and exports it in the shell.
// Also executes post-command if configured.
// Returns error if UUID validation or restoration fails.
func (m *SessionRecoveryManager) restoreWindowUUID(session *store.Session, window *store.Window, windowID string) error {
	if window.UUID == "" {
		return nil // No UUID to restore
	}

	// Validate UUID before using in commands (security)
	if err := sessionpkg.ValidateUUID(window.UUID); err != nil {
		return fmt.Errorf("invalid UUID: %w", err)
	}

	// Set UUID in tmux user-option
	err := m.executor.SetWindowOption(session.SessionID, windowID, tmux.WindowUUIDOption, window.UUID)
	if err != nil {
		return fmt.Errorf("set window option: %w", err)
	}

	// Export UUID in the running shell
	// Note: Assumes POSIX-compatible shell (zsh/bash/sh). Non-POSIX shells (fish/csh) not supported.
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", window.UUID)
	err = m.executor.SendMessage(session.SessionID, windowID, exportCmd)
	if err != nil {
		return fmt.Errorf("export TMUX_WINDOW_UUID: %w", err)
	}

	// Execute post-command for this window if configured
	postCmdConfig := session.GetEffectivePostCommand(window)
	err = sessionpkg.ExecutePostCommandWithFallback(m.executor, session.SessionID, windowID, postCmdConfig)
	// Post-command failure is not fatal during recovery - window is still usable
	// Error is intentionally ignored to allow partial recovery
	_ = err

	return nil
}

// VerifyRecovery confirms that session and all windows are running after recovery
// Implements FR14, NFR10
// Takes session object directly to avoid API mismatch with store.Load
func (m *SessionRecoveryManager) VerifyRecovery(session *store.Session) error {
	if session == nil {
		return fmt.Errorf("session is required")
	}

	// 1. Verify tmux session exists and is running
	exists, err := m.executor.HasSession(session.SessionID)
	if err != nil {
		return fmt.Errorf("check session exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("session %s not running after recovery", session.SessionID)
	}

	// 2. Get list of live windows from tmux
	liveWindows, err := m.executor.ListWindows(session.SessionID)
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

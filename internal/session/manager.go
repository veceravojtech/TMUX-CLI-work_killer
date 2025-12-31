package session

import (
	"fmt"
	"os"
	"time"

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

	// 4. List windows in the newly created session to capture default window
	// tmux automatically creates window 0 when creating a session
	windowList, err := m.executor.ListWindows(id)
	if err != nil {
		// Cleanup: kill the tmux session if listing windows fails
		_ = m.executor.KillSession(id)
		return fmt.Errorf("list windows in new session: %w", err)
	}

	// 5. Convert tmux window list to session windows
	windows := make([]store.Window, 0, len(windowList))
	for _, w := range windowList {
		windows = append(windows, store.Window{
			TmuxWindowID: w.TmuxWindowID,
			Name:         w.Name,
		})
	}

	// 5.5. Set UUID for supervisor window
	// The supervisor window is always the first window (index 0) named "supervisor"
	// Its UUID is set to the session ID for predictable identification
	if len(windows) > 0 && windows[0].Name == "supervisor" {
		supervisorUUID := id // Use session ID as supervisor UUID

		// Store UUID in tmux user-option for runtime access
		err = m.executor.SetWindowOption(id, windows[0].TmuxWindowID, tmux.WindowUUIDOption, supervisorUUID)
		if err != nil {
			// Cleanup: kill the tmux session if UUID setup fails
			_ = m.executor.KillSession(id)
			return fmt.Errorf("set supervisor window UUID: %w", err)
		}

		// Only set UUID in struct if tmux option succeeded
		windows[0].UUID = supervisorUUID

		// Export the UUID in the running shell
		// The init command tried to export it when the shell started, but the UUID wasn't set yet
		// Send keys to re-export it now that the UUID option is set
		exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", supervisorUUID)
		err = m.executor.SendMessage(id, windows[0].TmuxWindowID, exportCmd)
		if err != nil {
			// Cleanup: kill the tmux session if UUID export fails
			_ = m.executor.KillSession(id)
			return fmt.Errorf("export TMUX_WINDOW_UUID in shell: %w", err)
		}

		// Execute post-command for supervisor window if configured
		// Note: Using default config for now - will be configurable via session file
		postCmdConfig := store.DefaultPostCommandConfig()
		err = ExecutePostCommandWithFallback(m.executor, id, windows[0].TmuxWindowID, postCmdConfig)
		if err != nil {
			// Post-command failure is not fatal - log and continue
			// The window is created and UUID is exported, so session is usable
			// TODO: Add logging when logging infrastructure is available
			_ = err // Suppress unused variable warning
		}
	}

	// 6. Create session object with captured windows and timestamp
	session := &store.Session{
		SessionID:   id,
		ProjectPath: path,
		CreatedAt:   time.Now().Format(time.RFC3339),
		// LastRecoveryAt is empty for new sessions
		PostCommand: store.DefaultPostCommandConfig(),
		Windows:     windows,
	}

	// 7. Save to store (writes full JSON to .tmux-session file)
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
// The .tmux-session file is NEVER deleted to enable recovery
// IMPORTANT: This validates that the session file is in sync with tmux state
// If out of sync, returns an error - no automatic fallback/capture
// Returns error if:
// - session ID is invalid
// - session file doesn't exist in project path
// - session file is out of sync with tmux (different window count)
func (m *SessionManager) KillSession(projectPath string) error {
	if projectPath == "" {
		return fmt.Errorf("kill session: project path is required")
	}

	// 1. Load session to verify it exists and get session ID
	session, err := m.store.Load(projectPath)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 2. Validate UUID format
	if err := ValidateUUID(session.SessionID); err != nil {
		return err
	}

	// 3. Check if session is still running in tmux
	exists, err := m.executor.HasSession(session.SessionID)
	if err != nil {
		return fmt.Errorf("check if session exists: %w", err)
	}

	// 4. If session is running, validate that file is in sync with tmux
	if exists {
		// Get current window list from tmux
		windowList, err := m.executor.ListWindows(session.SessionID)
		if err != nil {
			return fmt.Errorf("list windows: %w", err)
		}

		// Verify window count matches
		if len(windowList) != len(session.Windows) {
			return fmt.Errorf("session file out of sync: tmux has %d windows but file has %d - please sync the session file before killing",
				len(windowList), len(session.Windows))
		}
	}

	// 5. Kill tmux session (ignore error if already dead - idempotent)
	// The .tmux-session file remains for recovery
	_ = m.executor.KillSession(session.SessionID)

	return nil
}

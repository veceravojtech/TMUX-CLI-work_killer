package session

import (
	"fmt"
	"os"

	"github.com/console/tmux-cli/internal/tmux"
)

// SessionManager orchestrates session operations across tmux
type SessionManager struct {
	executor tmux.TmuxExecutor
}

// NewSessionManager creates a new SessionManager with the given dependencies
func NewSessionManager(executor tmux.TmuxExecutor) *SessionManager {
	return &SessionManager{
		executor: executor,
	}
}

// CreateSession creates a new tmux session and stores project path in tmux environment
// Returns error if:
// - path does not exist
// - session already exists in tmux
// - tmux command fails
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

	// 4. Store project path in tmux session environment
	if err := m.executor.SetSessionEnvironment(id, "TMUX_CLI_PROJECT_PATH", path); err != nil {
		_ = m.executor.KillSession(id)
		return fmt.Errorf("set session environment: %w", err)
	}

	// 5. List windows in the newly created session to capture default window
	windowList, err := m.executor.ListWindows(id)
	if err != nil {
		_ = m.executor.KillSession(id)
		return fmt.Errorf("list windows in new session: %w", err)
	}

	// 6. Set UUID for supervisor window and run PostCommand
	if len(windowList) > 0 && windowList[0].Name == "supervisor" {
		supervisorUUID := GenerateUUID()

		// Store UUID in tmux user-option
		err = m.executor.SetWindowOption(id, windowList[0].TmuxWindowID, tmux.WindowUUIDOption, supervisorUUID)
		if err != nil {
			_ = m.executor.KillSession(id)
			return fmt.Errorf("set supervisor window UUID: %w", err)
		}

		// Export the UUID in the running shell
		exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", supervisorUUID)
		err = m.executor.SendMessage(id, windowList[0].TmuxWindowID, exportCmd)
		if err != nil {
			_ = m.executor.KillSession(id)
			return fmt.Errorf("export TMUX_WINDOW_UUID in shell: %w", err)
		}

		// Execute post-command for supervisor window
		postCmdConfig := DefaultPostCommandConfig()
		err = ExecutePostCommandWithFallback(m.executor, id, windowList[0].TmuxWindowID, postCmdConfig)
		if err != nil {
			// Post-command failure is not fatal
			_ = err
		}
	}

	return nil
}

// KillSession kills a tmux session by ID
// This is an idempotent operation - killing an already-dead session is not an error
func (m *SessionManager) KillSession(id string) error {
	if id == "" {
		return fmt.Errorf("kill session: session ID is required")
	}

	_ = m.executor.KillSession(id)
	return nil
}

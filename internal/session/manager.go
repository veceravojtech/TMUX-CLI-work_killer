package session

import (
	"fmt"
	"os"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
)

// SessionManager orchestrates session operations across tmux
type SessionManager struct {
	executor             tmux.TmuxExecutor
	sessionReadyAttempts int
	sessionReadyInterval time.Duration
	// model, when non-empty, is recorded in the new session's environment as
	// TMUX_CLI_MODEL and injected as --model into each window's claude launch (the
	// supervisor window directly, later windows/workers by reading TMUX_CLI_MODEL
	// back) so every window — the supervisor window, taskvisor, and all
	// MCP-spawned workers — launches Claude with that model.
	model string
}

// NewSessionManager creates a new SessionManager with the given dependencies
func NewSessionManager(executor tmux.TmuxExecutor) *SessionManager {
	return &SessionManager{
		executor:             executor,
		sessionReadyAttempts: 10,
		sessionReadyInterval: 50 * time.Millisecond,
	}
}

// WithModel returns the manager configured to record TMUX_CLI_MODEL=<model> in
// the session environment and inject --model '<model>' into claude launches at
// CreateSession time. An empty model is a no-op (the session inherits Claude's
// default model). Returns the receiver for chaining.
func (m *SessionManager) WithModel(model string) *SessionManager {
	m.model = model
	return m
}

// waitForSession polls HasSession until the newly created session is reachable.
// Handles the race where the tmux server socket isn't ready immediately after
// a fresh server start via new-session -d.
func (m *SessionManager) waitForSession(id string) error {
	for i := 0; i < m.sessionReadyAttempts; i++ {
		if exists, _ := m.executor.HasSession(id); exists {
			return nil
		}
		time.Sleep(m.sessionReadyInterval)
	}
	return fmt.Errorf("session %s not reachable after creation", id)
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

	// 3.5. Wait for tmux server to accept connections (fresh server race condition)
	if err := m.waitForSession(id); err != nil {
		_ = m.executor.KillSession(id)
		return err
	}

	// 4. Store project path in tmux session environment
	if err := m.executor.SetSessionEnvironment(id, "TMUX_CLI_PROJECT_PATH", path); err != nil {
		_ = m.executor.KillSession(id)
		return fmt.Errorf("set session environment: %w", err)
	}

	// 4.5. Record the chosen Claude model in the session environment as
	// TMUX_CLI_MODEL so the SEPARATE processes that spawn later windows (the MCP
	// server's windows-spawn-worker, the daemon recovery path, `windows-create`)
	// can retrieve it and inject the same --model into their launch commands.
	// Window 0 below gets the model directly from m.model. Best-effort: a model is
	// an optional override, so a set failure must not tear down a good session.
	if m.model != "" {
		_ = m.executor.SetSessionEnvironment(id, "TMUX_CLI_MODEL", m.model)
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

		// Execute post-command for supervisor window — inject --model when set.
		postCmdConfig := PostCommandConfigWithModel(m.model)
		err = ExecutePostCommandWithFallback(m.executor, id, windowList[0].TmuxWindowID, postCmdConfig)
		if err != nil {
			// Post-command failure is not fatal
			_ = err
		}
	}

	return nil
}

// EnsureTaskvisorWindow creates or restarts the taskvisor window idempotently.
// If absent: creates window, sets UUID, sends daemon command.
// If present but idle (CurrentCommand=="zsh"): re-sends daemon command.
// If present and running: no-op.
func (m *SessionManager) EnsureTaskvisorWindow(sessionID string) error {
	windows, err := m.executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows for taskvisor: %w", err)
	}

	for _, w := range windows {
		if w.Name == "taskvisor" {
			if w.CurrentCommand != "zsh" {
				return nil
			}
			return m.executor.SendMessage(sessionID, w.TmuxWindowID, "tmux-cli taskvisor --run")
		}
	}

	windowID, err := m.executor.CreateWindow(sessionID, "taskvisor", "")
	if err != nil {
		return fmt.Errorf("create taskvisor window: %w", err)
	}

	uuid := GenerateUUID()
	if err := m.executor.SetWindowOption(sessionID, windowID, tmux.WindowUUIDOption, uuid); err != nil {
		return fmt.Errorf("set taskvisor window UUID: %w", err)
	}

	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", uuid)
	if err := m.executor.SendMessage(sessionID, windowID, exportCmd); err != nil {
		return fmt.Errorf("export TMUX_WINDOW_UUID in taskvisor: %w", err)
	}

	return m.executor.SendMessage(sessionID, windowID, "tmux-cli taskvisor --run")
}

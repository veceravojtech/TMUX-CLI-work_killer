package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/tmux"
)

// WindowListItem represents a simplified window entry with only the name.
type WindowListItem struct {
	Name string `json:"name"`
}

// WindowsList returns all windows in the current tmux session.
func (s *Server) WindowsList() ([]WindowListItem, error) {
	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	result := make([]WindowListItem, len(windows))
	for i, w := range windows {
		result[i] = WindowListItem{Name: w.Name}
	}

	return result, nil
}

// resolveWindowIdentifier resolves a window identifier (ID or name) to a window ID.
// If the identifier starts with "@", it's treated as a window ID and returned as-is.
// Otherwise, it's treated as a window name and resolved by searching the window list.
func resolveWindowIdentifier(windows []tmux.WindowInfo, identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("%w: identifier cannot be empty", ErrInvalidWindowID)
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
	return "", fmt.Errorf("%w: window name %q not found (available: %v)",
		ErrWindowNotFound, identifier, availableNames)
}

// WindowsSend sends a text command to a specific window for execution.
func (s *Server) WindowsSend(windowIdentifier, command string) (bool, error) {
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}
	if command == "" {
		return false, fmt.Errorf("%w: command cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	windowID, err := resolveWindowIdentifier(windows, windowIdentifier)
	if err != nil {
		return false, err
	}

	// Verify window exists
	var windowExists bool
	for i := range windows {
		if windows[i].TmuxWindowID == windowID {
			windowExists = true
			break
		}
	}
	if !windowExists {
		return false, fmt.Errorf("%w: windowID=%s session=%s",
			ErrWindowNotFound, windowID, sessionID)
	}

	err = s.executor.SendMessageWithDelay(sessionID, windowID, command)
	if err != nil {
		return false, fmt.Errorf("%w: session=%s window=%s command=%q: %w",
			ErrTmuxCommandFailed, sessionID, windowID, command, err)
	}

	return true, nil
}

// WindowsMessage sends a formatted message to another window with auto-detected sender.
func (s *Server) WindowsMessage(receiver, message string) (bool, string, error) {
	if receiver == "" {
		return false, "", fmt.Errorf("%w: receiver cannot be empty", ErrInvalidInput)
	}
	if message == "" {
		return false, "", fmt.Errorf("%w: message cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, "", err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, "", fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Detect sender window by UUID from environment variable
	senderUUID := os.Getenv("TMUX_WINDOW_UUID")
	sender := sessionID // Default to session ID

	if senderUUID != "" {
		for i := range windows {
			uuid, err := s.executor.GetWindowOption(sessionID, windows[i].TmuxWindowID, tmux.WindowUUIDOption)
			if err == nil && uuid == senderUUID {
				sender = windows[i].Name
				break
			}
		}
	}

	// Resolve receiver window
	receiverWindowID, err := resolveWindowIdentifier(windows, receiver)
	if err != nil {
		return false, "", err
	}

	// Build formatted message
	formattedMessage := fmt.Sprintf("New message from: %s\n\n%s\n",
		sender, message)

	err = s.executor.SendMessageWithDelay(sessionID, receiverWindowID, formattedMessage)
	if err != nil {
		return false, "", fmt.Errorf("%w: session=%s window=%s: %w",
			ErrTmuxCommandFailed, sessionID, receiverWindowID, err)
	}

	return true, sender, nil
}

// WindowsCreate creates a new window in the current tmux session.
func (s *Server) WindowsCreate(name, command string) (*WindowInfo, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Validate window name uniqueness (case-insensitive)
	for _, w := range windows {
		if strings.EqualFold(w.Name, name) {
			return nil, fmt.Errorf("%w: window name %q already exists (case-insensitive match with %q)",
				ErrWindowCreateFailed, name, w.Name)
		}
	}

	// Generate UUID
	windowUUID := session.GenerateUUID()

	// Create window with "zsh" shell
	windowID, err := s.executor.CreateWindow(sessionID, name, "zsh")
	if err != nil {
		return nil, fmt.Errorf("%w: session=%s name=%q: %w",
			ErrWindowCreateFailed, sessionID, name, err)
	}

	// Set window UUID
	err = s.executor.SetWindowOption(sessionID, windowID, tmux.WindowUUIDOption, windowUUID)
	if err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: set window UUID: %w", ErrTmuxCommandFailed, err)
	}

	// Export TMUX_WINDOW_UUID in the running shell
	if err := session.ValidateUUID(windowUUID); err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: invalid window UUID: %w", ErrTmuxCommandFailed, err)
	}
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
	err = s.executor.SendMessage(sessionID, windowID, exportCmd)
	if err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: export TMUX_WINDOW_UUID in shell: %w", ErrTmuxCommandFailed, err)
	}

	// Execute postcommand if configured - NON-FATAL
	postCmdConfig := session.DefaultPostCommandConfig()
	err = session.ExecutePostCommandWithFallback(s.executor, sessionID, windowID, postCmdConfig)
	if err != nil {
		_ = err // Post-command failure is not fatal
	}

	return &WindowInfo{
		TmuxWindowID: windowID,
		Name:         name,
		UUID:         windowUUID,
	}, nil
}

// WindowsKill terminates a specific window in the current tmux session by name.
func (s *Server) WindowsKill(windowIdentifier string) (bool, error) {
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}

	// STRICT: Reject window IDs (@ prefix) - names only
	if strings.HasPrefix(windowIdentifier, "@") {
		return false, fmt.Errorf("%w: window IDs not allowed (use window name instead of %q)",
			ErrInvalidWindowID, windowIdentifier)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, fmt.Errorf("%w: tmux session not running: %w",
			ErrTmuxCommandFailed, err)
	}

	// Resolve window name to ID
	windowID, err := resolveWindowIdentifier(windows, windowIdentifier)
	if err != nil {
		return false, err
	}

	// Verify window exists in tmux
	var windowExistsInTmux bool
	for i := range windows {
		if windows[i].TmuxWindowID == windowID {
			windowExistsInTmux = true
			break
		}
	}
	if !windowExistsInTmux {
		return false, fmt.Errorf("%w: window %q not found in tmux session",
			ErrWindowNotFound, windowIdentifier)
	}

	// Prevent killing last window
	if len(windows) <= 1 {
		return false, fmt.Errorf("%w: cannot kill last window in session (would terminate session)",
			ErrWindowKillFailed)
	}

	err = s.executor.KillWindow(sessionID, windowID)
	if err != nil {
		return false, fmt.Errorf("%w: session=%s window=%q (ID=%s): %w",
			ErrTmuxCommandFailed, sessionID, windowIdentifier, windowID, err)
	}

	return true, nil
}

package tmux

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RealTmuxExecutor is the production implementation that executes actual tmux commands
type RealTmuxExecutor struct{}

// NewTmuxExecutor creates a new RealTmuxExecutor instance
func NewTmuxExecutor() *RealTmuxExecutor {
	return &RealTmuxExecutor{}
}

// CreateSession creates a new detached tmux session with the given ID and working directory
// Command: tmux new-session -d -s <id> -c <path> -n supervisor <init-command>
// -d: detached mode (don't attach immediately)
// -s: session name (our UUID)
// -c: working directory (project path)
// -n: name for the first window (supervisor)
// The supervisor window exports TMUX_WINDOW_UUID then exec's into an interactive shell
func (e *RealTmuxExecutor) CreateSession(id, path string) error {
	// Create a command that exports TMUX_WINDOW_UUID and exec's into an interactive shell
	// Using exec ensures the shell inherits the exported environment variable
	// The command: zsh -c 'export TMUX_WINDOW_UUID="$(tmux show-options -wv @window-uuid 2>/dev/null || echo "")"; exec zsh'
	initCommand := `zsh -c 'export TMUX_WINDOW_UUID="$(tmux show-options -wv @` + WindowUUIDOption + ` 2>/dev/null || echo "")"; exec zsh'`

	cmd := exec.Command("tmux", "new-session", "-d", "-s", id, "-c", path, "-n", "supervisor", initCommand)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux new-session failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// KillSession terminates a tmux session by ID
// Command: tmux kill-session -t <id>
// This operation is idempotent - returns nil if session doesn't exist
func (e *RealTmuxExecutor) KillSession(id string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", id)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		// Exit code 1 with "session not found" message means session doesn't exist
		// This is NOT an error for kill - session might already be dead
		// Make kill idempotent
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil // Session already dead - that's fine
		}
		return fmt.Errorf("tmux kill-session failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// HasSession checks if a session with the given ID exists
// Command: tmux has-session -t <id>
// Exit code 0: session exists
// Exit code 1: session doesn't exist
func (e *RealTmuxExecutor) HasSession(id string) (bool, error) {
	cmd := exec.Command("tmux", "has-session", "-t", id)
	err := cmd.Run()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return false, ErrTmuxNotFound
		}
		// Exit code 1 means session doesn't exist (not an error for us)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("tmux has-session failed: %w", err)
	}
	return true, nil
}

// ListSessions returns all active tmux session IDs
// Command: tmux list-sessions -F "#{session_name}"
func (e *RealTmuxExecutor) ListSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return nil, ErrTmuxNotFound
		}
		// Exit code 1 with no output means no sessions exist (not an error)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && len(output) == 0 {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Parse output - one session per line
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	return lines, nil
}

// CreateWindow creates a new window in the specified session with name and command.
// Returns the tmux-assigned window ID (e.g., "@0", "@1") or error.
// Command: tmux new-window -t <sessionID> -n <name> -P -F '#{window_id}' <command>
// -P: Print information about the new window
// -F '#{window_id}': Format output to only show window ID
func (e *RealTmuxExecutor) CreateWindow(sessionID, name, command string) (string, error) {
	args := []string{
		"new-window",
		"-t", sessionID,
		"-n", name,
		"-P",
		"-F", "#{window_id}",
	}

	// Append command as the final argument if provided
	// Wrap command in interactive shell to ensure window persistence
	if command != "" {
		wrappedCommand := WrapCommandForPersistence(command)
		args = append(args, wrappedCommand)
	}

	cmd := exec.Command("tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return "", ErrTmuxNotFound
		}
		return "", fmt.Errorf("tmux new-window failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Parse window ID (format: "@0\n" or "@1\n")
	windowID := strings.TrimSpace(string(output))

	// Validate format: must start with @ followed by digit(s)
	if !strings.HasPrefix(windowID, "@") {
		return "", fmt.Errorf("invalid window ID format: %s", windowID)
	}

	return windowID, nil
}

// ListWindows returns all windows in a session with their metadata.
// Command: tmux list-windows -t <sessionID> -F "#{window_id}|#{window_name}|#{pane_pid}|#{pane_current_command}"
// The pane_pid field is used to determine if the window is running (pid > 0)
// The pane_current_command field captures the currently running command for recovery
func (e *RealTmuxExecutor) ListWindows(sessionID string) ([]WindowInfo, error) {
	// Format: window_id|window_name|pane_pid|pane_current_command (e.g., "@0|editor|12345|vim")
	cmd := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}|#{window_name}|#{pane_pid}|#{pane_current_command}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return nil, ErrTmuxNotFound
		}
		return nil, fmt.Errorf("tmux list-windows failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Parse output - one window per line
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []WindowInfo{}, nil
	}

	windows := make([]WindowInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue // Skip malformed lines
		}

		windows = append(windows, WindowInfo{
			TmuxWindowID:   parts[0],
			Name:           parts[1],
			Running:        parts[2] != "", // If pane_pid is not empty, window is running
			CurrentCommand: parts[3],       // Command running in the pane
		})
	}

	return windows, nil
}

// SendMessage delivers a text message to a specific window in a session.
// The message is delivered to the first pane (.0) of the target window.
// An Enter key is automatically appended to execute the message.
// Command: tmux send-keys -t <sessionID>:<windowID> -l "<message>" && tmux send-keys -t <sessionID>:<windowID> Enter
// Uses -l flag for literal text to properly work with interactive applications like Claude Code CLI
// Implements FR35, FR37, NFR29
func (e *RealTmuxExecutor) SendMessage(sessionID, windowID, message string) error {
	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	// Step 1: Send the message text with -l (literal) flag
	// This ensures special characters are not interpreted
	cmd1 := exec.Command("tmux", "send-keys", "-t", target, "-l", message)
	output1, err := cmd1.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd1.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux send-keys (text) failed (target: %s): %w: %s",
			target, err, strings.TrimSpace(string(output1)))
	}

	// Step 2: Send Enter key separately
	cmd2 := exec.Command("tmux", "send-keys", "-t", target, "Enter")
	output2, err := cmd2.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd2.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux send-keys (Enter) failed (target: %s): %w: %s",
			target, err, strings.TrimSpace(string(output2)))
	}

	return nil
}

// SendMessageWithDelay delivers a text message to a specific window with a 1-second delay before pressing Enter.
// This is specifically designed for windows-message MCP action where formatted multi-line messages
// need time to be fully delivered before execution.
// The delay ensures that long formatted messages don't get truncated when sent character-by-character.
// Command: tmux send-keys -t <sessionID>:<windowID> -l "<message>" && sleep 1 && tmux send-keys -t <sessionID>:<windowID> Enter
func (e *RealTmuxExecutor) SendMessageWithDelay(sessionID, windowID, message string) error {
	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	// Step 1: Send the message text with -l (literal) flag
	cmd1 := exec.Command("tmux", "send-keys", "-t", target, "-l", message)
	output1, err := cmd1.CombinedOutput()
	if err != nil {
		if cmd1.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux send-keys (text) failed (target: %s): %w: %s",
			target, err, strings.TrimSpace(string(output1)))
	}

	// Step 2: Wait 1 second for complete message delivery
	// This ensures formatted multi-line messages in windows-message are fully visible
	time.Sleep(1 * time.Second)

	// Step 3: Send Enter key to execute the message
	cmd2 := exec.Command("tmux", "send-keys", "-t", target, "Enter")
	output2, err := cmd2.CombinedOutput()
	if err != nil {
		if cmd2.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux send-keys (Enter) failed (target: %s): %w: %s",
			target, err, strings.TrimSpace(string(output2)))
	}

	return nil
}

// KillWindow terminates a window in a session by ID
// Command: tmux kill-window -t <sessionID>:<windowID>
// This operation is idempotent - returns nil if window doesn't exist
func (e *RealTmuxExecutor) KillWindow(sessionID, windowID string) error {
	// Defensive validation: window ID must start with @ and be followed by digits
	if len(windowID) < 2 || windowID[0] != '@' {
		return fmt.Errorf("invalid window ID format: %s (must be @N)", windowID)
	}

	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	cmd := exec.Command("tmux", "kill-window", "-t", target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		// Exit code 1 with specific error messages means window/session doesn't exist
		// Only treat as idempotent if the error is actually about missing target
		// This prevents masking real errors (permissions, syntax, etc.)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			outputStr := strings.TrimSpace(string(output))
			// Idempotent cases: window not found or session not found
			if strings.Contains(outputStr, "can't find window") ||
				strings.Contains(outputStr, "can't find session") ||
				strings.Contains(outputStr, "no such window") {
				return nil // Window/session doesn't exist - that's fine
			}
			// Other exit code 1 errors are real errors
			return fmt.Errorf("tmux kill-window failed: %s: %w", outputStr, err)
		}
		return fmt.Errorf("tmux kill-window failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// SetWindowOption sets a user-defined window option (@option-name)
// Command: tmux set-option -w -t <sessionID>:<windowID> @<optionName> <value>
// -w: window-scoped option
// Returns error if session or window doesn't exist
func (e *RealTmuxExecutor) SetWindowOption(sessionID, windowID, optionName, value string) error {
	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	// User-defined options must be prefixed with @
	optionKey := "@" + optionName

	cmd := exec.Command("tmux", "set-option", "-w", "-t", target, optionKey, value)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux set-option failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// GetWindowOption retrieves a user-defined window option value
// Command: tmux show-options -wv -t <sessionID>:<windowID> @<optionName>
// -w: window-scoped option
// -v: show only value (not option name)
// Returns error if option is not set or window/session doesn't exist
func (e *RealTmuxExecutor) GetWindowOption(sessionID, windowID, optionName string) (string, error) {
	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	// User-defined options must be prefixed with @
	optionKey := "@" + optionName

	cmd := exec.Command("tmux", "show-options", "-wv", "-t", target, optionKey)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return "", ErrTmuxNotFound
		}
		// Exit code 1 means option not set or window/session doesn't exist
		return "", fmt.Errorf("tmux show-options failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Return the option value (trimmed)
	return strings.TrimSpace(string(output)), nil
}

// CaptureWindowOutput captures the current pane content from a window
// Command: tmux capture-pane -t <sessionID>:<windowID> -p -S -
// -p: print to stdout
// -S -: start from history beginning (captures entire scrollback)
// Returns the captured text output from the pane
func (e *RealTmuxExecutor) CaptureWindowOutput(sessionID, windowID string) (string, error) {
	// Build target: session:window format (e.g., "uuid:@0")
	target := sessionID + ":" + windowID

	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if tmux not found
		if cmd.Err == exec.ErrNotFound {
			return "", ErrTmuxNotFound
		}
		// Exit code 1 means session/window doesn't exist
		return "", fmt.Errorf("tmux capture-pane failed (target: %s): %w: %s",
			target, err, strings.TrimSpace(string(output)))
	}

	// Return the captured output (preserve whitespace, just return as-is)
	return string(output), nil
}

// SendMessageWithFeedback sends a message and waits to capture the output
// Returns the captured output after a 2-second delay to allow command execution
// This enables detection of command execution errors in post-command fallback logic
func (e *RealTmuxExecutor) SendMessageWithFeedback(sessionID, windowID, message string) (string, error) {
	// Step 1: Send the message
	err := e.SendMessage(sessionID, windowID, message)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}

	// Step 2: Wait for command to execute (increased from 1s to 2s for better error capture)
	time.Sleep(2 * time.Second)

	// Step 3: Capture the pane output to check for errors
	output, err := e.CaptureWindowOutput(sessionID, windowID)
	if err != nil {
		return "", fmt.Errorf("capture output: %w", err)
	}

	return output, nil
}

// SetSessionEnvironment sets an environment variable on a tmux session
// Command: tmux set-environment -t <sessionID> <key> <value>
func (e *RealTmuxExecutor) SetSessionEnvironment(sessionID, key, value string) error {
	cmd := exec.Command("tmux", "set-environment", "-t", sessionID, key, value)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		return fmt.Errorf("tmux set-environment failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// GetSessionEnvironment reads an environment variable from a tmux session
// Command: tmux show-environment -t <sessionID> <key>
// Output format: KEY=VALUE
func (e *RealTmuxExecutor) GetSessionEnvironment(sessionID, key string) (string, error) {
	cmd := exec.Command("tmux", "show-environment", "-t", sessionID, key)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if cmd.Err == exec.ErrNotFound {
			return "", ErrTmuxNotFound
		}
		return "", fmt.Errorf("tmux show-environment failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Parse KEY=VALUE format
	line := strings.TrimSpace(string(output))
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected show-environment output: %s", line)
	}
	return parts[1], nil
}

// FindSessionByEnvironment finds a session where key=value in its environment
// Iterates all sessions and checks each one's environment
func (e *RealTmuxExecutor) FindSessionByEnvironment(key, value string) (string, error) {
	sessions, err := e.ListSessions()
	if err != nil {
		return "", err
	}

	for _, sessionID := range sessions {
		envVal, err := e.GetSessionEnvironment(sessionID, key)
		if err != nil {
			continue // Key not set on this session
		}
		if envVal == value {
			return sessionID, nil
		}
	}

	return "", nil
}

// AttachSession attaches the current terminal to an existing tmux session
// Command: tmux attach-session -t <id>
// This method blocks until the user detaches from the tmux session.
// It forwards stdin/stdout/stderr to allow interactive terminal access.
func (e *RealTmuxExecutor) AttachSession(id string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", id)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout

	// Capture stderr in a buffer so we can include it in error messages,
	// while still forwarding it to the user's terminal via MultiWriter
	var stderrBuf strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	err := cmd.Run()
	if err != nil {
		if cmd.Err == exec.ErrNotFound {
			return ErrTmuxNotFound
		}
		stderrMsg := strings.TrimSpace(stderrBuf.String())
		if stderrMsg != "" {
			return fmt.Errorf("tmux attach-session failed: %s: %w", stderrMsg, err)
		}
		return fmt.Errorf("tmux attach-session failed: %w", err)
	}
	return nil
}

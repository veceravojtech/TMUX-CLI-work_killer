// Package tmux provides tmux command execution functionality.
// It handles direct interaction with tmux binary for session and window management.
package tmux

// TmuxExecutor defines the interface for executing tmux commands.
// Implementations wrap os/exec to enable testing via mocking.
type TmuxExecutor interface {
	// CreateSession creates a new tmux session with the given ID and working directory
	CreateSession(id, path string) error

	// KillSession terminates a tmux session by ID
	KillSession(id string) error

	// HasSession checks if a session with the given ID exists
	HasSession(id string) (bool, error)

	// ListSessions returns all active tmux session IDs
	ListSessions() ([]string, error)

	// CreateWindow creates a new window in the specified session with name and command.
	// Returns the tmux-assigned window ID (e.g., "@0", "@1") or error.
	CreateWindow(sessionID, name, command string) (windowID string, err error)

	// ListWindows returns all windows in a session with their metadata
	ListWindows(sessionID string) ([]WindowInfo, error)

	// SendEnter sends a bare Enter keystroke to a window (no text payload)
	SendEnter(sessionID, windowID string) error

	// NotifyPane delivers a text message followed by a SEPARATE Enter keystroke
	// directly to a tmux pane by its pane id (e.g. "%3"). Unlike SendMessage it
	// targets the pane id directly (pane ids are session-global), performing no
	// session:window join — used by the orchestrator reply channel.
	// An empty message delivers a bare Enter (a valid heartbeat ping).
	NotifyPane(paneID, message string) error

	// SendMessage sends a text message to a specific window in a session
	// The message is delivered to the first pane of the target window
	// An Enter key is automatically appended to the message
	// Implements FR35, FR37
	SendMessage(sessionID, windowID, message string) error

	// SendMessageWithDelay sends a text message with a 1-second delay before pressing Enter
	// Used specifically for windows-message MCP action where formatted multi-line messages
	// need time to be fully delivered before execution
	SendMessageWithDelay(sessionID, windowID, message string) error

	// KillWindow kills a window in the specified session
	// Returns nil if window doesn't exist (idempotent)
	KillWindow(sessionID, windowID string) error

	// InterruptWindow sends C-c to the window's active pane to interrupt the
	// running process WITHOUT destroying the window (unlike KillWindow, which
	// discards window options such as WindowUUIDOption). Takes only the tmux
	// window ID (e.g. "@3") — window IDs are server-unique, so no sessionID
	// join is needed. A missing window is a genuine failure and returns an
	// error (no idempotent nil-swallow).
	InterruptWindow(windowID string) error

	// TerminateWindowProcess deterministically terminates the pane's
	// foreground child process and waits for the pane to return to a shell
	// prompt, WITHOUT destroying the window (unlike KillWindow, which discards
	// window options such as WindowUUIDOption). Where InterruptWindow sends a
	// single C-c — an interrupt a process may catch and ignore (Claude Code
	// does) — this sends SIGTERM then, if needed, SIGKILL to the pane's
	// foreground child (pgrep -P #{pane_pid}), so the running program is
	// guaranteed gone before the caller relaunches. The pane shell
	// (#{pane_pid}) itself is NEVER killed. Takes only the tmux window ID
	// (e.g. "@3") — window IDs are server-unique. Returns nil if the pane is
	// already at an idle shell (no foreground child); returns an error if the
	// window/pane is unreadable or the child survives SIGKILL.
	TerminateWindowProcess(windowID string) error

	// SetWindowOption sets a user-defined window option (@option-name)
	// Window user-options are prefixed with @ and scoped to the specific window
	// Returns error if session or window doesn't exist
	SetWindowOption(sessionID, windowID, optionName, value string) error

	// GetWindowOption retrieves a user-defined window option value
	// Returns error if option is not set or window/session doesn't exist
	GetWindowOption(sessionID, windowID, optionName string) (string, error)

	// CaptureWindowOutput captures the current pane content from a window
	// Returns the captured text output from the pane
	// Useful for checking command execution results and error messages
	CaptureWindowOutput(sessionID, windowID string) (string, error)

	// SendMessageWithFeedback sends a message and waits to capture the output
	// Returns the captured output after a 1-second delay
	// Useful for detecting command execution errors in the pane
	SendMessageWithFeedback(sessionID, windowID, message string) (string, error)

	// SetSessionEnvironment sets an environment variable on a tmux session
	SetSessionEnvironment(sessionID, key, value string) error

	// GetSessionEnvironment reads an environment variable from a tmux session
	GetSessionEnvironment(sessionID, key string) (string, error)

	// FindSessionByEnvironment finds a session where key=value in its environment
	// Returns sessionID or empty string if not found
	FindSessionByEnvironment(key, value string) (string, error)

	// AttachSession attaches the current terminal to an existing tmux session
	AttachSession(id string) error

	// PipePane starts streaming pane output to a log file in append mode
	PipePane(sessionID, windowID, logPath string) error

	// PipePaneCommand starts streaming pane output through an arbitrary shell
	// command (e.g. a tee into the transcript capture process). tmux allows one
	// pipe per pane; -o semantics keep an existing pipe in place.
	PipePaneCommand(sessionID, windowID, command string) error

	// ClosePipePane closes any active pipe-pane on the window (idempotent)
	ClosePipePane(sessionID, windowID string) error
}

// WindowInfo contains metadata about a tmux window
type WindowInfo struct {
	TmuxWindowID   string // @0, @1, @2...
	Name           string
	Running        bool
	CurrentCommand string // The command currently running in the pane (from #{pane_current_command})
}

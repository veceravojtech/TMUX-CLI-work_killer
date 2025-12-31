package tmux

// Window option constants for tmux user-options.
// These are used with tmux set-option and show-options commands.
const (
	// WindowUUIDOption is the tmux user-option key for window UUIDs.
	// Set with: tmux set-option -w @window-uuid <uuid>
	// Read with: tmux show-options -wv @window-uuid
	WindowUUIDOption = "window-uuid"
)

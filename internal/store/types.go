// Package store provides session state persistence functionality.
package store

// PostCommandConfig defines the configuration for commands to execute
// after window initialization with fallback support.
//
// Example JSON in .tmux-session file:
//
//	{
//	  "enabled": true,
//	  "commands": [
//	    "claude --session-id=\"$TMUX_WINDOW_UUID\"",
//	    "claude --resume \"$TMUX_WINDOW_UUID\"",
//	    "claude"
//	  ],
//	  "errorPatterns": [
//	    "already in use",
//	    "No conversation found"
//	  ]
//	}
//
// The commands are tried in order. If a command fails with an error matching
// the corresponding errorPattern, the next command is tried. If a command
// succeeds or fails with an unexpected error, execution stops.
type PostCommandConfig struct {
	Enabled       bool     `json:"enabled"`
	Commands      []string `json:"commands,omitempty"`
	ErrorPatterns []string `json:"errorPatterns,omitempty"`
}

// DefaultPostCommandConfig returns the default post-command configuration
// with Claude CLI launch and fallback handling.
func DefaultPostCommandConfig() *PostCommandConfig {
	return &PostCommandConfig{
		Enabled: true,
		Commands: []string{
			`claude --dangerously-skip-permissions --session-id="$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions --resume "$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions`,
		},
		ErrorPatterns: []string{
			"already in use",
			"No conversation found",
		},
	}
}

// Session represents a tmux session with its associated metadata.
// The JSON tags use camelCase as specified in the PRD (FR18).
type Session struct {
	SessionID      string             `json:"sessionId"`
	ProjectPath    string             `json:"projectPath"`
	CreatedAt      string             `json:"createdAt,omitempty"`      // RFC3339 format
	LastRecoveryAt string             `json:"lastRecoveryAt,omitempty"` // RFC3339 format
	PostCommand    *PostCommandConfig `json:"postCommand,omitempty"`
	Windows        []Window           `json:"windows"`
}

// GetEffectivePostCommand returns the post-command configuration for a window.
// Window-level config takes precedence over session-level config.
// Returns nil if no config exists at either level or if config is invalid.
func (s *Session) GetEffectivePostCommand(w *Window) *PostCommandConfig {
	// Window override takes precedence
	if w.PostCommand != nil {
		// Validate window-level override before using
		if w.PostCommand.Enabled && len(w.PostCommand.Commands) == 0 {
			// Invalid config: enabled but no commands
			return nil
		}
		return w.PostCommand
	}
	// Fall back to session-level config
	if s.PostCommand != nil && s.PostCommand.Enabled && len(s.PostCommand.Commands) == 0 {
		// Invalid config: enabled but no commands
		return nil
	}
	return s.PostCommand
}

// Window represents a single tmux window within a session.
// The JSON tags use camelCase as specified in the PRD (FR18).
type Window struct {
	TmuxWindowID string             `json:"tmuxWindowId"`
	Name         string             `json:"name"`
	UUID         string             `json:"uuid,omitempty"`        // Persistent window identifier
	PostCommand  *PostCommandConfig `json:"postCommand,omitempty"` // Optional per-window override
	// RecoveryCommand removed - always defaults to zsh
}

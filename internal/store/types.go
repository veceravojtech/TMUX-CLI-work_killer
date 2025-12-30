// Package store provides session state persistence functionality.
package store

// Session represents a tmux session with its associated metadata.
// The JSON tags use camelCase as specified in the PRD (FR18).
type Session struct {
	SessionID   string   `json:"sessionId"`
	ProjectPath string   `json:"projectPath"`
	Windows     []Window `json:"windows"`
}

// Window represents a single tmux window within a session.
// The JSON tags use camelCase as specified in the PRD (FR18).
type Window struct {
	TmuxWindowID    string `json:"tmuxWindowId"`
	Name            string `json:"name"`
	RecoveryCommand string `json:"recoveryCommand"`
}

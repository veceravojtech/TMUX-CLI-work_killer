package tmux

import "errors"

var (
	// ErrTmuxNotFound is returned when tmux is not installed or not in PATH
	ErrTmuxNotFound = errors.New("tmux not found")

	// ErrSessionAlreadyExists is returned when trying to create existing session
	ErrSessionAlreadyExists = errors.New("session already exists")
)

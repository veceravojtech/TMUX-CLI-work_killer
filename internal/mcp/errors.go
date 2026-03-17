// Package mcp implements the Model Context Protocol server for tmux-cli.
//
// Error Handling:
// This package defines categorized error types to support AI interpretation:
// - Session errors (session not found)
// - Window errors (window not found, creation failed, kill failed)
// - Tmux errors (tmux not running, command execution failed)
// - Validation errors (invalid session/window IDs, invalid input)
// - Filesystem errors (working directory issues)
//
// All errors use Go 1.13+ error wrapping with %w format for proper error chains.
package mcp

import "errors"

// Session Errors
var (
	// ErrSessionNotFound indicates no tmux-cli session was found for this directory
	ErrSessionNotFound = errors.New("no tmux-cli session found for this directory")
)

// Window Errors
var (
	// ErrWindowNotFound indicates the requested window does not exist
	ErrWindowNotFound = errors.New("window not found")

	// ErrWindowCreateFailed indicates window creation failed
	ErrWindowCreateFailed = errors.New("window creation failed")

	// ErrWindowKillFailed indicates window termination failed
	ErrWindowKillFailed = errors.New("window kill failed")
)

// Tmux Errors
var (
	// ErrTmuxNotRunning indicates the tmux session is not currently running
	ErrTmuxNotRunning = errors.New("tmux session not running")

	// ErrTmuxCommandFailed indicates tmux command execution failed
	ErrTmuxCommandFailed = errors.New("tmux command execution failed")
)

// Validation Errors
var (
	// ErrInvalidSessionID indicates the session ID format is invalid
	ErrInvalidSessionID = errors.New("invalid session ID format")

	// ErrInvalidWindowID indicates the window ID format is invalid
	ErrInvalidWindowID = errors.New("invalid window ID format")

	// ErrInvalidInput indicates an input parameter is invalid
	ErrInvalidInput = errors.New("invalid input parameter")
)

// Filesystem Errors
var (
	// ErrWorkingDirNotFound indicates the working directory is not accessible
	ErrWorkingDirNotFound = errors.New("working directory not accessible")
)

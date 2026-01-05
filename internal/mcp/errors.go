// Package mcp implements the Model Context Protocol server for tmux-cli.
//
// The MCP server provides five window management operations that enable
// AI-assisted tmux control: windows-list, windows-create, windows-kill,
// windows-send, and windows-message.
//
// Error Handling:
// This package defines 10 categorized error types to support AI interpretation:
// - Session errors (session file not found, auto-detection failed)
// - Window errors (window not found, creation failed)
// - Tmux errors (tmux not running, command execution failed)
// - Validation errors (invalid session/window IDs)
// - Filesystem errors (working directory issues, corrupted session files)
//
// All errors use Go 1.13+ error wrapping with %w format for proper error chains.
package mcp

import "errors"

// Session Errors
var (
	// ErrSessionNotFound indicates the session file was not detected in the working directory
	ErrSessionNotFound = errors.New("session file not detected")

	// ErrSessionNotDetected indicates automatic session detection failed
	ErrSessionNotDetected = errors.New("session auto-detection failed")
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

	// ErrSessionFileCorrupt indicates the session file is corrupted
	ErrSessionFileCorrupt = errors.New("session file corrupted")
)

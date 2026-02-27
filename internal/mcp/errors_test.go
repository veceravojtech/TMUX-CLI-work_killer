package mcp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErrorTypes_Defined verifies all 12 error types exist with correct messages
func TestErrorTypes_Defined(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectedMsg string
	}{
		// Session Errors
		{"ErrSessionNotFound", ErrSessionNotFound, "session file not detected"},
		{"ErrSessionNotDetected", ErrSessionNotDetected, "session auto-detection failed"},

		// Window Errors
		{"ErrWindowNotFound", ErrWindowNotFound, "window not found"},
		{"ErrWindowCreateFailed", ErrWindowCreateFailed, "window creation failed"},
		{"ErrWindowKillFailed", ErrWindowKillFailed, "window kill failed"},

		// Tmux Errors
		{"ErrTmuxNotRunning", ErrTmuxNotRunning, "tmux session not running"},
		{"ErrTmuxCommandFailed", ErrTmuxCommandFailed, "tmux command execution failed"},

		// Validation Errors
		{"ErrInvalidSessionID", ErrInvalidSessionID, "invalid session ID format"},
		{"ErrInvalidWindowID", ErrInvalidWindowID, "invalid window ID format"},
		{"ErrInvalidInput", ErrInvalidInput, "invalid input parameter"},

		// Filesystem Errors
		{"ErrWorkingDirNotFound", ErrWorkingDirNotFound, "working directory not accessible"},
		{"ErrSessionFileCorrupt", ErrSessionFileCorrupt, "session file corrupted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err, "%s should not be nil", tt.name)
			assert.Equal(t, tt.expectedMsg, tt.err.Error(), "%s should have correct message", tt.name)
		})
	}
}

// TestErrorWrapping_Session tests error wrapping for session-related errors
func TestErrorWrapping_Session(t *testing.T) {
	t.Run("ErrSessionNotFound with directory context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w in directory /test/path", ErrSessionNotFound)

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrSessionNotFound)
		assert.Contains(t, wrappedErr.Error(), "session file not detected")
		assert.Contains(t, wrappedErr.Error(), "/test/path")
	})

	t.Run("ErrSessionNotDetected with session ID context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: session=%s", ErrSessionNotDetected, "test-session-123")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrSessionNotDetected)
		assert.Contains(t, wrappedErr.Error(), "session auto-detection failed")
		assert.Contains(t, wrappedErr.Error(), "test-session-123")
	})
}

// TestErrorWrapping_Window tests error wrapping for window-related errors
func TestErrorWrapping_Window(t *testing.T) {
	t.Run("ErrWindowNotFound with session and window context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: session=%s window=%s", ErrWindowNotFound, "my-session", "@1")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrWindowNotFound)
		assert.Contains(t, wrappedErr.Error(), "window not found")
		assert.Contains(t, wrappedErr.Error(), "my-session")
		assert.Contains(t, wrappedErr.Error(), "@1")
	})

	t.Run("ErrWindowCreateFailed with window name context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: name=%s", ErrWindowCreateFailed, "build-window")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrWindowCreateFailed)
		assert.Contains(t, wrappedErr.Error(), "window creation failed")
		assert.Contains(t, wrappedErr.Error(), "build-window")
	})

	t.Run("ErrWindowKillFailed with context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: cannot kill last window in session", ErrWindowKillFailed)

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrWindowKillFailed)
		assert.Contains(t, wrappedErr.Error(), "window kill failed")
		assert.Contains(t, wrappedErr.Error(), "cannot kill last window")
	})
}

// TestErrorWrapping_Tmux tests error wrapping for tmux-related errors
func TestErrorWrapping_Tmux(t *testing.T) {
	t.Run("ErrTmuxNotRunning with session context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: session=%s", ErrTmuxNotRunning, "dev-session")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrTmuxNotRunning)
		assert.Contains(t, wrappedErr.Error(), "tmux session not running")
		assert.Contains(t, wrappedErr.Error(), "dev-session")
	})

	t.Run("ErrTmuxCommandFailed with underlying error", func(t *testing.T) {
		underlyingErr := errors.New("exit status 1")
		wrappedErr := fmt.Errorf("%w: %s", ErrTmuxCommandFailed, underlyingErr.Error())

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrTmuxCommandFailed)
		assert.Contains(t, wrappedErr.Error(), "tmux command execution failed")
		assert.Contains(t, wrappedErr.Error(), "exit status 1")
	})
}

// TestErrorWrapping_Validation tests error wrapping for validation errors
func TestErrorWrapping_Validation(t *testing.T) {
	t.Run("ErrInvalidSessionID with session value", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: %s", ErrInvalidSessionID, "invalid@session!")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrInvalidSessionID)
		assert.Contains(t, wrappedErr.Error(), "invalid session ID format")
		assert.Contains(t, wrappedErr.Error(), "invalid@session!")
	})

	t.Run("ErrInvalidWindowID with window value", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: %s", ErrInvalidWindowID, "bad-window-id")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrInvalidWindowID)
		assert.Contains(t, wrappedErr.Error(), "invalid window ID format")
		assert.Contains(t, wrappedErr.Error(), "bad-window-id")
	})

	t.Run("ErrInvalidInput with parameter context", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: command cannot be empty", ErrInvalidInput)

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrInvalidInput)
		assert.Contains(t, wrappedErr.Error(), "invalid input parameter")
		assert.Contains(t, wrappedErr.Error(), "command cannot be empty")
	})
}

// TestErrorWrapping_Filesystem tests error wrapping for filesystem errors
func TestErrorWrapping_Filesystem(t *testing.T) {
	t.Run("ErrWorkingDirNotFound with directory path", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: %s", ErrWorkingDirNotFound, "/nonexistent/path")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrWorkingDirNotFound)
		assert.Contains(t, wrappedErr.Error(), "working directory not accessible")
		assert.Contains(t, wrappedErr.Error(), "/nonexistent/path")
	})

	t.Run("ErrSessionFileCorrupt with file path", func(t *testing.T) {
		wrappedErr := fmt.Errorf("%w: file=%s", ErrSessionFileCorrupt, ".tmux-cli-session.json")

		assert.Error(t, wrappedErr)
		assert.ErrorIs(t, wrappedErr, ErrSessionFileCorrupt)
		assert.Contains(t, wrappedErr.Error(), "session file corrupted")
		assert.Contains(t, wrappedErr.Error(), ".tmux-cli-session.json")
	})
}

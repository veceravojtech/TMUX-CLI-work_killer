package mcp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

// ============================================================================
// WindowsKill Tests - STRICT MODE (Names Only)
// ============================================================================

// TestServer_WindowsKill_Success_MultiWindow verifies that WindowsKill
// successfully terminates a window in a session with multiple windows.
func TestServer_WindowsKill_Success_MultiWindow(t *testing.T) {
	// Arrange: Mock store with multiple windows
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main"},
				{TmuxWindowID: "@1", Name: "build"},
				{TmuxWindowID: "@2", Name: "test"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	// Mock ListWindows to return the actual tmux windows
	tmuxWindows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "main"},
		{TmuxWindowID: "@1", Name: "build"},
		{TmuxWindowID: "@2", Name: "test"},
	}
	mockExecutor.On("ListWindows", "test-session").Return(tmuxWindows, nil)
	mockExecutor.On("KillWindow", "test-session", "@1").Return(nil)

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Use window NAME (not ID)
	success, err := server.WindowsKill("build")

	// Assert
	require.NoError(t, err)
	assert.True(t, success)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsKill_Error_EmptyWindowID verifies that WindowsKill
// returns ErrInvalidWindowID when windowIdentifier is empty.
func TestServer_WindowsKill_Error_EmptyWindowID(t *testing.T) {
	server := &Server{workingDir: "/test"}

	success, err := server.WindowsKill("")

	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrInvalidWindowID))
	assert.Contains(t, err.Error(), "windowIdentifier cannot be empty")
}

// TestServer_WindowsKill_Error_LastWindowInSession verifies that WindowsKill
// prevents killing the last window in a session (would terminate session).
func TestServer_WindowsKill_Error_LastWindowInSession(t *testing.T) {
	// CRITICAL: Prevent killing last window (would terminate session)
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "only-window"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	// Mock ListWindows to return only one window
	tmuxWindows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "only-window"},
	}
	mockExecutor.On("ListWindows", "test-session").Return(tmuxWindows, nil)

	server := &Server{store: mockStore, executor: mockExecutor, workingDir: "/test"}

	success, err := server.WindowsKill("only-window")

	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrWindowKillFailed))
	assert.Contains(t, err.Error(), "cannot kill last window")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsKill_Error_TmuxCommandFailed verifies that WindowsKill
// returns ErrTmuxCommandFailed when tmux kill-window command fails.
func TestServer_WindowsKill_Error_TmuxCommandFailed(t *testing.T) {
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main"},
				{TmuxWindowID: "@1", Name: "other"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	// Mock ListWindows to return both windows
	tmuxWindows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "main"},
		{TmuxWindowID: "@1", Name: "other"},
	}
	mockExecutor.On("ListWindows", "test-session").Return(tmuxWindows, nil)
	mockExecutor.On("KillWindow", "test-session", "@1").Return(errors.New("tmux not running"))

	server := &Server{store: mockStore, executor: mockExecutor, workingDir: "/test"}

	success, err := server.WindowsKill("other")

	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrTmuxCommandFailed))
	assert.Contains(t, err.Error(), "test-session")
	assert.Contains(t, err.Error(), "other")
	mockExecutor.AssertExpectations(t)
}

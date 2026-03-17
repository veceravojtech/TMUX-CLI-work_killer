package mcp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

// ============================================================================
// WindowsKill Tests - STRICT MODE (Names Only)
// ============================================================================

// TestServer_WindowsKill_Success_MultiWindow verifies that WindowsKill
// successfully terminates a window in a session with multiple windows.
func TestServer_WindowsKill_Success_MultiWindow(t *testing.T) {
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test").Return("test-session", nil)
	mockExecutor.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "main"},
		{TmuxWindowID: "@1", Name: "build"},
		{TmuxWindowID: "@2", Name: "test"},
	}, nil)
	mockExecutor.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExecutor, "/test")

	success, err := server.WindowsKill("build")

	require.NoError(t, err)
	assert.True(t, success)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsKill_Error_EmptyWindowID verifies that WindowsKill
// returns ErrInvalidWindowID when windowIdentifier is empty.
func TestServer_WindowsKill_Error_EmptyWindowID(t *testing.T) {
	mockExecutor := &testutil.MockTmuxExecutor{}
	server := newTestServer(mockExecutor, "/test")

	success, err := server.WindowsKill("")

	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrInvalidWindowID))
	assert.Contains(t, err.Error(), "windowIdentifier cannot be empty")
}

// TestServer_WindowsKill_Error_LastWindowInSession verifies that WindowsKill
// prevents killing the last window in a session (would terminate session).
func TestServer_WindowsKill_Error_LastWindowInSession(t *testing.T) {
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test").Return("test-session", nil)
	mockExecutor.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "only-window"},
	}, nil)

	server := newTestServer(mockExecutor, "/test")

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
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test").Return("test-session", nil)
	mockExecutor.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "main"},
		{TmuxWindowID: "@1", Name: "other"},
	}, nil)
	mockExecutor.On("KillWindow", "test-session", "@1").Return(errors.New("tmux not running"))

	server := newTestServer(mockExecutor, "/test")

	success, err := server.WindowsKill("other")

	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrTmuxCommandFailed))
	assert.Contains(t, err.Error(), "test-session")
	assert.Contains(t, err.Error(), "other")
	mockExecutor.AssertExpectations(t)
}

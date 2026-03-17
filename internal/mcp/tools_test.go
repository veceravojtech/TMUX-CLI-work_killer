package mcp

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

// newTestServer creates a Server with a mock executor for testing
func newTestServer(executor *testutil.MockTmuxExecutor, workingDir string) *Server {
	return &Server{
		executor:   executor,
		workingDir: workingDir,
	}
}

// TestServer_WindowsList_Success_MultipleWindows verifies that WindowsList
// returns all windows from tmux.
func TestServer_WindowsList_Success_MultipleWindows(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "main"},
		{TmuxWindowID: "@1", Name: "editor"},
		{TmuxWindowID: "@2", Name: "logs"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	windows, err := server.WindowsList()

	require.NoError(t, err)
	assert.Len(t, windows, 3)
	assert.Equal(t, "main", windows[0].Name)
	assert.Equal(t, "editor", windows[1].Name)
	assert.Equal(t, "logs", windows[2].Name)
}

// TestServer_WindowsList_Success_SingleWindow verifies with one window
func TestServer_WindowsList_Success_SingleWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	windows, err := server.WindowsList()

	require.NoError(t, err)
	assert.Len(t, windows, 1)
	assert.Equal(t, "supervisor", windows[0].Name)
}

// TestServer_WindowsList_SessionNotFound verifies error when no session found
func TestServer_WindowsList_SessionNotFound(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	windows, err := server.WindowsList()

	require.Error(t, err)
	assert.Nil(t, windows)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

// TestServer_WindowsSend_Success verifies successful command send
func TestServer_WindowsSend_Success(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@0", "echo hello").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsSend("@0", "echo hello")

	require.NoError(t, err)
	assert.True(t, success)
}

// TestServer_WindowsSend_ByName verifies send by window name
func TestServer_WindowsSend_ByName(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", "echo hello").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsSend("worker", "echo hello")

	require.NoError(t, err)
	assert.True(t, success)
}

// TestServer_WindowsSend_EmptyWindowID verifies error for empty window ID
func TestServer_WindowsSend_EmptyWindowID(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, err := server.WindowsSend("", "echo hello")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidWindowID)
}

// TestServer_WindowsSend_EmptyCommand verifies error for empty command
func TestServer_WindowsSend_EmptyCommand(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, err := server.WindowsSend("@0", "")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestServer_WindowsCreate_Success verifies window creation
func TestServer_WindowsCreate_Success(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "worker", "zsh").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	window, err := server.WindowsCreate("worker", "")

	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Equal(t, "@1", window.TmuxWindowID)
	assert.Equal(t, "worker", window.Name)
	assert.NotEmpty(t, window.UUID)
}

// TestServer_WindowsCreate_DuplicateName verifies error for duplicate name
func TestServer_WindowsCreate_DuplicateName(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	window, err := server.WindowsCreate("supervisor", "")

	require.Error(t, err)
	assert.Nil(t, window)
	assert.ErrorIs(t, err, ErrWindowCreateFailed)
}

// TestServer_WindowsCreate_DuplicateNameCaseInsensitive verifies case-insensitive check
func TestServer_WindowsCreate_DuplicateNameCaseInsensitive(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	window, err := server.WindowsCreate("SUPERVISOR", "")

	require.Error(t, err)
	assert.Nil(t, window)
	assert.ErrorIs(t, err, ErrWindowCreateFailed)
}

// TestServer_WindowsCreate_EmptyName verifies error for empty name
func TestServer_WindowsCreate_EmptyName(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	window, err := server.WindowsCreate("", "")

	require.Error(t, err)
	assert.Nil(t, window)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestServer_WindowsKill_Success verifies window kill
func TestServer_WindowsKill_Success(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsKill("worker")

	require.NoError(t, err)
	assert.True(t, success)
}

// TestServer_WindowsKill_RejectsWindowID verifies @ prefix rejection
func TestServer_WindowsKill_RejectsWindowID(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, err := server.WindowsKill("@0")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidWindowID)
}

// TestServer_WindowsKill_LastWindow verifies cannot kill last window
func TestServer_WindowsKill_LastWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsKill("supervisor")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrWindowKillFailed)
}

// TestServer_WindowsKill_WindowNotFound verifies error for non-existent window
func TestServer_WindowsKill_WindowNotFound(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsKill("nonexistent")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrWindowNotFound)
}

// TestServer_WindowsMessage_Success verifies message sending
func TestServer_WindowsMessage_Success(t *testing.T) {
	// Ensure no UUID in environment (may be set when running inside tmux-cli session)
	os.Unsetenv("TMUX_WINDOW_UUID")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@0", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "New message from:")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, sender, err := server.WindowsMessage("supervisor", "hello")

	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "test-session", sender) // Default sender when no UUID env
}

// TestServer_WindowsMessage_WithSenderUUID verifies sender detection from UUID
func TestServer_WindowsMessage_WithSenderUUID(t *testing.T) {
	// Set TMUX_WINDOW_UUID env var
	os.Setenv("TMUX_WINDOW_UUID", "worker-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	// Mock GetWindowOption for UUID lookup
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("supervisor-uuid", nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("worker-uuid", nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@0", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "New message from: worker")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, sender, err := server.WindowsMessage("supervisor", "hello")

	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "worker", sender)
}

// TestServer_WindowsMessage_EmptyReceiver verifies error for empty receiver
func TestServer_WindowsMessage_EmptyReceiver(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, _, err := server.WindowsMessage("", "hello")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestServer_WindowsMessage_EmptyMessage verifies error for empty message
func TestServer_WindowsMessage_EmptyMessage(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, _, err := server.WindowsMessage("supervisor", "")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestServer_DiscoverSession_Found verifies session discovery
func TestServer_DiscoverSession_Found(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("found-session", nil)

	server := newTestServer(mockExec, "/test/dir")
	sessionID, err := server.discoverSession()

	require.NoError(t, err)
	assert.Equal(t, "found-session", sessionID)
}

// TestServer_DiscoverSession_NotFound verifies error when no session found
func TestServer_DiscoverSession_NotFound(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	sessionID, err := server.discoverSession()

	require.Error(t, err)
	assert.Equal(t, "", sessionID)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

// TestServer_DiscoverSession_Error verifies tmux error is properly classified
func TestServer_DiscoverSession_Error(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("", errors.New("tmux error"))

	server := newTestServer(mockExec, "/test/dir")
	sessionID, err := server.discoverSession()

	require.Error(t, err)
	assert.Equal(t, "", sessionID)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	assert.Contains(t, err.Error(), "tmux error")
}

// TestResolveWindowIdentifier_WithID verifies @ prefix passthrough
func TestResolveWindowIdentifier_WithID(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}

	id, err := resolveWindowIdentifier(windows, "@5")
	require.NoError(t, err)
	assert.Equal(t, "@5", id)
}

// TestResolveWindowIdentifier_WithName verifies name resolution
func TestResolveWindowIdentifier_WithName(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}

	id, err := resolveWindowIdentifier(windows, "worker")
	require.NoError(t, err)
	assert.Equal(t, "@1", id)
}

// TestResolveWindowIdentifier_NotFound verifies error for missing name
func TestResolveWindowIdentifier_NotFound(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}

	_, err := resolveWindowIdentifier(windows, "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWindowNotFound)
}

// TestResolveWindowIdentifier_Empty verifies error for empty input
func TestResolveWindowIdentifier_Empty(t *testing.T) {
	windows := []tmux.WindowInfo{}

	_, err := resolveWindowIdentifier(windows, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidWindowID)
}

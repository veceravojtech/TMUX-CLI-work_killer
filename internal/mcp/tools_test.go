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
	success, output, err := server.WindowsSend("@0", "echo hello", false)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Empty(t, output)
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
	success, output, err := server.WindowsSend("worker", "echo hello", false)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Empty(t, output)
}

// TestServer_WindowsSend_EmptyWindowID verifies error for empty window ID
func TestServer_WindowsSend_EmptyWindowID(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, _, err := server.WindowsSend("", "echo hello", false)

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidWindowID)
}

// TestServer_WindowsSend_EmptyCommand verifies error for empty command
func TestServer_WindowsSend_EmptyCommand(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	success, _, err := server.WindowsSend("@0", "", false)

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

// TestServer_WindowsSend_WithSudo_NewWindow verifies sudo creates persistent window on first call
func TestServer_WindowsSend_WithSudo_NewWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	// No "sudo" window exists yet
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("GetSessionEnvironment", "test-session", "TMUX_CLI_SUDO_PASS").Return("mypassword", nil)
	mockExec.On("CreateWindow", "test-session", "sudo", "zsh").Return("@99", nil)
	mockExec.On("SendMessage", "test-session", "@99", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export _SP=")
	})).Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@99", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "sudo -S") && strings.Contains(s, "echo hello")
	})).Return(nil)
	mockExec.On("CaptureWindowOutput", "test-session", "@99").Return("root\n", nil)

	server := newTestServer(mockExec, "/test/dir")
	success, output, err := server.WindowsSend("@0", "echo hello", true)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "root", output)
	mockExec.AssertCalled(t, "CreateWindow", "test-session", "sudo", "zsh")
}

// TestServer_WindowsSend_WithSudo_ExistingWindow verifies sudo reuses existing persistent window
func TestServer_WindowsSend_WithSudo_ExistingWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	// "sudo" window already exists
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
		{TmuxWindowID: "@5", Name: "sudo"},
	}, nil)
	mockExec.On("GetSessionEnvironment", "test-session", "TMUX_CLI_SUDO_PASS").Return("mypassword", nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@5", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "sudo -S") && strings.Contains(s, "echo hello")
	})).Return(nil)
	mockExec.On("CaptureWindowOutput", "test-session", "@5").Return("root\n", nil)

	server := newTestServer(mockExec, "/test/dir")
	success, output, err := server.WindowsSend("@0", "echo hello", true)

	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "root", output)
	mockExec.AssertNotCalled(t, "CreateWindow", mock.Anything, mock.Anything, mock.Anything)
}

// TestServer_WindowsSend_WithSudo_CreateWindowFails verifies error when persistent window creation fails
func TestServer_WindowsSend_WithSudo_CreateWindowFails(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("GetSessionEnvironment", "test-session", "TMUX_CLI_SUDO_PASS").Return("mypassword", nil)
	mockExec.On("CreateWindow", "test-session", "sudo", "zsh").Return("", errors.New("tmux new-window failed"))

	server := newTestServer(mockExec, "/test/dir")
	success, _, err := server.WindowsSend("@0", "echo hello", true)

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	assert.Contains(t, err.Error(), "failed to create sudo window")
}

// TestServer_WindowsSend_WithSudo_NoPassword verifies error when sudo password not configured
func TestServer_WindowsSend_WithSudo_NoPassword(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)
	mockExec.On("GetSessionEnvironment", "test-session", "TMUX_CLI_SUDO_PASS").Return("", errors.New("not set"))

	server := newTestServer(mockExec, "/test/dir")
	success, _, err := server.WindowsSend("@0", "echo hello", true)

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "sudo not configured")
}

// --- nextExecuteN tests ---

func TestNextExecuteN_EmptyList(t *testing.T) {
	result := nextExecuteN(nil)
	assert.Equal(t, "execute-1", result)
}

func TestNextExecuteN_NoExecuteWindows(t *testing.T) {
	windows := []WindowListItem{{Name: "supervisor"}, {Name: "main"}}
	result := nextExecuteN(windows)
	assert.Equal(t, "execute-1", result)
}

func TestNextExecuteN_OneExecuteWindow(t *testing.T) {
	windows := []WindowListItem{{Name: "supervisor"}, {Name: "execute-1"}}
	result := nextExecuteN(windows)
	assert.Equal(t, "execute-2", result)
}

func TestNextExecuteN_GapInNumbers(t *testing.T) {
	windows := []WindowListItem{{Name: "execute-3"}, {Name: "execute-7"}}
	result := nextExecuteN(windows)
	assert.Equal(t, "execute-8", result)
}

func TestNextExecuteN_NonNumericSuffix(t *testing.T) {
	windows := []WindowListItem{{Name: "execute-foo"}, {Name: "supervisor"}}
	result := nextExecuteN(windows)
	assert.Equal(t, "execute-1", result)
}

// --- buildTaskMessage tests ---

func TestBuildTaskMessage_AllFields(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-3", "audit auth module", ".tmux-cli/research/2026-05-11-22/task-auth.md", "Check all auth endpoints", "Prior audit found XSS in login", "2026-05-11-22")

	assert.Contains(t, msg, "SUPERVISOR_WID=supervisor")
	assert.Contains(t, msg, "SELF_WID=execute-3")
	assert.Contains(t, msg, "SUBTASK: audit auth module")
	assert.Contains(t, msg, ".tmux-cli/research/2026-05-11-22/task-auth.md")
	assert.Contains(t, msg, "Check all auth endpoints")
	assert.Contains(t, msg, "Prior audit found XSS in login")
	assert.Contains(t, msg, "DELIVERABLE")
	assert.Contains(t, msg, "RESPONSE PROTOCOL (MANDATORY)")
	assert.Contains(t, msg, "execute-3-<slug>.md")
	assert.Contains(t, msg, "windows-message to supervisor")
	assert.Contains(t, msg, "[EXECUTE:DONE wid=execute-3 sup=supervisor")
}

func TestBuildTaskMessage_EmptyContext(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", "2026-05-11-22")

	assert.Contains(t, msg, "CONTEXT:\n(none)")
}

func TestBuildTaskMessage_ResearchDir(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-5", "task", "ctx.md", "scope", "", "2026-01-15-09")

	assert.Contains(t, msg, ".tmux-cli/research/2026-01-15-09/execute-5-<slug>.md")
}

// --- WindowsSpawnWorker tests ---

func TestServer_WindowsSpawnWorker_Success(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "zsh").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "SUPERVISOR_WID=supervisor") &&
			strings.Contains(s, "SELF_WID=execute-1") &&
			strings.Contains(s, "SUBTASK: audit auth") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	window, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "prior findings")

	require.NoError(t, err)
	assert.Equal(t, "execute-1", workerName)
	assert.Equal(t, "execute-1", window.Name)
	assert.Equal(t, "@1", window.TmuxWindowID)
	assert.Contains(t, taskMessage, "SUPERVISOR_WID=supervisor")
	assert.Contains(t, taskMessage, "SELF_WID=execute-1")
	assert.Contains(t, taskMessage, "SUBTASK: audit auth")
	mockExec.AssertExpectations(t)
}

func TestServer_WindowsSpawnWorker_EmptySupervisorWid(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("", "task", "ctx.md", "scope", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptySubtask(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "", "ctx.md", "scope", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyContextFile(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "", "scope", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyScope(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_ExecuteSendFails_CleansUp(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "zsh").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(errors.New("send failed"))
	mockExec.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	mockExec.AssertCalled(t, "KillWindow", "test-session", "@1")
}

func TestServer_WindowsSpawnWorker_TaskMessageFails_CleansUp(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "zsh").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(errors.New("delay send failed"))
	mockExec.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	mockExec.AssertCalled(t, "KillWindow", "test-session", "@1")
}

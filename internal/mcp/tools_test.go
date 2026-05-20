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

// TestServer_WindowsKill_RejectsTaskvisorWindow verifies kill protection for taskvisor window
func TestServer_WindowsKill_RejectsTaskvisorWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "taskvisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsKill("taskvisor")

	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrWindowKillFailed)
	assert.Contains(t, err.Error(), "kill-protected")
}

// TestServer_WindowsKill_AllowsNonProtectedWindow verifies non-protected windows can still be killed
func TestServer_WindowsKill_AllowsNonProtectedWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "taskvisor"},
		{TmuxWindowID: "@2", Name: "execute-1"},
	}, nil)
	mockExec.On("KillWindow", "test-session", "@2").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	success, err := server.WindowsKill("execute-1")

	require.NoError(t, err)
	assert.True(t, success)
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
	msg := buildTaskMessage("supervisor", "execute-3", "audit auth module", ".tmux-cli/research/2026-05-11-22/task-auth.md", "Check all auth endpoints", "Prior audit found XSS in login", "2026-05-11-22", "")

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
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", "2026-05-11-22", "")

	assert.Contains(t, msg, "CONTEXT:\n(none)")
}

func TestBuildTaskMessage_ResearchDir(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-5", "task", "ctx.md", "scope", "", "2026-01-15-09", "")

	assert.Contains(t, msg, ".tmux-cli/research/2026-01-15-09/execute-5-<slug>.md")
}

func TestBuildTaskMessage_DefaultDeliverable(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", "2026-05-11-23", "")

	assert.Contains(t, msg, "FINDINGS")
	assert.Contains(t, msg, "RISKS")
	assert.Contains(t, msg, "RECOMMENDATION")
	assert.Contains(t, msg, "FILES")
	assert.Contains(t, msg, "DELIVERABLE")
}

func TestBuildTaskMessage_CustomDeliverable(t *testing.T) {
	custom := "- SPEC: structured specification with sections\n- DESIGN: architecture decisions\n- TESTS: test plan"
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", "2026-05-11-23", custom)

	assert.Contains(t, msg, "DELIVERABLE")
	assert.Contains(t, msg, custom)
	assert.NotContains(t, msg, ">=3 bullets")
	assert.NotContains(t, msg, "verb of decision")
	assert.Contains(t, msg, "RESPONSE PROTOCOL (MANDATORY)")
}

// --- WindowsSpawnWorker tests ---

// --- WindowsRecoverWorkers tests ---

func TestServer_WindowsRecoverWorkers_Success(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@2", Name: "execute-2"},
		{TmuxWindowID: "@3", Name: "execute-3"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendEnter", "test-session", "@2").Return(nil)
	mockExec.On("SendEnter", "test-session", "@3").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "continue").Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", "continue").Return(nil)
	mockExec.On("SendMessage", "test-session", "@3", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 3, output.Count)
	assert.Equal(t, "continue", output.Message)
	assert.ElementsMatch(t, []string{"execute-1", "execute-2", "execute-3"}, output.Recovered)
	mockExec.AssertExpectations(t)
}

func TestServer_WindowsRecoverWorkers_DefaultMessage(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, "continue", output.Message)
	mockExec.AssertCalled(t, "SendMessage", "test-session", "@1", "continue")
}

func TestServer_WindowsRecoverWorkers_CustomMessage(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "retry").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("retry")

	require.NoError(t, err)
	assert.Equal(t, "retry", output.Message)
	mockExec.AssertCalled(t, "SendMessage", "test-session", "@1", "retry")
}

func TestServer_WindowsRecoverWorkers_NoWorkers(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 0, output.Count)
	assert.Empty(t, output.Recovered)
	assert.Equal(t, "continue", output.Message)
}

func TestServer_WindowsRecoverWorkers_SessionNotFound(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	_, err := server.WindowsRecoverWorkers("")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestServer_WindowsRecoverWorkers_ListWindowsFails(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return(nil, errors.New("tmux list-windows failed"))

	server := newTestServer(mockExec, "/test/dir")
	_, err := server.WindowsRecoverWorkers("")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
}

func TestServer_WindowsRecoverWorkers_SendEnterFails_ContinuesOthers(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@2", Name: "execute-2"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(errors.New("window dead"))
	mockExec.On("SendEnter", "test-session", "@2").Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-2"}, output.Recovered)
	mockExec.AssertNotCalled(t, "SendMessage", "test-session", "@1", "continue")
}

func TestServer_WindowsRecoverWorkers_SendMessageFails_ContinuesOthers(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@2", Name: "execute-2"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendEnter", "test-session", "@2").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "continue").Return(errors.New("send failed"))
	mockExec.On("SendMessage", "test-session", "@2", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-2"}, output.Recovered)
}

func TestServer_WindowsRecoverWorkers_SkipsNonExecuteWindows(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "monitor"},
		{TmuxWindowID: "@2", Name: "execute-1"},
		{TmuxWindowID: "@3", Name: "main"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@2").Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-1"}, output.Recovered)
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@0")
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@1")
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@3")
}

func TestServer_WindowsRecoverWorkers_NonNumericExecuteSuffix(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-foo"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-foo"}, output.Recovered)
}

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
	window, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "prior findings", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-1", workerName)
	assert.Equal(t, "execute-1", window.Name)
	assert.Equal(t, "@1", window.TmuxWindowID)
	assert.Contains(t, taskMessage, "SUPERVISOR_WID=supervisor")
	assert.Contains(t, taskMessage, "SELF_WID=execute-1")
	assert.Contains(t, taskMessage, "SUBTASK: audit auth")
	assert.Contains(t, taskMessage, "FINDINGS")
	assert.Contains(t, taskMessage, "RISKS")
	mockExec.AssertExpectations(t)
}

func TestServer_WindowsSpawnWorker_EmptySupervisorWid(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("", "task", "ctx.md", "scope", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptySubtask(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "", "ctx.md", "scope", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyContextFile(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "", "scope", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyScope(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_WithCustomDeliverable(t *testing.T) {
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

	customDeliverable := "- SPEC: structured specification\n- DESIGN: architecture decisions"
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, customDeliverable) &&
			!strings.Contains(s, ">=3 bullets") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "write spec", "ctx.md", "create spec", "", customDeliverable)

	require.NoError(t, err)
	assert.Equal(t, "execute-1", workerName)
	assert.Contains(t, taskMessage, customDeliverable)
	assert.NotContains(t, taskMessage, ">=3 bullets")
	assert.Contains(t, taskMessage, "RESPONSE PROTOCOL")
	assert.Contains(t, taskMessage, "sections specified in DELIVERABLE above")
	assert.NotContains(t, taskMessage, "## FINDINGS / ## RISKS / ## RECOMMENDATION / ## FILES")
}

func TestServer_WindowsSpawnWorker_WithoutDeliverable(t *testing.T) {
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
		return strings.Contains(s, "FINDINGS") &&
			strings.Contains(s, "RISKS") &&
			strings.Contains(s, "RECOMMENDATION") &&
			strings.Contains(s, "FILES") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-1", workerName)
	assert.Contains(t, taskMessage, "FINDINGS")
	assert.Contains(t, taskMessage, "RISKS")
	assert.Contains(t, taskMessage, "RECOMMENDATION")
	assert.Contains(t, taskMessage, "FILES")
	assert.Contains(t, taskMessage, "## FINDINGS / ## RISKS / ## RECOMMENDATION / ## FILES")
	assert.NotContains(t, taskMessage, "sections specified in DELIVERABLE above")
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
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "")

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
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	mockExec.AssertCalled(t, "KillWindow", "test-session", "@1")
}

func TestServer_WindowsSpawnWorker_MaxWorkersExceeded(t *testing.T) {
	root := t.TempDir()
	settingsDir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(settingsDir+"/setting.yaml", []byte(`supervisor:
  max_workers: 2
`), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{Name: "supervisor", TmuxWindowID: "@0"},
		{Name: "execute-1", TmuxWindowID: "@1"},
		{Name: "execute-2", TmuxWindowID: "@2"},
	}, nil)

	server := newTestServer(mockExec, root)
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxWorkersExceeded)
	assert.Contains(t, err.Error(), "2 execute workers already running")
}

func TestServer_WindowsSpawnWorker_MaxWorkersNotExceeded(t *testing.T) {
	root := t.TempDir()
	settingsDir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(settingsDir+"/setting.yaml", []byte(`supervisor:
  max_workers: 3
`), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{Name: "supervisor", TmuxWindowID: "@0"},
		{Name: "execute-1", TmuxWindowID: "@1"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-2", mock.Anything).Return("@2", nil)
	mockExec.On("SetWindowOption", "test-session", "@2", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@2", mock.Anything).Return("", nil)
	mockExec.On("SendMessage", "test-session", "@2", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@2", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	window, name, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-2", name)
	assert.NotNil(t, window)
}

func TestServer_WindowsSpawnWorker_MaxWorkersZeroUnlimited(t *testing.T) {
	root := t.TempDir()
	settingsDir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(settingsDir+"/setting.yaml", []byte(`supervisor:
  max_workers: 0
`), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{Name: "supervisor", TmuxWindowID: "@0"},
		{Name: "execute-1", TmuxWindowID: "@1"},
		{Name: "execute-2", TmuxWindowID: "@2"},
		{Name: "execute-3", TmuxWindowID: "@3"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-4", mock.Anything).Return("@4", nil)
	mockExec.On("SetWindowOption", "test-session", "@4", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@4", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@4", mock.Anything).Return("", nil)
	mockExec.On("SendMessage", "test-session", "@4", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@4", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	_, name, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-4", name)
}

func TestServer_TasksValidate_Clean(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	dir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(dir+"/tasks.yaml", []byte(`status: ready
cycle: 1
tasks:
  - name: "clean task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`), 0o644))

	out, err := server.TasksValidate()
	require.NoError(t, err)
	assert.True(t, out.Valid)
	assert.Empty(t, out.Errors)
}

func TestServer_TasksValidate_ExtraFields(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	dir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(dir+"/tasks.yaml", []byte(`status: ready
cycle: 1
tasks:
  - name: "bloated task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    scope: "inline scope"
`), 0o644))

	out, err := server.TasksValidate()
	require.NoError(t, err)
	assert.False(t, out.Valid)
	require.Len(t, out.Errors, 1)
	assert.Contains(t, out.Errors[0], "scope")
	assert.Contains(t, out.Errors[0], "context .md file")
}

func TestServer_TasksValidate_NoFile(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	_, err := server.TasksValidate()
	require.Error(t, err)
}

func TestServer_SpecValidate_Valid(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	spec := "## Intent\n\n**Problem:** Broken.\n**Approach:** Fix.\n\n" +
		"## Boundaries & Constraints\n\n**Always:** Do.\n**Never:** Don't.\n\n" +
		"## Dependencies\n\nnone\n\n" +
		"## Code Map\n\n- `file.go:10` — thing\n\n" +
		"## Implementation Plan\n\n### Files to Create/Modify\n\n- `file.go` — modify\n\n" +
		"## Test Plan\n\n- TestThing: checks it\n\n" +
		"## Acceptance Criteria\n\n- [ ] Given X, when Y, then Z\n"
	require.NoError(t, os.WriteFile(root+"/spec.md", []byte(spec), 0o644))

	out, err := server.SpecValidate(root + "/spec.md")
	require.NoError(t, err)
	assert.True(t, out.Valid)
	assert.Empty(t, out.Gaps)
}

func TestServer_SpecValidate_WithGaps(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	spec := "## Dependencies\n\nnone\n\n## Code Map\n\n## Test Plan\n\n- TestX: check\n\n" +
		"## Acceptance Criteria\n\n- [ ] It works\n"
	require.NoError(t, os.WriteFile(root+"/spec.md", []byte(spec), 0o644))

	out, err := server.SpecValidate(root + "/spec.md")
	require.NoError(t, err)
	assert.False(t, out.Valid)
	assert.NotEmpty(t, out.Gaps)
}

func TestServer_SpecValidate_NotFound(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	_, err := server.SpecValidate("/nonexistent/spec.md")
	require.Error(t, err)
}

package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	mockExec.On("CreateWindow", "test-session", "worker", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	window, err := server.WindowsCreate("worker", "", "")

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
	window, err := server.WindowsCreate("supervisor", "", "")

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
	window, err := server.WindowsCreate("SUPERVISOR", "", "")

	require.Error(t, err)
	assert.Nil(t, window)
	assert.ErrorIs(t, err, ErrWindowCreateFailed)
}

// TestServer_WindowsCreate_EmptyName verifies error for empty name
func TestServer_WindowsCreate_EmptyName(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, "/test/dir")

	window, err := server.WindowsCreate("", "", "")

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
	result := nextExecuteN(nil, "execute-")
	assert.Equal(t, "execute-1", result)
}

func TestNextExecuteN_NoExecuteWindows(t *testing.T) {
	windows := []WindowListItem{{Name: "supervisor"}, {Name: "main"}}
	result := nextExecuteN(windows, "execute-")
	assert.Equal(t, "execute-1", result)
}

func TestNextExecuteN_OneExecuteWindow(t *testing.T) {
	windows := []WindowListItem{{Name: "supervisor"}, {Name: "execute-1"}}
	result := nextExecuteN(windows, "execute-")
	assert.Equal(t, "execute-2", result)
}

func TestNextExecuteN_GapInNumbers(t *testing.T) {
	windows := []WindowListItem{{Name: "execute-3"}, {Name: "execute-7"}}
	result := nextExecuteN(windows, "execute-")
	assert.Equal(t, "execute-8", result)
}

func TestNextExecuteN_NonNumericSuffix(t *testing.T) {
	windows := []WindowListItem{{Name: "execute-foo"}, {Name: "supervisor"}}
	result := nextExecuteN(windows, "execute-")
	assert.Equal(t, "execute-1", result)
}

// --- buildTaskMessage tests ---

func TestBuildTaskMessage_AllFields(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-3", "audit auth module", ".tmux-cli/research/2026-05-11-22/task-auth.md", "Check all auth endpoints", "Prior audit found XSS in login", ".tmux-cli/research/2026-05-11-22", "")

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
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", ".tmux-cli/research/2026-05-11-22", "")

	assert.Contains(t, msg, "CONTEXT:\n(none)")
}

func TestBuildTaskMessage_ResearchDir(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-5", "task", "ctx.md", "scope", "", ".tmux-cli/research/2026-01-15-09", "")

	assert.Contains(t, msg, ".tmux-cli/research/2026-01-15-09/execute-5-<slug>.md")
}

func TestBuildTaskMessage_DefaultDeliverable(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", ".tmux-cli/research/2026-05-11-23", "")

	assert.Contains(t, msg, "FINDINGS")
	assert.Contains(t, msg, "RISKS")
	assert.Contains(t, msg, "RECOMMENDATION")
	assert.Contains(t, msg, "FILES")
	assert.Contains(t, msg, "DELIVERABLE")
}

func TestBuildTaskMessage_CustomDeliverable(t *testing.T) {
	custom := "- SPEC: structured specification with sections\n- DESIGN: architecture decisions\n- TESTS: test plan"
	msg := buildTaskMessage("supervisor", "execute-1", "task", "ctx.md", "scope", "", ".tmux-cli/research/2026-05-11-23", custom)

	assert.Contains(t, msg, "DELIVERABLE")
	assert.Contains(t, msg, custom)
	assert.NotContains(t, msg, ">=3 bullets")
	assert.NotContains(t, msg, "verb of decision")
	assert.Contains(t, msg, "RESPONSE PROTOCOL (MANDATORY)")
}

func TestBuildTaskMessage_GoalScopedSavePath(t *testing.T) {
	goalMsg := buildTaskMessage("supervisor", "execute-2", "task", "ctx.md", "scope", "", ".tmux-cli/goals/goal-007/research", "")
	assert.Contains(t, goalMsg, ".tmux-cli/goals/goal-007/research/execute-2-<slug>.md")
	assert.NotContains(t, goalMsg, ".tmux-cli/research/")

	standaloneMsg := buildTaskMessage("supervisor", "execute-2", "task", "ctx.md", "scope", "", ".tmux-cli/research/2026-06-01-14", "")
	assert.Contains(t, standaloneMsg, ".tmux-cli/research/2026-06-01-14/execute-2-<slug>.md")
}

// --- resolveResearchRoot tests ---

func TestResolveResearchRoot_GoalMode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"), []byte("goal-007\n"), 0o644))

	s := &Server{workingDir: dir}
	assert.Equal(t, ".tmux-cli/goals/goal-007/research", s.resolveResearchRoot("supervisor"))
}

func TestResolveResearchRoot_Standalone(t *testing.T) {
	dir := t.TempDir()
	s := &Server{workingDir: dir}

	root := s.resolveResearchRoot("supervisor")
	assert.True(t, strings.HasPrefix(root, ".tmux-cli/research/"), "expected standalone research prefix, got %q", root)
	assert.NotContains(t, root, ".tmux-cli/goals/")
}

func TestResolveResearchRoot_EmptyGoalFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"), []byte("  \n\t"), 0o644))

	s := &Server{workingDir: dir}

	root := s.resolveResearchRoot("supervisor")
	assert.True(t, strings.HasPrefix(root, ".tmux-cli/research/"), "expected standalone research prefix, got %q", root)
	assert.NotContains(t, root, ".tmux-cli/goals/")
}

// writeMarkers is a tiny helper that writes the goal and (optionally) cycle
// marker files into a temp working dir for resolveResearchRoot tests.
func writeMarkers(t *testing.T, dir, goal string, cycle *string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	if goal != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"), []byte(goal), 0o644))
	}
	if cycle != nil {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-cycle"), []byte(*cycle), 0o644))
	}
}

func strptr(s string) *string { return &s }

// goal=goal-008 + cycle=2 -> .tmux-cli/goals/goal-008/research/cycle-2
func TestResolveResearchRoot_GoalAndCycle(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "goal-008\n", strptr("2\n"))

	s := &Server{workingDir: dir}
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-008", "research", "cycle-2"), s.resolveResearchRoot("supervisor"))
}

// goal set, cycle marker absent -> .tmux-cli/goals/goal-008/research (unchanged)
func TestResolveResearchRoot_GoalNoCycle(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "goal-008\n", nil)

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-008", "research"), root)
	assert.NotContains(t, root, "cycle-")
}

// goal set + cycle = ""/"abc"/"0"/"-1" -> goal-scoped no-cycle path, never error/panic.
func TestResolveResearchRoot_InvalidCycleFallsBack(t *testing.T) {
	for _, cycle := range []string{"", "  \n\t", "abc", "0", "-1", "1.5", "2x"} {
		t.Run("cycle="+cycle, func(t *testing.T) {
			dir := t.TempDir()
			writeMarkers(t, dir, "goal-008\n", strptr(cycle))

			s := &Server{workingDir: dir}
			root := s.resolveResearchRoot("supervisor")
			assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-008", "research"), root,
				"invalid cycle %q must fall back to goal-scoped no-cycle path", cycle)
			assert.NotContains(t, root, "cycle-")
		})
	}
}

// no goal marker -> standalone timestamped path; a stray cycle marker must NOT
// promote a standalone run into a goal/cycle path.
func TestResolveResearchRoot_StandaloneIgnoresCycle(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "", strptr("3\n"))

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor")
	assert.True(t, strings.HasPrefix(root, ".tmux-cli/research/"), "expected standalone research prefix, got %q", root)
	assert.NotContains(t, root, ".tmux-cli/goals/")
	assert.NotContains(t, root, "cycle-")
}

// --- parseGoalBinding tests ---

// "sup-goal-020-c3" -> goalID="goal-020", cycle=3, ok=true.
func TestParseGoalBinding_NamespacedWithCycle(t *testing.T) {
	id, cycle, ok := parseGoalBinding("sup-goal-020-c3")
	assert.True(t, ok)
	assert.Equal(t, "goal-020", id)
	assert.Equal(t, 3, cycle)
}

// "sup-goal-020" (no cycle suffix) -> goalID="goal-020", cycle=0, ok=true.
func TestParseGoalBinding_NamespacedNoCycle(t *testing.T) {
	id, cycle, ok := parseGoalBinding("sup-goal-020")
	assert.True(t, ok)
	assert.Equal(t, "goal-020", id)
	assert.Equal(t, 0, cycle)
}

// "supervisor" carries no goal token -> ok=false (forces marker fallback).
func TestParseGoalBinding_PlainSupervisor(t *testing.T) {
	id, cycle, ok := parseGoalBinding("supervisor")
	assert.False(t, ok)
	assert.Equal(t, "", id)
	assert.Equal(t, 0, cycle)
}

// "" -> ok=false, never panics.
func TestParseGoalBinding_Empty(t *testing.T) {
	id, cycle, ok := parseGoalBinding("")
	assert.False(t, ok)
	assert.Equal(t, "", id)
	assert.Equal(t, 0, cycle)
}

// "sup-goal-020-c0": cycle<1 is dropped to the no-cycle path; goalID still parsed.
func TestParseGoalBinding_InvalidCycleIgnored(t *testing.T) {
	id, cycle, ok := parseGoalBinding("sup-goal-020-c0")
	assert.True(t, ok)
	assert.Equal(t, "goal-020", id)
	assert.Equal(t, 0, cycle)
}

// --- resolveResearchRoot caller-derived tests ---

// A goal-namespaced caller wins over a conflicting global marker (no cross-goal leak).
func TestResolveResearchRoot_DerivesFromCallerName(t *testing.T) {
	dir := t.TempDir()
	// Conflicting global markers point at a DIFFERENT goal; caller name must win.
	writeMarkers(t, dir, "goal-999\n", strptr("7\n"))

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("sup-goal-020-c3")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research", "cycle-3"), root)
	assert.NotContains(t, root, "goal-999")
}

// Goal-namespaced caller without a cycle suffix -> goal-scoped no-cycle path.
func TestResolveResearchRoot_NamespacedNoCycle(t *testing.T) {
	dir := t.TempDir()
	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("sup-goal-020")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research"), root)
	assert.NotContains(t, root, "cycle-")
}

// Plain "supervisor" caller + global goal marker (no cycle) -> preserves current behavior.
func TestResolveResearchRoot_FallbackToGlobalGoal(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "goal-007\n", nil)

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-007", "research"), root)
	assert.NotContains(t, root, "cycle-")
}

// Plain "supervisor" caller + global goal & cycle markers -> per-cycle fallback path.
func TestResolveResearchRoot_FallbackGoalWithCycle(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "goal-008\n", strptr("2\n"))

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-008", "research", "cycle-2"), root)
}

// Plain "supervisor" caller + no markers -> standalone timestamped path.
func TestResolveResearchRoot_FallbackStandaloneTimestamp(t *testing.T) {
	dir := t.TempDir()
	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor")
	assert.True(t, strings.HasPrefix(root, ".tmux-cli/research/"), "expected standalone research prefix, got %q", root)
	assert.NotContains(t, root, ".tmux-cli/goals/")
}

// Two callers for different goals on one Server map to isolated trees (parallel-safety
// regression guard): no shared global file can route one goal's worker into another's.
func TestResolveResearchRoot_TwoCallersIsolated(t *testing.T) {
	dir := t.TempDir()
	s := &Server{workingDir: dir}

	rootA := s.resolveResearchRoot("sup-goal-001-c1")
	rootB := s.resolveResearchRoot("sup-goal-002-c1")

	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-001", "research", "cycle-1"), rootA)
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-002", "research", "cycle-1"), rootB)
	assert.NotEqual(t, rootA, rootB)
}

// --- R1: reconciliation with the daemon's actual namespaced window names ---

// The taskvisor daemon (window_names.go) emits supervisor-<ns> / validator-<ns> /
// execute-<ns>-<n> / inv-<ns>-<n> at MaxGoals>1, where <ns> is the goal id with
// its "goal-" prefix stripped. parseGoalBinding must recover goal-<ns> from each.
func TestParseGoalBinding_DaemonNamespacedForms(t *testing.T) {
	cases := map[string]string{
		"supervisor-020": "goal-020",
		"validator-020":  "goal-020",
		"execute-020-1":  "goal-020",
		"execute-020-12": "goal-020",
		"inv-021-3":      "goal-021",
	}
	for win, wantID := range cases {
		t.Run(win, func(t *testing.T) {
			id, cycle, ok := parseGoalBinding(win)
			assert.True(t, ok, "namespaced window %q must bind a goal", win)
			assert.Equal(t, wantID, id)
			assert.Equal(t, 0, cycle, "namespaced windows carry no cycle suffix")
		})
	}
}

// The MaxGoals<=1 bare window names must NOT bind a goal — they fall back to the
// global marker so single-goal routing stays byte-identical.
func TestParseGoalBinding_BareNamesDoNotBind(t *testing.T) {
	for _, win := range []string{"supervisor", "validator", "execute-1", "inv-2", "execute-12"} {
		t.Run(win, func(t *testing.T) {
			id, cycle, ok := parseGoalBinding(win)
			assert.False(t, ok, "bare MaxGoals<=1 name %q must not bind a goal", win)
			assert.Equal(t, "", id)
			assert.Equal(t, 0, cycle)
		})
	}
}

// R1 regression guard: two workers spawned under DISTINCT namespaced supervisor
// windows (supervisor-020 / supervisor-021), as the daemon emits at MaxGoals>1,
// must resolve to DISTINCT goal-scoped research roots — even against a conflicting
// shared global marker. Before the reconciliation both fell through to the global
// marker and collided on one root.
func TestResolveResearchRoot_ConcurrentNamespacedGoalsAreDistinct(t *testing.T) {
	dir := t.TempDir()
	// A conflicting global marker that, pre-fix, both callers would have shared.
	writeMarkers(t, dir, "goal-999\n", strptr("7\n"))
	s := &Server{workingDir: dir}

	root20 := s.resolveResearchRoot("supervisor-020")
	root21 := s.resolveResearchRoot("supervisor-021")

	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research"), root20)
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-021", "research"), root21)
	assert.NotEqual(t, root20, root21, "concurrent namespaced goals must not share a research root")
	assert.NotContains(t, root20, "goal-999", "namespaced caller must win over the shared global marker")
	assert.NotContains(t, root21, "goal-999")
}

// --- per-goal cycle marker tests ---

// writePerGoalCycleMarker writes the per-goal .tmux-cli/goals/<goalID>/current-cycle
// marker (the daemon's mg>1 race-free cycle source) into a temp working dir for
// resolveResearchRoot tests.
func writePerGoalCycleMarker(t *testing.T, dir, goalID, content string) {
	t.Helper()
	goalDir := filepath.Join(dir, ".tmux-cli", "goals", goalID)
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "current-cycle"), []byte(content), 0o644))
}

// Namespaced caller + per-goal marker "3" -> per-goal cycle layer in the path.
func TestResolveResearchRoot_NamespacedReadsPerGoalCycleMarker(t *testing.T) {
	dir := t.TempDir()
	writePerGoalCycleMarker(t, dir, "goal-020", "3")

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("execute-020-1")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research", "cycle-3"), root)
}

// Namespaced caller, no per-goal marker -> no-cycle path, byte-identical to today.
func TestResolveResearchRoot_NamespacedMarkerAbsent(t *testing.T) {
	dir := t.TempDir()
	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("execute-020-1")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research"), root)
	assert.NotContains(t, root, "cycle-")
}

// Namespaced caller + empty/non-numeric/zero/negative marker -> no-cycle path, never panics.
func TestResolveResearchRoot_NamespacedMarkerInvalid(t *testing.T) {
	for _, marker := range []string{"", "  \n\t", "abc", "0", "-1", "1.5", "2x"} {
		t.Run("marker="+marker, func(t *testing.T) {
			dir := t.TempDir()
			writePerGoalCycleMarker(t, dir, "goal-020", marker)

			s := &Server{workingDir: dir}
			root := s.resolveResearchRoot("execute-020-1")
			assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research"), root,
				"invalid per-goal marker %q must fall back to the no-cycle path", marker)
			assert.NotContains(t, root, "cycle-")
		})
	}
}

// Explicit -c<N> suffix wins over a conflicting per-goal marker; marker not consulted.
func TestResolveResearchRoot_SuffixWinsOverPerGoalMarker(t *testing.T) {
	dir := t.TempDir()
	writePerGoalCycleMarker(t, dir, "goal-020", "9")

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("sup-goal-020-c3")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research", "cycle-3"), root)
	assert.NotContains(t, root, "cycle-9")
}

// The GLOBAL cycle marker is last-writer-wins under mg>1 and must NEVER be read
// for a namespaced caller — only the per-goal marker counts.
func TestResolveResearchRoot_NamespacedIgnoresGlobalCycleMarker(t *testing.T) {
	dir := t.TempDir()
	writeMarkers(t, dir, "goal-020\n", strptr("7\n"))

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor-020")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research"), root)
	assert.NotContains(t, root, "cycle-7")
}

// Two concurrent goals with distinct per-goal markers resolve to distinct cycle roots.
func TestResolveResearchRoot_TwoGoalsDistinctPerGoalCycles(t *testing.T) {
	dir := t.TempDir()
	writePerGoalCycleMarker(t, dir, "goal-020", "2")
	writePerGoalCycleMarker(t, dir, "goal-021", "5")

	s := &Server{workingDir: dir}
	root20 := s.resolveResearchRoot("execute-020-1")
	root21 := s.resolveResearchRoot("execute-021-1")

	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research", "cycle-2"), root20)
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-021", "research", "cycle-5"), root21)
	assert.NotEqual(t, root20, root21)
}

// Marker content with trailing whitespace is trimmed before parsing.
func TestResolveResearchRoot_NamespacedMarkerWhitespace(t *testing.T) {
	dir := t.TempDir()
	writePerGoalCycleMarker(t, dir, "goal-020", "2\n")

	s := &Server{workingDir: dir}
	root := s.resolveResearchRoot("supervisor-020")
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-020", "research", "cycle-2"), root)
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
	output, err := server.WindowsRecoverWorkers("", "")

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
	output, err := server.WindowsRecoverWorkers("", "")

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
	output, err := server.WindowsRecoverWorkers("retry", "")

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
	output, err := server.WindowsRecoverWorkers("", "")

	require.NoError(t, err)
	assert.Equal(t, 0, output.Count)
	assert.Empty(t, output.Recovered)
	assert.Equal(t, "continue", output.Message)
}

func TestServer_WindowsRecoverWorkers_SessionNotFound(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("", nil)

	server := newTestServer(mockExec, "/test/dir")
	_, err := server.WindowsRecoverWorkers("", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestServer_WindowsRecoverWorkers_ListWindowsFails(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return(nil, errors.New("tmux list-windows failed"))

	server := newTestServer(mockExec, "/test/dir")
	_, err := server.WindowsRecoverWorkers("", "")

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
	output, err := server.WindowsRecoverWorkers("", "")

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
	output, err := server.WindowsRecoverWorkers("", "")

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
	output, err := server.WindowsRecoverWorkers("", "")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-1"}, output.Recovered)
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@0")
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@1")
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@3")
}

// At MaxGoals>1 a goal-namespaced caller (e.g. supervisor-020) must recover ONLY
// its own goal's execute-<ns>-* workers — never inject "continue" into another
// goal's healthy workers mid-task.
func TestServer_WindowsRecoverWorkers_GoalScopedCaller(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-020-1"},
		{TmuxWindowID: "@2", Name: "execute-021-1"},
	}, nil)
	mockExec.On("SendEnter", "test-session", "@1").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "continue").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	output, err := server.WindowsRecoverWorkers("", "supervisor-020")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-020-1"}, output.Recovered)
	mockExec.AssertNotCalled(t, "SendEnter", "test-session", "@2")
	mockExec.AssertNotCalled(t, "SendMessage", "test-session", "@2", "continue")
}

// Bare ("supervisor") or absent callerWid keeps today's global "execute-" prefix:
// ALL execute-* workers are recovered, so the tool stays usable as a manual
// catch-all recovery (back-compat byte-identical).
func TestServer_WindowsRecoverWorkers_BareOrEmptyCaller(t *testing.T) {
	for _, callerWid := range []string{"", "supervisor"} {
		t.Run("caller="+callerWid, func(t *testing.T) {
			mockExec := new(testutil.MockTmuxExecutor)
			mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
			mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
				{TmuxWindowID: "@0", Name: "supervisor"},
				{TmuxWindowID: "@1", Name: "execute-020-1"},
				{TmuxWindowID: "@2", Name: "execute-021-1"},
			}, nil)
			mockExec.On("SendEnter", "test-session", "@1").Return(nil)
			mockExec.On("SendEnter", "test-session", "@2").Return(nil)
			mockExec.On("SendMessage", "test-session", "@1", "continue").Return(nil)
			mockExec.On("SendMessage", "test-session", "@2", "continue").Return(nil)

			server := newTestServer(mockExec, "/test/dir")
			output, err := server.WindowsRecoverWorkers("", callerWid)

			require.NoError(t, err)
			assert.Equal(t, 2, output.Count)
			assert.ElementsMatch(t, []string{"execute-020-1", "execute-021-1"}, output.Recovered)
		})
	}
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
	output, err := server.WindowsRecoverWorkers("", "")

	require.NoError(t, err)
	assert.Equal(t, 1, output.Count)
	assert.Equal(t, []string{"execute-foo"}, output.Recovered)
}

func TestServer_WindowsSpawnWorker_Success(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "SUPERVISOR_WID=supervisor") &&
			strings.Contains(s, "SELF_WID=execute-1") &&
			strings.Contains(s, "SUBTASK: audit auth") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	window, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "prior findings", "", "", "")

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

// TestWindowsSpawnWorker_SelfIdentifiesSupervisorFromUUID verifies the MaxGoals>1
// misrouting fix: at >1 goal in flight there are multiple supervisor windows
// (supervisor-045, supervisor-046, and the bare "supervisor"), and the supervisor
// LLM cannot reliably name its OWN per-goal window — a wrong supervisorWid silently
// misroutes every worker's [EXECUTE:DONE] to the wrong window. The spawn must
// override the caller-supplied name with the spawning window's REAL name, resolved
// from TMUX_WINDOW_UUID, so DONE replies always come home.
func TestWindowsSpawnWorker_SelfIdentifiesSupervisorFromUUID(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "sup-045-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "supervisor-045"},
	}, nil)
	// Self-resolution: the spawning window's UUID matches supervisor-045, NOT the
	// bare "supervisor" the caller wrongly passed.
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("bare-uuid", nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("sup-045-uuid", nil)
	// Worker is namespaced per goal (execute-045-1), derived from the resolved
	// supervisor window — not the bare execute-1.
	mockExec.On("CreateWindow", "test-session", "execute-045-1", "").Return("@2", nil)
	mockExec.On("SetWindowOption", "test-session", "@2", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@2", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@2", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@2", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	// Caller passes the WRONG name ("supervisor"); the real window is supervisor-045.
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

	require.NoError(t, err)
	assert.Contains(t, taskMessage, "SUPERVISOR_WID=supervisor-045\n",
		"worker must be told its REAL per-goal supervisor window, not the caller's guess")
	assert.NotContains(t, taskMessage, "SUPERVISOR_WID=supervisor\n",
		"must not stamp the bare wrong supervisor name")
	assert.Contains(t, taskMessage, "windows-message to supervisor-045",
		"RESPONSE PROTOCOL must route DONE to the real supervisor")
	assert.Equal(t, "execute-045-1", workerName,
		"worker window must be namespaced per goal so the MaxWorkers cap is per-supervisor")
}

// TestWindowsSpawnWorker_MaxWorkersIsPerSupervisor verifies that at MaxGoals>1 the
// MaxWorkers cap is enforced PER SUPERVISOR, not as a shared global pool: a sibling
// goal's already-running workers (execute-046-*) must NOT count against this goal's
// (supervisor-045) budget, because each goal's workers carry a distinct per-goal
// prefix and the cap is counted by prefix.
func TestWindowsSpawnWorker_MaxWorkersIsPerSupervisor(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "sup-045-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	root := t.TempDir()
	settingsDir := root + "/.tmux-cli"
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(settingsDir+"/setting.yaml", []byte("supervisor:\n  max_workers: 2\n"), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	// supervisor-045 is THIS goal; execute-046-* are a SIBLING goal already at its
	// own limit. With a global cap (prefix "execute-") this spawn would be blocked;
	// with a per-supervisor cap (prefix "execute-045-") it must succeed.
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-045"},
		{TmuxWindowID: "@1", Name: "execute-046-1"},
		{TmuxWindowID: "@2", Name: "execute-046-2"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("sup-045-uuid", nil)
	mockExec.On("CreateWindow", "test-session", "execute-045-1", "").Return("@9", nil)
	mockExec.On("SetWindowOption", "test-session", "@9", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@9", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@9", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@9", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@9", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@9", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	_, workerName, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.NoError(t, err, "sibling goal's workers must not consume this supervisor's budget")
	assert.Equal(t, "execute-045-1", workerName)
}

// TestWindowsSpawnWorker_WorkingDirectory_ThreadsToCreateWindow verifies the
// E1-1c plumbing: a non-empty workingDirectory must reach the executor's
// cwd-aware factory (CreateWindowInDir) so the worker's shell starts in the goal's
// worktree.
func TestWindowsSpawnWorker_WorkingDirectory_ThreadsToCreateWindow(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	const wt = "/repo/.tmux-cli/worktrees/goal-001"
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "validator"},
	}, nil)
	// The cwd must arrive via CreateWindowInDir, NOT the session-default CreateWindow.
	mockExec.On("CreateWindowInDir", "test-session", "inv-1", "", wt).Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, name, _, err := server.WindowsSpawnWorker("validator", "Investigate: tests", "goal.md", "run tests", "", "", "inv-", wt)

	require.NoError(t, err)
	assert.Equal(t, "inv-1", name)
	mockExec.AssertExpectations(t)
	mockExec.AssertNotCalled(t, "CreateWindow", "test-session", "inv-1", "")
}

// TestWindowsSpawnWorker_EmptyWorkingDirectory_UsesSessionDefault verifies that an
// empty workingDirectory (every pre-E1-1c caller) keeps the original
// session-default CreateWindow path — byte-identical to today, never touching the
// cwd-aware factory.
func TestWindowsSpawnWorker_EmptyWorkingDirectory_UsesSessionDefault(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, name, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-1", name)
	mockExec.AssertExpectations(t)
	mockExec.AssertNotCalled(t, "CreateWindowInDir", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestServer_WindowsSpawnWorker_EmptySupervisorWid(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("", "task", "ctx.md", "scope", "", "", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptySubtask(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "", "ctx.md", "scope", "", "", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyContextFile(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "", "scope", "", "", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_EmptyScope(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "", "", "", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_WindowsSpawnWorker_WithCustomDeliverable(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)

	customDeliverable := "- SPEC: structured specification\n- DESIGN: architecture decisions"
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, customDeliverable) &&
			!strings.Contains(s, ">=3 bullets") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "write spec", "ctx.md", "create spec", "", customDeliverable, "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-1", workerName)
	assert.Contains(t, taskMessage, customDeliverable)
	assert.NotContains(t, taskMessage, ">=3 bullets")
	assert.Contains(t, taskMessage, "RESPONSE PROTOCOL")
	assert.Contains(t, taskMessage, "sections specified in DELIVERABLE above")
	assert.NotContains(t, taskMessage, "## FINDINGS / ## RISKS / ## RECOMMENDATION / ## FILES")
}

func TestServer_WindowsSpawnWorker_WithoutDeliverable(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "FINDINGS") &&
			strings.Contains(s, "RISKS") &&
			strings.Contains(s, "RECOMMENDATION") &&
			strings.Contains(s, "FILES") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

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
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(errors.New("send failed"))
	mockExec.On("ClosePipePane", "test-session", "@1").Return(nil)
	mockExec.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	mockExec.AssertCalled(t, "ClosePipePane", "test-session", "@1")
	mockExec.AssertCalled(t, "KillWindow", "test-session", "@1")
}

func TestServer_WindowsSpawnWorker_TaskMessageFails_CleansUp(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(errors.New("delay send failed"))
	mockExec.On("ClosePipePane", "test-session", "@1").Return(nil)
	mockExec.On("KillWindow", "test-session", "@1").Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	mockExec.AssertCalled(t, "ClosePipePane", "test-session", "@1")
	mockExec.AssertCalled(t, "KillWindow", "test-session", "@1")
}

func TestServer_WindowsSpawnWorker_MaxWorkersExceeded(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
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
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxWorkersExceeded)
	assert.Contains(t, err.Error(), "2 execute-workers already running")
}

func TestServer_WindowsSpawnWorker_MaxWorkersNotExceeded(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
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
	mockExec.On("PipePane", "test-session", "@2", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@2", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@2", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	window, name, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-2", name)
	assert.NotNil(t, window)
}

func TestServer_WindowsSpawnWorker_MaxWorkersZeroUnlimited(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
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
	mockExec.On("PipePane", "test-session", "@4", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@4", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@4", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	_, name, _, err := server.WindowsSpawnWorker("supervisor", "task", "ctx.md", "scope", "", "", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-4", name)
}

func TestWindowsSpawnWorker_PipePaneCalledAfterCreate(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", "/test/dir/.tmux-cli/logs/panes/execute-1.log").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

	require.NoError(t, err)
	mockExec.AssertCalled(t, "PipePane", "test-session", "@1", "/test/dir/.tmux-cli/logs/panes/execute-1.log")
	mockExec.AssertExpectations(t)
}

func TestWindowsSpawnWorker_PipePaneError_DoesNotFailSpawn(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(fmt.Errorf("pipe-pane failed"))
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, _, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

	require.NoError(t, err, "pipe-pane error must not fail spawn")
	assert.Equal(t, "execute-1", workerName)
}

func TestWindowsSpawnWorker_PipePaneLogPath_MatchesWorkerName(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "sup-045-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-045"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("sup-045-uuid", nil)
	mockExec.On("CreateWindow", "test-session", "execute-045-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", "/test/dir/.tmux-cli/logs/panes/execute-045-1.log").Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, _, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-045-1", workerName)
	mockExec.AssertCalled(t, "PipePane", "test-session", "@1", "/test/dir/.tmux-cli/logs/panes/execute-045-1.log")
}

func TestWindowsSpawnWorker_PipePaneLogDir_Created(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, root)
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "")

	require.NoError(t, err)
	logDir := filepath.Join(root, ".tmux-cli", "logs", "panes")
	info, statErr := os.Stat(logDir)
	require.NoError(t, statErr, ".tmux-cli/logs/panes/ directory must be created")
	assert.True(t, info.IsDir())
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

	out, err := server.TasksValidate(TasksValidateInput{})
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

	out, err := server.TasksValidate(TasksValidateInput{})
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

	_, err := server.TasksValidate(TasksValidateInput{})
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

// TestTasksValidate_GoalScoped proves a set GoalID validates the per-goal
// fan-out file, not the top-level planning-queue.
func TestTasksValidate_GoalScoped(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	// Invalid top-level file (extra field) — must be ignored when GoalID is set.
	topDir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(topDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tasks.yaml"), []byte(`status: ready
cycle: 1
tasks:
  - name: "bloated"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    scope: "inline"
`), 0o644))

	// Valid per-goal file.
	goalDir := filepath.Join(root, ".tmux-cli", "goals", "g1")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "tasks.yaml"), []byte(`status: ready
cycle: 1
tasks:
  - name: "clean task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`), 0o644))

	out, err := server.TasksValidate(TasksValidateInput{GoalID: "g1"})
	require.NoError(t, err)
	assert.True(t, out.Valid, "per-goal file is valid; invalid top-level must be ignored")
	assert.Empty(t, out.Errors)
}

// TestTasksValidate_TopLevelDefault proves an empty GoalID validates the
// top-level planning-queue exactly as before.
func TestTasksValidate_TopLevelDefault(t *testing.T) {
	root := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, root)

	topDir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(topDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tasks.yaml"), []byte(`status: ready
cycle: 1
tasks:
  - name: "clean task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`), 0o644))

	out, err := server.TasksValidate(TasksValidateInput{})
	require.NoError(t, err)
	assert.True(t, out.Valid)
	assert.Empty(t, out.Errors)
}

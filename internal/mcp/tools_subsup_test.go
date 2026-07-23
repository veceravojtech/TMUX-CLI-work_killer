package mcp

// Tests for the windows-spawn-supervisor tool (depth-1 sub-supervisor
// delegation: supervisor → supervisor-task-N → execute-task-N-M) and its
// routing/validation seams: parseSubsupBinding, the resolveResearchRoot subsup
// branch, and the tasks-validate subsup_id path.

import (
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

func TestServer_WindowsSpawnSupervisor_Success(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "supervisor-task-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_MODEL").Return("", nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_FLAGS").Return("", nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@1", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:supervisor:new").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "PARENT_WID=supervisor") &&
			strings.Contains(s, "SELF_WID=supervisor-task-1") &&
			strings.Contains(s, "SUBSUP_ID=task-1") &&
			strings.Contains(s, "SUBTREE: build auth module") &&
			strings.Contains(s, "RESPONSE PROTOCOL")
	})).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	window, subsupName, taskMessage, err := server.WindowsSpawnSupervisor("supervisor", "build auth module", "ctx.md", "implement login + JWT middleware", "prior findings", "", "")

	require.NoError(t, err)
	assert.Equal(t, "supervisor-task-1", subsupName)
	assert.Equal(t, "supervisor-task-1", window.Name)
	assert.Equal(t, "@1", window.TmuxWindowID)
	// The subtree message speaks the SUBSUP protocol, not the worker EXECUTE one.
	assert.Contains(t, taskMessage, "[SUBSUP:DONE wid=supervisor-task-1 sup=supervisor file=")
	assert.Contains(t, taskMessage, "SUBSUP:ESCALATE")
	assert.Contains(t, taskMessage, "SYNTHESIS")
	assert.Contains(t, taskMessage, ".tmux-cli/subsup/task-1/research")
	assert.Contains(t, taskMessage, "NEVER spawn another sub-supervisor")
	assert.NotContains(t, taskMessage, "[EXECUTE:DONE")
	mockExec.AssertExpectations(t)
}

// TestWindowsSpawnSupervisor_DepthCap verifies the one-level rule: a caller
// that IS a sub-supervisor (resolved from its window UUID) is refused — no
// supervisor-task window may spawn another.
func TestWindowsSpawnSupervisor_DepthCap(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "subsup-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "supervisor-task-1"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("parent-uuid", nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("subsup-uuid", nil)

	server := newTestServer(mockExec, "/test/dir")
	// Caller lies ("supervisor") but the UUID resolves to supervisor-task-1.
	_, _, _, err := server.WindowsSpawnSupervisor("supervisor", "subtask", "ctx.md", "scope", "", "", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "depth cap")
}

// TestWindowsSpawnSupervisor_GoalSupervisorRefused verifies the standalone-only
// bound: a daemon-dispatched goal supervisor (supervisor-<ns>, digits-only
// namespace) may not spawn sub-supervisors — goal-sized delegation goes
// through the escalation.md relay.
func TestWindowsSpawnSupervisor_GoalSupervisorRefused(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "sup-045-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-045"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("sup-045-uuid", nil)

	server := newTestServer(mockExec, "/test/dir")
	_, _, _, err := server.WindowsSpawnSupervisor("supervisor-045", "subtask", "ctx.md", "scope", "", "", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "standalone-only")
}

// TestWindowsSpawnSupervisor_CapSharedMaxWorkers verifies concurrent
// sub-supervisors are bounded by supervisor.max_workers counted over the
// supervisor-task- prefix (per-prefix, same mechanic as the worker cap).
func TestWindowsSpawnSupervisor_CapSharedMaxWorkers(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".tmux-cli", "setting.yaml"), []byte("supervisor:\n  max_workers: 2\n"), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", root).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "supervisor-task-1"},
		{TmuxWindowID: "@2", Name: "supervisor-task-2"},
	}, nil)

	server := newTestServer(mockExec, root)
	_, _, _, err := server.WindowsSpawnSupervisor("supervisor", "subtask", "ctx.md", "scope", "", "", "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxWorkersExceeded)
}

// TestParseSubsupBinding pins the binding parser: subsup windows and their
// namespaced workers resolve to "task-N"; every goal-namespaced or bare form
// stays unbound (disjointness with parseGoalBinding).
func TestParseSubsupBinding(t *testing.T) {
	cases := []struct {
		win string
		id  string
		ok  bool
	}{
		{"supervisor-task-1", "task-1", true},
		{"supervisor-task-12", "task-12", true},
		{"execute-task-3-2", "task-3", true},
		{"supervisor", "", false},
		{"supervisor-045", "", false}, // goal supervisor
		{"execute-045-1", "", false},  // goal worker
		{"execute-1", "", false},      // bare worker
		{"validator-045", "", false},
		{"supervisor-task-", "", false},
	}
	for _, c := range cases {
		id, ok := parseSubsupBinding(c.win)
		assert.Equal(t, c.ok, ok, c.win)
		assert.Equal(t, c.id, id, c.win)
	}

	// Disjointness both ways: subsup names must never resolve as goal bindings.
	_, _, goalOK := parseGoalBinding("supervisor-task-1")
	assert.False(t, goalOK, "supervisor-task-1 must not parse as a goal binding")
	_, _, goalOK = parseGoalBinding("execute-task-3-2")
	assert.False(t, goalOK, "execute-task-3-2 must not parse as a goal binding")
}

// TestResolveResearchRoot_SubsupBinding verifies subsup callers route to the
// delegation's own state root — and win over a stale global current-goal
// marker, which must never pull a standalone sub-supervisor's reports into a
// goal folder.
func TestResolveResearchRoot_SubsupBinding(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".tmux-cli"), 0o755))
	// Stale marker from some earlier goal run.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".tmux-cli", "taskvisor-current-goal"), []byte("goal-007\n"), 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), root)

	assert.Equal(t, filepath.Join(".tmux-cli", "subsup", "task-1", "research"),
		server.resolveResearchRoot("supervisor-task-1"))
	assert.Equal(t, filepath.Join(".tmux-cli", "subsup", "task-1", "research"),
		server.resolveResearchRoot("execute-task-1-3"),
		"a sub-supervisor's worker must save into the same subsup research root")
	// Goal-namespaced callers keep their goal routing untouched.
	assert.Equal(t, filepath.Join(".tmux-cli", "goals", "goal-045", "research"),
		server.resolveResearchRoot("supervisor-045"))
}

// TestServer_TasksValidate_SubsupPath verifies subsup_id routes validation to
// .tmux-cli/subsup/<id>/tasks.yaml and that the subsup worker wid form passes.
func TestServer_TasksValidate_SubsupPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli", "subsup", "task-1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	yaml := `status: ready
cycle: 1
tasks:
  - name: "implement login"
    wid: execute-task-1-1
    status: pending
    context: ".tmux-cli/subsup/task-1/research/task-login.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yaml), 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), root)
	out, err := server.TasksValidate(TasksValidateInput{SubsupID: "task-1"})
	require.NoError(t, err)
	assert.True(t, out.Valid, "errors: %v", out.Errors)
}

func TestServer_TasksValidate_SubsupID_Invalid(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), t.TempDir())
	_, err := server.TasksValidate(TasksValidateInput{SubsupID: "../../etc"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestServer_TasksValidate_GoalAndSubsupMutuallyExclusive(t *testing.T) {
	server := newTestServer(new(testutil.MockTmuxExecutor), t.TempDir())
	_, err := server.TasksValidate(TasksValidateInput{GoalID: "goal-001", SubsupID: "task-1"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestWindowsSpawnWorker_SubsupCallerNamespacesWorkers verifies the existing
// role-prefix derivation gives a sub-supervisor its own worker namespace: a
// supervisor-task-1 caller spawns execute-task-1-N workers, so its pool and
// MaxWorkers budget never collide with the parent's execute-N pool.
func TestWindowsSpawnWorker_SubsupCallerNamespacesWorkers(t *testing.T) {
	os.Setenv("TMUX_WINDOW_UUID", "subsup-uuid")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", "/test/dir").Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "supervisor-task-1"},
		{TmuxWindowID: "@2", Name: "execute-1"}, // parent's own worker — different prefix
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@0", "window-uuid").Return("parent-uuid", nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("subsup-uuid", nil)
	mockExec.On("CreateWindow", "test-session", "execute-task-1-1", "").Return("@3", nil)
	mockExec.On("SetWindowOption", "test-session", "@3", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@3", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_MODEL").Return("", nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_FLAGS").Return("", nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@3", mock.Anything).Return("", nil)
	mockExec.On("PipePane", "test-session", "@3", mock.Anything).Return(nil)
	mockExec.On("SendMessage", "test-session", "@3", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@3", mock.Anything).Return(nil)

	server := newTestServer(mockExec, "/test/dir")
	_, workerName, taskMessage, err := server.WindowsSpawnWorker("supervisor-task-1", "implement login", "ctx.md", "scope", "", "", "", "", "")

	require.NoError(t, err)
	assert.Equal(t, "execute-task-1-1", workerName)
	assert.Contains(t, taskMessage, "SUPERVISOR_WID=supervisor-task-1\n",
		"the child's workers must reply to the sub-supervisor, not the parent")
	assert.Contains(t, taskMessage, ".tmux-cli/subsup/task-1/research",
		"worker reports must land in the subsup research root")
}

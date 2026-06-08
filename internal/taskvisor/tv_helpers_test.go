package taskvisor

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testSession = "test-session"

func setupDaemon(t *testing.T) (*Daemon, *testutil.MockTmuxExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	exec.On("ClosePipePane", mock.Anything, mock.Anything).Return(nil).Maybe()
	d := New(dir, exec)
	d.pollInterval = 50 * time.Millisecond
	d.promptSettleDelay = 0
	d.promptPollInterval = 0
	// Disable the P2 progress heartbeat by default so existing tests stay focused
	// on the timeout/crash/verdict paths and remain byte-identical (no extra
	// ListWindows/CaptureWindowOutput per sig==nil tick). Heartbeat tests opt in by
	// setting d.progressTimeout (and an injectable d.clock) explicitly.
	d.progressTimeout = 0
	// Disable the P3 wall-clock ceiling by default for the same reason: New() now
	// seeds it to 4h, but most tests use a zero activatedAt with the real clock
	// (elapsed ≈ now-year-1 ≫ 4h), which would spuriously halt every modeActive
	// tick. Wall-clock tests opt in by setting d.maxWallClock (and d.clock) explicitly.
	d.maxWallClock = 0
	return d, exec, dir
}

func writeGoals(t *testing.T, dir string, gf *GoalsFile) {
	t.Helper()
	require.NoError(t, SaveGoals(dir, gf))
}

func writeStartSignal(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, ".tmux-cli", "taskvisor-start")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, nil, 0o644))
}

func writeSettings(t *testing.T, dir string, autoApprove, autoExecute bool) {
	t.Helper()
	content := fmt.Sprintf(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
  max_workers: 4
  cycle_delay: 5
  unplanned_audit: true
plan:
  auto_approve: %v
  auto_execute: %v
sudo:
  timeout: 30
taskvisor:
  dispatch_timeout: 3600
  validate_timeout: 300
  poll_interval: 0
`, autoApprove, autoExecute)
	p := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func writeGuardFile(t *testing.T, dir string) {
	t.Helper()
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	require.NoError(t, os.MkdirAll(filepath.Dir(guardPath), 0o755))
	require.NoError(t, os.WriteFile(guardPath, nil, 0o644))
}

func mockCreateWindowFn(tmuxWindowID string) WindowCreateFunc {
	return func(name, command, cwd string) (*CreatedWindow, error) {
		return &CreatedWindow{TmuxWindowID: tmuxWindowID, Name: name}, nil
	}
}

// setupDeactivateMocks programs the ListWindows sequence for deactivate() and
// deactivateOnCompletion(). Covers: notifyCompletion (1 ListWindows + SendMessage
// calls), teardownGoalWindows (4 kill lookups + collectManagedNames +
// waitWindowsGone), and ensureWindow0Supervisor (deactivate() only).
func setupDeactivateMocks(exec *testutil.MockTmuxExecutor, session, newWindowID string) {
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: newWindowID, Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", session, newWindowID, mock.Anything).Return(nil).Maybe()
}

// setupDispatchMocks programs the ListWindows sequence one dispatch() makes. The
// goal supervisor window is ALWAYS namespaced now; supName (default "supervisor-001",
// the dominant test goal) names the window waitClaudeBoot/waitForPrompt resolve —
// pass it explicitly for any non-goal-001 dispatch.
func setupDispatchMocks(exec *testutil.MockTmuxExecutor, session, newWindowID string, supName ...string) {
	name := "supervisor-001"
	if len(supName) > 0 {
		name = supName[0]
	}
	// 4 calls for kill lookups (execute-<ns>-, supervisor-<ns>, validator-<ns>, inv-<ns>-)
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Times(4)
	// 1 call for collectManagedNames
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitWindowsGone
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitClaudeBoot
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: newWindowID, Name: name, CurrentCommand: "claude"},
	}, nil)
	// 1 call for waitForPrompt (prompt detected immediately)
	exec.On("CaptureWindowOutput", session, newWindowID).Return("some output ❯ ", nil)
	exec.On("SendMessage", session, newWindowID, mock.Anything).Return(nil)
}

func writeAllRuntimeMarkers(t *testing.T, dir string) {
	t.Helper()
	tmuxDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	for _, name := range []string{"taskvisor-current-goal", "taskvisor-current-cycle", "taskvisor-current-worktree", "taskvisor-active"} {
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, name), nil, 0o644))
	}
}

func assertAllRuntimeMarkersAbsent(t *testing.T, dir string) {
	t.Helper()
	tmuxDir := filepath.Join(dir, ".tmux-cli")
	for _, name := range []string{"taskvisor-current-goal", "taskvisor-current-cycle", "taskvisor-current-worktree", "taskvisor-active"} {
		_, err := os.Stat(filepath.Join(tmuxDir, name))
		assert.True(t, os.IsNotExist(err), "%s should be removed", name)
	}
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func setupValidatorMocks(exec *testutil.MockTmuxExecutor, session, validatorWindowID string, valName ...string) {
	name := "validator-001"
	if len(valName) > 0 {
		name = valName[0]
	}
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: validatorWindowID, Name: name, CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", session, validatorWindowID).Return("❯ ", nil)
	exec.On("SendMessage", session, validatorWindowID, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origOutput) })
	fn()
	return buf.String()
}

// routeGoal is a one-goal GoalsFile with explicit per-class budgets, used by the
// C2-routing verdict tests so each test states its starting budgets inline.
func routeGoal(id string, code, spec, val, block int) Goal {
	return Goal{
		ID: id, Description: "test", Status: GoalRunning,
		StartedAt: "2026-05-20T10:00:00Z",
		Retries:   0, MaxRetries: 9,
		CodeRetries: code, MaxCodeRetries: code,
		SpecRetries: spec, MaxSpecRetries: spec,
		ValidationRetries: val, MaxValidationRetries: val,
		BlockRetries: block, MaxBlockRetries: block,
		StuckRetries: 3, MaxStuckRetries: 3,
	}
}

// noWindows makes killWindowByName/killWindowsByPrefix no-ops (empty session).
func noWindows(exec *testutil.MockTmuxExecutor) {
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
}

func writeTasksYaml(t *testing.T, dir string, content string) {
	t.Helper()
	p := filepath.Join(dir, ".tmux-cli", "tasks.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// writeGoalTasksYaml writes the per-goal fan-out file at
// .tmux-cli/goals/<goalID>/tasks.yaml — the path the daemon reads in goal mode.
func writeGoalTasksYaml(t *testing.T, dir, goalID, content string) {
	t.Helper()
	p := tasks.GoalTasksFilePath(dir, goalID)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func writeTaskContext(t *testing.T, dir, relPath, content string) {
	t.Helper()
	p := filepath.Join(dir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func setupDeactivateOnCompletionMocks(exec *testutil.MockTmuxExecutor, session string) {
	// notifySupervisor calls (GOAL-DONE per done goal + ALL-COMPLETE) + teardown
	// (4 kill lookups + collectManagedNames + waitWindowsGone). Count varies by
	// goal mix so use an unbounded return for all empty-list ListWindows calls.
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil)
}

// countingCreateWindowFn returns a WindowCreateFunc that increments *count on
// each call, so precondition-block tests can assert no worker window is spawned.
func countingCreateWindowFn(count *int, tmuxWindowID string) WindowCreateFunc {
	return func(name, command, cwd string) (*CreatedWindow, error) {
		*count++
		return &CreatedWindow{TmuxWindowID: tmuxWindowID, Name: name}, nil
	}
}

// rerunValidationMocks wires the validator re-spawn path used by the error and
// unsubstantiated-spec-defect routes: the validator window is present (killed by
// the :1275 pre-kill and again no-op inside rerunValidationOnly), then a fresh
// validator is created and sent the /tmux:investigate command. Modeled on
// TestErrorVerdict_ReRunsValidationOnly. Returns a pointer to the captured
// commands so the caller can assert the planner/implementer are NOT re-dispatched.
func rerunValidationMocks(d *Daemon, exec *testutil.MockTmuxExecutor) *[]string {
	d.validatorSendDelay = 0
	d.createWindowFn = mockCreateWindowFn("@5")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("ready ❯ ", nil)
	sentCmds := &[]string{}
	exec.On("SendMessage", testSession, "@5", mock.Anything).Run(func(args mock.Arguments) {
		*sentCmds = append(*sentCmds, args.Get(2).(string))
	}).Return(nil)
	return sentCmds
}

// writeSettingsMaxGoals writes a setting.yaml identical to writeSettings but with
// an explicit supervisor.max_goals, so d.maxGoals() (which reads setting.yaml)
// returns the multi-goal bound under test.
func writeSettingsMaxGoals(t *testing.T, dir string, maxGoals int) {
	t.Helper()
	content := fmt.Sprintf(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
  max_workers: 4
  cycle_delay: 5
  unplanned_audit: true
  max_goals: %d
plan:
  auto_approve: true
  auto_execute: true
sudo:
  timeout: 30
taskvisor:
  dispatch_timeout: 3600
  validate_timeout: 300
  poll_interval: 0
`, maxGoals)
	p := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// setupNamespacedDispatchMocks programs the exact ListWindows sequence one
// dispatch consumes for a per-goal namespaced supervisor window at MaxGoals>1:
// 6 empty (4 kill lookups + collectManagedNames + waitWindowsGone) then 2 returning
// the booted supervisor window (waitClaudeBoot + waitForPrompt's findWindowByName).
func setupNamespacedDispatchMocks(exec *testutil.MockTmuxExecutor, session, supName, winID string) {
	empty := []tmux.WindowInfo{}
	claude := []tmux.WindowInfo{{TmuxWindowID: winID, Name: supName, CurrentCommand: "claude"}}
	exec.On("ListWindows", session).Return(empty, nil).Times(6)
	exec.On("ListWindows", session).Return(claude, nil).Times(2)
}

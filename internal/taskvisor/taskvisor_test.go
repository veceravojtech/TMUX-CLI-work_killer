package taskvisor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/setup"
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

func TestNew_Defaults(t *testing.T) {
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	d := New(dir, exec)

	assert.Equal(t, modeIdle, d.mode)
	assert.Equal(t, 10*time.Second, d.pollInterval)
	assert.NotNil(t, d.executor)
}

func TestRun_IdlePolling(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettings(t, dir, true, true)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := d.Run(ctx)
	assert.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)
}

func TestRun_ActivateOnSignal(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)
	writeGoals(t, dir, &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	})
	writeStartSignal(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := d.Run(ctx)
	assert.NoError(t, err)
	assert.Equal(t, modeActive, d.mode)

	_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-start"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRun_ContextCancellation(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettings(t, dir, true, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := d.Run(ctx)
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}

func TestActivate_WritesGuardFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.activate(gf)
	require.NoError(t, err)

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.NoError(t, statErr)
}

func TestActivate_EnforcesAutoApprove(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, false, false)
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.activate(gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "auto_approve: true")
	assert.Contains(t, string(data), "auto_execute: true")
}

func TestActivate_SetsCurrentGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "first", Status: GoalPending},
			{ID: "goal-002", Description: "second", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.activate(gf)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", gf.CurrentGoal)
}

func TestActivate_KillsExistingWindows(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	// The startup sweep kills only goal-001's NAMESPACED leftover windows; the
	// human's window-0 bare "supervisor" (@0) must never be swept.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@s", Name: "supervisor-001"},
		{TmuxWindowID: "@1", Name: "execute-001-1"},
		{TmuxWindowID: "@3", Name: "execute-001-3"},
		{TmuxWindowID: "@4", Name: "validator-001"},
	}, nil)
	exec.On("KillWindow", testSession, "@s").Return(nil)
	exec.On("KillWindow", testSession, "@1").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)
	exec.On("KillWindow", testSession, "@4").Return(nil)

	err := d.activate(gf)
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@s")
	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
	exec.AssertCalled(t, "KillWindow", testSession, "@4")
	exec.AssertNotCalled(t, "KillWindow", testSession, "@0")
}

func TestActivate_NoWindowsToKill(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)
	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.activate(gf)
	require.NoError(t, err)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestDeactivate_KillsAllManagedWindows(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	d.currentGoal = "goal-001"
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Kill lookups find goal-001's NAMESPACED windows; the human's window-0 bare
	// "supervisor" (@0) is present but must never be killed.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@s", Name: "supervisor-001"},
		{TmuxWindowID: "@2", Name: "execute-001-2"},
		{TmuxWindowID: "@3", Name: "validator-001"},
	}, nil).Times(4)
	exec.On("KillWindow", testSession, "@s").Return(nil)
	exec.On("KillWindow", testSession, "@2").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)
	// collectManagedNames + waitWindowsGone — namespaced windows gone after kills
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	// ensureWindow0Supervisor existence check: window-0 "supervisor" still live → no-op
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	err := d.deactivate()
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@s")
	exec.AssertCalled(t, "KillWindow", testSession, "@2")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
	exec.AssertNotCalled(t, "KillWindow", testSession, "@0")
}

func TestDeactivate_WaitsForWindowsGone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	d.currentGoal = "goal-001"
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Kill lookups find the goal's namespaced supervisor-001
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@s", Name: "supervisor-001"},
	}, nil).Times(4)
	exec.On("KillWindow", testSession, "@s").Return(nil)
	// collectManagedNames
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitWindowsGone — first poll still has the namespaced supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@s", Name: "supervisor-001"},
	}, nil).Once()
	// waitWindowsGone — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// ensureWindow0Supervisor existence check: window-0 present → no-op
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	err := d.deactivate()
	require.NoError(t, err)
}

func TestDeactivate_CreatesFreshSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// No window-0 "supervisor" is live (teardown + existence check all empty), so
	// ensureWindow0Supervisor creates a BARE "supervisor" — never a namespaced one.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	var createdName string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createdName = name
		return &CreatedWindow{TmuxWindowID: "@0", Name: name}, nil
	})

	err := d.deactivate()
	require.NoError(t, err)
	assert.Equal(t, "supervisor", createdName)
}

// TestDeactivate_PreservesWindow0Supervisor pins the P1 invariant: deactivate()
// NEVER kills or recreates a live window-0 "supervisor", and never spawns a
// namespaced supervisor-<ns>. When window-0 is live it is a pure no-op (no
// createWindow); when absent it creates a BARE "supervisor".
func TestDeactivate_PreservesWindow0Supervisor(t *testing.T) {
	t.Run("live window-0 supervisor is left untouched (no create)", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.mode = modeActive
		d.session = testSession
		d.currentGoal = "goal-001"
		writeSettings(t, dir, true, true)
		writeGuardFile(t, dir)

		// Teardown lookups empty; existence check + everything after sees a live
		// bare window-0 "supervisor".
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(6)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		}, nil)

		var created []string
		d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
			created = append(created, name)
			return &CreatedWindow{TmuxWindowID: "@x", Name: name}, nil
		})

		require.NoError(t, d.deactivate())
		assert.Empty(t, created, "a live window-0 supervisor must be a no-op — never recreated")
		exec.AssertNotCalled(t, "KillWindow", testSession, "@0")
	})

	t.Run("absent window-0 supervisor is created bare, never namespaced", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.mode = modeActive
		d.session = testSession
		d.currentGoal = "goal-001"
		writeSettings(t, dir, true, true)
		writeGuardFile(t, dir)

		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		}, nil)

		var created []string
		d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
			created = append(created, name)
			return &CreatedWindow{TmuxWindowID: "@0", Name: name}, nil
		})

		require.NoError(t, d.deactivate())
		assert.Equal(t, []string{"supervisor"}, created,
			"absent window-0 ⇒ create exactly one BARE supervisor (never supervisor-<ns>)")
	})
}

func TestDeactivate_WaitsForClaudeBoot(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Teardown (4 kills + collectManagedNames + waitWindowsGone) + existence check
	// all empty → window-0 absent → ensureWindow0Supervisor creates + boots it.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
	// waitClaudeBoot — first poll zsh
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil).Once()
	// waitClaudeBoot — claude
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)
}

func TestDeactivate_RemovesGuardFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestDeactivate_ReturnsToIdle(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)
}

func TestDispatch_WritesDispatchMd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Fix pricing",
			Acceptance:  []string{"Price matches API"},
			Validate:    []string{"run pricing test"},
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	goal := &gf.Goals[0]
	err = d.dispatch(goal, gf)
	require.NoError(t, err)

	dispatchPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	data, err := os.ReadFile(dispatchPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Price matches API")
	assert.NotContains(t, string(data), "run pricing test")
}

func TestDispatch_WritesCurrentGoalFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"))
	require.NoError(t, err)
	assert.Equal(t, "goal-001", string(data))
}

func TestDispatch_KillWaitCreateBootSend(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	callOrder := make([]string, 0, 10)

	// Call 1: killWindowByName("supervisor") — finds supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@0").Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "kill")
	})
	// Calls 2-3: killWindowsByPrefix("execute-") and killWindowByName("validator") — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	// Call 4: collectManagedNames — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 5: waitWindowsGone — still has supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
	}, nil).Once()
	// Call 6: waitWindowsGone — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 7: waitClaudeBoot — zsh
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "zsh"},
	}, nil).Once()
	// Call 8+: waitClaudeBoot — claude
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	// waitForPrompt — prompt detected
	exec.On("CaptureWindowOutput", testSession, "@1").Return("❯ ", nil)

	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		callOrder = append(callOrder, "create")
		return &CreatedWindow{TmuxWindowID: "@1", Name: name}, nil
	})

	exec.On("SendMessage", testSession, "@1", mock.MatchedBy(func(msg string) bool {
		return strings.HasPrefix(msg, "/tmux:plan")
	})).Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "send")
	})

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	killIdx := indexOf(callOrder, "kill")
	createIdx := indexOf(callOrder, "create")
	sendIdx := indexOf(callOrder, "send")
	require.NotEqual(t, -1, killIdx, "kill should have been called")
	require.NotEqual(t, -1, createIdx, "create should have been called")
	require.NotEqual(t, -1, sendIdx, "send should have been called")
	assert.Greater(t, createIdx, killIdx, "create must come after kill")
	assert.Greater(t, sendIdx, createIdx, "send must come after create")
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func TestDispatch_SetsRunningStatus(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	goal := &gf.Goals[0]
	err = d.dispatch(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalRunning, goal.Status)

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status)
}

func TestDispatch_RecordsDispatchTime(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	before := time.Now()
	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").dispatchTime, time.Second)
	assert.True(t, d.runtime("goal-001").dispatchTime.After(before) || d.runtime("goal-001").dispatchTime.Equal(before))
}

func TestTick_PendingGoalDispatches(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	err = d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
}

func TestTick_RunningGoalSkipped(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	}
	writeGoals(t, dir, gf)

	ctx := context.Background()
	err := d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
}

func TestTick_AllDoneDeactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
			{ID: "goal-002", Description: "also done", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateOnCompletionMocks(exec, testSession)

	ctx := context.Background()
	err := d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	assert.FileExists(t, reportPath)
}

func TestTick_MixedDoneAndFailed(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
			{ID: "goal-002", Description: "failed", Status: GoalFailed},
			{ID: "goal-003", Description: "done too", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateOnCompletionMocks(exec, testSession)

	ctx := context.Background()
	err := d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	assert.FileExists(t, reportPath)
}

func TestKillWindowByName_Found(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("KillWindow", testSession, "@0").Return(nil)

	err := d.killWindowByName("supervisor")
	require.NoError(t, err)
	exec.AssertCalled(t, "KillWindow", testSession, "@0")
}

func TestKillWindowByName_NotFound(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.killWindowByName("foo")
	assert.NoError(t, err)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestKillWindowsByPrefix_MatchesMultiple(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@3", Name: "execute-3"},
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("KillWindow", testSession, "@1").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)

	err := d.killWindowsByPrefix("execute-")
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
	exec.AssertNotCalled(t, "KillWindow", testSession, "@0")
}

func TestKillWindowsByPrefix_NoMatches(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.killWindowsByPrefix("execute-")
	assert.NoError(t, err)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestWaitWindowsGone_ImmediateSuccess(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, time.Second)
	assert.NoError(t, err)
}

func TestWaitWindowsGone_EventualSuccess(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, 2*time.Second)
	assert.NoError(t, err)
}

func TestWaitWindowsGone_Timeout(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, 200*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestWaitClaudeBoot_ImmediateBoot(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitClaudeBoot_EventualBoot(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitClaudeBoot_Timeout(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 200*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestWaitClaudeBoot_WindowNotFound(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitClaudeBoot("supervisor", time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestWriteDispatchMd_FirstAttempt(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
	}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "None (first attempt)")
	assert.Contains(t, content, "Price matches API")
}

func TestWriteDispatchMd_WithCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	correctionsDir := filepath.Join(goalDir, "corrections")
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-1.md"), []byte("First correction"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-2.md"), []byte("Second correction"), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "First correction")
	assert.Contains(t, content, "Second correction")
	assert.NotContains(t, content, "None (first attempt)")
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

func TestCheckProgress_SupervisorDone_TransitionsToValidating(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") — no workers
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// killWindowByName("supervisor") — no supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitClaudeBoot("validator") + validator mocks
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, "done", d.runtime("goal-001").lastSupervisorStatus)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.False(t, d.runtime("goal-001").validateTime.IsZero())

	sig, err := LoadSignal(dir, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestCheckProgress_SupervisorStopped_TransitionsToValidating(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "stopped", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, "stopped", d.runtime("goal-001").lastSupervisorStatus)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	sig, err := LoadSignal(dir, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestCheckProgress_Supervising_NoSignalWithinTimeout(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.runtime("goal-001").dispatchTime = time.Now().Add(-10 * time.Minute)
	d.dispatchTimeout = time.Hour

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3}},
	}
	writeGoals(t, dir, gf)

	goal := &gf.Goals[0]
	err := d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalRunning, goal.Status)
}

func TestCheckProgress_Supervising_TimeoutExceeded(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.dispatchTimeout = 3600 * time.Second
	d.runtime("goal-001").dispatchTime = time.Now().Add(-3601 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(correctionPath)
	assert.NoError(t, statErr)
}

func TestCheckProgress_Supervising_CrashDetected(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.dispatchTimeout = time.Hour
	d.runtime("goal-001").dispatchTime = time.Now().Add(-10 * time.Second)
	d.runtime("goal-001").bootConfirmedAt = time.Now().Add(-6 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001", CurrentCommand: "zsh"},
	}, nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)
}

func TestCheckProgress_ValidatorPass_GoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalDone, goal.Status)
	assert.Equal(t, "goal-002", gf.CurrentGoal)

	sig, err := LoadSignal(dir, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)

	exec.AssertCalled(t, "KillWindow", testSession, "@5")
}

func TestCheckProgress_ValidatorFail_CorrectionDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix price calc", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.True(t, strings.HasPrefix(string(data), "Implementation completed but failed acceptance criteria."))
	assert.Contains(t, string(data), "fix price calc")
}

func TestCheckProgress_ValidatorFail_CorrectionStopped(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "stopped"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 1, MaxRetries: 3, CodeRetries: 2, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "finish booking page", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.CodeRetries, "code budget 3->2->1")
	assert.Equal(t, GoalPending, goal.Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-2.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.True(t, strings.HasPrefix(string(data), "Previous cycle hit the supervisor cycle limit"))
	assert.Contains(t, string(data), "finish booking page")
}

func TestCheckProgress_ValidatorFail_RetriesExhausted(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 2, MaxRetries: 3, CodeRetries: 1, MaxCodeRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "give up", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 0, goal.CodeRetries, "code budget exhausted")
	assert.Equal(t, GoalFailed, goal.Status)
	assert.Equal(t, "goal-002", gf.CurrentGoal)
}

func TestCheckProgress_Validating_TimeoutExceeded(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.validateTimeout = 300 * time.Second
	d.runtime("goal-001").validateTime = time.Now().Add(-301 * time.Second)
	d.createWindowFn = mockCreateWindowFn("@5")

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, ValidationRetries: 2, MaxValidationRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// A validation timeout is a validator error: re-run validation only.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	// Not a code-defect failed cycle: code retry counter untouched, no correction.
	assert.Equal(t, 0, goal.Retries)
	assert.Equal(t, 1, goal.ValidationRetries)
	assert.Equal(t, GoalRunning, goal.Status)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(correctionPath)
	assert.True(t, os.IsNotExist(statErr), "timeout must not write a code-defect correction")
}

func TestCheckProgress_Validating_CrashDetected(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.validateTimeout = 5 * time.Minute
	d.runtime("goal-001").validateTime = time.Now().Add(-10 * time.Second)
	d.runtime("goal-001").bootConfirmedAt = time.Now().Add(-6 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "zsh"},
	}, nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)
}

func TestWriteCorrectionFile_DoneHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	// No structured findings → fallback writes NextAction verbatim (the call site
	// primes it with the daemon framing header).
	sig := &ValidatorSignal{NextAction: "Implementation completed but failed acceptance criteria.\n\nfix the pricing"}
	err := d.writeCorrectionFile(goalDir, 1, sig)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "Implementation completed but failed acceptance criteria."))
	assert.Contains(t, content, "fix the pricing")
}

func TestWriteCorrectionFile_StoppedHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	header := "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup."
	sig := &ValidatorSignal{NextAction: header + "\n\nfinish booking page"}
	err := d.writeCorrectionFile(goalDir, 2, sig)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-2.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "Previous cycle hit the supervisor cycle limit"))
	assert.Contains(t, content, "finish booking page")
}

func TestWriteCorrectionFile_CreatesDirectory(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")

	err := d.writeCorrectionFile(goalDir, 1, &ValidatorSignal{NextAction: "content"})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "content")
}

// TestWriteCorrectionFile_StructuredPerFinding asserts that each non-pass
// finding is emitted as its own structured ### Finding block with
// Command/Output/Expected/Correction lines, and that pass findings are omitted.
func TestWriteCorrectionFile_StructuredPerFinding(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	sig := &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestTotal",
				OutputExcerpt:  "want 1000 got 100",
				ExpectedState:  "total in cents matches the API",
				Correction:     "multiply dollars by 100 before formatting",
			},
			{
				Rule: "currency-format", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestLocale",
				OutputExcerpt:  "want 1.000,00 got 1,000.00",
				ExpectedState:  "locale-aware currency formatting",
				Correction:     "use the locale formatter for the active request",
			},
			{Rule: "smoke", Status: "pass"},
		},
		NextAction: "should not appear when structured findings exist",
	}

	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig))

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)

	// Both non-pass findings produce a structured block.
	assert.Contains(t, content, "### Finding: price-calc")
	assert.Contains(t, content, "### Finding: currency-format")
	assert.Contains(t, content, "Command: go test ./pricing -run TestTotal")
	assert.Contains(t, content, "Output: want 1000 got 100")
	assert.Contains(t, content, "Expected: total in cents matches the API")
	assert.Contains(t, content, "Correction: multiply dollars by 100 before formatting")
	assert.Contains(t, content, "Command: go test ./pricing -run TestLocale")
	assert.Contains(t, content, "Correction: use the locale formatter for the active request")

	// Pass finding is omitted and the NextAction one-liner is not used.
	assert.NotContains(t, content, "### Finding: smoke")
	assert.NotContains(t, content, "should not appear when structured findings exist")
}

// TestBounceToGeneration_ForwardsValidatorFindings asserts that a blocked/planner
// (spec-defect) verdict writes the validator's REAL per-finding detail into
// corrections/cycle-N.md, with the SPEC-DEFECT framing header prepended ABOVE the
// rendered finding blocks (not replacing them).
func TestBounceToGeneration_ForwardsValidatorFindings(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	valSig := &ValidatorSignal{
		Verdict: "blocked",
		Findings: []ValidationFinding{
			{
				Rule: "spec-contradiction", Status: "blocked", FailureClass: "spec-defect", Owner: "planner",
				Correction: "Resolve the contradictory acceptance: spec demands both sync and async writes — pick one.",
			},
		},
	}

	require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)

	// Framing header survives AND the real per-finding detail is forwarded.
	assert.Contains(t, content, "SPEC DEFECT (owner: PLANNER)")
	assert.Contains(t, content, "### Finding: spec-contradiction")
	assert.Contains(t, content, "Resolve the contradictory acceptance")

	// The framing header is a PREFIX above the finding detail, not a replacement.
	framingIdx := strings.Index(content, "SPEC DEFECT (owner: PLANNER)")
	corrIdx := strings.Index(content, "Resolve the contradictory acceptance")
	require.NotEqual(t, -1, framingIdx)
	require.NotEqual(t, -1, corrIdx)
	assert.Less(t, framingIdx, corrIdx, "framing header must appear above the rendered finding")

	// SpecRetries-only decrement; goal bounced back to the generation phase.
	assert.Equal(t, 1, goal.SpecRetries)
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase)
}

// TestBounceToGeneration_NoFindingsKeepsFramingFallback asserts the findingless
// bounce (empty Findings, and the nil-valSig sub-case for a synthesized bounce)
// still writes a non-empty file carrying the framing header.
func TestBounceToGeneration_NoFindingsKeepsFramingFallback(t *testing.T) {
	run := func(t *testing.T, valSig *ValidatorSignal) {
		t.Helper()
		d, _, dir := setupDaemon(t)
		gf := &GoalsFile{
			CurrentGoal: "goal-001",
			Goals: []Goal{
				{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
			},
		}
		writeGoals(t, dir, gf)
		goal := &gf.Goals[0]

		require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

		data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
		require.NoError(t, err)
		content := string(data)

		assert.NotEmpty(t, strings.TrimSpace(content), "fallback file must not be empty")
		assert.Contains(t, content, "SPEC DEFECT (owner: PLANNER)")
		assert.NotContains(t, content, "### Finding:", "no findings means no structured blocks")
		assert.Equal(t, 1, goal.SpecRetries)
	}

	t.Run("EmptyFindings", func(t *testing.T) {
		run(t, &ValidatorSignal{Verdict: "blocked", Findings: []ValidationFinding{}})
	})
	t.Run("NilValSig", func(t *testing.T) {
		run(t, nil)
	})
}

// TestBounceToGeneration_ExhaustionCascades is the preservation guard: when the
// spec budget reaches 0 the goal hard-fails and dependents cascade with the hard
// class "fail", confirming the SpecRetries/exhaustion/CascadeFailure block is intact.
func TestBounceToGeneration_ExhaustionCascades(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, SpecRetries: 1, MaxSpecRetries: 2},
			{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	valSig := &ValidatorSignal{
		Verdict: "blocked",
		Findings: []ValidationFinding{
			{Rule: "spec-contradiction", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Correction: "fix the spec"},
		},
	}

	require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

	assert.Equal(t, GoalFailed, goal.Status, "spec budget exhausted -> goal failed")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent cascaded with hard class fail")
	assert.Equal(t, "goal-001", gf.Goals[1].BlockedBy)
}

// --- B10: spec-route convergence circuit-breaker ---------------------------
//
// These eight tests mirror the code-route breaker (handleFailedCycle) onto the
// spec-defect bounce path. They are built on execute-2's goal-025 repeat-signature
// fixture (newGoal025Fixture / newRecurringFindingSignal) and assert: the breaker
// halts at K identical spec-signature bounces to blocked/owner=human WITHOUT
// draining SpecRetries, never fires on an empty/changing signature set, and tracks
// streak state in DEDICATED spec-side fields so an interleaved code-defect cycle
// cannot cross-contaminate the spec streak.

// TestBounceToGeneration_IdenticalSpecSignatures_HaltsAtK: K consecutive bounces
// with an identical finding-signature set halt the goal at the Kth (K=2) bounce
// with the shared convergence-circuit-breaker sentinel.
func TestBounceToGeneration_IdenticalSpecSignatures_HaltsAtK(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Description: "non-converging spec", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	// First bounce primes the streak to 1 and drains normally — must NOT halt.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.NotEqual(t, GoalBlocked, goal.Status, "first bounce must not halt")
	assert.Equal(t, 1, goal.SpecConvergenceStreak)
	assert.True(t, equalSorted(sigs, goal.SpecConvergenceSignatures))

	// Second (Kth) identical bounce trips the breaker.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecConvergenceStreak)
}

// TestBounceToGeneration_HaltDoesNotDecrementSpecRetries: a breaker halt leaves
// SpecRetries at its pre-call value (the breaker fires on recurrence, never on
// budget drain).
func TestBounceToGeneration_HaltDoesNotDecrementSpecRetries(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 4, MaxSpecRetries: 5,
				SpecConvergenceSignatures: sigs, SpecConvergenceStreak: 1},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]
	pre := goal.SpecRetries

	require.NoError(t, d.bounceToGeneration(goal, gf, sig))

	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, pre, goal.SpecRetries, "breaker halt must not decrement SpecRetries")
}

// TestBounceToGeneration_HaltSetsOwnerHumanBlockedSignal: the persisted signal on
// a breaker halt is Verdict=blocked, Owner=human, Signatures=the current sorted set.
func TestBounceToGeneration_HaltSetsOwnerHumanBlockedSignal(t *testing.T) {
	d, _, dir := setupDaemon(t)
	sig, sigs := newRecurringFindingSignal()
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 4, MaxSpecRetries: 5,
				SpecConvergenceSignatures: sigs, SpecConvergenceStreak: 1},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	require.NoError(t, d.bounceToGeneration(goal, gf, sig))

	raw, err := LoadSignal(dir, "goal-025")
	require.NoError(t, err)
	vs, ok := raw.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", raw)
	assert.Equal(t, VerdictBlocked, vs.Verdict)
	assert.Equal(t, "human", vs.Owner)
	assert.Equal(t, sigs, vs.Signatures)
}

// TestBounceToGeneration_ChangingSpecSignatures_DrainsBudgetNotBreaker: a different
// finding signature each bounce keeps the streak pinned at 1, so the breaker never
// fires and the goal follows the normal SpecRetries-- drain.
func TestBounceToGeneration_ChangingSpecSignatures_DrainsBudgetNotBreaker(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	for i := 0; i < 3; i++ {
		sig := &ValidatorSignal{
			Verdict: VerdictBlocked,
			Findings: []ValidationFinding{
				{Rule: fmt.Sprintf("rule-%d", i), Status: VerdictFail, FailureClass: "spec-defect",
					Detail: fmt.Sprintf("distinct spec defect %d", i)},
			},
		}
		require.NoError(t, d.bounceToGeneration(goal, gf, sig))
		assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy, "changing signatures must not trip the breaker")
		assert.Equal(t, 1, goal.SpecConvergenceStreak, "streak resets to 1 on a changed signature set")
	}
	assert.Equal(t, 2, goal.SpecRetries, "3 normal bounces drained 5->2")
	assert.Equal(t, GoalPending, goal.Status)
}

// TestBounceToGeneration_EmptyFindings_NeverFires: a pass-only (empty signature)
// set never fires the breaker even when replayed past K; the budget drains normally.
func TestBounceToGeneration_EmptyFindings_NeverFires(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning, SpecRetries: 5, MaxSpecRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	passSig := &ValidatorSignal{Verdict: VerdictBlocked, Findings: []ValidationFinding{
		{Rule: "noop", Status: VerdictPass},
	}}
	for i := 0; i < 3; i++ {
		require.NoError(t, d.bounceToGeneration(goal, gf, passSig))
		assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy, "empty signature set must never fire the breaker")
		assert.Empty(t, goal.SpecConvergenceSignatures, "empty set stored, never matched")
		assert.Equal(t, 1, goal.SpecConvergenceStreak, "streak pinned at 1 for an empty set")
	}
	assert.Equal(t, 2, goal.SpecRetries, "budget drains normally 5->2")
}

// TestBounceToGeneration_NilValSig_NoPanic: a nil valSig completes without panic,
// skips the breaker, and writes the header-only framing correction.
func TestBounceToGeneration_NilValSig_NoPanic(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning, SpecRetries: 2, MaxSpecRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	require.NotPanics(t, func() {
		require.NoError(t, d.bounceToGeneration(goal, gf, nil))
	})

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "SPEC DEFECT (owner: PLANNER)")
	assert.NotContains(t, string(data), "### Finding:", "no findings means no structured blocks")
	assert.Equal(t, 1, goal.SpecRetries)
	assert.NotEqual(t, "convergence-circuit-breaker", goal.BlockedBy)
}

// TestBounceToGeneration_AlternatingCodeSpecCycles_NoCrossContamination: a code
// cycle interleaved between two identical spec bounces neither resets nor inflates
// the spec streak — the spec streak accumulates to K ACROSS the code cycle and
// trips, while the code streak lives only in ConvergenceStreak.
func TestBounceToGeneration_AlternatingCodeSpecCycles_NoCrossContamination(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-025").lastSupervisorStatus = "done"
	_, err := EnsureGoalDir(dir, "goal-025")
	require.NoError(t, err)

	specSig, specSigs := newRecurringFindingSignal()
	codeSig := &ValidatorSignal{
		Source: "validator", Verdict: VerdictFail,
		Findings: []ValidationFinding{
			{Rule: "deptrac", Status: VerdictFail, FailureClass: "code-defect",
				FailingCommand: "vendor/bin/deptrac", Detail: "layer violation in billing"},
		},
	}
	codeSigs := ComputeSignatures(codeSig.Findings)
	require.False(t, equalSorted(specSigs, codeSigs), "spec and code signature sets must differ")

	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Status: GoalRunning,
				SpecRetries: 5, MaxSpecRetries: 5, CodeRetries: 5, MaxCodeRetries: 5},
			{ID: "goal-026", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	// 1) Spec bounce S — primes spec streak; leaves code streak untouched.
	require.NoError(t, d.bounceToGeneration(goal, gf, specSig))
	assert.Equal(t, 1, goal.SpecConvergenceStreak)
	assert.True(t, equalSorted(specSigs, goal.SpecConvergenceSignatures))
	assert.Equal(t, 0, goal.ConvergenceStreak, "spec bounce must not touch the code streak")
	assert.Empty(t, goal.ConvergenceSignatures)

	// 2) Intervening code-defect cycle C — primes the code streak; spec state PRESERVED.
	writeFixtureSignal(t, dir, "goal-025", codeSig) // handleFailedCycle re-loads from disk
	require.NoError(t, d.handleFailedCycle(goal, gf, "code still failing", "code-defect"))
	assert.Equal(t, 1, goal.ConvergenceStreak)
	assert.True(t, equalSorted(codeSigs, goal.ConvergenceSignatures))
	assert.Equal(t, 1, goal.SpecConvergenceStreak, "code cycle must NOT reset the spec streak")
	assert.True(t, equalSorted(specSigs, goal.SpecConvergenceSignatures), "code cycle must NOT overwrite spec sigs")

	// 3) Spec bounce S again — spec streak reaches K across the code cycle and trips.
	require.NoError(t, d.bounceToGeneration(goal, gf, specSig))
	assert.Equal(t, GoalBlocked, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecConvergenceStreak, "spec streak survived the intervening code cycle")
	assert.Equal(t, 1, goal.ConvergenceStreak, "code streak not inflated by spec bounces")
}

// TestBounceToGeneration_FixtureReplay_GoalThreeIdenticalBounces: replaying the
// goal-025 fixture's recurring signal 3 times halts at K (the 2nd bounce) with the
// sentinel BEFORE draining the spec budget to a failed goal.
func TestBounceToGeneration_FixtureReplay_GoalThreeIdenticalBounces(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf, sig := newGoal025Fixture(1, 2) // recurring-signature replay fixture
	goal, ok := gf.GoalByID("goal-025")
	require.True(t, ok)
	// The fixture stages the CODE-route streak; give the SPEC route its own budget
	// and a clean spec-side streak so only the spec breaker can halt the goal.
	goal.Status = GoalRunning
	goal.SpecRetries = 3
	goal.MaxSpecRetries = 3
	goal.SpecConvergenceSignatures = nil
	goal.SpecConvergenceStreak = 0
	writeGoals(t, dir, gf)

	// Bounce 1: streak 1, SpecRetries 3->2, no halt.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	require.NotEqual(t, GoalBlocked, goal.Status)
	require.Equal(t, 2, goal.SpecRetries)

	// Bounce 2 (Kth identical): halts at K rather than draining to failed.
	require.NoError(t, d.bounceToGeneration(goal, gf, sig))
	assert.Equal(t, GoalBlocked, goal.Status, "halts at K, not a budget-exhausted failed")
	assert.NotEqual(t, GoalFailed, goal.Status)
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.SpecRetries, "SpecRetries preserved at its post-1st-bounce value, not drained to 0")
}

// TestM05_DispatchMdFullCorrections proves a multi-finding failure flows through
// handleFailedCycle → corrections/cycle-N.md → writeDispatchMd VERBATIM: the
// dispatch.md "## Prior Corrections" reproduces every per-finding block with all
// four fields and never collapses to the generic one-liner.
func TestM05_DispatchMdFullCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Fix pricing", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"Price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestTotal",
				OutputExcerpt:  "want 1000 got 100",
				ExpectedState:  "total in cents matches the API",
				Correction:     "multiply dollars by 100 before formatting",
			},
			{
				Rule: "currency-format", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestLocale",
				OutputExcerpt:  "want 1.000,00 got 1,000.00",
				ExpectedState:  "locale-aware currency formatting",
				Correction:     "use the locale formatter for the active request",
			},
		},
		NextAction: "fix pricing", Timestamp: "2026-05-20T14:35:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix pricing", "code-defect"))
	require.NoError(t, d.writeDispatchMd(goal))

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Prior Corrections")
	// Both per-finding blocks reproduced with all four fields.
	assert.Contains(t, content, "### Finding: price-calc")
	assert.Contains(t, content, "### Finding: currency-format")
	assert.Contains(t, content, "Command: go test ./pricing -run TestTotal")
	assert.Contains(t, content, "Output: want 1000 got 100")
	assert.Contains(t, content, "Expected: total in cents matches the API")
	assert.Contains(t, content, "Correction: multiply dollars by 100 before formatting")
	assert.Contains(t, content, "Command: go test ./pricing -run TestLocale")
	assert.Contains(t, content, "Correction: use the locale formatter for the active request")
	// No generic one-liner collapse.
	assert.NotContains(t, content, "failed acceptance criteria")
}

// TestInjectCorrections_CarriesPerFindingBlock proves injectCorrections appends
// the full structured per-finding block (Command:/Expected: lines) to each task
// context — not just a nextAction summary.
func TestInjectCorrections_CarriesPerFindingBlock(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "broken-test", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./checkout -run TestPay",
				OutputExcerpt:  "panic: nil pointer in Pay()",
				ExpectedState:  "payment succeeds without panicking",
				Correction:     "guard the nil gateway before calling Pay()",
			},
		},
		NextAction: "fix the broken test", Timestamp: "2026-05-20T14:35:00Z",
	}))

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1 context")

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix the broken test", "code-defect"))
	require.NoError(t, d.injectCorrections(goal))

	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)

	assert.Contains(t, ctxContent, "# Task 1 context")
	assert.Contains(t, ctxContent, "## Prior Corrections (Cycle 1)")
	// Full per-finding block carried, not just the nextAction summary.
	assert.Contains(t, ctxContent, "### Finding: broken-test")
	assert.Contains(t, ctxContent, "Command: go test ./checkout -run TestPay")
	assert.Contains(t, ctxContent, "Expected: payment succeeds without panicking")
	assert.Contains(t, ctxContent, "Correction: guard the nil gateway before calling Pay()")
}

func TestWriteDispatchMd_ExcludesValidateRules(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
		Validate:    []string{"run pricing e2e test", "check price format"},
	}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "run pricing e2e test")
	assert.NotContains(t, content, "check price format")
	assert.NotContains(t, content, "validate")
	assert.NotContains(t, content, "Validate")
}

func TestWriteDispatchMd_GoalMdPresent(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"inline criterion"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- Price matches API response\n- Currency symbol shown\n\n## Context\n\nPricing page redesign"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Price matches API response")
	assert.Contains(t, content, "Currency symbol shown")
	assert.Contains(t, content, "Pricing page redesign")
	assert.NotContains(t, content, "- inline criterion")
}

func TestWriteDispatchMd_GoalMdEmpty_FallsBackToInline(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"criterion A"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(""), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "- criterion A")
}

func TestWriteDispatchMd_GoalMdTakesPrecedence(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"inline criterion"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- goal.md criterion\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "goal.md criterion")
	assert.NotContains(t, content, "inline criterion")
}

func TestWriteDispatchMd_GoalMdPreservesCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- goal.md criterion\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	correctionsDir := filepath.Join(goalDir, "corrections")
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-1.md"), []byte("First correction"), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "goal.md criterion")
	assert.Contains(t, content, "First correction")
	assert.NotContains(t, content, "None (first attempt)")
}

// --- validate.sh gate tests ---

func TestRunValidateScript_NoScript(t *testing.T) {
	d, _, dir := setupDaemon(t)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitNonZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'test failed' >&2\nexit 1\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Contains(t, stderr, "test failed")
}

func TestRunValidateScript_Timeout(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.scriptTimeout = 100 * time.Millisecond
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 60\n"), 0o755))

	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		select {
		case <-ctx.Done():
			return "", "signal: killed", 137, nil
		case <-time.After(60 * time.Second):
			return "", "", 0, nil
		}
	})

	goal := &Goal{ID: "goal-001"}
	start := time.Now()
	passed, stderr, err := d.runValidateScript(goal)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Contains(t, stderr, "killed")
	assert.Less(t, elapsed, 5*time.Second)
}

func TestRunValidateScript_NotExecutable(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o644))

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_EnvAndCwd(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho \"GOAL_ID=$GOAL_ID\"\necho \"CWD=$(pwd)\"\n"), 0o755))

	var capturedStdout string
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		stdout, stderr, code, err := defaultScriptRunner(ctx, sp, wd, env)
		capturedStdout = stdout
		return stdout, stderr, code, err
	})

	goal := &Goal{ID: "goal-001"}
	passed, _, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed)
	assert.Contains(t, capturedStdout, "GOAL_ID=goal-001")
	assert.Contains(t, capturedStdout, fmt.Sprintf("CWD=%s", dir))
}

func TestCheckProgress_SupervisorDone_ValidateShPass(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") + killWindowByName("supervisor") — no workers
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	// deactivate mocks: kill supervisor, execute-, validator + create supervisor window
	setupDeactivateMocks(exec, testSession, "@9")
	d.SetWindowCreateFunc(mockCreateWindowFn("@9"))

	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "", 0, nil
	})

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, GoalDone, reloaded.Goals[0].Status)
	assert.NotEqual(t, phaseValidating, d.runtime("goal-001").phase)
}

func TestCheckProgress_SupervisorDone_ValidateShFail_ValidateMdExists(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.md"), []byte("- check tests pass\n"), 0o644))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") + killWindowByName("supervisor")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "some error", 1, nil
	})

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
}

func TestCheckProgress_SupervisorDone_ValidateShFail_NoValidateMd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") + killWindowByName("supervisor")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)

	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "validation error details", 1, nil
	})

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, GoalPending, reloaded.Goals[0].Status)
	assert.Equal(t, 2, reloaded.Goals[0].CodeRetries, "code budget 3->2")

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "validation error details")
}

func TestRecoverAfterCrash_RetriesExhausted_SetsFinishedAt(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{
				ID:          "goal-001",
				Description: "Crash test goal",
				Status:      GoalRunning,
				StartedAt:   time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
				Retries:     3,
				MaxRetries:  3,
			},
		},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.crashRecovery()
	require.NoError(t, err)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)

	g := reloaded.Goals[0]
	assert.Equal(t, GoalFailed, g.Status)
	assert.NotEmpty(t, g.FinishedAt, "FinishedAt must be set for crash-failed goals")

	_, parseErr := time.Parse(time.RFC3339, g.FinishedAt)
	assert.NoError(t, parseErr, "FinishedAt must be valid RFC3339")
}

func TestCheckProgress_SupervisorDone_NoValidateSh(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") + killWindowByName("supervisor")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
}

// --- transition logging tests ---

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origOutput) })
	fn()
	return buf.String()
}

func TestPhaseName_AllValues(t *testing.T) {
	assert.Equal(t, "idle", phaseName(phaseNone))
	assert.Equal(t, "supervising", phaseName(phaseSupervising))
	assert.Equal(t, "validating", phaseName(phaseValidating))
}

func TestGoalDuration_ValidTimestamps(t *testing.T) {
	g := &Goal{
		StartedAt:  "2026-05-20T10:00:00Z",
		FinishedAt: "2026-05-20T10:12:34Z",
	}
	assert.Equal(t, "12m34s", goalDuration(g))
}

func TestGoalDuration_EmptyTimestamps(t *testing.T) {
	assert.Equal(t, "", goalDuration(&Goal{}))
	assert.Equal(t, "", goalDuration(&Goal{StartedAt: "2026-05-20T10:00:00Z"}))
	assert.Equal(t, "", goalDuration(&Goal{FinishedAt: "2026-05-20T10:00:00Z"}))
	assert.Equal(t, "", goalDuration(&Goal{StartedAt: "bad", FinishedAt: "2026-05-20T10:00:00Z"}))
}

func TestDispatch_LogsStateTransition(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	output := captureLog(t, func() {
		err = d.dispatch(&gf.Goals[0], gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: pending -> running")
}

func TestDispatch_LogsPhaseTransition(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseNone

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	output := captureLog(t, func() {
		err = d.dispatch(&gf.Goals[0], gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: phase idle -> supervising")
}

func TestCheckSupervisingPhase_Done_LogsGoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z", MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	setupDeactivateMocks(exec, testSession, "@9")
	d.SetWindowCreateFunc(mockCreateWindowFn("@9"))
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "", 0, nil
	})

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.checkSupervisingPhase(goal, gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> done")
	assert.Regexp(t, `running -> done \(\d+`, output)
}

func TestCheckSupervisingPhase_LogsPhaseToValidating(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.checkSupervisingPhase(goal, gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: phase supervising -> validating")
}

func TestCheckValidatingPhase_Pass_LogsGoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z", MaxRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.checkValidatingPhase(goal, gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> done")
	assert.Regexp(t, `running -> done \(\d+`, output)
}

// --- C2-routing: per-verdict-class routing + per-class budgets ---

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

// TestM02_CodeDefectRouting — fail/code-defect routes to handleFailedCycle:
// re-dispatch implementer (status pending, phase supervising), dec CodeRetries
// only (2->1); Spec/Validation/Block unchanged.
func TestM02_CodeDefectRouting(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix the off-by-one in pricing",
		Findings:  []ValidationFinding{{Rule: "unit-tests", Status: "fail", FailureClass: "code-defect", Detail: "test red"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.CodeRetries, "code budget decremented")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget untouched")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, 0, goal.Retries, "legacy Retries stays read-only")
	assert.Equal(t, GoalPending, goal.Status, "implementer re-dispatched")
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corr)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "fix the off-by-one in pricing", "correction keeps the remediation text")
}

// TestErrorVerdict_ReRunsValidationOnly — error/ops re-spawns the validator and
// does NOT re-dispatch the implementer; dec ValidationRetries only (2->1);
// Code/Spec/Block unchanged; no code-defect correction written.
func TestErrorVerdict_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.validatorSendDelay = 0
	d.createWindowFn = mockCreateWindowFn("@5")

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict:   "error",
		Findings:  []ValidationFinding{{Rule: "validate-script", Status: "error", FailureClass: "validator-error", Detail: "validator crashed"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))

	// Re-spawn path: validator window present, killed, then a fresh one created.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("ready ❯ ", nil)
	var sentCmds []string
	exec.On("SendMessage", testSession, "@5", mock.Anything).Run(func(args mock.Arguments) {
		sentCmds = append(sentCmds, args.Get(2).(string))
	}).Return(nil)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not supervising")

	for _, c := range sentCmds {
		assert.NotContains(t, c, "/tmux:supervisor", "implementer must NOT be re-dispatched on error")
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on error")
		assert.Contains(t, c, "/tmux:investigate", "error re-runs the validator only")
	}

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr), "error must not write a code-defect correction")
}

// TestSpecDefectRouting_BouncesToGeneration — blocked/planner bounces to the
// generation/planner slot (not the implementer); dec SpecRetries only (2->1);
// Code/Validation/Block unchanged; CodeRetries untouched so the next dispatch
// re-plans rather than re-running the implementer.
func TestSpecDefectRouting_BouncesToGeneration(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "criteria contradict"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.SpecRetries, "spec budget decremented 2->1")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched (-> next dispatch re-plans)")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase, "marked for generation re-dispatch")

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corr)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "SPEC DEFECT")
	assert.Contains(t, string(data), "PLANNER")
}

// TestBlockedEnvHold_NoBudget — blocked/ops parks the goal and writes a runbook,
// charging NO budget (all four counters unchanged); no resume loop is started.
func TestBlockedEnvHold_NoBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "ops", Remedy: "export STRIPE_KEY then resume",
		Findings:  []ValidationFinding{{Rule: "env:STRIPE_KEY", Status: "blocked", FailureClass: "env-config", Detail: "missing secret"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "no budget charged on env hold")
	assert.Equal(t, 2, goal.SpecRetries, "no budget charged on env hold")
	assert.Equal(t, 1, goal.ValidationRetries, "no budget charged on env hold")
	assert.Equal(t, 1, goal.BlockRetries, "no budget charged on env hold")
	assert.Equal(t, GoalBlocked, goal.Status, "goal parked")

	runbook := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	data, readErr := os.ReadFile(runbook)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "export STRIPE_KEY then resume")
	assert.Contains(t, string(data), "env/infra")

	// No code-defect correction is written on an env hold.
	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr))
}

// TestBlockedEnvHold_SetsPreconditionFlagsAndConcreteRemedy — WS4 regression
// backstop. Locks the two properties that no single green test pins down, so a
// future refactor cannot silently re-route an env/infra precondition (e.g. an
// unreachable test DB) back into the implementer-retry loop:
//
//	A1 (precondition-park flags): on a blocked/env-config/ops verdict,
//	    checkValidatingPhase -> haltBlockedEnv parks the goal with
//	    BlockedBy=="env_precondition" AND BlockedByPrecondition==true, and
//	    charges ZERO budget (CodeRetries stays 2) — the §5 auto-resume contract.
//	A2 (concrete remedy, not fallback): the runbook quotes the validator's
//	    concrete Remedy verbatim and does NOT fall back to the generic
//	    taskvisor.go:1641 string — proving WS3's remedy actually reaches the
//	    operator and the fallback branch was not hit.
//
// A3 (§5 blocked->pending resume with budget unchanged) is fully locked by
// TestResumeDownstreamLoop_PreconditionClears; A4 (a true code-defect still
// decrements CodeRetries, so switch precedence is intact) by
// TestM02_CodeDefectRouting. Both are green-guards — no code is duplicated here.
//
// Class:"env-config" is set BOTH at the top level AND on the blocking finding so
// ClassifyVerdict (finding-driven) and latestSignalIsPreconditionClass
// (signal-driven) both recognise the env-config class.
func TestBlockedEnvHold_SetsPreconditionFlagsAndConcreteRemedy(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	const concreteRemedy = "start the test DB per docs/architecture/test-environment.md"
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops", Remedy: concreteRemedy,
		Findings:  []ValidationFinding{{Rule: "service:db", Status: "blocked", FailureClass: "env-config", Detail: "connection refused"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	// A1 (NEW): precondition-park flags drive the §5 auto-resume loop.
	assert.Equal(t, "env_precondition", goal.BlockedBy, "BlockedBy marks the env precondition for §5 resume")
	assert.True(t, goal.BlockedByPrecondition, "BlockedByPrecondition arms scanPreconditionBlocked")

	// Cross-check: parked with NO budget charged (the regression we guard against
	// is this path burning CodeRetries like a code defect).
	assert.Equal(t, 2, goal.CodeRetries, "env hold charges no code budget")
	assert.Equal(t, GoalBlocked, goal.Status, "goal parked, not re-dispatched")

	runbook := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	data, readErr := os.ReadFile(runbook)
	require.NoError(t, readErr)

	// A2 (NEW negative): the generic fallback (taskvisor.go:1641) must NOT appear —
	// the concrete remedy reached the operator.
	assert.NotContains(t, string(data), "Resolve the missing environment/infrastructure precondition",
		"runbook must quote the concrete remedy, not the generic fallback")
	// Cross-check: the verbatim remedy is what the operator sees.
	assert.Contains(t, string(data), concreteRemedy, "runbook quotes the validator's concrete remedy verbatim")
}

// TestBudgetCountersIndependent — code=2,spec=1: fail/code-defect moves only code
// (2,1 -> 1,1), then blocked/spec-defect moves only spec (1,1 -> 1,0). Proves
// each verdict moves exactly its own counter.
func TestBudgetCountersIndependent(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 1, 1, 0),
		{ID: "goal-002", Description: "next", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]

	// Step 1: fail/code-defect -> (code 2->1, spec stays 1).
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix bug",
		Findings:  []ValidationFinding{{Rule: "r1", Status: "fail", FailureClass: "code-defect"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	require.NoError(t, d.checkValidatingPhase(goal, gf))
	assert.Equal(t, 1, goal.CodeRetries, "code 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec unchanged by a code defect")

	// Step 2: blocked/spec-defect -> (spec 1->0, code stays 1). The finding carries
	// a concrete Detail so it is a SUBSTANTIVE spec defect (HasSubstantiveSpecDefect
	// true) — without one the M2 substance guard would correctly re-route an
	// unsubstantiated verdict to rerunValidationOnly. validateFindings already
	// guarantees every real blocked finding carries non-stub content.
	goal.Status = GoalRunning
	d.runtime("goal-001").phase = phaseValidating
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "r2", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "acceptance criteria contradict"}},
		Timestamp: "2026-05-20T14:36:00Z",
	}))
	require.NoError(t, d.checkValidatingPhase(goal, gf))
	assert.Equal(t, 1, goal.CodeRetries, "code unchanged by a spec defect")
	assert.Equal(t, 0, goal.SpecRetries, "spec 1->0")
	assert.Equal(t, 1, goal.ValidationRetries, "validation never moved")
	assert.Equal(t, 0, goal.BlockRetries, "block never moved")
}

// TestCodeBudgetExhausted_HardHalt — code=1: a fail/code-defect drives CodeRetries
// to 0, hard-halts the goal (GoalFailed), cascades to dependents, and advances.
func TestCodeBudgetExhausted_HardHalt(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 1, 1, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "still broken",
		Findings:  []ValidationFinding{{Rule: "r1", Status: "fail", FailureClass: "code-defect"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 0, goal.CodeRetries, "code budget exhausted")
	assert.Equal(t, GoalFailed, goal.Status, "hard halt")
	dep, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalBlocked, dep.Status, "CascadeFailure blocked the dependent")
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advanceToNextGoal moved on")

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.NoError(t, statErr, "exhaustion still records the final correction")
}

func TestHandleFailedCycle_RetriesExhausted_LogsGoalFailed(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z", Retries: 2, MaxRetries: 3, CodeRetries: 1, MaxCodeRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.handleFailedCycle(goal, gf, "give up", "code-defect")
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> failed")
	assert.Regexp(t, `running -> failed \(\d+`, output)
}

func TestHandleFailedCycle_Retry_LogsPendingAndPhase(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.handleFailedCycle(goal, gf, "fix it", "code-defect")
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: running -> pending (code budget left 2)")
	assert.Contains(t, output, "goal-001: phase validating -> supervising")
}

// --- dispatchRetry tests ---

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

func TestTick_RetryUsesDispatchRetry_WhenTasksYamlExists(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "create templates"
    wid: execute-1
    status: done
    context: .tmux-cli/research/2026-01-01/task-templates.md
`)

	writeTaskContext(t, dir, ".tmux-cli/research/2026-01-01/task-templates.md", "# Original task context")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Fix quality-gates.md: remove Doctrine refs"), 0o644))

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	// Should have sent /tmux:supervisor (not /tmux:plan)
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:supervisor", "retry should send /tmux:supervisor, not /tmux:plan")
	assert.NotContains(t, sentCmd, "/tmux:plan", "retry must skip planning")
}

func TestTick_FirstAttempt_UsesDispatch_EvenWithoutTasksYaml(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	// First attempt: should send /tmux:plan
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:plan", "first attempt should use /tmux:plan")
}

func TestDispatchRetry_ResetsTaskStatuses(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
  - name: "task two"
    wid: execute-2
    status: done
    context: .tmux-cli/research/ctx2.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")
	writeTaskContext(t, dir, ".tmux-cli/research/ctx2.md", "# Task 2")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Fix task two"), 0o644))

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	goal := &gf.Goals[0]
	err := d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	// per-goal tasks.yaml should have all tasks reset to pending
	data, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "status: done", "all task statuses should be reset from done")
	assert.Contains(t, content, "status: pending", "tasks should be reset to pending")
}

func TestDispatchRetry_InjectsCorrections(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3, CodeRetries: 2, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Original context")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Remove Doctrine from quality-gates.md"), 0o644))

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	goal := &gf.Goals[0]
	err := d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	// Context file should have corrections appended
	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)
	assert.Contains(t, ctxContent, "# Original context", "original context preserved")
	assert.Contains(t, ctxContent, "Prior Corrections", "corrections section appended")
	assert.Contains(t, ctxContent, "Remove Doctrine from quality-gates.md", "correction content present")
}

func setupDeactivateOnCompletionMocks(exec *testutil.MockTmuxExecutor, session string) {
	// notifySupervisor calls (GOAL-DONE per done goal + ALL-COMPLETE) + teardown
	// (4 kill lookups + collectManagedNames + waitWindowsGone). Count varies by
	// goal mix so use an unbounded return for all empty-list ListWindows calls.
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil)
}

func TestDeactivateOnCompletion_KillsWindowsNoSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	// notifyCompletion (1 ListWindows for supervisor lookup)
	// + killWindowByName("supervisor-001"), killWindowsByPrefix("execute-001-"),
	// killWindowByName("validator-001"), killWindowsByPrefix("inv-001-"),
	// collectManagedNames, waitWindowsGone — all need ListWindows.
	// First: notifyCompletion finds no bare "supervisor" → logs and skips.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	// killWindowByName("supervisor-001") — finds the goal's namespaced supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@0").Return(nil)
	// killWindowsByPrefix("execute-001-") — finds execute-001-1
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@1").Return(nil)
	// killWindowByName("validator-001") — none
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// killWindowsByPrefix("inv-") — none
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// collectManagedNames — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitWindowsGone — immediate success
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()

	var createCalled bool
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createCalled = true
		return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
	})

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.False(t, createCalled, "supervisor window should NOT be created on completion")
	exec.AssertCalled(t, "KillWindow", testSession, "@0")
	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	assert.Equal(t, modeIdle, d.mode)
}

func TestDeactivateOnCompletion_RemovesGuardFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed")
}

func TestDeactivateOnCompletion_BlocksDepsBeforeShutdown(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.Equal(t, GoalBlocked, gf.Goals[1].Status)
	assert.Equal(t, "deps_unsatisfied", gf.Goals[1].BlockedBy)
}

func TestDeactivateOnCompletion_AllResolved(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalFailed},
			{ID: "goal-003", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode)
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, GoalFailed, gf.Goals[1].Status)
	assert.Equal(t, GoalDone, gf.Goals[2].Status)
}

func TestTick_RetryCeilingReached_HaltsGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal:      "goal-001",
		GlobalMaxRetries: 3,
		Goals: []Goal{
			// consumed code budget 3 (MaxCode5-Code2) == GlobalMaxRetries 3 => ceiling reached.
			{ID: "goal-001", Description: "test", Status: GoalPending, MaxRetries: 5, MaxCodeRetries: 5, CodeRetries: 2},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	// After halting goal-001 and cascading, advanceToNextGoal finds goal-002
	// No dispatch mocks needed since ceiling prevents dispatch
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "goal at ceiling should be failed")
	assert.NotEmpty(t, gf.Goals[0].FinishedAt)
	assert.Equal(t, "goal-002", gf.CurrentGoal, "should advance to next goal")
}

func TestTick_AllBlockedCascade_DeactivatesWithReport(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal:      "goal-001",
		GlobalMaxRetries: 3,
		Goals: []Goal{
			// consumed code budget 3 (MaxCode5-Code2) == GlobalMaxRetries 3 => ceiling reached.
			{ID: "goal-001", Description: "root task", Status: GoalPending, MaxRetries: 5, MaxCodeRetries: 5, CodeRetries: 2},
			{ID: "goal-002", Description: "depends on root", Status: GoalPending, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "also depends on root", Status: GoalPending, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "root goal should be failed")
	assert.NotEmpty(t, gf.Goals[0].FinishedAt, "root goal should have FinishedAt set")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent goal-002 should be blocked")
	assert.Equal(t, "goal-001", gf.Goals[1].BlockedBy, "goal-002 should be blocked by goal-001")
	assert.Equal(t, GoalBlocked, gf.Goals[2].Status, "dependent goal-003 should be blocked")
	assert.Equal(t, "goal-001", gf.Goals[2].BlockedBy, "goal-003 should be blocked by goal-001")
	assert.Equal(t, modeIdle, d.mode, "daemon should be idle after all goals blocked")

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, err := os.ReadFile(reportPath)
	require.NoError(t, err, "completion report should exist")
	report := string(reportData)
	assert.Contains(t, report, "Blocked| 2", "report should show 2 blocked goals")
	assert.Contains(t, report, "Failed | 1", "report should show 1 failed goal")
}

func TestTick_RetryCeilingNotReached_Dispatches(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal:      "goal-001",
		GlobalMaxRetries: 10,
		Goals: []Goal{
			// consumed code budget 2 (MaxCode5-Code3) < GlobalMaxRetries 10 => under ceiling.
			{ID: "goal-001", Description: "test", Status: GoalPending, MaxRetries: 5, MaxCodeRetries: 5, CodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "should dispatch normally when under ceiling")
}

func TestDispatchRetry_FallsBackToDispatch_WhenNoTasksYaml(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	// No tasks.yaml written — should fallback

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	// Without tasks.yaml, should fall back to /tmux:plan
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:plan", "without tasks.yaml should fallback to /tmux:plan")
}

func TestTick_DoneAdvancesToDependentGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "root task", Status: GoalDone},
			{ID: "goal-B", Description: "depends on A", Status: GoalPending, DependsOn: []string{"goal-A"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-B")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-B")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-B", gf.CurrentGoal)
	assert.Equal(t, GoalRunning, gf.Goals[1].Status)
}

func TestTick_ChainedDeps_SkipsUnsatisfied(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "root", Status: GoalDone},
			{ID: "goal-B", Description: "depends on A", Status: GoalPending, DependsOn: []string{"goal-A"}},
			{ID: "goal-C", Description: "depends on B", Status: GoalPending, DependsOn: []string{"goal-B"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-B")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-B")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-B", gf.CurrentGoal, "should pick B, not C")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "B should be dispatched")
	assert.Equal(t, GoalPending, gf.Goals[2].Status, "C should remain pending (B not done)")
}

func TestTick_DiamondDeps_PicksEligible(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "root", Status: GoalDone},
			{ID: "goal-B", Description: "left branch", Status: GoalPending, DependsOn: []string{"goal-A"}},
			{ID: "goal-C", Description: "right branch", Status: GoalPending, DependsOn: []string{"goal-A"}},
			{ID: "goal-D", Description: "diamond join", Status: GoalPending, DependsOn: []string{"goal-B", "goal-C"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-B")
	require.NoError(t, err)
	_, err = EnsureGoalDir(dir, "goal-C")
	require.NoError(t, err)
	_, err = EnsureGoalDir(dir, "goal-D")
	require.NoError(t, err)

	emptyWindows := []tmux.WindowInfo{}
	// Goal windows are namespaced per goal, so each dispatch boots a distinct
	// supervisor-<ns> window.
	claudeB := []tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor-B", CurrentCommand: "claude"}}
	claudeC := []tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor-C", CurrentCommand: "claude"}}
	claudeD := []tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor-D", CurrentCommand: "claude"}}

	// Dispatch 1 (goal-B): 6 empty (kills+collect+waitGone) + 2 claude (waitClaudeBoot+waitForPrompt)
	exec.On("ListWindows", testSession).Return(emptyWindows, nil).Times(6)
	exec.On("ListWindows", testSession).Return(claudeB, nil).Times(2)
	// Dispatch 2 (goal-C): same pattern
	exec.On("ListWindows", testSession).Return(emptyWindows, nil).Times(6)
	exec.On("ListWindows", testSession).Return(claudeC, nil).Times(2)
	// Dispatch 3 (goal-D): 6 empty + unlimited claude
	exec.On("ListWindows", testSession).Return(emptyWindows, nil).Times(6)
	exec.On("ListWindows", testSession).Return(claudeD, nil)

	exec.On("CaptureWindowOutput", testSession, "@0").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()

	// Tick 1: A(done) → picks B (first eligible pending with deps satisfied)
	err = d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, "goal-B", gf.CurrentGoal)
	assert.Equal(t, GoalRunning, gf.Goals[1].Status)
	assert.Equal(t, GoalPending, gf.Goals[2].Status, "C still pending")
	assert.Equal(t, GoalPending, gf.Goals[3].Status, "D still pending")

	// Between ticks: B completes
	gf.Goals[1].Status = GoalDone
	writeGoals(t, dir, gf)

	// Tick 2: B(done) → picks C (dep A satisfied; D skipped because C not done)
	err = d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, "goal-C", gf.CurrentGoal)
	assert.Equal(t, GoalRunning, gf.Goals[2].Status)
	assert.Equal(t, GoalPending, gf.Goals[3].Status, "D still pending")

	// Between ticks: C completes
	gf.Goals[2].Status = GoalDone
	writeGoals(t, dir, gf)

	// Tick 3: C(done) → picks D (both B and C done)
	err = d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, "goal-D", gf.CurrentGoal)
	assert.Equal(t, GoalRunning, gf.Goals[3].Status)
}

func TestTick_BlockedGoalSkipped_NextPendingPicked(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "completed", Status: GoalDone},
			{ID: "goal-B", Description: "blocked by external", Status: GoalBlocked, BlockedBy: "external-issue"},
			{ID: "goal-C", Description: "ready to go", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-C")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-C")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-C", gf.CurrentGoal, "should skip blocked B and pick C")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "B should remain blocked")
	assert.Equal(t, GoalRunning, gf.Goals[2].Status, "C should be dispatched")
}

func TestTick_AllBlockedOrUnsatisfied_Deactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals: []Goal{
			{ID: "goal-A", Description: "completed", Status: GoalDone},
			{ID: "goal-B", Description: "blocked", Status: GoalBlocked, BlockedBy: "external"},
			{ID: "goal-C", Description: "unsatisfied dep", Status: GoalPending, DependsOn: []string{"goal-D"}},
		},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "should deactivate when no eligible pending goals")
}

func TestTick_AdvancesPastPreconditionParkedCurrentGoal_HaltBlockedEnv(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-12",
		Goals: []Goal{
			{ID: "goal-12", Description: "parked on env precondition", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true},
			{ID: "goal-13", Description: "runnable peer", Status: GoalPending},
			{ID: "goal-14", Description: "another runnable peer", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-13")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-13")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-13", gf.CurrentGoal, "should advance past parked current to first runnable peer")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "goal-13 should be dispatched")
	assert.Equal(t, GoalBlocked, gf.Goals[0].Status, "parked goal-12 should remain blocked")
	assert.True(t, gf.Goals[0].BlockedByPrecondition, "park flag preserved on goal-12")
}

func TestTick_AdvancesPastPreflightParkedCurrentGoal_EmptyBlockedBy(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	// Literal incident path: dispatch preflight gate parks with BlockedBy=="".
	gf := &GoalsFile{
		CurrentGoal: "goal-12",
		Goals: []Goal{
			{ID: "goal-12", Description: "preflight park, empty BlockedBy", Status: GoalBlocked, BlockedBy: "", BlockedByPrecondition: true},
			{ID: "goal-13", Description: "runnable peer", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-13")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-13")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-13", gf.CurrentGoal, "case keys on Status, not BlockedBy")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "goal-13 should be dispatched")
	assert.Equal(t, GoalBlocked, gf.Goals[0].Status, "parked goal-12 should remain blocked")
}

func TestTick_BlockedCurrentGoal_IdlesWhenNothingDispatchable(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-12",
		Goals: []Goal{
			{ID: "goal-12", Description: "parked", Status: GoalBlocked, BlockedByPrecondition: true},
			{ID: "goal-13", Description: "external hold", Status: GoalBlocked, BlockedBy: "external"},
			{ID: "goal-14", Description: "dep unsatisfied", Status: GoalPending, DependsOn: []string{"goal-13"}},
		},
	}
	writeGoals(t, dir, gf)

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-12", gf.CurrentGoal, "current stays on parked goal")
	assert.Equal(t, modeActive, d.mode, "should idle (stay active), not deactivate")
	assert.Equal(t, GoalBlocked, gf.Goals[0].Status, "parked goal stays blocked")
}

func TestTick_DoesNotDeactivateWhilePreconditionParkOutstanding(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-12",
		Goals: []Goal{
			{ID: "goal-12", Description: "completed", Status: GoalDone},
			{ID: "goal-13", Description: "parked precondition w/ pending work", Status: GoalBlocked, BlockedByPrecondition: true},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-13")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-13")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	// Tick 1: current done, only remaining goal is a resumable park -> NextPendingGoal
	// returns false. The deactivation guard must keep the daemon active, not tear down.
	err = d.tick(context.Background(), gf)
	require.NoError(t, err)
	assert.Equal(t, modeActive, d.mode, "must NOT deactivate while resumable park outstanding")

	// Simulate scanPreconditionBlocked clearing the precondition: blocked -> pending.
	gf.Goals[1].Status = GoalPending
	gf.Goals[1].BlockedByPrecondition = false
	gf.Goals[1].BlockedBy = ""
	writeGoals(t, dir, gf)

	// Tick 2: the un-parked goal is now dispatchable.
	err = d.tick(context.Background(), gf)
	require.NoError(t, err)
	assert.Equal(t, "goal-13", gf.CurrentGoal, "un-parked goal becomes current")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "un-parked goal dispatched")
}

func TestDeactivateOnCompletion_GeneratesCompletionReport(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "first task", Status: GoalDone, Retries: 1, MaxRetries: 3},
			{ID: "goal-002", Description: "second task", Status: GoalFailed, Retries: 3, MaxRetries: 3},
			{ID: "goal-003", Description: "third task", Status: GoalBlocked, BlockedBy: "deps_unsatisfied", Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err, "completion report file should exist")

	report := string(data)
	assert.Contains(t, report, "# Taskvisor Completion Report")
	assert.Contains(t, report, "| Done   | 1     |")
	assert.Contains(t, report, "| Failed | 1     |")
	assert.Contains(t, report, "| Blocked| 1     |")
	assert.Contains(t, report, "| Total  | 3     |")

	assert.Contains(t, report, "### goal-001: first task")
	assert.Contains(t, report, "### goal-002: second task")
	assert.Contains(t, report, "### goal-003: third task")

	assert.Contains(t, report, "- **Status:** done")
	assert.Contains(t, report, "- **Status:** failed")
	assert.Contains(t, report, "- **Status:** blocked")

	assert.Contains(t, report, "- **Retries:** 1/3")
	assert.Contains(t, report, "- **Retries:** 3/3")
	assert.Contains(t, report, "- **Retries:** 0/3")
}

func TestTick_AllDone_StaysIdleNoSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal:      "goal-001",
		GlobalMaxRetries: 3,
		Goals: []Goal{
			// consumed code budget 3 (MaxCode5-Code2) == GlobalMaxRetries 3 => ceiling reached.
			{ID: "goal-001", Description: "only goal", Status: GoalPending, MaxRetries: 5, MaxCodeRetries: 5, CodeRetries: 2},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	var createCalled bool
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createCalled = true
		return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
	})

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "daemon should be idle after all goals resolved")
	assert.False(t, createCalled, "supervisor window should NOT be created via deactivateOnCompletion")

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed")
}

func TestInvestigationLifecycle_ValidatorSpawnsSendsInvestigateCommand(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession

	goal := &Goal{
		ID:          "goal-001",
		Description: "test goal",
		Acceptance:  []string{"it works"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdPath := filepath.Join(goalDir, "goal.md")
	require.NoError(t, os.WriteFile(goalMdPath, []byte("# Test Goal\n"), 0o644))

	var capturedCmd string
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		capturedCmd = cmd
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	err = d.createValidatorAndSendPayload(goal)
	require.NoError(t, err)

	expectedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md")
	assert.Equal(t, "/tmux:investigate "+expectedPath, capturedCmd)
}

func TestInvestigationLifecycle_FailedValidation_WritesCorrectionAndRetries(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix pricing bug", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)

	corrPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corrPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "fix pricing bug")
	assert.Contains(t, string(data), "Implementation completed but failed acceptance criteria.")

	sig, sigErr := LoadSignal(dir, "goal-001")
	assert.NoError(t, sigErr)
	assert.Nil(t, sig)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)
	dispatchData, _ := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	assert.Contains(t, string(dispatchData), "fix pricing bug")
	assert.NotContains(t, string(dispatchData), "None (first attempt)")
}

func TestInvestigationLifecycle_RedispatchIncludesCorrectionsInDispatchMd(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Fix pricing display", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"Price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.handleFailedCycle(goal, gf, "Fix pricing bug — API returns cents not dollars", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Fix pricing bug")
	assert.Contains(t, content, "API returns cents not dollars")
	assert.NotContains(t, content, "None (first attempt)")
	assert.Contains(t, content, "Prior Corrections")
}

func TestInvestigationLifecycle_RedispatchInjectsCorrectionsIntoTaskContext(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
  - name: "task two"
    wid: execute-2
    status: done
    context: .tmux-cli/research/ctx2.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1 context")
	writeTaskContext(t, dir, ".tmux-cli/research/ctx2.md", "# Task 2 context")

	goal := &gf.Goals[0]
	err = d.handleFailedCycle(goal, gf, "Fix the broken test", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err = d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	ctx1Data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ctx1Data), "# Task 1 context")
	assert.Contains(t, string(ctx1Data), "## Prior Corrections (Cycle 1)")
	assert.Contains(t, string(ctx1Data), "Fix the broken test")

	ctx2Data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx2.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ctx2Data), "# Task 2 context")
	assert.Contains(t, string(ctx2Data), "## Prior Corrections (Cycle 1)")
	assert.Contains(t, string(ctx2Data), "Fix the broken test")
}

func TestInvestigationLifecycle_MultipleCycles_CorrectionsAccumulate(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5, Acceptance: []string{"it works"}, CodeRetries: 5, MaxCodeRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]

	err = d.handleFailedCycle(goal, gf, "Fix pricing calculation", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 4, goal.CodeRetries, "code budget 5->4")

	goal.Status = GoalRunning
	err = d.handleFailedCycle(goal, gf, "Also fix currency formatting", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 3, goal.CodeRetries, "code budget 5->4->3")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	_, statErr1 := os.Stat(filepath.Join(corrDir, "cycle-1.md"))
	assert.NoError(t, statErr1)
	_, statErr2 := os.Stat(filepath.Join(corrDir, "cycle-2.md"))
	assert.NoError(t, statErr2)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Fix pricing calculation")
	assert.Contains(t, content, "Also fix currency formatting")
	assert.NotContains(t, content, "None (first attempt)")

	idx1 := strings.Index(content, "Fix pricing calculation")
	idx2 := strings.Index(content, "Also fix currency formatting")
	assert.True(t, idx1 < idx2, "cycle-1 correction should appear before cycle-2")
}

func TestInvestigationLifecycle_FullChain_FailThenPassOnRetry(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	writeGuardFile(t, dir)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Context")

	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		switch name {
		case "validator-001":
			return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
		case "supervisor-001":
			return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
		}
		return nil, fmt.Errorf("unexpected window: %s", name)
	})

	validatorClaude := []tmux.WindowInfo{{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"}}
	validatorPlain := []tmux.WindowInfo{{TmuxWindowID: "@5", Name: "validator-001"}}
	supervisorClaude := []tmux.WindowInfo{{TmuxWindowID: "@9", Name: "supervisor-001", CurrentCommand: "claude"}}
	empty := []tmux.WindowInfo{}

	// Stage 1: checkSupervisingPhase — supervisor done, create validator
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	// Stage 2: checkValidatingPhase — validator fail
	exec.On("ListWindows", testSession).Return(validatorPlain, nil).Once()
	// Stage 3: dispatchRetry — kill all, create supervisor
	exec.On("ListWindows", testSession).Return(empty, nil).Times(6)
	exec.On("ListWindows", testSession).Return(supervisorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(supervisorClaude, nil).Once()
	// Stage 4: checkSupervisingPhase — supervisor done again, create validator
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	// Stage 5: checkValidatingPhase pass + deactivateOnCompletion
	// 1 for killWindowByName("validator"), 1 for notifyCompletion,
	// 4 for teardown kill lookups, 1 for collectManagedNames, 1 for waitWindowsGone
	exec.On("ListWindows", testSession).Return(validatorPlain, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Times(7)

	exec.On("CaptureWindowOutput", testSession, "@5").Return("", fmt.Errorf("no prompt")).Times(2)
	exec.On("CaptureWindowOutput", testSession, "@9").Return("", fmt.Errorf("no prompt")).Once()

	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate")
	})).Return(nil).Times(2)
	exec.On("SendMessage", testSession, "@9", "/tmux:supervisor goal-001").Return(nil).Once()

	exec.On("KillWindow", testSession, "@5").Return(nil).Times(2)

	goal := &gf.Goals[0]

	// Stage 1: Supervisor done → validator spawned
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	// Stage 2: Validator fail → correction written, retries incremented
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix pricing", Timestamp: "2026-05-20T14:35:00Z",
	}))
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)

	corrPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, corrStatErr := os.Stat(corrPath)
	require.NoError(t, corrStatErr, "correction file should exist after failed cycle")

	// Stage 3: Re-dispatch with corrections
	err = d.dispatchRetry(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, GoalRunning, goal.Status)

	// Stage 4: Supervisor done again → validator spawned
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T15:30:00Z",
	}))
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	// Stage 5: Validator pass → goal done
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T15:35:00Z",
	}))
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalDone, goal.Status)
	assert.NotEmpty(t, goal.FinishedAt)
	assert.Equal(t, modeIdle, d.mode)
}

func TestCheckProgress_Supervising_TimeoutExceeded_KillsWindows(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.dispatchTimeout = 1 * time.Second
	d.runtime("goal-001").dispatchTime = time.Now().Add(-2 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	// handleFailedCycle does NOT directly kill windows — it sets goal to pending
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")

	// The kill happens on the NEXT tick when dispatch/dispatchRetry is called
	// Verify correction was written with timeout message
	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "timed out")
}

func TestTemplateContent_NoInteractionFlags(t *testing.T) {
	templateDir := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "php-symfony")
	entries, err := os.ReadDir(templateDir)
	require.NoError(t, err)

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(templateDir, e.Name()))
		require.NoError(t, err)
		content := string(data)

		if strings.Contains(content, "composer require") || strings.Contains(content, "composer install") {
			assert.Contains(t, content, "--no-interaction",
				"%s mentions composer command but lacks --no-interaction", e.Name())
		}
		if strings.Contains(content, "npm install") || strings.Contains(content, "npm ci") {
			assert.Contains(t, content, "CI=1",
				"%s mentions npm command but lacks CI=1", e.Name())
		}
	}
}

// --- 6.27: Goal→Task bridge dispatch.md + tasks.yaml ---

func TestDispatch_DispatchMdWellFormedForPlan(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Implement pricing module",
			Acceptance:  []string{"Price matches API"},
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMd := "# Implement pricing module\n\n## Acceptance Criteria\n\n- Price matches API response exactly\n- Currency formatting follows locale\n\n## Context\n\nThe pricing page was redesigned.\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0o644))

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Dispatch:")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "Price matches API response exactly")
	assert.Contains(t, content, "Currency formatting follows locale")
	assert.Contains(t, content, "## Prior Corrections")
}

func TestDispatch_PlanCommandSendsCorrectPath(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	expectedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	// dispatch() ships goal.ID as a trailing token so plan.xml gets an explicit
	// goal binding (see TestDispatch_PlanCommandCarriesGoalID).
	expectedCmd := "/tmux:plan " + expectedPath + " goal-001"
	exec.AssertCalled(t, "SendMessage", testSession, "@0", expectedCmd)
}

func TestDispatch_ResultingTasksYamlValid(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: implement pricing
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "valid single-task tasks.yaml should produce no validation errors")
}

// --- 6.28: Multi-task fan-out 3 BCs → 3 parallel tasks ---

func TestDispatch_FanOutHintsInGoalMd_PropagatedToDispatchMd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Fan-out test",
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMd := "# Fan-out test\n\n## Acceptance Criteria\n\n- All three BCs implemented\n\n## Fan-Out\n\nParallel tasks for three bounded contexts:\n- BC-Pricing: implement price calculation\n- BC-Display: implement UI rendering\n- BC-Logging: implement audit trail\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0o644))

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Fan-Out")
	assert.Contains(t, content, "BC-Pricing: implement price calculation")
	assert.Contains(t, content, "BC-Display: implement UI rendering")
	assert.Contains(t, content, "BC-Logging: implement audit trail")
}

func TestValidateTasksFile_ThreeParallelTasks(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: implement BC-Pricing
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
  - name: implement BC-Display
    wid: execute-2
    status: pending
    context: .tmux-cli/research/2026-05-28-14/display.md
  - name: implement BC-Logging
    wid: execute-3
    status: pending
    context: .tmux-cli/research/2026-05-28-14/logging.md
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "three parallel tasks should validate without errors")
}

func TestValidateTasksFile_ThreeTasksWithDependency(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: scaffold shared types
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/scaffold.md
  - name: implement BC-Pricing
    wid: execute-2
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
    depends_on:
      - execute-1
  - name: implement BC-Display
    wid: execute-3
    status: pending
    context: .tmux-cli/research/2026-05-28-14/display.md
    depends_on:
      - execute-1
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "three tasks with dependency fan-out should validate without errors")
}

func TestGoalTemplates_ContainTestRequirements(t *testing.T) {
	qualityGatesPath := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "quality-gates.md")
	testStrategyPath := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "test-strategy.md")

	qgData, err := os.ReadFile(qualityGatesPath)
	require.NoError(t, err, "quality-gates.md must exist")
	qgContent := strings.ToLower(string(qgData))

	tsData, err := os.ReadFile(testStrategyPath)
	require.NoError(t, err, "test-strategy.md must exist")
	tsContent := strings.ToLower(string(tsData))

	testKeywords := []string{"test", "unit test", "integration test", "e2e"}

	hasTestRef := false
	for _, kw := range testKeywords {
		if strings.Contains(qgContent, kw) || strings.Contains(tsContent, kw) {
			hasTestRef = true
			break
		}
	}
	assert.True(t, hasTestRef, "templates must contain test requirement patterns")

	assert.Contains(t, tsContent, "unit test", "test-strategy.md must define unit test layer")
	assert.Contains(t, tsContent, "integration test", "test-strategy.md must define integration test layer")
	assert.Contains(t, tsContent, "e2e test", "test-strategy.md must define e2e test layer")
}

func TestWaitForPrompt_PanicPropagates(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@0").Panic("simulated nil pointer dereference")

	assert.Panics(t, func() {
		_ = d.waitForPrompt("supervisor", 5*time.Second)
	}, "waitForPrompt must not swallow panics from CaptureWindowOutput")
}

// --- C4: validate-timeout clamp (derived from worker budget) ---

func TestNew_NoHardcodedValidateTimeout(t *testing.T) {
	exec := new(testutil.MockTmuxExecutor)
	d := New(t.TempDir(), exec)
	// The 5*time.Minute literal is gone; the field is finalized by the clamp in Run().
	assert.Equal(t, time.Duration(0), d.validateTimeout)
}

func TestDaemon_ClampValidateTimeout_BelowBudget(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	d := &Daemon{validateTimeout: 300 * time.Second}
	d.clampValidateTimeout(4)

	// DeriveValidateTimeout(600,4,4) = 1260 (incl. ValidatorOverheadSec).
	assert.Equal(t, 1260*time.Second, d.validateTimeout)
	assert.Contains(t, buf.String(), "clamping up")
}

func TestDaemon_ClampValidateTimeout_AboveBudget_NoOp(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	d := &Daemon{validateTimeout: 1800 * time.Second}
	d.clampValidateTimeout(4)

	assert.Equal(t, 1800*time.Second, d.validateTimeout)
	assert.Empty(t, buf.String())
}

func TestDaemon_ClampValidateTimeout_LoadFailureStillClamps(t *testing.T) {
	// Simulates the load-error branch: validateTimeout is the zero value from New().
	d := &Daemon{}
	d.clampValidateTimeout(setup.DefaultMaxWorkers)
	assert.GreaterOrEqual(t, d.validateTimeout, 1260*time.Second)
}

// countingCreateWindowFn returns a WindowCreateFunc that increments *count on
// each call, so precondition-block tests can assert no worker window is spawned.
func countingCreateWindowFn(count *int, tmuxWindowID string) WindowCreateFunc {
	return func(name, command, cwd string) (*CreatedWindow, error) {
		*count++
		return &CreatedWindow{TmuxWindowID: tmuxWindowID, Name: name}, nil
	}
}

// TestM01_PreflightPreconditionBlock (alias TestM01_UnsetSecret): a goal with an
// unset env precondition is blocked before any worker spawn — signal.json is
// written with verdict=blocked/class=env-config/owner=ops, no window is created,
// the retry counter is untouched, the goal is marked blocked, and dispatch
// returns nil (handled, not an error).
func TestM01_PreflightPreconditionBlock(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_UNSET_DB_USER"
	os.Unsetenv(envSpec)

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "needs DB_USER",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec + "=..."},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// No windows exist, so the kill/wait lookups all return empty.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	goal := &gf.Goals[0]
	retriesBefore := goal.Retries
	err = d.dispatch(goal, gf)
	require.NoError(t, err, "block path returns nil, not an error")

	assert.Equal(t, 0, createCount, "no worker window may be created on a block")
	assert.Equal(t, retriesBefore, goal.Retries, "retry counter must be untouched on a block")
	assert.Equal(t, GoalBlocked, goal.Status, "goal must be marked blocked")
	assert.True(t, goal.BlockedByPrecondition, "env/infra precondition block must flag the goal for §5 auto-resume")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "block signal must be a validator signal")
	assert.Equal(t, "blocked", vs.Verdict)
	assert.Equal(t, "env-config", vs.Class)
	assert.Equal(t, "ops", vs.Owner)
	assert.NotEmpty(t, vs.Remedy, "remedy runbook must be present")
	require.Len(t, vs.Findings, 1)
	assert.Equal(t, envSpec, vs.Findings[0].Rule)
	assert.Equal(t, "blocked", vs.Findings[0].Status)

	// Persisted goal status confirms the re-dispatch loop guard.
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, found := loaded.GoalByID("goal-001")
	require.True(t, found)
	assert.Equal(t, GoalBlocked, g.Status)
	assert.True(t, g.BlockedByPrecondition, "BlockedByPrecondition must persist so the resume loop can re-evaluate")
}

func TestEvaluatePreconditions_ServiceUnreachable(t *testing.T) {
	d, _, _ := setupDaemon(t)

	// Bind then immediately release a port so dialing it reliably fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	goal := &Goal{
		ID: "goal-001",
		Preconditions: []Precondition{
			{Kind: "service", Spec: addr, Remedy: "start the service on " + addr},
		},
	}

	ok, class, remedy := d.evaluatePreconditions(goal)
	assert.False(t, ok, "unreachable service must fail the precondition")
	assert.Equal(t, "infra-flake", class)
	assert.Equal(t, "start the service on "+addr, remedy)
}

func TestEvaluatePreconditions_AllPass(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_SET_VAR"
	t.Setenv(envSpec, "present")

	// A live listener makes the service precondition reachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "all preconds satisfied",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec},
				{Kind: "service", Spec: ln.Addr().String(), Remedy: "start svc"},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err = EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	ok, class, remedy := d.evaluatePreconditions(goal)
	assert.True(t, ok, "all preconditions satisfied → pass")
	assert.Empty(t, class)
	assert.Empty(t, remedy)

	// And dispatch spawns the supervisor exactly as before.
	setupDispatchMocks(exec, testSession, "@0")
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	err = d.dispatch(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, 1, createCount, "supervisor window must be spawned when preconditions pass")
	assert.Equal(t, GoalRunning, goal.Status)
}

// TestDispatchBlockedGoalNotRedispatched: a blocked goal lands in GoalBlocked so
// the next pending-goal selection skips it — the existing mechanism that halts
// the daemon's re-dispatch loop. The block signal is the only artifact written
// and no worker windows are created.
func TestDispatchBlockedGoalNotRedispatched(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_UNSET_REDISPATCH"
	os.Unsetenv(envSpec)

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "blocked goal",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	goal := &gf.Goals[0]
	retriesBefore := goal.Retries
	require.NoError(t, d.dispatch(goal, gf))

	assert.Equal(t, 0, createCount, "no supervisor/execute-* window on a block")
	assert.Equal(t, retriesBefore, goal.Retries, "retries untouched")
	assert.Equal(t, GoalBlocked, goal.Status)

	// signal.json is present (the sole signal artifact).
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	// Reloading and selecting the next pending goal must skip the blocked one,
	// so the poll loop never re-dispatches it.
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	_, hasPending := loaded.NextPendingGoal()
	assert.False(t, hasPending, "blocked goal must not be selected as pending again")
}

// --- C6 convergence circuit-breaker (M03) ---

// TestM03_CircuitBreakerHalt: when cycle 2 produces the SAME failure-signature
// set as cycle 1 and circuit_breaker_k=2 (default), the goal halts to
// blocked/owner=human BEFORE cycle 3 — without consuming the code budget and
// without re-dispatching. Fires on recurrence, not on budget exhaustion.
func TestM03_CircuitBreakerHalt(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	findings := []ValidationFinding{
		{Rule: "build", Status: "fail", FailureClass: "code-defect",
			FailingCommand: "go build ./...", OutputExcerpt: "undefined: Foo", Detail: "compile error"},
	}
	cycle1Sigs := ComputeSignatures(findings)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5,
				CodeRetries: 5, MaxCodeRetries: 5,
				ConvergenceSignatures: cycle1Sigs, ConvergenceStreak: 1},
			// A second pending goal so advanceToNextGoal just re-points
			// CurrentGoal (no deactivation / tmux teardown needed).
			{ID: "goal-002", Description: "next", Status: GoalPending, MaxRetries: 5, CodeRetries: 5, MaxCodeRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// Cycle 2 reports the SAME signature set.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", Findings: findings, NextAction: "fix build", Timestamp: "2026-06-01T15:00:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix build", "code-defect"))

	assert.Equal(t, GoalBlocked, goal.Status, "K-recurrence halts to blocked")
	assert.Equal(t, "convergence-circuit-breaker", goal.BlockedBy)
	assert.Equal(t, 2, goal.ConvergenceStreak, "streak reaches K=2")
	assert.Equal(t, 5, goal.CodeRetries, "code budget NOT decremented on circuit-break")
	assert.Less(t, goal.Retries, goal.MaxRetries, "halt is recurrence, not budget exhaustion")

	loaded, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	sig, ok := loaded.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", loaded)
	assert.Equal(t, VerdictBlocked, sig.Verdict, "persisted verdict is blocked")
	assert.Equal(t, "human", sig.Owner, "blocked verdict is owned by human")
	assert.Equal(t, cycle1Sigs, sig.Signatures, "blocked signal carries the recurrent signatures")

	// Phase NOT advanced to supervising for this goal (no re-dispatch).
	assert.NotEqual(t, phaseSupervising, d.runtime("goal-001").phase, "circuit-break does not re-dispatch")
}

// TestM03_NoHaltWhenSignaturesChange: when cycle 2's signature set differs from
// cycle 1, the streak resets to 1 and the goal follows the normal code-defect
// route (budget consumed, GoalPending re-dispatch) rather than halting.
func TestM03_NoHaltWhenSignaturesChange(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	cycle1Findings := []ValidationFinding{
		{Rule: "build", Status: "fail", FailureClass: "code-defect", Detail: "compile error in pkg a"},
	}
	cycle2Findings := []ValidationFinding{
		{Rule: "test", Status: "fail", FailureClass: "code-defect", Detail: "assertion failed in pkg b"},
	}
	cycle1Sigs := ComputeSignatures(cycle1Findings)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5,
				CodeRetries: 5, MaxCodeRetries: 5,
				ConvergenceSignatures: cycle1Sigs, ConvergenceStreak: 1},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", Findings: cycle2Findings, NextAction: "fix test", Timestamp: "2026-06-01T15:05:00Z",
	}))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix test", "code-defect"))

	assert.Equal(t, GoalPending, goal.Status, "changed signatures -> normal re-dispatch")
	assert.Equal(t, 1, goal.ConvergenceStreak, "streak resets to 1 on a new signature set")
	assert.Equal(t, ComputeSignatures(cycle2Findings), goal.ConvergenceSignatures, "baseline updated to current cycle")
	assert.Equal(t, 4, goal.CodeRetries, "code budget consumed on the normal route (5->4)")
}

// ---- §5: verdict-class-aware cascade + downstream auto-resume -------------

// TestCascadeFailure_HardFailBlocksDependents — a hard verdict class
// ("fail"/"code-defect") blocks every dependent with BlockedBy recorded.
func TestCascadeFailure_HardFailBlocksDependents(t *testing.T) {
	for _, class := range []string{"fail", "code-defect"} {
		gf := &GoalsFile{Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
		}}
		gf.CascadeFailure("goal-001", class)
		dep, _ := gf.GoalByID("goal-002")
		assert.Equal(t, GoalBlocked, dep.Status, "class %q hard-blocks direct dependent", class)
		assert.Equal(t, "goal-001", dep.BlockedBy, "class %q records BlockedBy", class)
		// BFS reaches the transitive dependent too.
		dep2, _ := gf.GoalByID("goal-003")
		assert.Equal(t, GoalBlocked, dep2.Status, "class %q hard-blocks transitive dependent", class)
	}
}

// TestCascadeFailure_SoftHoldLeavesPending — a soft verdict class
// ("blocked"/"env-config"/"infra-flake") leaves dependents GoalPending with
// BlockedBy recorded; never failed/blocked.
func TestCascadeFailure_SoftHoldLeavesPending(t *testing.T) {
	for _, class := range []string{"blocked", "env-config", "infra-flake"} {
		gf := &GoalsFile{Goals: []Goal{
			{ID: "goal-001", Status: GoalBlocked},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
		}}
		gf.CascadeFailure("goal-001", class)
		dep, _ := gf.GoalByID("goal-002")
		assert.Equal(t, GoalPending, dep.Status, "class %q keeps dependent pending", class)
		assert.NotEqual(t, GoalFailed, dep.Status, "class %q must not fail dependent", class)
		assert.NotEqual(t, GoalBlocked, dep.Status, "class %q must not block dependent", class)
		assert.Equal(t, "goal-001", dep.BlockedBy, "class %q records BlockedBy", class)
	}
}

// TestM07_BlockedUpstreamAutoResume — an env/infra-blocked upstream soft-holds
// its dependent (pending + BlockedBy), and when the upstream later completes
// resumeDownstream clears the hold without consuming any retry budget.
func TestM07_BlockedUpstreamAutoResume(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z"},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"},
			CodeRetries: 3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// (a) Upstream env-blocked: dependent stays pending with BlockedBy set.
	valSig := &ValidatorSignal{Verdict: "blocked", Class: "env-config", Owner: "ops", Remedy: "export DATABASE_URL"}
	require.NoError(t, d.haltBlockedEnv(&gf.Goals[0], gf, valSig))

	dep, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalPending, dep.Status, "soft hold leaves dependent pending (NOT failed/blocked)")
	assert.Equal(t, "goal-001", dep.BlockedBy, "dependent records its blocking upstream")
	assert.True(t, gf.Goals[0].BlockedByPrecondition, "upstream flagged for the auto-resume loop")
	beforeCode := dep.CodeRetries

	// (b) Upstream completes → resumeDownstream clears the hold, budget untouched.
	gf.Goals[0].Status = GoalDone
	d.resumeDownstream(gf, "goal-001")

	dep, _ = gf.GoalByID("goal-002")
	assert.Equal(t, "", dep.BlockedBy, "resume cleared BlockedBy")
	assert.False(t, dep.BlockedByPrecondition, "resume cleared the precondition flag")
	assert.Equal(t, GoalPending, dep.Status, "resumed goal stays pending for re-validation")
	assert.Equal(t, beforeCode, dep.CodeRetries, "resume consumed no code retry budget")
}

// TestHaltRetryCeiling_BlocksDependents — the retry-ceiling halt (no valSig in
// scope) cascades with the literal hard class "fail", blocking dependents.
func TestHaltRetryCeiling_BlocksDependents(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z"},
		{ID: "goal-002", Description: "independent", Status: GoalPending},
		{ID: "goal-003", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.haltRetryCeiling(&gf.Goals[0], gf))

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "ceiling halts the goal")
	dep, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalBlocked, dep.Status, "ceiling hard-blocks the dependent")
	assert.Equal(t, "goal-001", dep.BlockedBy)
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advanced to the independent pending goal (no deactivate)")
}

// TestResumeDownstreamLoop_StopsOnCtxCancel — the background loop returns
// promptly when its ctx is cancelled, leaking no goroutine.
func TestResumeDownstreamLoop_StopsOnCtxCancel(t *testing.T) {
	d, _, _ := setupDaemon(t)
	d.autoResumeInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.resumeDownstreamLoop(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resumeDownstreamLoop did not exit within one interval of ctx cancel")
	}
}

// TestResumeDownstreamLoop_PreconditionClears — scanPreconditionBlocked leaves a
// goal blocked while its precondition fails, and resumes it (pending, flag
// cleared, no budget consumed) once the precondition passes.
func TestResumeDownstreamLoop_PreconditionClears(t *testing.T) {
	d, _, dir := setupDaemon(t)
	const envVar = "TMUX_CLI_TEST_PRECOND_M07"
	require.NoError(t, os.Unsetenv(envVar))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true,
			Preconditions: []Precondition{{Kind: "env", Spec: envVar, Remedy: "export it"}},
			CodeRetries:   3, MaxCodeRetries: 3, SpecRetries: 1, MaxSpecRetries: 1,
			ValidationRetries: 1, MaxValidationRetries: 1},
	}}
	writeGoals(t, dir, gf)
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Class: "env-config", Owner: "ops",
		Findings:  []ValidationFinding{{Rule: "env:" + envVar, Status: "blocked", FailureClass: "env-config"}},
		Timestamp: "2026-05-20T10:00:00Z",
	}))

	// First tick: env unset → precondition still fails → stays blocked & flagged.
	d.scanPreconditionBlocked()
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, _ := reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status, "precondition still failing — goal stays blocked")
	assert.True(t, g.BlockedByPrecondition, "flag retained for next tick")

	// Clear the precondition and tick again.
	require.NoError(t, os.Setenv(envVar, "1"))
	defer os.Unsetenv(envVar)

	d.scanPreconditionBlocked()
	reloaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g, _ = reloaded.GoalByID("goal-001")
	assert.Equal(t, GoalPending, g.Status, "precondition cleared — resumed to pending")
	assert.False(t, g.BlockedByPrecondition, "precondition flag cleared on resume")
	assert.Equal(t, "", g.BlockedBy, "BlockedBy cleared on resume")
	assert.Equal(t, 3, g.CodeRetries, "resume consumed no code retry budget")
}

// TestBackfill_Goal002Preconditions — the goal.md the first-run backfill writes
// carries a ## Preconditions section while preserving the other goal content.
// (The first-run trigger itself is an LLM step in task-plan-generate.xml; this
// pins WriteGoalMD's precondition emission, the section that backfill produces.)
func TestBackfill_Goal002Preconditions(t *testing.T) {
	dir := t.TempDir()
	goalDir, err := EnsureGoalDir(dir, "goal-002")
	require.NoError(t, err)

	require.NoError(t, WriteGoalMD(goalDir, "Scaffold goal-002", "scaffold",
		[]string{"PA-01 scaffolding present"}, []string{"go build ./..."},
		[]Precondition{{Kind: "env", Spec: "DATABASE_URL", Remedy: "export DATABASE_URL"}},
		"context preserved", "out-of-scope preserved", nil))

	data, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	md := string(data)

	assert.Contains(t, md, "## Preconditions", "backfill injects the Preconditions section")
	assert.Contains(t, md, "DATABASE_URL", "precondition spec rendered")
	// Other content preserved.
	assert.Contains(t, md, "# Scaffold goal-002")
	assert.Contains(t, md, "PA-01 scaffolding present")
	assert.Contains(t, md, "go build ./...")
	assert.Contains(t, md, "context preserved")
	assert.Contains(t, md, "out-of-scope preserved")
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

// TestSpecDefectRouting_Unsubstantiated_ReRunsValidationOnly — a blocked/planner
// verdict whose only finding carries an empty/stub Detail AND Correction has no
// concretely-cited contradiction. The substance guard re-routes it to
// rerunValidationOnly (dec ValidationRetries 2->1) instead of bounceToGeneration,
// so the scarce single SpecRetries is preserved (no spec-retry burn + cascade).
func TestSpecDefectRouting_Unsubstantiated_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: ""}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	sentCmds := rerunValidationMocks(d, exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget UNTOUCHED — the scarce single retry is preserved")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 0, goal.BlockRetries, "block budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not generation")

	for _, c := range *sentCmds {
		assert.NotContains(t, c, "/tmux:supervisor", "implementer must NOT be re-dispatched")
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on an unsubstantiated verdict")
		assert.Contains(t, c, "/tmux:investigate", "unsubstantiated blocked/planner re-runs the validator only")
	}

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(corr)
	assert.True(t, os.IsNotExist(statErr), "no code/spec correction is written on a re-validate")
}

// TestSpecDefectRouting_TopLevelFallback_NoFindings_ReRunsValidationOnly — the
// dominant previo2 vector: a top-level blocked/planner verdict with NO
// classifiable findings (the :1286 fallback). ClassifyVerdict returns pass, the
// fallback promotes it to (blocked, planner), and the guard — finding no
// substantive contradiction — re-validates rather than charging SpecRetries.
func TestSpecDefectRouting_TopLevelFallback_NoFindings_ReRunsValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 1, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  nil,
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	sentCmds := rerunValidationMocks(d, exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation budget decremented 2->1")
	assert.Equal(t, 1, goal.SpecRetries, "spec budget UNTOUCHED on a contentless fallback verdict")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, GoalRunning, goal.Status, "stays running for re-validation")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-enters validating, not generation")

	for _, c := range *sentCmds {
		assert.NotContains(t, c, "/tmux:plan", "planner must NOT be re-dispatched on a no-finding fallback")
		assert.Contains(t, c, "/tmux:investigate", "fallback re-runs the validator only")
	}
}

// TestSpecDefectRouting_Substantive_NonStubCorrectionOnly_Bounces — predicate A
// (the zero-regression default): a blocked/planner finding with an empty Detail
// but a NON-stub Correction is substantive, so it still reaches bounceToGeneration
// and charges SpecRetries. Locks the recommended reading against a future switch
// to the stricter Detail-only predicate B.
func TestSpecDefectRouting_Substantive_NonStubCorrectionOnly_Bounces(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "planner",
		Findings:  []ValidationFinding{{Rule: "acceptance-3", Status: "blocked", FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: "acceptance #3 requires X but precondition forbids X"}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.SpecRetries, "spec budget decremented 2->1 — substantive defect still bounces")
	assert.Equal(t, 2, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, 1, goal.ValidationRetries, "validation budget untouched")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase, "marked for generation re-dispatch")
}

// --- E1-0d: per-goal fan-out tasks.yaml relocation ---

func TestTasksYamlExists_TrueForPerGoalFile(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeGoalTasksYaml(t, dir, "g1", "status: ready\ncycle: 1\ntasks: []\n")
	assert.True(t, d.tasksYamlExists("g1"))
}

func TestTasksYamlExists_FalseWhenOnlyTopLevelExists(t *testing.T) {
	d, _, dir := setupDaemon(t)
	// Only the top-level planning-queue exists — the per-goal probe must NOT
	// fall back to it, so a missing per-goal file routes to full dispatch.
	writeTasksYaml(t, dir, "status: ready\ncycle: 1\ntasks: []\n")
	assert.False(t, d.tasksYamlExists("g1"), "must not cross-read the top-level planning-queue")
}

func TestResetTaskStatuses_RependsPerGoalFile(t *testing.T) {
	d, _, dir := setupDaemon(t)

	// Sentinel top-level planning-queue that must be left untouched.
	writeTasksYaml(t, dir, `status: ready
cycle: 1
tasks:
  - name: "top task"
    wid: execute-9
    status: done
    context: top.md
`)

	writeGoalTasksYaml(t, dir, "g1", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: ctx1.md
  - name: "task two"
    wid: execute-2
    status: in_progress
    context: ctx2.md
`)

	require.NoError(t, d.resetTaskStatuses("g1"))

	data, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "g1"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "status: done", "all task statuses reset from done")
	assert.NotContains(t, content, "status: in_progress", "all task statuses reset from in_progress")
	assert.Contains(t, content, "status: pending", "tasks reset to pending")
	assert.Contains(t, content, "status: ready", "file-level status set to ready")

	// Top-level planning-queue must NOT be mutated.
	topData, err := os.ReadFile(tasks.TasksFilePath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(topData), "status: done", "top-level planning-queue must be left untouched")
}

func TestInjectCorrections_ReadsPerGoalTasksFile(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{ID: "g1", Description: "test", Status: GoalRunning, CodeRetries: 2, MaxCodeRetries: 3}
	_, err := EnsureGoalDir(dir, "g1")
	require.NoError(t, err)

	// CurrentCycle(goal)=2 -> injectCorrections reads corrections/cycle-1.md.
	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "g1", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Remove Doctrine from quality-gates.md"), 0o644))

	// The per-goal tasks file names the context .md to amend.
	writeGoalTasksYaml(t, dir, "g1", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Original context")

	require.NoError(t, d.injectCorrections(goal))

	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)
	assert.Contains(t, ctxContent, "# Original context", "original context preserved")
	assert.Contains(t, ctxContent, "## Prior Corrections", "corrections section appended from per-goal tasks file")
	assert.Contains(t, ctxContent, "Remove Doctrine from quality-gates.md", "correction content present")
}

func TestDispatchRetry_UsesPerGoalPath(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	// Sentinel top-level planning-queue: dispatchRetry must not touch it.
	writeTasksYaml(t, dir, `status: ready
cycle: 1
tasks:
  - name: "top task"
    wid: execute-9
    status: done
    context: top.md
`)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# context")

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	// Drives the tick retry-branch: tasksYamlExists("goal-001") gates re-dispatch.
	require.NoError(t, d.tick(context.Background(), gf))

	// reset operated on the GOAL-SCOPED file.
	goalData, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	assert.NotContains(t, string(goalData), "status: done", "per-goal tasks reset to pending")
	assert.Contains(t, string(goalData), "status: pending")

	// top-level planning-queue untouched.
	topData, err := os.ReadFile(tasks.TasksFilePath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(topData), "status: done", "top-level planning-queue must be left untouched")

	// retry sends /tmux:supervisor (skip planning), confirming the retry branch.
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:supervisor")
	assert.NotContains(t, sentCmd, "/tmux:plan")
}

// --- E1-0e: ready-set scheduler tests ---

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

// MaxGoals=1: two ready disjoint goals, exactly ONE dispatch this tick; the head
// is the first candidate and the second stays pending (byte-identical to the old
// single-goal cadence).
func TestTick_MaxGoalsOne_DispatchesSingleCandidate(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "first", Status: GoalPending},
			{ID: "goal-002", Description: "second disjoint", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 1)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "first goal dispatched")
	assert.Equal(t, GoalPending, gf.Goals[1].Status, "second goal stays pending at MaxGoals=1")
	assert.Equal(t, "goal-001", gf.CurrentGoal, "scalar head tracks the single in-flight goal")
}

// MaxGoals=2: two ready disjoint goals both reach GoalRunning in ONE tick, each
// with its own distinct namespaced supervisor window.
func TestTick_MaxGoalsTwo_DispatchesTwoDisjointGoals(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-020",
		Goals: []Goal{
			// Disjoint declared scopes: the disjoint-scope co-scheduling gate
			// (DisjointReadySet, wired into tick) admits BOTH this tick only because
			// their footprints provably do not overlap. Without a known scope the
			// gate would conservatively serialize them (see the _UnknownScope test).
			{ID: "goal-020", Description: "alpha", Status: GoalPending, Scope: []string{"internal/alpha/**"}},
			{ID: "goal-021", Description: "beta disjoint", Status: GoalPending, Scope: []string{"internal/beta/**"}},
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 2)
	_, err := EnsureGoalDir(dir, "goal-020")
	require.NoError(t, err)
	_, err = EnsureGoalDir(dir, "goal-021")
	require.NoError(t, err)

	// goal-020 dispatch then goal-021 dispatch, each its own namespaced supervisor.
	setupNamespacedDispatchMocks(exec, testSession, "supervisor-020", "@20")
	setupNamespacedDispatchMocks(exec, testSession, "supervisor-021", "@21")
	exec.On("CaptureWindowOutput", testSession, mock.Anything).Return("❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)

	var createdNames []string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createdNames = append(createdNames, name)
		id := "@" + name[len(name)-2:]
		return &CreatedWindow{TmuxWindowID: id, Name: name}, nil
	})

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "goal-020 dispatched")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "goal-021 dispatched same tick")
	assert.Equal(t, "goal-020", gf.CurrentGoal, "scalar head stays on the first in-flight goal")
	assert.Contains(t, createdNames, "supervisor-020", "goal-020 owns a distinct supervisor window")
	assert.Contains(t, createdNames, "supervisor-021", "goal-021 owns a distinct supervisor window")
	assert.NotEqual(t, "supervisor-020", "supervisor-021")
}

// MaxGoals=2 but neither candidate declares a scope (UNKNOWN): the disjoint-scope
// gate wired into tick must conservatively serialize — only the head goal
// dispatches this tick, the second stays pending until a free slot opens. This
// locks the gate's wiring at the tick level: it is NOT enough that two slots are
// free; co-scheduling requires PROVABLY disjoint declared scope.
func TestTick_MaxGoalsTwo_UnknownScopeSerializes(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-020",
		Goals: []Goal{
			{ID: "goal-020", Description: "alpha", Status: GoalPending}, // no scope
			{ID: "goal-021", Description: "beta", Status: GoalPending},  // no scope
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 2)
	_, err := EnsureGoalDir(dir, "goal-020")
	require.NoError(t, err)
	_, err = EnsureGoalDir(dir, "goal-021")
	require.NoError(t, err)

	setupNamespacedDispatchMocks(exec, testSession, "supervisor-020", "@20")
	exec.On("CaptureWindowOutput", testSession, mock.Anything).Return("❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)

	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		id := "@" + name[len(name)-2:]
		return &CreatedWindow{TmuxWindowID: id, Name: name}, nil
	})

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "head goal-020 dispatched (vacuously co-schedulable)")
	assert.Equal(t, GoalPending, gf.Goals[1].Status, "goal-021 serialized: unknown scope cannot co-schedule with an in-flight goal")
}

// MaxGoals=2 but both goals already running -> 0 free slots -> no new dispatch;
// each running goal is driven through checkProgress (no signal -> stays running).
func TestTick_MaxGoalsTwo_NoFreeSlotsSkipsDispatch(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-020",
		Goals: []Goal{
			{ID: "goal-020", Description: "alpha", Status: GoalRunning},
			{ID: "goal-021", Description: "beta", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 2)
	// Both runtimes mid-supervising with no signal on disk -> checkProgress is a no-op.
	d.runtime("goal-020").phase = phaseSupervising
	d.runtime("goal-021").phase = phaseSupervising

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "no free slot: goal-020 stays running")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "no free slot: goal-021 stays running")
	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

// MaxGoals=3 with a single ready candidate -> exactly one dispatch, remaining
// slots idle, no error.
func TestTick_MaxGoalsThree_OneCandidateLeavesSlotsIdle(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-030",
		Goals: []Goal{
			{ID: "goal-030", Description: "only ready", Status: GoalPending},
			{ID: "goal-031", Description: "waits on 030", Status: GoalPending, DependsOn: []string{"goal-030"}},
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 3)
	_, err := EnsureGoalDir(dir, "goal-030")
	require.NoError(t, err)

	setupNamespacedDispatchMocks(exec, testSession, "supervisor-030", "@30")
	exec.On("CaptureWindowOutput", testSession, mock.Anything).Return("❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@30"))

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "the one ready goal dispatched")
	assert.Equal(t, GoalPending, gf.Goals[1].Status, "dependent goal stays pending; spare slots idle")
}

// All goals terminal and none running -> deactivateOnCompletion fires and the
// daemon goes idle with a completion report.
func TestTick_AllTerminalNoneRunning_Deactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
			{ID: "goal-002", Description: "failed", Status: GoalFailed},
		},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, modeIdle, d.mode, "no running, no candidates -> deactivate")
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md"))
}

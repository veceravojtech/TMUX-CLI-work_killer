package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	d := New(dir, exec)
	d.pollInterval = 50 * time.Millisecond
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
	return func(name, command string) (*CreatedWindow, error) {
		return &CreatedWindow{TmuxWindowID: tmuxWindowID, Name: name}, nil
	}
}

func setupDeactivateMocks(exec *testutil.MockTmuxExecutor, session, newWindowID string) {
	// 3 calls for kill lookups: killWindowByName("supervisor"), killWindowsByPrefix("execute-"), killWindowByName("validator")
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Times(3)
	// 1 call for collectManagedNames
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitWindowsGone (immediate success)
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitClaudeBoot
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: newWindowID, Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
}

func setupDispatchMocks(exec *testutil.MockTmuxExecutor, session, newWindowID string) {
	// 3 calls for kill lookups
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Times(3)
	// 1 call for collectManagedNames
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitWindowsGone
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Once()
	// 1 call for waitClaudeBoot
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: newWindowID, Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
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
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@3", Name: "execute-3"},
		{TmuxWindowID: "@4", Name: "validator"},
	}, nil)
	exec.On("KillWindow", testSession, "@0").Return(nil)
	exec.On("KillWindow", testSession, "@1").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)
	exec.On("KillWindow", testSession, "@4").Return(nil)

	err := d.activate(gf)
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@0")
	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
	exec.AssertCalled(t, "KillWindow", testSession, "@4")
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
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Calls 1-3: kill lookups find managed windows
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@2", Name: "execute-2"},
		{TmuxWindowID: "@3", Name: "validator"},
	}, nil).Times(3)
	exec.On("KillWindow", testSession, "@0").Return(nil)
	exec.On("KillWindow", testSession, "@2").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)
	// Call 4: collectManagedNames — windows gone after kills
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 5: waitWindowsGone — immediate success
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 6+: waitClaudeBoot — supervisor booted
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	err := d.deactivate()
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@0")
	exec.AssertCalled(t, "KillWindow", testSession, "@2")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
}

func TestDeactivate_WaitsForWindowsGone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Calls 1-3: kill lookups find supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Times(3)
	exec.On("KillWindow", testSession, "@0").Return(nil)
	// Call 4: collectManagedNames
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 5: waitWindowsGone — first poll still has supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	// Call 6: waitWindowsGone — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 7+: waitClaudeBoot
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor", CurrentCommand: "claude"},
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

	setupDeactivateMocks(exec, testSession, "@0")

	var createdName string
	d.SetWindowCreateFunc(func(name, command string) (*CreatedWindow, error) {
		createdName = name
		return &CreatedWindow{TmuxWindowID: "@0", Name: name}, nil
	})

	err := d.deactivate()
	require.NoError(t, err)
	assert.Equal(t, "supervisor", createdName)
}

func TestDeactivate_WaitsForClaudeBoot(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	// Calls 1-3: kill lookups
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(3)
	// Call 4: collectManagedNames
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 5: waitWindowsGone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 6: waitClaudeBoot — first poll zsh
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil).Once()
	// Call 7+: waitClaudeBoot — claude
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
		{TmuxWindowID: "@0", Name: "supervisor"},
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
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	// Call 6: waitWindowsGone — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 7: waitClaudeBoot — zsh
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil).Once()
	// Call 8+: waitClaudeBoot — claude
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	d.SetWindowCreateFunc(func(name, command string) (*CreatedWindow, error) {
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

	assert.WithinDuration(t, time.Now(), d.currentGoalDispatchTime, time.Second)
	assert.True(t, d.currentGoalDispatchTime.After(before) || d.currentGoalDispatchTime.Equal(before))
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

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	err := d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)
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

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	err := d.tick(ctx, gf)
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)
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

func setupValidatorMocks(exec *testutil.MockTmuxExecutor, session, validatorWindowID string) {
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: validatorWindowID, Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", session, validatorWindowID, "/tmux:validate").Return(nil)
	exec.On("SendMessageWithDelay", session, validatorWindowID, mock.Anything).Return(nil)
}

func TestCheckProgress_SupervisorDone_TransitionsToValidating(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseSupervising
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

	assert.Equal(t, "done", d.lastSupervisorStatus)
	assert.Equal(t, phaseValidating, d.phase)
	assert.False(t, d.currentGoalValidateTime.IsZero())

	sig, err := LoadSignal(dir, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestCheckProgress_SupervisorStopped_TransitionsToValidating(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseSupervising
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

	assert.Equal(t, "stopped", d.lastSupervisorStatus)
	assert.Equal(t, phaseValidating, d.phase)

	sig, err := LoadSignal(dir, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestCheckProgress_Supervising_NoSignalWithinTimeout(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseSupervising
	d.currentGoalDispatchTime = time.Now().Add(-10 * time.Minute)
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
	d.phase = phaseSupervising
	d.dispatchTimeout = 3600 * time.Second
	d.currentGoalDispatchTime = time.Now().Add(-3601 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.Retries)
	assert.Equal(t, GoalPending, goal.Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(correctionPath)
	assert.NoError(t, statErr)
}

func TestCheckProgress_Supervising_CrashDetected(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseSupervising
	d.dispatchTimeout = time.Hour
	d.currentGoalDispatchTime = time.Now().Add(-10 * time.Second)
	d.bootConfirmedAt = time.Now().Add(-6 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.Retries)
	assert.Equal(t, GoalPending, goal.Status)
}

func TestCheckProgress_ValidatorPass_GoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseValidating

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
		{TmuxWindowID: "@5", Name: "validator"},
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
	d.phase = phaseValidating
	d.lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix price calc", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.Retries)
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
	d.phase = phaseValidating
	d.lastSupervisorStatus = "stopped"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "finish booking page", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.Retries)
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
	d.phase = phaseValidating
	d.lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 2, MaxRetries: 3},
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
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 3, goal.Retries)
	assert.Equal(t, GoalFailed, goal.Status)
	assert.Equal(t, "goal-002", gf.CurrentGoal)
}

func TestCheckProgress_Validating_TimeoutExceeded(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseValidating
	d.validateTimeout = 300 * time.Second
	d.currentGoalValidateTime = time.Now().Add(-301 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.Retries)
	assert.Equal(t, GoalPending, goal.Status)
}

func TestCheckProgress_Validating_CrashDetected(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.phase = phaseValidating
	d.validateTimeout = 5 * time.Minute
	d.currentGoalValidateTime = time.Now().Add(-10 * time.Second)
	d.bootConfirmedAt = time.Now().Add(-6 * time.Second)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "zsh"},
	}, nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.Retries)
	assert.Equal(t, GoalPending, goal.Status)
}

func TestWriteCorrectionFile_DoneHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	err := d.writeCorrectionFile(goalDir, 1, "Implementation completed but failed acceptance criteria.", "fix the pricing")
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
	err := d.writeCorrectionFile(goalDir, 2, header, "finish booking page")
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

	err := d.writeCorrectionFile(goalDir, 1, "header", "content")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "header")
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

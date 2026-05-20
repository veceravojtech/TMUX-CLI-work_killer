package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Crash Recovery Tests ---

func TestCrashRecovery_NoGuardFile(t *testing.T) {
	d, _, _ := setupDaemon(t)

	err := d.crashRecovery()
	require.NoError(t, err)
	assert.Equal(t, modeIdle, d.mode)
}

func TestCrashRecovery_GuardWithSupervisorWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseSupervising, d.phase)
	assert.WithinDuration(t, time.Now(), d.phaseStartedAt, time.Second)
	assert.True(t, d.phaseStartedAt.After(before) || d.phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_GuardWithValidatorWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "validator"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.phase)
	assert.WithinDuration(t, time.Now(), d.phaseStartedAt, time.Second)
	assert.True(t, d.phaseStartedAt.After(before) || d.phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_GuardWithSignalFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, "goal-001", d.currentGoal)
	assert.WithinDuration(t, time.Now(), d.phaseStartedAt, time.Second)
	assert.True(t, d.phaseStartedAt.After(before) || d.phaseStartedAt.Equal(before))
	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
}

func TestCrashRecovery_GuardNoWindowsRetriesLeft(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 1, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g.Status)
}

func TestCrashRecovery_GuardNoWindowsRetriesExhausted(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 3, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status)
}

func TestCrashRecovery_MissingGoalsYaml(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode)
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestCrashRecovery_GuardCorruptGoals(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	goalsPath := filepath.Join(dir, ".tmux-cli", "goals.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(goalsPath), 0o755))
	require.NoError(t, os.WriteFile(goalsPath, []byte("{{invalid yaml"), 0o644))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode)
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestCrashRecovery_GuardNoRunningGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
			{ID: "goal-002", Description: "failed", Status: GoalFailed},
		},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode)
}

// --- Signal Handler Tests ---

func TestSignalHandler_SessionAlive(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)
	writeSettings(t, dir, true, true)

	exitCh := make(chan int, 1)
	d.exitFunc = func(code int) { exitCh <- code }

	exec.On("HasSession", testSession).Return(true, nil)
	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	d.setupSignalHandler(context.Background())

	d.signalCh <- syscall.SIGTERM

	select {
	case code := <-exitCh:
		assert.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for exit")
	}

	assert.Equal(t, modeIdle, d.mode)
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestSignalHandler_SessionGone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	exitCh := make(chan int, 1)
	d.exitFunc = func(code int) { exitCh <- code }

	exec.On("HasSession", testSession).Return(false, nil)

	d.setupSignalHandler(context.Background())

	d.signalCh <- syscall.SIGTERM

	select {
	case code := <-exitCh:
		assert.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for exit")
	}

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))

	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
}

func TestSignalHandler_CancelsContext(t *testing.T) {
	d, exec, _ := setupDaemon(t)

	exitCh := make(chan int, 1)
	d.exitFunc = func(code int) { exitCh <- code }

	exec.On("HasSession", mock.Anything).Return(false, nil)

	d.setupSignalHandler(context.Background())

	select {
	case <-d.ctx.Done():
		t.Fatal("ctx should not be done yet")
	default:
	}

	d.signalCh <- syscall.SIGINT

	select {
	case <-d.ctx.Done():
		// Context was cancelled
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for context cancellation")
	}

	<-exitCh
}

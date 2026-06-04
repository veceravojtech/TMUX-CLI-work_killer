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
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, "goal-001", d.currentGoal)
	// Crash recovery now resets supervisor-phase goals to pending for re-dispatch
	goals, err2 := LoadGoals(dir)
	require.NoError(t, err2)
	g, _ := goals.GoalByID("goal-001")
	assert.Equal(t, GoalPending, g.Status)
	_ = before
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
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
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
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)
	assert.Equal(t, "goal-001", d.currentGoal)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
}

func TestCrashRecovery_GuardWithValidatorSignalFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.Equal(t, "goal-001", d.currentGoal)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
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

func TestCrashRecovery_MultipleRunningGoals_AllRecovered(t *testing.T) {
	// MaxGoals>1: after a crash NO supervisor survives, so EVERY in-flight goal must
	// be recovered — not just the first. Recovering one and leaving the others as
	// GoalRunning strands them as zombies that permanently consume the running
	// budget (free = maxGoals - running), so no free slot ever refills.
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-045",
		Goals: []Goal{
			{ID: "goal-045", Description: "pricing", Status: GoalRunning, Retries: 1, MaxRetries: 3},
			{ID: "goal-046", Description: "identity", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.crashRecovery()
	require.NoError(t, err)
	assert.Equal(t, modeActive, d.mode)

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g45, ok45 := goals.GoalByID("goal-045")
	require.True(t, ok45)
	g46, ok46 := goals.GoalByID("goal-046")
	require.True(t, ok46)
	assert.Equal(t, GoalPending, g45.Status, "goal-045 must be re-pended for re-dispatch")
	assert.Equal(t, GoalPending, g46.Status, "goal-046 must ALSO be re-pended, not stranded as a zombie running goal")
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

func TestCrashRecovery_GuardWithInvestigatorWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "inv-goal-001"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_GuardWithMultipleInvWindows(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "inv-goal-001"},
		{TmuxWindowID: "@1", Name: "inv-goal-002"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_GuardWithValidatorAndInvWindows(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "validator"},
		{TmuxWindowID: "@1", Name: "inv-goal-001"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_GuardWithSupervisorAndInvWindows(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "inv-goal-001"},
	}, nil)

	before := time.Now()
	err := d.crashRecovery()
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").phaseStartedAt, time.Second)
	assert.True(t, d.runtime("goal-001").phaseStartedAt.After(before) || d.runtime("goal-001").phaseStartedAt.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal)
}

func TestCrashRecovery_ReDispatchSavesOnce(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	goalsPath := filepath.Join(dir, ".tmux-cli", "goals.yaml")
	infoBefore, err := os.Stat(goalsPath)
	require.NoError(t, err)
	timeBefore := infoBefore.ModTime()

	// Small sleep so any file write gets a distinct mtime
	time.Sleep(10 * time.Millisecond)

	err = d.crashRecovery()
	require.NoError(t, err)

	// Read the final saved state
	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g.Status, "retries left → status must be pending")

	// Verify the file was written (mtime changed)
	infoAfter, err := os.Stat(goalsPath)
	require.NoError(t, err)
	assert.True(t, infoAfter.ModTime().After(timeBefore), "goals file should have been saved")
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

package taskvisor

import (
	"context"
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

func setupIntegrationDaemon(t *testing.T) (*Daemon, *testutil.MockTmuxExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	d := New(dir, exec)
	d.pollInterval = 50 * time.Millisecond
	d.validatorSendDelay = 0
	d.promptSettleDelay = 0
	d.promptPollInterval = 0
	d.session = testSession
	d.mode = modeActive
	writeSettings(t, dir, true, true)
	return d, exec, dir
}

func writeSupervisorSignal(t *testing.T, dir, goalID, status string) {
	t.Helper()
	require.NoError(t, SaveSupervisorSignal(dir, goalID, &SupervisorSignal{
		Status: status, Timestamp: time.Now().Format(time.RFC3339),
	}))
}

func writeValidatorSignal(t *testing.T, dir, goalID, verdict, nextAction string) {
	t.Helper()
	require.NoError(t, SaveValidatorSignal(dir, goalID, &ValidatorSignal{
		Verdict: verdict, NextAction: nextAction, Timestamp: time.Now().Format(time.RFC3339),
	}))
}

func setupKillMocksEmpty(exec *testutil.MockTmuxExecutor) {
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
}

func TestIntegration_FullCyclePass(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "Fix pricing",
			Acceptance: []string{"Price matches API"},
			Status:     GoalPending, MaxRetries: 3,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// --- Tick 1: dispatch (pending → running) ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.Equal(t, phaseSupervising, d.phase)

	dispatchPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	_, statErr := os.Stat(dispatchPath)
	assert.NoError(t, statErr)

	// --- Tick 2: supervising, supervisor completes → transitions to validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)

	// --- Tick 3: validating, validator passes → goal done, deactivate ---
	writeValidatorSignal(t, dir, "goal-001", "pass", "")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, modeIdle, d.mode)

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr = os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestIntegration_FullCycleFailRetry(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "Fix checkout",
			Acceptance: []string{"Checkout works"},
			Status:     GoalPending, MaxRetries: 3,
			CodeRetries: 3, MaxCodeRetries: 3,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// --- Tick 1: dispatch ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)

	// --- Tick 2: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)
	assert.Equal(t, "done", d.lastSupervisorStatus)

	// --- Tick 3: validator fails → correction written, retries++ ---
	writeValidatorSignal(t, dir, "goal-001", "fail", "fix the price calc")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, 2, gf.Goals[0].CodeRetries, "code budget decremented 3->2")
	assert.Equal(t, 0, gf.Goals[0].Retries, "legacy Retries stays read-only")
	assert.Equal(t, GoalPending, gf.Goals[0].Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.True(t, strings.HasPrefix(string(data), "Implementation completed but failed acceptance criteria."))
	assert.Contains(t, string(data), "fix the price calc")
}

func TestIntegration_StoppedRetry(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "Build feature",
			Acceptance: []string{"Feature works"},
			Status:     GoalPending, MaxRetries: 3,
			CodeRetries: 3, MaxCodeRetries: 3,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// --- Tick 1: dispatch ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	require.NoError(t, d.tick(ctx, gf))

	// --- Tick 2: supervisor stopped → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "stopped")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, "stopped", d.lastSupervisorStatus)

	// --- Tick 3: validator fails → correction with stopped header ---
	writeValidatorSignal(t, dir, "goal-001", "fail", "finish the booking page")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, 2, gf.Goals[0].CodeRetries, "code budget decremented 3->2")
	assert.Equal(t, GoalPending, gf.Goals[0].Status)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.True(t, strings.HasPrefix(string(data), "Previous cycle hit the supervisor cycle limit"))
	assert.Contains(t, string(data), "finish the booking page")
}

func TestIntegration_MaxRetriesExhausted(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "Impossible task",
			Acceptance: []string{"Can't be done"},
			Status:     GoalPending, MaxRetries: 1,
			CodeRetries: 1, MaxCodeRetries: 1,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Cycle 1: dispatch → supervise → validate fail (retries 0→1) ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))

	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))

	writeValidatorSignal(t, dir, "goal-001", "fail", "first failure")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	// max_retries=1, after fail retries becomes 1 >= 1, so goal fails → deactivate
	setupDeactivateMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))

	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, 0, gf.Goals[0].CodeRetries, "code budget exhausted 1->0")
	assert.Equal(t, GoalFailed, gf.Goals[0].Status)
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_MultiGoalSequential(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First", Acceptance: []string{"A"}, Status: GoalPending, MaxRetries: 3},
			{ID: "goal-002", Description: "Second", Acceptance: []string{"B"}, Status: GoalPending, MaxRetries: 3},
			{ID: "goal-003", Description: "Third", Acceptance: []string{"C"}, Status: GoalPending, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	passGoal := func(goalID string) {
		// dispatch
		exec.ExpectedCalls = nil
		exec.Calls = nil
		setupDispatchMocks(exec, testSession, "@0")
		d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
		require.NoError(t, d.tick(ctx, gf))

		// reload goals from disk to get consistent state
		loaded, err := LoadGoals(dir)
		require.NoError(t, err)
		*gf = *loaded

		// supervisor done
		writeSupervisorSignal(t, dir, goalID, "done")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
		}, nil)
		exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
		exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
			return strings.HasPrefix(cmd, "/tmux:investigate ")
		})).Return(nil)
		d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
		require.NoError(t, d.tick(ctx, gf))

		// validator pass
		writeValidatorSignal(t, dir, goalID, "pass", "")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator"},
		}, nil).Once()
		exec.On("KillWindow", testSession, "@5").Return(nil)
	}

	// Pass goal-001
	passGoal("goal-001")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g1, _ := gf.GoalByID("goal-001")
	assert.Equal(t, GoalDone, g1.Status)
	assert.Equal(t, "goal-002", gf.CurrentGoal)

	// Pass goal-002
	passGoal("goal-002")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g2, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalDone, g2.Status)
	assert.Equal(t, "goal-003", gf.CurrentGoal)

	// Pass goal-003 — should deactivate
	passGoal("goal-003")
	setupDeactivateMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g3.Status)
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_CrashRecoveryMidValidation(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)
	d.mode = modeIdle

	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "In progress",
			Acceptance: []string{"Must pass"},
			Status:     GoalRunning, MaxRetries: 3,
		}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)

	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)

	require.NoError(t, d.crashRecovery())
	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, phaseValidating, d.phase)
	assert.Equal(t, "goal-001", d.currentGoal)

	// Now simulate validator completing with pass
	writeValidatorSignal(t, dir, "goal-001", "pass", "")
	exec.ExpectedCalls = nil
	exec.Calls = nil

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	goal, ok := gf.GoalByID("goal-001")
	require.True(t, ok)

	require.NoError(t, d.checkProgress(goal, gf))
	assert.Equal(t, GoalDone, goal.Status)
}

func TestIntegration_DispatchMdExcludesValidateRules(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "Feature with validate rules",
			Acceptance: []string{"UI shows correct price", "API returns 200"},
			Validate:   []string{"run pricing e2e test", "check format compliance"},
			Status:     GoalPending, MaxRetries: 3,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	ctx := context.Background()
	require.NoError(t, d.tick(ctx, gf))

	dispatchPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	data, readErr := os.ReadFile(dispatchPath)
	require.NoError(t, readErr)
	content := string(data)

	assert.Contains(t, content, "UI shows correct price")
	assert.Contains(t, content, "API returns 200")
	assert.NotContains(t, content, "run pricing e2e test")
	assert.NotContains(t, content, "check format compliance")
	assert.NotContains(t, content, "Validate")
}

func TestIntegration_GuardFileLifecycle(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)
	d.mode = modeIdle

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")

	// activate → guard file created
	require.NoError(t, d.activate(gf))
	_, statErr := os.Stat(guardPath)
	assert.NoError(t, statErr, "guard file should exist after activate")
	assert.Equal(t, modeActive, d.mode)

	// deactivate → guard file removed
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.deactivate())
	_, statErr = os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed after deactivate")
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_HookGuardSkip(t *testing.T) {
	dir := t.TempDir()
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")

	// No guard file → hook should run
	_, err := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(err), "guard file should not exist initially")

	// Create guard file → hook should skip
	require.NoError(t, os.MkdirAll(filepath.Dir(guardPath), 0o755))
	require.NoError(t, os.WriteFile(guardPath, nil, 0o644))

	_, err = os.Stat(guardPath)
	assert.NoError(t, err, "guard file should exist — hook should skip")

	// Remove guard file → hook should run again
	require.NoError(t, os.Remove(guardPath))
	_, err = os.Stat(guardPath)
	assert.True(t, os.IsNotExist(err), "guard file removed — hook should run")
}

func TestIntegration_RetryExhaustion_CascadeAndDeactivate(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Root task", Acceptance: []string{"Must pass"}, Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1},
			{ID: "goal-002", Description: "Depends on root", Acceptance: []string{"B"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "Depends on root too", Acceptance: []string{"C"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: dispatch goal-001 ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)

	// --- Tick 2: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)

	// --- Tick 3: validator fails → retries exhausted → cascade + deactivateOnCompletion ---
	writeValidatorSignal(t, dir, "goal-001", "fail", "root task failed")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	// Verify cascade: A=failed, B+C=blocked(blocked_by=goal-001)
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	g2, _ := loaded.GoalByID("goal-002")
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalFailed, g1.Status)
	assert.Equal(t, 0, g1.CodeRetries)
	assert.Equal(t, GoalBlocked, g2.Status)
	assert.Equal(t, "goal-001", g2.BlockedBy)
	assert.Equal(t, GoalBlocked, g3.Status)
	assert.Equal(t, "goal-001", g3.BlockedBy)

	// Verify completion report
	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Failed | 1")
	assert.Contains(t, report, "Blocked| 2")

	// Verify guard file removed and mode idle
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file should be removed after deactivateOnCompletion")
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_RetryExhaustion_DiamondDeps_TerminalState(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Diamond root", Acceptance: []string{"Root"}, Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1},
			{ID: "goal-002", Description: "Left branch", Acceptance: []string{"Left"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "Right branch", Acceptance: []string{"Right"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-004", Description: "Diamond bottom", Acceptance: []string{"Bottom"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002", "goal-003"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: dispatch goal-001 ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))

	// --- Tick 2: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))

	// --- Tick 3: validator fails → cascade all dependents + deactivateOnCompletion ---
	writeValidatorSignal(t, dir, "goal-001", "fail", "diamond root failed")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	// Verify cascade: A=failed, B+C+D all blocked by goal-001
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	assert.Equal(t, GoalFailed, g1.Status)

	for _, id := range []string{"goal-002", "goal-003", "goal-004"} {
		g, ok := loaded.GoalByID(id)
		require.True(t, ok, "goal %s should exist", id)
		assert.Equal(t, GoalBlocked, g.Status, "%s should be blocked", id)
		assert.Equal(t, "goal-001", g.BlockedBy, "%s should be blocked by goal-001", id)
	}

	// Verify report: 1 failed + 3 blocked
	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Failed | 1")
	assert.Contains(t, report, "Blocked| 3")
	assert.Contains(t, report, "Total  | 4")

	// Verify terminal state
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_RetryExhaustion_PartialProgress_TerminalState(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{ID: "goal-001", Description: "Already done", Acceptance: []string{"Done"}, Status: GoalDone, MaxRetries: 3},
			{ID: "goal-002", Description: "Will fail", Acceptance: []string{"Fail"}, Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "Blocked by fail", Acceptance: []string{"Blocked"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: dispatch goal-002 (goal-001 already done) ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[1].Status)

	// --- Tick 2: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-002", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))

	// --- Tick 3: validator fails → retries exhausted → cascade + deactivateOnCompletion ---
	writeValidatorSignal(t, dir, "goal-002", "fail", "goal-002 failed")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	// Verify: A stays done, B=failed, C=blocked
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	g2, _ := loaded.GoalByID("goal-002")
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g1.Status, "goal-001 should remain done")
	assert.Equal(t, GoalFailed, g2.Status)
	assert.Equal(t, 0, g2.CodeRetries)
	assert.Equal(t, GoalBlocked, g3.Status)
	assert.Equal(t, "goal-002", g3.BlockedBy)

	// Verify report: 1 done + 1 failed + 1 blocked
	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Done   | 1")
	assert.Contains(t, report, "Failed | 1")
	assert.Contains(t, report, "Blocked| 1")
	assert.Contains(t, report, "Total  | 3")

	// Verify per-goal details in report
	assert.Contains(t, report, "goal-001: Already done")
	assert.Contains(t, report, "goal-002: Will fail")
	assert.Contains(t, report, "goal-003: Blocked by fail")

	// Verify terminal state
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_GlobalRetryCeiling_MultiGoalHaltWithReport(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal:      "goal-001",
		GlobalMaxRetries: 3,
		Goals: []Goal{
			{ID: "goal-001", Description: "BC-A root", Status: GoalPending, Retries: 3, MaxRetries: 5},
			{ID: "goal-002", Description: "BC-A dependent", Status: GoalPending, MaxRetries: 5, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "BC-B independent", Status: GoalPending, MaxRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: goal-001 pending, ceiling reached → haltRetryCeiling ---
	// goal-001 → Failed + cascade (goal-002 → Blocked), advance to goal-003
	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	g2, _ := loaded.GoalByID("goal-002")
	assert.Equal(t, GoalFailed, g1.Status)
	assert.NotEmpty(t, g1.FinishedAt)
	assert.Equal(t, GoalBlocked, g2.Status)
	assert.Equal(t, "goal-001", g2.BlockedBy)
	assert.Equal(t, "goal-003", loaded.CurrentGoal)

	// --- Tick 2: goal-003 pending, ceiling still reached → haltRetryCeiling ---
	// goal-003 → Failed, no pending left → deactivateOnCompletion → report
	*gf = *loaded
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalFailed, g3.Status)
	assert.NotEmpty(t, g3.FinishedAt)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Failed | 2")
	assert.Contains(t, report, "Blocked| 1")
	assert.Contains(t, report, "Total  | 3")

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, modeIdle, d.mode)

	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestIntegration_CrossBCIndependence_FailedGoalDoesNotBlockOtherBC(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "BC-A root", Acceptance: []string{"A passes"}, Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1},
			{ID: "goal-002", Description: "BC-A dependent", Acceptance: []string{"B passes"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "BC-B independent", Acceptance: []string{"C passes"}, Status: GoalPending, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: dispatch goal-001 ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.Equal(t, phaseSupervising, d.phase)

	// --- Tick 2: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)

	// --- Tick 3: validator fail → goal-001 retries exhausted (1>=1) → cascade + advance ---
	writeValidatorSignal(t, dir, "goal-001", "fail", "BC-A root failed")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	g2, _ := loaded.GoalByID("goal-002")
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalFailed, g1.Status)
	assert.Equal(t, 0, g1.CodeRetries)
	assert.Equal(t, GoalBlocked, g2.Status)
	assert.Equal(t, "goal-001", g2.BlockedBy)
	assert.Equal(t, GoalPending, g3.Status)
	assert.Equal(t, "goal-003", loaded.CurrentGoal)

	// --- Tick 4: dispatch goal-003 (cross-BC independence) ---
	*gf = *loaded
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDispatchMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g3, _ = loaded.GoalByID("goal-003")
	assert.Equal(t, GoalRunning, g3.Status)

	// --- Tick 5: supervisor done for goal-003 → validating ---
	*gf = *loaded
	writeSupervisorSignal(t, dir, "goal-003", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@15", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@15").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@15", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@15"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)

	// --- Tick 6: validator pass → goal-003 done → deactivateOnCompletion ---
	writeValidatorSignal(t, dir, "goal-003", "pass", "")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@15", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@15").Return(nil)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g1, _ = loaded.GoalByID("goal-001")
	g2, _ = loaded.GoalByID("goal-002")
	g3, _ = loaded.GoalByID("goal-003")
	assert.Equal(t, GoalFailed, g1.Status)
	assert.Equal(t, 0, g1.CodeRetries)
	assert.Equal(t, GoalBlocked, g2.Status)
	assert.Equal(t, "goal-001", g2.BlockedBy)
	assert.Equal(t, GoalDone, g3.Status)
	assert.NotEmpty(t, g3.FinishedAt)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Done   | 1")
	assert.Contains(t, report, "Failed | 1")
	assert.Contains(t, report, "Blocked| 1")
	assert.Contains(t, report, "Total  | 3")

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_CompletionReport_AllCategories(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal:      "goal-002",
		GlobalMaxRetries: 3,
		Goals: []Goal{
			{ID: "goal-001", Description: "Completed task", Status: GoalDone, Retries: 1, MaxRetries: 3,
				StartedAt: "2026-05-20T10:00:00Z", FinishedAt: "2026-05-20T10:15:30Z"},
			{ID: "goal-002", Description: "Ceiling-halted task", Status: GoalPending, Retries: 3, MaxRetries: 5},
			{ID: "goal-003", Description: "Cascade-blocked task", Status: GoalPending, MaxRetries: 3,
				DependsOn: []string{"goal-002"}},
			{ID: "goal-004", Description: "Independent ceiling-halted", Status: GoalPending, MaxRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: goal-002 pending, ceiling reached (1+3=4 >= 3) → haltRetryCeiling ---
	// goal-002 → Failed, goal-003 → Blocked, advance to goal-004
	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g2, _ := loaded.GoalByID("goal-002")
	g3, _ := loaded.GoalByID("goal-003")
	assert.Equal(t, GoalFailed, g2.Status)
	assert.Equal(t, GoalBlocked, g3.Status)
	assert.Equal(t, "goal-002", g3.BlockedBy)
	assert.Equal(t, "goal-004", loaded.CurrentGoal)

	// --- Tick 2: goal-004 pending, ceiling still reached → haltRetryCeiling ---
	// advance → no pending → deactivateOnCompletion → report
	*gf = *loaded
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)

	assert.Contains(t, report, "# Taskvisor Completion Report")
	assert.Contains(t, report, "Generated:")

	assert.Contains(t, report, "Done   | 1")
	assert.Contains(t, report, "Failed | 2")
	assert.Contains(t, report, "Blocked| 1")
	assert.Contains(t, report, "Total  | 4")

	assert.Contains(t, report, "### goal-001: Completed task")
	assert.Contains(t, report, "### goal-002: Ceiling-halted task")
	assert.Contains(t, report, "### goal-003: Cascade-blocked task")
	assert.Contains(t, report, "### goal-004: Independent ceiling-halted")

	// Per-goal section assertions
	g1Idx := strings.Index(report, "### goal-001:")
	g2Idx := strings.Index(report, "### goal-002:")
	g3Idx := strings.Index(report, "### goal-003:")
	g4Idx := strings.Index(report, "### goal-004:")
	require.True(t, g1Idx >= 0 && g2Idx >= 0 && g3Idx >= 0 && g4Idx >= 0)

	g1Section := report[g1Idx:g2Idx]
	g2Section := report[g2Idx:g3Idx]
	g3Section := report[g3Idx:g4Idx]
	g4Section := report[g4Idx:]

	assert.Contains(t, g1Section, "- **Status:** done")
	assert.Contains(t, g1Section, "- **Retries:** 1/3")
	assert.Contains(t, g1Section, "- **Duration:** 15m30s")

	assert.Contains(t, g2Section, "- **Status:** failed")
	assert.Contains(t, g2Section, "- **Retries:** 3/5")
	assert.NotContains(t, g2Section, "- **Duration:**")

	assert.Contains(t, g3Section, "- **Status:** blocked")
	assert.Contains(t, g3Section, "- **Retries:** 0/3")

	assert.Contains(t, g4Section, "- **Status:** failed")
	assert.Contains(t, g4Section, "- **Retries:** 0/5")
	assert.NotContains(t, g4Section, "- **Duration:**")

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_DependencyOrdering_FullGraph(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "scaffold", Status: GoalDone, MaxRetries: 3},
			{ID: "goal-002", Description: "error-handling", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "auth", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002"}},
			{ID: "goal-004", Description: "action-A", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002", "goal-003"}},
			{ID: "goal-005", Description: "action-B", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002", "goal-003"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	passGoal := func(goalID string) {
		exec.ExpectedCalls = nil
		exec.Calls = nil
		setupDispatchMocks(exec, testSession, "@0")
		d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
		require.NoError(t, d.tick(ctx, gf))
		loaded, err := LoadGoals(dir)
		require.NoError(t, err)
		*gf = *loaded

		writeSupervisorSignal(t, dir, goalID, "done")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
		}, nil)
		exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
		exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
			return strings.HasPrefix(cmd, "/tmux:investigate ")
		})).Return(nil)
		d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
		require.NoError(t, d.tick(ctx, gf))

		writeValidatorSignal(t, dir, goalID, "pass", "")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator"},
		}, nil).Once()
		exec.On("KillWindow", testSession, "@5").Return(nil)
	}

	var dispatchOrder []string

	// Goal-002 (error-handling): scaffold done → error-handling dispatched
	passGoal("goal-002")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g2, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalDone, g2.Status)
	dispatchOrder = append(dispatchOrder, "goal-002")

	// Goal-003 (auth): error-handling done → auth dispatched
	passGoal("goal-003")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g3, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g3.Status)
	dispatchOrder = append(dispatchOrder, "goal-003")

	// Goal-004 (action-A): auth done → first eligible by list position
	passGoal("goal-004")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g4, _ := gf.GoalByID("goal-004")
	assert.Equal(t, GoalDone, g4.Status)
	dispatchOrder = append(dispatchOrder, "goal-004")

	// Goal-005 (action-B): last one → deactivate
	passGoal("goal-005")
	setupDeactivateOnCompletionMocks(exec, testSession)
	require.NoError(t, d.tick(ctx, gf))
	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g5, _ := loaded.GoalByID("goal-005")
	assert.Equal(t, GoalDone, g5.Status)
	dispatchOrder = append(dispatchOrder, "goal-005")

	assert.Equal(t, []string{"goal-002", "goal-003", "goal-004", "goal-005"}, dispatchOrder)
	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_DependencyOrdering_FailedDepBlocksDownstream(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "scaffold", Status: GoalDone, MaxRetries: 3},
			{ID: "goal-002", Description: "error-handling", Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "auth", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002"}},
			{ID: "goal-004", Description: "action", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002", "goal-003"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// Tick 1: dispatch goal-002
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[1].Status)

	// Tick 2: supervisor done → validating
	writeSupervisorSignal(t, dir, "goal-002", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))

	// Tick 3: validator fail → retries exhausted (max_retries=1) → cascade + deactivate
	writeValidatorSignal(t, dir, "goal-002", "fail", "error-handling broken")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g2, _ := loaded.GoalByID("goal-002")
	g3, _ := loaded.GoalByID("goal-003")
	g4, _ := loaded.GoalByID("goal-004")
	assert.Equal(t, GoalFailed, g2.Status)
	assert.Equal(t, 0, g2.CodeRetries)
	assert.Equal(t, GoalBlocked, g3.Status)
	assert.Equal(t, "goal-002", g3.BlockedBy)
	assert.Equal(t, GoalBlocked, g4.Status)
	assert.Equal(t, "goal-002", g4.BlockedBy)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	reportData, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	report := string(reportData)
	assert.Contains(t, report, "Done   | 1")
	assert.Contains(t, report, "Failed | 1")
	assert.Contains(t, report, "Blocked| 2")

	assert.Equal(t, modeIdle, d.mode)
}

func TestIntegration_DependencyOrdering_IndependentBranchesNotBlocked(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "scaffold", Status: GoalDone, MaxRetries: 3},
			{ID: "goal-002", Description: "BC-A-domain", Status: GoalPending, MaxRetries: 1, CodeRetries: 1, MaxCodeRetries: 1, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "BC-B-domain", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-004", Description: "BC-A-infra", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002"}},
			{ID: "goal-005", Description: "BC-B-infra", Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-003"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// Tick 1: dispatch goal-002 (BC-A-domain) — first eligible by list position
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[1].Status)

	// Tick 2: supervisor done → validating
	writeSupervisorSignal(t, dir, "goal-002", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))

	// Tick 3: validator fail → BC-A-domain retries exhausted → cascade blocks BC-A-infra only
	writeValidatorSignal(t, dir, "goal-002", "fail", "BC-A-domain failed")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded

	g2, _ := gf.GoalByID("goal-002")
	g3, _ := gf.GoalByID("goal-003")
	g4, _ := gf.GoalByID("goal-004")
	g5, _ := gf.GoalByID("goal-005")

	assert.Equal(t, GoalFailed, g2.Status, "BC-A-domain should fail")
	assert.Equal(t, GoalBlocked, g4.Status, "BC-A-infra should be blocked")
	assert.Equal(t, "goal-002", g4.BlockedBy)
	assert.Equal(t, GoalPending, g3.Status, "BC-B-domain should remain pending (independent)")
	assert.Equal(t, GoalPending, g5.Status, "BC-B-infra should remain pending (independent)")
	assert.Equal(t, "goal-003", gf.CurrentGoal, "daemon should advance to BC-B-domain")

	// Tick 4: dispatch goal-003 (BC-B-domain) — independent branch continues
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDispatchMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g3, _ = loaded.GoalByID("goal-003")
	assert.Equal(t, GoalRunning, g3.Status, "BC-B-domain should be dispatched")
}

func TestIntegration_DispatchTimeout_FullLifecycle(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)
	d.dispatchTimeout = 1 * time.Second

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout test", Acceptance: []string{"it works"},
				Status: GoalPending, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	ctx := context.Background()

	// Tick 1: dispatch
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.Equal(t, phaseSupervising, d.phase)

	// Simulate timeout: set dispatch time in the past
	d.currentGoalDispatchTime = time.Now().Add(-2 * time.Second)

	// Tick 2: no signal, timeout exceeded → handleFailedCycle → pending
	require.NoError(t, d.tick(ctx, gf))
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g1, _ := gf.GoalByID("goal-001")
	assert.Equal(t, GoalPending, g1.Status, "timed-out goal should be pending for retry")
	assert.Equal(t, 2, g1.CodeRetries, "timed-out goal keeps code budget: 3->2")

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	corrData, readErr := os.ReadFile(correctionPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(corrData), "timed out")

	// Tick 3: re-dispatch — old windows killed, fresh supervisor
	exec.ExpectedCalls = nil
	exec.Calls = nil

	// Kill phase: killWindowByName("supervisor") finds old supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "execute-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@0").Return(nil)
	// killWindowsByPrefix("execute-") finds execute-1
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@1").Return(nil)
	// killWindowByName("validator") — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// killWindowsByPrefix("inv-") — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// collectManagedNames — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitWindowsGone — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitClaudeBoot — booted
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@10", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@10").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@10", mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))

	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g1, _ = loaded.GoalByID("goal-001")
	assert.Equal(t, GoalRunning, g1.Status, "goal should be re-dispatched after timeout")

	exec.AssertCalled(t, "KillWindow", testSession, "@0")
	exec.AssertCalled(t, "KillWindow", testSession, "@1")
}

// --- 6.29: Goals sequential / tasks parallel ---

func TestIntegration_GoalsSequentialTasksParallel(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First goal", Acceptance: []string{"A"}, Status: GoalPending, MaxRetries: 3},
			{ID: "goal-002", Description: "Second goal", Acceptance: []string{"B"}, Status: GoalPending, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	// --- Tick 1: dispatch goal-001 (pending → running) ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.Equal(t, GoalPending, gf.Goals[1].Status, "goal-002 must stay pending during goal-001 dispatch")

	// --- Tick 2: goal-001 running, no signal → goal-002 must NOT start ---
	d.dispatchTimeout = time.Hour
	d.currentGoalDispatchTime = time.Now()
	require.NoError(t, d.tick(ctx, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, _ := loaded.GoalByID("goal-001")
	g2, _ := loaded.GoalByID("goal-002")
	assert.Equal(t, GoalRunning, g1.Status, "goal-001 should remain running")
	assert.Equal(t, GoalPending, g2.Status, "goal-002 must NOT dispatch while goal-001 is running")

	// --- Complete goal-001: supervisor done → validating ---
	writeSupervisorSignal(t, dir, "goal-001", "done")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, phaseValidating, d.phase)

	// --- Validator pass → goal-001 done, advance to goal-002 ---
	writeValidatorSignal(t, dir, "goal-001", "pass", "")
	exec.ExpectedCalls = nil
	exec.Calls = nil
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded
	g1, _ = gf.GoalByID("goal-001")
	g2, _ = gf.GoalByID("goal-002")
	assert.Equal(t, GoalDone, g1.Status, "goal-001 should be done")
	assert.Equal(t, GoalPending, g2.Status, "goal-002 pending, ready to dispatch")
	assert.Equal(t, "goal-002", gf.CurrentGoal)

	// --- Tick: goal-002 dispatches now that goal-001 is done ---
	exec.ExpectedCalls = nil
	exec.Calls = nil
	setupDispatchMocks(exec, testSession, "@10")
	d.SetWindowCreateFunc(mockCreateWindowFn("@10"))
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g2, _ = loaded.GoalByID("goal-002")
	assert.Equal(t, GoalRunning, g2.Status, "goal-002 should now be dispatched")
}

func TestIntegration_GoalBlockedUntilPriorComplete(t *testing.T) {
	d, exec, dir := setupIntegrationDaemon(t)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Foundation", Acceptance: []string{"A"}, Status: GoalPending, MaxRetries: 3},
			{ID: "goal-002", Description: "Depends on foundation", Acceptance: []string{"B"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Description: "Depends on goal-002", Acceptance: []string{"C"}, Status: GoalPending, MaxRetries: 3, DependsOn: []string{"goal-002"}},
		},
	}
	writeGoals(t, dir, gf)
	for _, g := range gf.Goals {
		_, err := EnsureGoalDir(dir, g.ID)
		require.NoError(t, err)
	}
	writeGuardFile(t, dir)

	ctx := context.Background()

	passGoal := func(goalID string) {
		exec.ExpectedCalls = nil
		exec.Calls = nil
		setupDispatchMocks(exec, testSession, "@0")
		d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
		require.NoError(t, d.tick(ctx, gf))
		loaded, err := LoadGoals(dir)
		require.NoError(t, err)
		*gf = *loaded

		writeSupervisorSignal(t, dir, goalID, "done")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator", CurrentCommand: "claude"},
		}, nil)
		exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
		exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
			return strings.HasPrefix(cmd, "/tmux:investigate ")
		})).Return(nil)
		d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
		require.NoError(t, d.tick(ctx, gf))

		writeValidatorSignal(t, dir, goalID, "pass", "")
		exec.ExpectedCalls = nil
		exec.Calls = nil
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@5", Name: "validator"},
		}, nil).Once()
		exec.On("KillWindow", testSession, "@5").Return(nil)
	}

	// --- Pass goal-001 → advance to goal-002 ---
	passGoal("goal-001")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded

	g1, _ := gf.GoalByID("goal-001")
	g2, _ := gf.GoalByID("goal-002")
	g3, _ := gf.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g1.Status)
	assert.Equal(t, GoalPending, g2.Status, "goal-002 pending (deps satisfied: goal-001 done)")
	assert.Equal(t, GoalPending, g3.Status, "goal-003 pending (deps NOT satisfied: goal-002 not done)")
	assert.Equal(t, "goal-002", gf.CurrentGoal)

	// --- Pass goal-002 → advance to goal-003 ---
	passGoal("goal-002")
	require.NoError(t, d.tick(ctx, gf))
	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	*gf = *loaded

	g2, _ = gf.GoalByID("goal-002")
	g3, _ = gf.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g2.Status)
	assert.Equal(t, GoalPending, g3.Status, "goal-003 pending (deps now satisfied: goal-002 done)")
	assert.Equal(t, "goal-003", gf.CurrentGoal)

	// --- Pass goal-003 → all done → deactivate ---
	passGoal("goal-003")
	setupDeactivateOnCompletionMocks(exec, testSession)
	require.NoError(t, d.tick(ctx, gf))

	loaded, err = LoadGoals(dir)
	require.NoError(t, err)
	g3, _ = loaded.GoalByID("goal-003")
	assert.Equal(t, GoalDone, g3.Status)
	assert.Equal(t, modeIdle, d.mode)
}

// TestM09_IncrementalRevalidation exercises the C10 reuse seam end to end on the
// Go side: seed a cycle-1 ledger (1 fail, 2 pass), change only the failed
// finding's in-scope file in cycle 2, and assert PlanRevalidation returns
// exactly one RERUN (the previously-failed finding) and two REUSE entries
// carrying reused_from_cycle=1. The orchestrator authors the [REUSED FROM CYCLE
// 1] marker into corrections/cycle-2.md, which flows through writeDispatchMd
// (a pure concatenator) into dispatch.md unchanged.
func TestM09_IncrementalRevalidation(t *testing.T) {
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	d := New(dir, exec)

	goalID := "goal-001"
	goalDir := filepath.Join(dir, ".tmux-cli", "goals", goalID)
	require.NoError(t, os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"),
		[]byte("# Test goal\n\n## Acceptance Criteria\n\n- works\n"), 0o644))

	findings := []ValidationFinding{
		{Rule: "alpha", Scope: []string{"alpha.go"}},
		{Rule: "beta", Scope: []string{"beta.go"}},
		{Rule: "gamma", Scope: []string{"gamma.go"}},
	}

	// Cycle-1 ledger: alpha failed, beta + gamma passed.
	prev := &Results{Results: map[string]ResultEntry{
		"alpha": {FindingID: "alpha", Status: VerdictFail, InputFingerprint: ComputeInputFingerprint(findings[0], nil), CycleNumber: 1},
		"beta":  {FindingID: "beta", Status: VerdictPass, InputFingerprint: ComputeInputFingerprint(findings[1], nil), CycleNumber: 1},
		"gamma": {FindingID: "gamma", Status: VerdictPass, InputFingerprint: ComputeInputFingerprint(findings[2], nil), CycleNumber: 1},
	}}
	require.NoError(t, SaveResults(dir, goalID, prev))

	// Cycle 2: only the failed finding's fix touches its in-scope file.
	loaded, err := LoadResults(dir, goalID)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	plans := PlanRevalidation(loaded, findings, []string{"alpha.go"}, false, false)

	var rerun, reuse []FindingPlan
	for _, p := range plans {
		if p.Action == ActionRerun {
			rerun = append(rerun, p)
		} else {
			reuse = append(reuse, p)
		}
	}
	require.Len(t, rerun, 1, "exactly 1 worker is spawned in cycle 2 (one inv-* per RERUN)")
	assert.Equal(t, "alpha", rerun[0].FindingID, "the previously-failed finding is re-run")
	require.Len(t, reuse, 2, "the two unchanged passes are reused")
	for _, p := range reuse {
		assert.Equal(t, 1, p.ReusedFromCycle, "%s reused from cycle 1", p.FindingID)
		assert.NotEqual(t, "alpha", p.FindingID, "the re-run finding is never in the reuse set")
	}

	// Orchestrator authors the reuse marker into cycle-2.md; writeDispatchMd
	// concatenates it verbatim.
	cycle2 := "# Investigation Corrections — Cycle 2\n\n" +
		"## Reused Findings\n" +
		"- beta [REUSED FROM CYCLE 1] fingerprint=" + reuse[0].Fingerprint + "\n" +
		"- gamma [REUSED FROM CYCLE 1]\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "corrections", "cycle-2.md"), []byte(cycle2), 0o644))

	require.NoError(t, d.writeDispatchMd(&Goal{ID: goalID, Description: "Test goal"}))

	dispatch, err := os.ReadFile(filepath.Join(goalDir, "dispatch.md"))
	require.NoError(t, err)
	assert.Contains(t, string(dispatch), "[REUSED FROM CYCLE 1]", "dispatch.md carries the reuse marker authored into cycle-2.md")
}

// TestM08_CyclePathNoReuse verifies per-cycle research-dir isolation: cycle 1
// resolves under goals/<id>/research/cycle-1/, and after a code-defect consumes
// one budget unit the next cycle resolves under cycle-2/ — the cycle-1 dir is
// never the resolved verdict source for cycle 2, and its report is untouched.
func TestM08_CyclePathNoReuse(t *testing.T) {
	dir := t.TempDir()
	goalID := "goal-001"

	// Fresh full-budget goal => CurrentCycle == 1.
	g := &Goal{
		ID:                   goalID,
		MaxCodeRetries:       3,
		CodeRetries:          3,
		MaxSpecRetries:       1,
		SpecRetries:          1,
		MaxValidationRetries: 1,
		ValidationRetries:    1,
	}
	require.Equal(t, 1, CurrentCycle(g))

	// Cycle 1: pre-create the research dir and drop a dummy report.
	cycle1Dir, err := EnsureCycleResearchDir(dir, g)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(cycle1Dir, filepath.Join("goals", goalID, "research", "cycle-1")),
		"cycle 1 resolves under cycle-1/, got %s", cycle1Dir)
	cycle1Report := filepath.Join(cycle1Dir, "inv-1-x.md")
	require.NoError(t, os.WriteFile(cycle1Report, []byte("## VERDICT\nfail\n"), 0o644))

	// Simulate a failed cycle on the code-defect route: one code budget unit is
	// consumed (CodeRetries decrements toward zero).
	g.CodeRetries--
	require.Equal(t, 2, CurrentCycle(g))

	// Cycle 2: must resolve a DISTINCT dir.
	cycle2Dir, err := EnsureCycleResearchDir(dir, g)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(cycle2Dir, filepath.Join("goals", goalID, "research", "cycle-2")),
		"cycle 2 resolves under cycle-2/, got %s", cycle2Dir)
	require.NotEqual(t, cycle1Dir, cycle2Dir, "cycle 2 must not reuse the cycle-1 dir")
	cycle2Report := filepath.Join(cycle2Dir, "inv-1-x.md")
	require.NoError(t, os.WriteFile(cycle2Report, []byte("## VERDICT\npass\n"), 0o644))

	// Path isolation: the resolved cycle-2 verdict source is NOT the cycle-1 dir,
	// and the cycle-1 report survives untouched (never read/overwritten as cycle 2).
	assert.NotEqual(t, cycle1Dir, CycleResearchDir(dir, g))
	c1, err := os.ReadFile(cycle1Report)
	require.NoError(t, err)
	assert.Equal(t, "## VERDICT\nfail\n", string(c1), "cycle-1 report must remain untouched")
}

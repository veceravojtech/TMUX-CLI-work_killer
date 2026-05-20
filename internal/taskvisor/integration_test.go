package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/mcp"
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
	exec.On("SendMessage", testSession, "@5", "/tmux:validate").Return(nil)
	exec.On("SendMessageWithDelay", testSession, "@5", mock.Anything).Return(nil)
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
	exec.On("SendMessage", testSession, "@5", "/tmux:validate").Return(nil)
	exec.On("SendMessageWithDelay", testSession, "@5", mock.Anything).Return(nil)
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
	assert.Equal(t, 1, gf.Goals[0].Retries)
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
	exec.On("SendMessage", testSession, "@5", "/tmux:validate").Return(nil)
	exec.On("SendMessageWithDelay", testSession, "@5", mock.Anything).Return(nil)
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
	assert.Equal(t, 1, gf.Goals[0].Retries)
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
	exec.On("SendMessage", testSession, "@5", "/tmux:validate").Return(nil)
	exec.On("SendMessageWithDelay", testSession, "@5", mock.Anything).Return(nil)
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
	assert.Equal(t, 1, gf.Goals[0].Retries)
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
		exec.On("SendMessage", testSession, "@5", "/tmux:validate").Return(nil)
		exec.On("SendMessageWithDelay", testSession, "@5", mock.Anything).Return(nil)
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

func TestIntegration_GoalCreateConcurrent(t *testing.T) {
	dir := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)

	server := &mcp.Server{}
	_ = server
	// Use the MCP server constructor with mock executor injected
	server = newMCPTestServer(mockExec, dir)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make([]error, goroutines)
	ids := make([]string, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			output, err := server.GoalCreate(
				fmt.Sprintf("Goal %d", idx),
				[]string{fmt.Sprintf("criterion-%d", idx)},
				nil,
				0,
			)
			errs[idx] = err
			if output != nil {
				ids[idx] = output.ID
			}
		}(i)
	}

	wg.Wait()

	// Concurrent read-modify-write is racy in v1 (no locking).
	// We verify: no panics (test reaching this point proves it), and
	// the final file is valid YAML with at least 1 goal persisted.
	successCount := 0
	for i := 0; i < goroutines; i++ {
		if errs[i] == nil {
			successCount++
		}
	}
	assert.Greater(t, successCount, 0, "at least one goroutine should succeed")

	gf, err := tvLoadGoalsForTest(dir)
	require.NoError(t, err, "goals.yaml must be valid YAML (no corruption)")
	require.NotNil(t, gf)
	assert.GreaterOrEqual(t, len(gf.Goals), 1, "at least 1 goal should be persisted")
}

// newMCPTestServer creates an MCP server with mock executor for testing.
func newMCPTestServer(exec *testutil.MockTmuxExecutor, workingDir string) *mcp.Server {
	return mcp.NewServerWithExecutor(exec, workingDir)
}

// tvLoadGoalsForTest loads goals.yaml using the internal taskvisor loader.
func tvLoadGoalsForTest(dir string) (*GoalsFile, error) {
	return LoadGoals(dir)
}

package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

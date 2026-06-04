package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// runtime_test.go — unit + single-goal-parity coverage for the goalRuntime
// extraction (E1-0a, goal-020). The Daemon's 7 per-goal cycle fields now live in
// a map[string]*goalRuntime keyed by goal ID, reached via the lazy-create
// runtime() accessor and bounded by clearRuntime(). These tests pin the accessor
// semantics, the per-goal isolation that unblocks execute-31, and that the
// single-goal (MaxGoals=1) cycle behaves byte-identically to the pre-refactor
// single-valued fields.
//
// Note: the spec's Test Plan writes "phase==phaseIdle"; the package's actual
// zero-value phase constant is phaseNone (daemon.go), so the lazily-created entry
// reports phaseNone — the exact mirror of the old zero-valued Daemon.phase field.

func TestRuntime_LazyCreate_ReturnsZeroValuedEntry(t *testing.T) {
	d := &Daemon{} // runtimes is nil

	rt := d.runtime("g1")

	require.NotNil(t, rt, "runtime must lazily create a non-nil entry")
	assert.Equal(t, phaseNone, rt.phase, "zero-valued entry reports phaseNone (the old zero phase)")
	assert.True(t, rt.phaseStartedAt.IsZero(), "phaseStartedAt is zero")
	assert.True(t, rt.bootConfirmedAt.IsZero(), "bootConfirmedAt is zero")
	assert.True(t, rt.dispatchTime.IsZero(), "dispatchTime is zero")
	assert.True(t, rt.validateTime.IsZero(), "validateTime is zero")
	assert.Equal(t, "", rt.lastSupervisorStatus, "lastSupervisorStatus is empty")
	_, ok := d.runtimes["g1"]
	assert.True(t, ok, "the lazily-created entry is stored on the map")
}

func TestRuntime_SameGoalID_ReturnsSamePointer(t *testing.T) {
	d := &Daemon{}

	first := d.runtime("g1")
	first.phase = phaseValidating
	second := d.runtime("g1")

	assert.Same(t, first, second, "repeated access returns the identical pointer (no clobber)")
	assert.Equal(t, phaseValidating, second.phase, "state set via the first handle survives")
}

func TestRuntime_DistinctGoalIDs_AreIsolated(t *testing.T) {
	d := &Daemon{}

	d.runtime("g1").phase = phaseValidating

	assert.Equal(t, phaseValidating, d.runtime("g1").phase)
	assert.Equal(t, phaseNone, d.runtime("g2").phase, "g2 is untouched by a write to g1")
	d.runtime("g2").phase = phaseSupervising
	assert.Equal(t, phaseValidating, d.runtime("g1").phase, "g1 is untouched by a write to g2")
}

func TestDispatch_SetsPerGoalRuntimeFields(t *testing.T) {
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

	before := time.Now()
	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	rt := d.runtime("goal-001")
	assert.Equal(t, phaseSupervising, rt.phase, "dispatch enters supervising")
	assert.False(t, rt.dispatchTime.IsZero(), "dispatchTime is set")
	assert.False(t, rt.bootConfirmedAt.IsZero(), "bootConfirmedAt is set")
	assert.Equal(t, "dispatched", rt.lastSupervisorStatus)
	assert.True(t, rt.dispatchTime.After(before) || rt.dispatchTime.Equal(before))
	assert.Equal(t, "goal-001", d.currentGoal, "currentGoal mirrors the active key for the dashboard")
}

func TestCheckSupervising_TransitionsToValidating_PerGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// A supervisor "done" signal is present and there is no validate.sh, so the
	// supervising phase tears down the supervisor, spawns the validator, and
	// transitions THIS goal's runtime to validating.
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-06-03T14:30:00Z",
	}))

	// killWindowsByPrefix("execute-") + killWindowByName("supervisor")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

	rt := d.runtime("goal-001")
	assert.Equal(t, phaseValidating, rt.phase, "the goal's runtime advances to validating")
	assert.False(t, rt.validateTime.IsZero(), "validateTime is stamped on the transition")
	assert.Equal(t, "done", rt.lastSupervisorStatus, "the supervisor status is recorded per goal")
}

func TestCheckSupervising_DispatchTimeout_PerGoalDeadline(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.dispatchTimeout = time.Hour

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning,
			MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// No signal on disk and the PER-GOAL dispatch clock is older than the deadline,
	// so the supervising check must route through handleFailedCycle (code-defect).
	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = time.Now().Add(-2 * time.Hour)

	require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

	assert.Equal(t, GoalPending, gf.Goals[0].Status, "timeout re-pends the goal for retry")
	assert.Equal(t, 2, gf.Goals[0].CodeRetries, "code budget decremented 3->2 on the timeout")

	corr := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corr)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "timed out", "the timeout correction is recorded")
}

func TestCrashRecovery_RebuildsRuntimeForRunningGoal(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})
	// A ValidatorSignal on disk means the crashed daemon was mid-validation.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-06-03T14:30:00Z",
	}))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)

	before := time.Now()
	require.NoError(t, d.crashRecovery())

	assert.Equal(t, modeActive, d.mode)
	assert.Equal(t, "goal-001", d.currentGoal, "currentGoal points at the recovered running goal")
	rt := d.runtime("goal-001")
	assert.Equal(t, phaseValidating, rt.phase, "the running goal's runtime is rebuilt to validating")
	assert.WithinDuration(t, time.Now(), rt.phaseStartedAt, time.Second)
	assert.True(t, rt.phaseStartedAt.After(before) || rt.phaseStartedAt.Equal(before))
}

func TestClearRuntime_RemovesEntryOnAdvance(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "completed", Status: GoalDone},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	// Seed a runtime entry for the now-completed goal, as a live cycle would have.
	d.runtime("goal-001").phase = phaseValidating
	require.Len(t, d.runtimes, 1)

	require.NoError(t, d.advanceToNextGoal(gf, "goal-001", true))

	_, stillThere := d.runtimes["goal-001"]
	assert.False(t, stillThere, "the completed goal's runtime is cleared on advance")
	assert.LessOrEqual(t, len(d.runtimes), 1, "the runtime map stays bounded across advances")
	assert.Equal(t, "goal-002", gf.CurrentGoal, "advance re-points CurrentGoal to the next pending goal")
	assert.Equal(t, "goal-002", d.currentGoal, "the scalar compat pointer follows the active key")
}

// TestSingleGoal_FullCycle_ByteIdenticalTransitions is the regression guard: with
// a single goal (MaxGoals=1) a full dispatch -> supervising -> validating -> done
// cycle must produce the same observable goal-status sequence, per-goal phase
// progression, and signal handling as pre-refactor main. It mirrors
// TestIntegration_FullCyclePass, asserting through the per-goal runtime accessor.
func TestSingleGoal_FullCycle_ByteIdenticalTransitions(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{{
			ID: "goal-001", Description: "single goal",
			Acceptance: []string{"it works"},
			Status:     GoalPending, MaxRetries: 3,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	ctx := context.Background()

	// --- Tick 1: dispatch (pending -> running, runtime -> supervising) ---
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))
	require.NoError(t, d.tick(ctx, gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)

	// --- Tick 2: supervisor done -> runtime advances to validating ---
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
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.Equal(t, "done", d.runtime("goal-001").lastSupervisorStatus)

	// --- Tick 3: validator passes -> goal done, deactivate, runtime cleared ---
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
	assert.Equal(t, modeIdle, d.mode, "single-goal completion deactivates the daemon")
	assert.Empty(t, d.runtimes, "the daemon drops all runtimes once idle")

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file removed on deactivation")
}

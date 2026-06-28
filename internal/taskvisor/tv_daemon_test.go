package taskvisor

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
	// The goal is already in-flight (GoalRunning, phaseNone ⇒ checkProgress is a
	// no-op), so activation is exercised WITHOUT a live dispatch. A bare PENDING
	// goal here would drive an unmocked dispatch that fails IDENTICALLY every tick,
	// which the new poll-error circuit breaker (task 313) now correctly fails fast +
	// deactivates after K errors — that is the wedge path, not the activation path
	// this test pins. Mirrors the wallclock TestRun_ accepted idiom.
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	})
	writeStartSignal(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@9", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessageWithDelay", testSession, "@9", mock.Anything).Return(nil).Maybe()
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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

	// Kill lookups find the goal's namespaced supervisor-001 (5 lookups in killGoalWindows:
	// sup + exec-prefix + val + inv-prefix + plan-audit)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@s", Name: "supervisor-001"},
	}, nil).Times(5)
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
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	// 8 = killGoalWindows(5: sup + exec-prefix + val + inv-prefix + plan-audit)
	//   + collectManagedNames(1) + waitWindowsGone(1) + ensureWindow0Supervisor(1)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(8)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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
		// 7 = killGoalWindows(5) + collectManagedNames(1) + waitWindowsGone(1)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		}, nil)
		exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
		exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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

		// 8 = killGoalWindows(5) + collectManagedNames(1) + waitWindowsGone(1) + ensureWindow0Supervisor(1)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(8)
		exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
			{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		}, nil)
		exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
		exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)
}

func TestDeactivate_RemovesAllRuntimeMarkers(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeAllRuntimeMarkers(t, dir)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)

	assertAllRuntimeMarkersAbsent(t, dir)
}

func TestDeactivate_ToleratesMissingMarkers(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err := d.deactivate()
	require.NoError(t, err)

	assertAllRuntimeMarkersAbsent(t, dir)
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

// --- Poll-error circuit breaker (task 313) -------------------------------------

// TestNotePollErr_TripsAtK pins the PURE counter: at k=2 (the circuit_breaker_k
// default) two IDENTICAL poll errors trip fail-fast; the first does not. No mocks,
// no tmux server — this is the test that keeps the breaker logic deterministic.
func TestNotePollErr_TripsAtK(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettings(t, dir, true, true) // circuit_breaker_k absent -> default 2

	err := errors.New("create supervisor: boom")
	assert.False(t, d.notePollErr(err), "first identical error must not trip")
	assert.True(t, d.notePollErr(err), "second identical error trips at k=2")
	assert.Equal(t, 2, d.consecutivePollErrs)
}

// TestNotePollErr_ResetsOnDifferentMessage proves a changing error message never
// accumulates the streak — only an IDENTICAL message recurring counts.
func TestNotePollErr_ResetsOnDifferentMessage(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettings(t, dir, true, true)

	assert.False(t, d.notePollErr(errors.New("error A")))
	assert.Equal(t, 1, d.consecutivePollErrs)
	assert.False(t, d.notePollErr(errors.New("error B")), "a different message restarts the streak at 1")
	assert.Equal(t, 1, d.consecutivePollErrs)
	assert.Equal(t, "error B", d.lastPollErrMsg)
}

// TestNotePollErr_ResetsOnSuccess proves resetPollErrStreak (called on a clean
// poll) restarts the streak: the same error recurring once more does not trip.
func TestNotePollErr_ResetsOnSuccess(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettings(t, dir, true, true)

	err := errors.New("create supervisor: boom")
	assert.False(t, d.notePollErr(err))
	d.resetPollErrStreak()
	assert.Equal(t, 0, d.consecutivePollErrs)
	assert.Equal(t, "", d.lastPollErrMsg)
	assert.False(t, d.notePollErr(err), "after a clean poll the streak restarts at 1, not 2")
	assert.Equal(t, 1, d.consecutivePollErrs)
}

// TestRun_FailFastOnRepeatedPollError drives the REAL poll loop into a
// deterministic, identical bring-up error every tick (window creation fails the
// same way, the goal stays GoalPending, so the next tick re-dispatches and
// re-fails identically). After K errors the daemon must mark the goal GoalFailed,
// emit the ALL-COMPLETE failure milestone, and drop to modeIdle — never loop
// forever. Run returns when the bounding ctx expires.
func TestRun_FailFastOnRepeatedPollError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeSettings(t, dir, true, true) // circuit_breaker_k default 2
	writeGoals(t, dir, &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	})
	writeStartSignal(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	// Window-0 "supervisor" is live so notifyCompletion finds it to emit the
	// ALL-COMPLETE milestone; no goal-namespaced windows exist, so no kills fire.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	var sent []string
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Run(func(args mock.Arguments) {
		sent = append(sent, args.Get(2).(string))
	}).Return(nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Deterministic, identical bring-up error on every dispatch tick.
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		return nil, errors.New("boom: cannot create window")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, d.Run(ctx))

	assert.Equal(t, modeIdle, d.mode, "fail-fast must deactivate to idle, not loop forever")

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "the wedged goal must be marked failed via the existing path")

	var allComplete string
	for _, m := range sent {
		if strings.Contains(m, "ALL-COMPLETE") {
			allComplete = m
		}
	}
	assert.Contains(t, allComplete, "failed=1", "deactivateOnCompletion must emit the ALL-COMPLETE failure milestone")
}

// TestRun_HonorsStopSignalDuringPollError pins the error-path stop check: when a
// taskvisor-stop signal is present while the daemon is wedged in a poll-error
// loop, handlePollError consumes it and deactivates cleanly — stop takes
// PRECEDENCE over fail-fast (the in-flight goal is NOT marked failed) even when
// the streak is already one short of tripping K. This closes the window where the
// pre-tick stop check is starved by an in-flight tick() bring-up error.
func TestRun_HonorsStopSignalDuringPollError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeSettings(t, dir, true, true) // k = 2
	d.mode = modeActive
	d.session = testSession
	d.currentGoal = "goal-001"
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
		CurrentGoal: "goal-001",
	})

	// Stop signal written mid-wedge.
	stopPath := filepath.Join(dir, ".tmux-cli", "taskvisor-stop")
	require.NoError(t, os.WriteFile(stopPath, nil, 0o644))

	setupDeactivateMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	// One short of tripping K=2: without the stop check the very next notePollErr
	// would fail-fast the goal. The stop check must win.
	d.consecutivePollErrs = 1
	d.lastPollErrMsg = "boom: cannot create window"

	deactivated := d.handlePollError(context.Background(), errors.New("boom: cannot create window"))

	assert.True(t, deactivated, "a pending stop must deactivate the wedged daemon")
	assert.Equal(t, modeIdle, d.mode, "stop deactivates to idle")
	assert.Equal(t, 0, d.consecutivePollErrs, "deactivation resets the poll-error streak")

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status, "stop takes precedence over fail-fast — the goal must NOT be failed")

	_, statErr := os.Stat(stopPath)
	assert.True(t, os.IsNotExist(statErr), "the stop signal must be consumed on the error path")
}

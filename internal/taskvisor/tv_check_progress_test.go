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

func TestCheckSupervisingPhase_Done_LogsGoalDone(t *testing.T) {
	// After always-validate: validate.sh pass no longer short-circuits to GoalDone.
	// It must transition to phaseValidating and set rt.scriptPassed=true.
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z", Acceptance: []string{"it works"}, MaxRetries: 3},
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
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "", 0, nil
	})

	goal := &gf.Goals[0]
	output := captureLog(t, func() {
		err = d.checkSupervisingPhase(goal, gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: phase supervising -> validating")
	assert.True(t, d.runtime("goal-001").scriptPassed, "scriptPassed must be true when validate.sh exits 0")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
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

func TestCheckSupervisingPhase_ValidatePass_SetsScriptPassed(t *testing.T) {
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
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "", 0, nil
	})

	goal := &gf.Goals[0]
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	assert.True(t, rt.scriptPassed, "scriptPassed must be true when validate.sh exits 0")
	assert.Equal(t, phaseValidating, rt.phase, "must transition to validating, not done")
}

func TestCheckSupervisingPhase_ValidateFail_ScriptPassedFalse(t *testing.T) {
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
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.md"), []byte("- check tests\n"), 0o644))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "tests failed", 1, nil
	})

	goal := &gf.Goals[0]
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	assert.False(t, rt.scriptPassed, "scriptPassed must be false when validate.sh exits non-zero")
	assert.Equal(t, phaseValidating, rt.phase, "must transition to validating")
}

func TestCheckValidatingPhase_P7Gate_ScriptPassedTrue_Passes(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").scriptPassed = true

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z",
				Validate: []string{"go test ./..."}, MaxRetries: 3},
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
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalDone, goal.Status, "P7 gate must allow pass when ScriptPassed=true")
}

func TestCheckValidatingPhase_P7Gate_ScriptPassedFalse_Downgrades(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").scriptPassed = false
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-05-20T10:00:00Z",
				Validate: []string{"go test ./..."}, MaxRetries: 3, ValidationRetries: 2, MaxValidationRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T14:35:00Z",
	}))

	// killWindowByName("validator-001")
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)
	// rerunValidationOnly: killWindowByName("validator-001") + createValidator
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@6")
	d.SetWindowCreateFunc(mockCreateWindowFn("@6"))

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.NotEqual(t, GoalDone, goal.Status, "P7 gate must downgrade pass when ScriptPassed=false")
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, reloaded.Goals[0].ValidationRetries, "ValidationRetries must be decremented (error/ops route)")
}

func TestCheckSupervisingPhase_NoValidateScript_ScriptPassedFalse(t *testing.T) {
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

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)

	rt := d.runtime("goal-001")
	assert.False(t, rt.scriptPassed, "scriptPassed must be false when no validate.sh exists")
	assert.Equal(t, phaseValidating, rt.phase, "must transition to validating")
}

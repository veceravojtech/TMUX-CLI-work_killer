package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunValidateScript_NoScript(t *testing.T) {
	d, _, dir := setupDaemon(t)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &Goal{ID: "goal-001"}
	passed, reason, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "missing", reason)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, reason, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed)
	assert.Empty(t, reason, "pass carries no reason")
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitNonZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'test failed' >&2\nexit 1\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, reason, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "exit-1", reason, "red exit is classified by its code, not as an op error")
	assert.Contains(t, stderr, "test failed")
}

func TestRunValidateScript_RunnerMissing_127(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	// Exit 127 = the script invoked a runner that is not on PATH ("command not
	// found"). This is an infra/exec error, NOT a red suite — it must map to the
	// runner-missing reason so the goal's code budget is never charged (goal-009).
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "fake-runner: command not found", 127, nil
	})

	goal := &Goal{ID: "goal-001"}
	passed, reason, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "runner-missing", reason, "exit 127 is a missing runner, not a red suite")
	assert.Contains(t, stderr, "command not found")
}

func TestRunValidateScript_RunnerMissing_126(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	// Exit 126 = the runner exists but is not executable — same infra/exec class.
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "fake-runner: Permission denied", 126, nil
	})

	goal := &Goal{ID: "goal-001"}
	passed, reason, _, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "runner-missing", reason, "exit 126 (not executable) maps to the same infra/exec class as 127")
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
	passed, reason, stderr, err := d.runValidateScript(goal)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "timeout", reason, "a deadline kill is a timeout, never mistaken for a red suite")
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
	passed, reason, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Equal(t, "not-executable", reason)
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
	passed, _, _, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed)
	assert.Contains(t, capturedStdout, "GOAL_ID=goal-001")
	assert.Contains(t, capturedStdout, fmt.Sprintf("CWD=%s", dir))
}

func TestCheckProgress_SupervisorDone_ValidateShPass(t *testing.T) {
	// After always-validate: validate.sh pass transitions to phaseValidating
	// (not GoalDone). The investigator still runs.
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
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "", 0, nil
	})

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	assert.True(t, d.runtime("goal-001").scriptPassed)
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

func TestCheckProgress_SupervisorDone_ValidateShExit127_NoCodeRetryDecrement(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Acceptance: []string{"it works"}, MaxRetries: 3, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	// The 127 halt returns BEFORE validator dispatch, so the only windows traffic
	// is the pre-runValidateScript kill lookups plus the advanceToNextGoal →
	// deactivateOnCompletion tail (notifyCompletion + teardown). setupDeactivateMocks
	// programs an unbounded ListWindows return that absorbs all of them; no new
	// windows are created on the halt path.
	setupDeactivateMocks(exec, testSession, "@9")

	// validate.sh invokes a runner that is not on PATH → exit 127. This is infra,
	// not a red suite: it must NOT burn the per-goal code budget (the goal-009 bug).
	d.SetScriptRunnerFunc(func(ctx context.Context, sp, wd string, env []string) (string, string, int, error) {
		return "", "tmux-cli-fake: command not found", 127, nil
	})

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, GoalFailed, reloaded.Goals[0].Status, "exit 127 terminally fails the goal on cycle 1")
	assert.Equal(t, 3, reloaded.Goals[0].CodeRetries, "code budget UNCHANGED — a missing runner is not a code defect")
	assert.Equal(t, "runner-missing", reloaded.Goals[0].FailedBy, "failure reason is the infra/exec class")
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

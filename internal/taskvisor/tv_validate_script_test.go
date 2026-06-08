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
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed)
	assert.Empty(t, stderr)
}

func TestRunValidateScript_ExitNonZero(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'test failed' >&2\nexit 1\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
	assert.Contains(t, stderr, "test failed")
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
	passed, stderr, err := d.runValidateScript(goal)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, passed)
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
	passed, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.False(t, passed)
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
	passed, _, err := d.runValidateScript(goal)

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

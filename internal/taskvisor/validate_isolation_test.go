package taskvisor

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E1-1c: the validate step's cwd must be routed to the goal's worktree so a
// verdict observes ONLY that goal's edits. goalWorkDir is the single chokepoint;
// runValidateScript and createValidatorAndSendPayload both derive their cwd from
// it. Under MaxGoals=1 (empty WorktreeDir) cwd resolves to base — byte-identical.

func TestGoalWorkDir_EmptyWorktree_ReturnsBase(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").WorktreeDir = ""

	assert.Equal(t, dir, d.goalWorkDir("goal-001"),
		"empty WorktreeDir must resolve to the base workDir")
}

func TestGoalWorkDir_SetWorktree_ReturnsWorktreeDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	wt := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	d.runtime("goal-001").WorktreeDir = wt

	assert.Equal(t, wt, d.goalWorkDir("goal-001"),
		"an existing WorktreeDir must be returned verbatim")
}

func TestGoalWorkDir_StaleWorktree_FallsBackToBaseWithWarning(t *testing.T) {
	d, _, dir := setupDaemon(t)
	stale := filepath.Join(dir, ".tmux-cli", "worktrees", "gone")
	d.runtime("goal-001").WorktreeDir = stale // never created → os.Stat fails

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	got := d.goalWorkDir("goal-001")

	assert.Equal(t, dir, got, "a stale WorktreeDir must degrade to base, never crash")
	assert.Contains(t, logBuf.String(), "stale worktree",
		"a stale WorktreeDir must log a warning")
}

func TestRunValidateScript_NoWorktree_RunsInBaseDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	var gotDir string
	d.SetScriptRunnerFunc(func(_ context.Context, _ string, wd string, _ []string) (string, string, int, error) {
		gotDir = wd
		return "", "", 0, nil
	})

	passed, _, _, err := d.runValidateScript(&Goal{ID: "goal-001"})
	require.NoError(t, err)
	assert.True(t, passed)
	assert.Equal(t, dir, gotDir, "no worktree ⇒ runner dir must be the base workDir")
}

func TestRunValidateScript_WithWorktree_RunsInWorktreeDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	wt := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	d.runtime("goal-001").WorktreeDir = wt

	var gotDir, gotScriptPath string
	d.SetScriptRunnerFunc(func(_ context.Context, scriptPath string, wd string, _ []string) (string, string, int, error) {
		gotDir = wd
		gotScriptPath = scriptPath
		return "", "", 0, nil
	})

	_, _, _, err = d.runValidateScript(&Goal{ID: "goal-001"})
	require.NoError(t, err)
	assert.Equal(t, wt, gotDir, "worktree set ⇒ runner dir must be the worktree")
	// scriptPath stays rooted at the base control plane regardless of cwd.
	assert.Equal(t, filepath.Join(goalDir, "validate.sh"), gotScriptPath)
	assert.True(t, strings.HasPrefix(gotScriptPath, filepath.Join(dir, ".tmux-cli", "goals")),
		"validate.sh path must remain under base .tmux-cli/goals")
}

func TestRunValidateScript_ExportsWorktreeDirEnv(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	wt := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	d.runtime("goal-001").WorktreeDir = wt

	var gotEnv []string
	d.SetScriptRunnerFunc(func(_ context.Context, _ string, _ string, env []string) (string, string, int, error) {
		gotEnv = env
		return "", "", 0, nil
	})

	_, _, _, err = d.runValidateScript(&Goal{ID: "goal-001"})
	require.NoError(t, err)
	assert.Contains(t, gotEnv, "GOAL_ID=goal-001", "GOAL_ID must be preserved")
	assert.Contains(t, gotEnv, "WORKTREE_DIR="+wt, "WORKTREE_DIR must be exported to validate.sh")
}

func TestCreateValidator_NoWorktree_UsesBaseCwd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.validatorSendDelay = 0
	d.runtime("goal-001").WorktreeDir = ""

	setupValidatorMocks(exec, testSession, "@5")

	var gotCwd string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		gotCwd = cwd
		return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
	})

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-001"}))
	assert.Equal(t, dir, gotCwd, "no worktree ⇒ validator window cwd must be base workDir")
}

func TestCreateValidator_WithWorktree_UsesWorktreeCwd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.validatorSendDelay = 0

	wt := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	d.runtime("goal-001").WorktreeDir = wt

	setupValidatorMocks(exec, testSession, "@5")

	var gotCwd string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		gotCwd = cwd
		return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
	})

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-001"}))
	assert.Equal(t, wt, gotCwd, "worktree set ⇒ validator window cwd must be the worktree")
}

package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validator_cwd_test.go — validator-window cwd routing through the goalWorkDir
// chokepoint (goal-004 C1a root cause): when the in-memory runtime's WorktreeDir
// is empty (daemon restart / crash recovery / lanes that never re-ran
// ensureWorktree), goalWorkDir must re-resolve the goal's worktree from disk
// (stat + isRegisteredWorktree) instead of silently degrading to the base repo,
// and cache the adopted path back into the runtime. createValidatorAndSendPayload
// (retry + inline lanes) and the .tmux-cli/taskvisor-current-worktree marker are
// the consumers under test — no code change in dispatch.go.

// cwdCapturingCreateWindowFn is a cwd-recording variant of mockCreateWindowFn:
// it stores the cwd each createWindow call receives so tests can assert which
// tree the validator window boots in.
func cwdCapturingCreateWindowFn(tmuxWindowID string, gotCwd *string) WindowCreateFunc {
	return func(name, command, cwd string) (*CreatedWindow, error) {
		*gotCwd = cwd
		return &CreatedWindow{TmuxWindowID: tmuxWindowID, Name: name}, nil
	}
}

// registeredWorktreeGit returns a fakeGitRunner whose `worktree list --porcelain`
// registers exactly wtPath, so isRegisteredWorktree accepts it.
func registeredWorktreeGit(base, wtPath string) *fakeGitRunner {
	return &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "worktree", "list", "--porcelain") {
			return "worktree " + base + "\n\nworktree " + wtPath + "\n", 0
		}
		return "", 0
	}}
}

// mkWorktreeDir materializes an on-disk worktree dir for goalID (as a crashed
// run would have left it) and returns its path.
func mkWorktreeDir(t *testing.T, d *Daemon, goalID string) string {
	t.Helper()
	wtPath := d.worktreePath(goalID)
	require.NoError(t, os.MkdirAll(wtPath, 0o755))
	return wtPath
}

// --- goalWorkDir disk re-resolution -----------------------------------------

// TC-1: empty runtime + registered on-disk worktree ⇒ adopt the worktree path
// and cache it back into rt.WorktreeDir.
func TestGoalWorkDir_EmptyRuntime_ResolvesWorktreeFromDisk(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	wtPath := mkWorktreeDir(t, d, "goal-007")
	d.SetGitRunnerFunc(registeredWorktreeGit(dir, wtPath).run)

	got := d.goalWorkDir("goal-007")

	assert.Equal(t, wtPath, got, "empty runtime must re-resolve the registered worktree from disk")
	assert.Equal(t, wtPath, d.runtime("goal-007").WorktreeDir, "adopted path must be cached back into the runtime")
}

// TC-2: empty runtime + no worktree dir on disk (the common MaxGoals=1 path) ⇒
// base workDir, zero git probes, no warn-log spam.
func TestGoalWorkDir_EmptyRuntime_NoWorktreeOnDisk_FallsBackToBase(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	var got string
	logged := captureLog(t, func() { got = d.goalWorkDir("goal-007") })

	assert.Equal(t, dir, got, "no worktree on disk must fall back to base")
	assert.Empty(t, d.runtime("goal-007").WorktreeDir, "nothing to cache on the no-dir path")
	assert.Equal(t, 0, len(fake.calls), "no-dir path must make zero git calls")
	assert.NotContains(t, logged, "warning", "the common no-dir path must not warn-log")
}

// TC-3: empty runtime + dir exists but git does not register it ⇒ never adopt a
// stale tree — base fallback, no cache-back, warn-logged.
func TestGoalWorkDir_EmptyRuntime_UnregisteredDir_FallsBackToBase(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	mkWorktreeDir(t, d, "goal-007")
	// `worktree list --porcelain` registers only the base — the dir is stale.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "worktree", "list", "--porcelain") {
			return "worktree " + dir + "\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	var got string
	logged := captureLog(t, func() { got = d.goalWorkDir("goal-007") })

	assert.Equal(t, dir, got, "an unregistered dir must never be adopted")
	assert.Empty(t, d.runtime("goal-007").WorktreeDir, "a rejected dir must not be cached")
	assert.Contains(t, logged, "warning", "a rejected on-disk dir must be warn-logged")
}

// TC-4: primed runtime (inline lane) ⇒ returns rt.WorktreeDir unchanged with no
// disk/git re-probe regression.
func TestGoalWorkDir_RuntimeSet_ReturnsWorktreeUnchanged(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	wtPath := mkWorktreeDir(t, d, "goal-007")
	primeWorktree(d, "goal-007")
	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	got := d.goalWorkDir("goal-007")

	assert.Equal(t, wtPath, got, "primed runtime must be returned as-is")
	assert.Equal(t, 0, len(fake.calls), "the set-WorktreeDir path must make zero git calls")
}

// --- createValidatorAndSendPayload cwd + marker ------------------------------

// setupValidatorCwdTest wires a daemon ready for a direct
// createValidatorAndSendPayload call (dispatch_valwin_marker_test.go pattern)
// and returns the captured validator-window cwd.
func setupValidatorCwdTest(t *testing.T) (*Daemon, string, *string) {
	t.Helper()
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.validatorSendDelay = 0
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)
	setupValidatorMocks(exec, testSession, "@5", "validator-007")
	gotCwd := new(string)
	d.SetWindowCreateFunc(cwdCapturingCreateWindowFn("@5", gotCwd))
	return d, dir, gotCwd
}

// TC-5: retry/crash-recovery lane — EMPTY runtime, worktree present on disk ⇒
// the validator window boots in the worktree, not the base repo.
func TestCreateValidator_RetryLane_EmptyRuntime_CwdIsWorktree(t *testing.T) {
	d, dir, gotCwd := setupValidatorCwdTest(t)
	mkGitRepo(t, dir)
	wtPath := mkWorktreeDir(t, d, "goal-007")
	d.SetGitRunnerFunc(registeredWorktreeGit(dir, wtPath).run)

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-007"}))

	assert.Equal(t, wtPath, *gotCwd, "validator window must boot in the goal worktree on the empty-runtime lane")
}

// TC-6: inline lane — primed runtime ⇒ cwd is the worktree (no regression).
func TestCreateValidator_InlineLane_PrimedRuntime_CwdIsWorktree(t *testing.T) {
	d, dir, gotCwd := setupValidatorCwdTest(t)
	mkGitRepo(t, dir)
	wtPath := mkWorktreeDir(t, d, "goal-007")
	primeWorktree(d, "goal-007")

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-007"}))

	assert.Equal(t, wtPath, *gotCwd, "validator window must boot in the primed worktree on the inline lane")
}

// TC-7: worktree goal ⇒ the taskvisor-current-worktree marker is published
// non-empty under BASE .tmux-cli with the worktree path.
func TestCreateValidator_WorktreeGoal_WritesNonEmptyWorktreeMarker(t *testing.T) {
	d, dir, _ := setupValidatorCwdTest(t)
	mkGitRepo(t, dir)
	wtPath := mkWorktreeDir(t, d, "goal-007")
	d.SetGitRunnerFunc(registeredWorktreeGit(dir, wtPath).run)

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-007"}))

	markerPath := filepath.Join(dir, ".tmux-cli", "taskvisor-current-worktree")
	assert.Equal(t, wtPath, readMarker(t, markerPath),
		"marker must be published non-empty at BASE .tmux-cli with the worktree path")
}

// TC-8: base goal (MaxGoals=1 state, no worktree) ⇒ remove-on-base semantics
// preserved — a stale prior-goal marker is removed, none is written.
func TestCreateValidator_BaseGoal_MarkerAbsent(t *testing.T) {
	d, dir, gotCwd := setupValidatorCwdTest(t)
	markerPath := filepath.Join(dir, ".tmux-cli", "taskvisor-current-worktree")
	require.NoError(t, os.MkdirAll(filepath.Dir(markerPath), 0o755))
	require.NoError(t, os.WriteFile(markerPath, []byte("/stale/prior-goal"), 0o644))

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-007"}))

	assert.Equal(t, dir, *gotCwd, "no worktree ⇒ validator cwd is the base repo")
	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "base goal must leave NO worktree marker (stale one removed)")
}

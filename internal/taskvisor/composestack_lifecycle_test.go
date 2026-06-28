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

// composestack_lifecycle_test.go — T2: per-worktree compose stack wired into the
// goal lifecycle (bring-up at dispatch, validate retarget + db-lock gate, teardown
// at discard, orphan reap at activate). Mirrors worktree_test.go's fake-git pattern
// and composestack_test.go's fake-compose pattern. Implements TC-1..TC-15.

// dockerTestEnv is a minimal docker-mode test-environment.md so ResolveExecRuntime
// returns RunTarget=docker (AppSvc "app") and the stack lifecycle engages.
const dockerTestEnv = "**Run Target:** docker\n**Playwright Status:** not applicable (API-only)\n"

// fakeStackRunner is a recording ComposeRunnerFunc that also returns a configurable
// stdout/exit/err per argv — needed for the `compose ls` enumeration in the reap
// tests (the composestack_test.go fakeComposeRunner returns no stdout). Reuses the
// in-package composeCall / containsSeq helpers from composestack_test.go.
type fakeStackRunner struct {
	calls   []composeCall
	respond func(args []string) (stdout string, code int, err error)
}

func (f *fakeStackRunner) run(_ context.Context, dir string, _ []string, args ...string) (string, string, int, error) {
	f.calls = append(f.calls, composeCall{dir: dir, args: append([]string(nil), args...)})
	if f.respond != nil {
		out, code, err := f.respond(args)
		return out, "", code, err
	}
	return "", "", 0, nil
}

// countSeq returns how many recorded calls contain seq as a contiguous subslice.
func (f *fakeStackRunner) countSeq(seq ...string) int {
	n := 0
	for _, c := range f.calls {
		if containsSeq(c.args, seq...) {
			n++
		}
	}
	return n
}

// primeWorktreeDir primes a goal's runtime worktree AND materializes the dir with a
// base compose file, so the stack helpers (which stat for a compose file / use the
// dir as cwd) operate on a real path.
func primeWorktreeDir(t *testing.T, d *Daemon, goalID string) string {
	t.Helper()
	wt := d.worktreePath(goalID)
	require.NoError(t, os.MkdirAll(wt, 0o755))
	writeComposeFile(t, wt, "docker-compose.yml")
	rt := d.runtime(goalID)
	rt.WorktreeDir = wt
	rt.Branch = worktreeBranch(goalID)
	return wt
}

// --- TC-1: bring-up on the worktree path ----------------------------------

func TestBringUpWorktreeStack_WorktreePath(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, dockerTestEnv)
	wt := d.worktreePath("goal-015")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	writeComposeFile(t, wt, "docker-compose.yml")

	fake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(fake.run)

	require.NoError(t, d.bringUpWorktreeStack(&Goal{ID: "goal-015"}, wt))

	require.NotEmpty(t, fake.calls, "worktree goal must bring its stack up")
	first := fake.calls[0]
	assert.Equal(t, wt, first.dir, "up must run with cwd=worktree")
	assert.True(t, containsSeq(first.args, "compose", "-p", "taskvisor-goal-015"),
		"up pins the per-worktree project: %v", first.args)
	assert.True(t, containsSeq(first.args, "up", "-d"), "up -d (detached): %v", first.args)
}

// --- TC-2: no-worktree no-op ----------------------------------------------

func TestBringUpWorktreeStack_NoWorktreeNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, dockerTestEnv)

	fake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(fake.run)

	// cwd == d.workDir is the MaxGoals=1 / no-worktree shape.
	require.NoError(t, d.bringUpWorktreeStack(&Goal{ID: "goal-1"}, dir))
	assert.Empty(t, fake.calls, "no-worktree path makes zero compose calls (byte-identical)")
}

// TestBringUpWorktreeStack_LocalRuntimeNoop: a worktree goal on a LOCAL (non-docker)
// project has no compose stack — bring-up must no-op rather than try to `up` a
// missing stack and halt dispatch (the parallel-Go-project regression guard).
func TestBringUpWorktreeStack_LocalRuntimeNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, "**Run Target:** local\n")
	wt := d.worktreePath("goal-015")
	require.NoError(t, os.MkdirAll(wt, 0o755))

	fake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(fake.run)

	require.NoError(t, d.bringUpWorktreeStack(&Goal{ID: "goal-015"}, wt))
	assert.Empty(t, fake.calls, "local runtime has no stack — zero compose calls")
}

// --- TC-3: up fails ⇒ infra/ops halt, no window, no code-retry charge ------

func TestBringUpWorktreeStack_UpFailsHaltsInfraOps(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	mkGitRepo(t, dir)
	writeTestEnvMD(t, dir, dockerTestEnv)
	writeSettingsMaxGoals(t, dir, 2) // MaxGoals>1 so ensureWorktree materializes a worktree
	_, err := EnsureGoalDir(dir, "goal-015")
	require.NoError(t, err)

	wtPath := d.worktreePath("goal-015")
	gitFake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
			_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
			writeComposeFile(t, wtPath, "docker-compose.yml")
		}
	}}
	d.SetGitRunnerFunc(gitFake.run)

	// Compose `up` fails — an environment fault.
	composeFake := &fakeStackRunner{respond: func(args []string) (string, int, error) {
		if containsSeq(args, "up", "-d") {
			return "", 1, nil
		}
		return "", 0, nil
	}}
	d.SetComposeRunnerFunc(composeFake.run)

	// killGoalWindows / collectManagedNames / waitWindowsGone all see an empty session.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	var windowsCreated int
	d.SetWindowCreateFunc(countingCreateWindowFn(&windowsCreated, "@1"))

	gf := &GoalsFile{Goals: []Goal{{
		ID: "goal-015", Description: "t", Status: GoalPending,
		CodeRetries: 3, MaxCodeRetries: 3,
	}}}
	writeGoals(t, dir, gf)

	err = d.dispatch(&gf.Goals[0], gf)

	require.Error(t, err, "a failed compose up must halt the dispatch")
	assert.Contains(t, err.Error(), "bring up worktree stack")
	assert.Equal(t, 0, windowsCreated, "no supervisor window is leaked on a bring-up failure")
	assert.Equal(t, 3, gf.Goals[0].CodeRetries, "infra halt charges ZERO code-retry budget")
	assert.NotEqual(t, GoalRunning, gf.Goals[0].Status, "goal is not flipped to running on an infra halt")
}

func TestDiscardWorktree_TearsDownStack(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeTestEnvMD(t, dir, dockerTestEnv)
	wt := primeWorktreeDir(t, d, "goal-015")

	gitFake := &fakeGitRunner{}
	d.SetGitRunnerFunc(gitFake.run)
	composeFake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(composeFake.run)

	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-015"}))

	require.NotEmpty(t, composeFake.calls, "discard tears the per-worktree stack down")
	down := composeFake.calls[0]
	assert.Equal(t, wt, down.dir, "down runs with cwd=worktree (still present)")
	assert.True(t, containsSeq(down.args, "compose", "-p", "taskvisor-goal-015", "down", "-v"),
		"down -v on the per-worktree project: %v", down.args)
	assert.Equal(t, 1, gitFake.count("worktree", "remove", "--force"), "worktree dir still removed")
	assert.Equal(t, 1, gitFake.count("branch", "-D", worktreeBranch("goal-015")), "branch still removed")
	assert.Empty(t, d.runtime("goal-015").WorktreeDir, "runtime worktree cleared")
}

func TestDiscardWorktree_NoWorktreeNoTeardown(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, dockerTestEnv) // docker, but no worktree primed

	gitFake := &fakeGitRunner{}
	d.SetGitRunnerFunc(gitFake.run)
	composeFake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(composeFake.run)

	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-1"}))
	assert.Empty(t, composeFake.calls, "no worktree ⇒ zero compose (byte-identical no-op)")
	assert.Equal(t, 0, len(gitFake.calls), "no worktree ⇒ zero git")
}

func TestDiscardWorktree_TeardownIdempotent(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeTestEnvMD(t, dir, dockerTestEnv)
	primeWorktreeDir(t, d, "goal-015")

	gitFake := &fakeGitRunner{}
	d.SetGitRunnerFunc(gitFake.run)
	// down reports the stack was never up (project-not-found ⇒ non-zero exit).
	composeFake := &fakeStackRunner{respond: func(args []string) (string, int, error) {
		if containsSeq(args, "down", "-v") {
			return "no configuration file provided: not found", 1, nil
		}
		return "", 0, nil
	}}
	d.SetComposeRunnerFunc(composeFake.run)

	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-015"}),
		"an idempotent down failure is warn-and-continue, never surfaced")
	assert.Equal(t, 1, gitFake.count("worktree", "remove", "--force"),
		"worktree removal still proceeds after a best-effort teardown failure")
}

// --- TC-12 / TC-13: finalizeWorktreeOnDone --------------------------------

// cleanMergeResponder drives a clean merge-back (dirty status, 1 commit ahead, base
// "main") so finalizeWorktreeOnDone reaches discardWorktree.
func cleanMergeResponder(args []string) (string, int) {
	switch {
	case argsContain(args, "status", "--porcelain"):
		return "M internal/a.go\n", 0
	case argsContain(args, "rev-list", "--count"):
		return "1\n", 0
	case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
		return "main\n", 0
	}
	return "", 0
}

func TestFinalizeWorktreeOnDone_TeardownOnDone(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeTestEnvMD(t, dir, dockerTestEnv)
	primeWorktreeDir(t, d, "goal-015")

	gitFake := &fakeGitRunner{respond: cleanMergeResponder}
	d.SetGitRunnerFunc(gitFake.run)
	composeFake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(composeFake.run)

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-015", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed)

	assert.GreaterOrEqual(t, composeFake.countSeq("down", "-v"), 1, "done goal tears its stack down")
	assert.Equal(t, 1, gitFake.count("worktree", "remove", "--force"), "worktree removed on done")
}

func TestFinalizeWorktreeOnDone_NeedsMergePreservesStack(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	mkGitRepo(t, dir)
	writeTestEnvMD(t, dir, dockerTestEnv)
	primeWorktreeDir(t, d, "goal-015")

	// In-scope conflict ⇒ BLOCK path, which deliberately SKIPS discardWorktree.
	gitFake := &fakeGitRunner{respond: conflictResponder("internal/taskvisor/worktree.go")}
	d.SetGitRunnerFunc(gitFake.run)
	composeFake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(composeFake.run)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor"}}, nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-015", Status: GoalDone, Scope: []string{"internal/taskvisor/**"}}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed)
	assert.Equal(t, GoalBlocked, gf.Goals[0].Status, "in-scope conflict ⇒ needs-merge BLOCK")

	assert.Empty(t, composeFake.calls,
		"needs-merge BLOCK preserves the worktree AND its stack — no teardown")
	assert.Equal(t, 0, gitFake.count("worktree", "remove", "--force"), "worktree preserved")
}

// --- TC-14 / TC-15: reapOrphanStacks --------------------------------------

func TestReapOrphanStacks_DownsNonRunning(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, dockerTestEnv)

	composeFake := &fakeStackRunner{respond: func(args []string) (string, int, error) {
		if containsSeq(args, "compose", "ls") {
			return `[{"Name":"taskvisor-goal-A"},{"Name":"taskvisor-goal-B"},{"Name":"productivitytool"}]`, 0, nil
		}
		return "", 0, nil
	}}
	d.SetComposeRunnerFunc(composeFake.run)

	goals := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},    // orphan ⇒ reap
		{ID: "goal-B", Status: GoalRunning}, // in-flight ⇒ keep
	}}

	d.reapOrphanStacks(goals)

	assert.Equal(t, 1, composeFake.countSeq("-p", "taskvisor-goal-A", "down", "-v"), "non-running orphan A is torn down")
	assert.Equal(t, 0, composeFake.countSeq("-p", "taskvisor-goal-B"), "in-flight goal B's stack is kept")
	assert.Equal(t, 0, composeFake.countSeq("-p", "productivitytool"), "the operator's main stack is NEVER touched")
}

func TestReapOrphanStacks_NoStacksNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, dockerTestEnv)

	composeFake := &fakeStackRunner{respond: func(args []string) (string, int, error) {
		if containsSeq(args, "compose", "ls") {
			return "[]", 0, nil
		}
		return "", 0, nil
	}}
	d.SetComposeRunnerFunc(composeFake.run)

	d.reapOrphanStacks(&GoalsFile{})
	assert.Equal(t, 0, composeFake.countSeq("down", "-v"), "no taskvisor-* projects ⇒ zero teardown")
}

// TestReapOrphanStacks_LocalRuntimeNoop: a local-runtime project never enumerates or
// reaps stacks — zero compose calls (byte-identical activation).
func TestReapOrphanStacks_LocalRuntimeNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeTestEnvMD(t, dir, "**Run Target:** local\n")

	composeFake := &fakeStackRunner{}
	d.SetComposeRunnerFunc(composeFake.run)

	d.reapOrphanStacks(&GoalsFile{})
	assert.Empty(t, composeFake.calls, "local runtime ⇒ zero compose calls on reap")
}

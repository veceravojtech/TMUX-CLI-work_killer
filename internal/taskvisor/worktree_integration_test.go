//go:build integration

package taskvisor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runGit runs a real git command in dir, failing the test on error. Used only to
// build the fixture repo; the code under test drives git through the default
// runner (d.gitRunner() with no override).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// TestMergeWorktreeBack_Conflict exercises the real-repo conflict path end to
// end: a peer advances base so the worktree branch cannot rebase cleanly. The
// merge-back must abort, return errMergeConflict with the conflicting path, and
// leave base's HEAD untouched (no partial merge).
//
// Run with: go test -tags=integration ./internal/taskvisor/... -run TestMergeWorktreeBack_Conflict
func TestMergeWorktreeBack_Conflict(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))

	// Fixture repo on branch "main" with one tracked file.
	runGit(t, dir, "init", "-b", "main")
	shared := filepath.Join(dir, "shared.txt")
	require.NoError(t, os.WriteFile(shared, []byte("base line\n"), 0o644))
	runGit(t, dir, "add", "shared.txt")
	runGit(t, dir, "commit", "-m", "initial")

	d := New(dir, nil) // executor unused on this path
	goal := &Goal{ID: "goal-001"}

	// Create the goal's worktree from HEAD, then make a conflicting edit inside it.
	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)
	wtShared := filepath.Join(cwd, "shared.txt")
	require.NoError(t, os.WriteFile(wtShared, []byte("worktree change\n"), 0o644))

	// A peer advances base with an overlapping change on the SAME line.
	require.NoError(t, os.WriteFile(shared, []byte("peer change\n"), 0o644))
	runGit(t, dir, "add", "shared.txt")
	runGit(t, dir, "commit", "-m", "peer advance")
	baseHeadBefore := runGit(t, dir, "rev-parse", "HEAD")

	// Merge-back must detect the conflict, abort, and surface the path.
	_, mergeErr := d.mergeWorktreeBack(goal)
	require.Error(t, mergeErr)
	var mc errMergeConflict
	require.True(t, errors.As(mergeErr, &mc), "want errMergeConflict, got %v", mergeErr)
	assert.Contains(t, mc.paths, "shared.txt")

	// Base HEAD is untouched — no partial merge landed.
	assert.Equal(t, baseHeadBefore, runGit(t, dir, "rev-parse", "HEAD"), "base must have no partial merge")

	// Cleanup leaves no worktree or branch behind.
	require.NoError(t, d.discardWorktree(goal))
	wl := runGit(t, dir, "worktree", "list")
	assert.NotContains(t, wl, filepath.Join(worktreesDirName, "goal-001"))
}

// TestEnsureWorktree_UnbornHeadRealRepo reproduces backend task 317 against real
// git: a freshly `git init`'d repo has an UNBORN HEAD, so `git worktree add …
// HEAD` fails with "fatal: invalid reference: HEAD" and goal-001 poll-wedges to
// failed. ensureWorktree must self-heal by seeding an empty baseline commit so
// the add succeeds and HEAD is born in the worktree.
//
// A single base file is staged (but never committed) before the call so the
// `commit --allow-empty` seed captures it — proving born HEAD AND satisfying the
// baseCheckedOut guard (a worktree off a truly-empty commit has only `.git`,
// which baseCheckedOut treats as control-plane → base-less). The unborn-HEAD
// trigger is unchanged: rev-parse exits non-zero until the seed commit lands.
//
// Run with: go test -tags=integration ./internal/taskvisor/... -run TestEnsureWorktree_UnbornHeadRealRepo
func TestEnsureWorktree_UnbornHeadRealRepo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))

	// Unborn HEAD: init only, NO initial commit. Stage (don't commit) a base file
	// so the seed commit materializes base content in the worktree.
	runGit(t, dir, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644))
	runGit(t, dir, "add", "README.md")

	// Precondition: HEAD really is unborn (the task-317 failure trigger).
	unborn := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", "HEAD")
	require.Error(t, unborn.Run(), "precondition: HEAD must be unborn before ensureWorktree")

	d := New(dir, nil) // executor unused on this path
	goal := &Goal{ID: "goal-001"}

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err, "unborn HEAD must self-heal — no 'invalid reference: HEAD'")

	// HEAD is now born in the worktree.
	born := exec.Command("git", "-C", cwd, "rev-parse", "HEAD")
	require.NoError(t, born.Run(), "worktree HEAD must be born after the seed commit")

	// The per-goal worktree is registered against its branch.
	assert.Contains(t, runGit(t, dir, "worktree", "list"), worktreeBranch("goal-001"),
		"git worktree list must show the goal-001 worktree")

	require.NoError(t, d.discardWorktree(goal))
}

// TestMergeWorktreeBack_NeverCommitsControlPlaneSymlink is the regression guard
// for the parallel-mode ELOOP that destroyed the control plane. A per-goal
// worktree carries a .tmux-cli back-symlink into the shared base control plane.
// Before the fix, `git add -A` staged it (the git-exclude was directory-only
// "/.tmux-cli/"), it was committed and fast-forward-merged into base — replacing
// base's real .tmux-cli DIRECTORY with a self-referential symlink (ELOOP). After
// the fix the back-symlink is excluded by name AND by a :(exclude) staging
// pathspec, so base's .tmux-cli survives the merge as a real directory and is
// never tracked.
//
// Run with: go test -tags=integration ./internal/taskvisor/... -run NeverCommitsControlPlaneSymlink
func TestMergeWorktreeBack_NeverCommitsControlPlaneSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	// A real control-plane file that MUST survive the merge untouched.
	ctlFile := filepath.Join(dir, ".tmux-cli", "goals.yaml")
	require.NoError(t, os.WriteFile(ctlFile, []byte("goals: []\n"), 0o644))

	runGit(t, dir, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644))
	runGit(t, dir, "add", "app.go")
	runGit(t, dir, "commit", "-m", "initial")

	// The fix under test (Layer A): managed git-excludes must match the back-symlink.
	require.NoError(t, setup.EnsureGitExclude(dir))

	d := New(dir, nil) // executor unused on this path
	goal := &Goal{ID: "goal-001"}

	// ensureWorktree creates the worktree AND the .tmux-cli back-symlink.
	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)
	// Precondition: the worktree really carries a .tmux-cli SYMLINK (the hazard).
	lfi, err := os.Lstat(filepath.Join(cwd, ".tmux-cli"))
	require.NoError(t, err)
	require.NotZero(t, lfi.Mode()&os.ModeSymlink, "precondition: worktree .tmux-cli must be a symlink")

	// Layer-A isolation: git must now ignore the back-symlink inside the worktree.
	ci, _ := exec.Command("git", "-C", cwd, "check-ignore", ".tmux-cli").CombinedOutput()
	assert.Equal(t, ".tmux-cli", strings.TrimSpace(string(ci)),
		"back-symlink must be git-ignored in the worktree")

	// A real implementer edit so the merge-back has something to land.
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "app.go"),
		[]byte("package app\n\nvar X = 1\n"), 0o644))

	// Drive the real merge-back (real git add -A/commit/rebase/merge --ff-only).
	_, mwbErr := d.mergeWorktreeBack(goal)
	require.NoError(t, mwbErr)

	// Regression assertions ---------------------------------------------------
	// 1. base/.tmux-cli is STILL a real directory (NOT replaced by a symlink).
	bfi, err := os.Lstat(filepath.Join(dir, ".tmux-cli"))
	require.NoError(t, err)
	assert.Zero(t, bfi.Mode()&os.ModeSymlink, "base .tmux-cli must remain a real directory after merge")
	assert.True(t, bfi.IsDir(), "base .tmux-cli must remain a directory")
	// 2. the control-plane file survived untouched.
	got, err := os.ReadFile(ctlFile)
	require.NoError(t, err, "control-plane file must survive the merge")
	assert.Equal(t, "goals: []\n", string(got))
	// 3. .tmux-cli is NEVER tracked in base.
	assert.NotContains(t, runGit(t, dir, "ls-files"), ".tmux-cli",
		"control plane must never be tracked in base")
	// 4. the implementer edit DID land (the merge actually worked).
	assert.Contains(t, runGit(t, dir, "show", "HEAD:app.go"), "var X = 1")

	require.NoError(t, d.discardWorktree(goal))
}

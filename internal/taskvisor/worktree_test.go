package taskvisor

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGitRunner is a recording GitRunnerFunc: it captures every invocation's
// argv (so tests assert the exact git commands without a real repo) and returns
// canned (stdout, exitCode) per a responder. A sideEffect hook lets a test
// materialize the worktree dir on `worktree add` so the control-plane symlink
// path is exercised on the real (temp) filesystem.
type fakeGitRunner struct {
	mu         sync.Mutex
	calls      [][]string
	respond    func(args []string) (stdout string, code int)
	sideEffect func(args []string)
}

func (f *fakeGitRunner) run(_ context.Context, args ...string) (string, string, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if f.sideEffect != nil {
		f.sideEffect(args)
	}
	if f.respond != nil {
		out, code := f.respond(args)
		return out, "", code, nil
	}
	return "", "", 0, nil
}

// count returns how many recorded calls contain seq as a contiguous subslice.
func (f *fakeGitRunner) count(seq ...string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if argsContain(c, seq...) {
			n++
		}
	}
	return n
}

func argsContain(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j := range seq {
			if args[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func mkGitRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
}

// --- TestDispatch_MaxGoals1_NoWorktree ------------------------------------

func TestDispatch_MaxGoals1_NoWorktree(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "t", Status: GoalPending}}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	setupDispatchMocks(exec, testSession, "@1")

	var gotCwd string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		gotCwd = cwd
		return &CreatedWindow{TmuxWindowID: "@1", Name: name}, nil
	})

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	assert.Equal(t, 0, len(fake.calls), "MaxGoals=1 must run zero git commands")
	assert.Equal(t, dir, gotCwd, "supervisor window cwd must be the base workDir")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir, "no worktree at MaxGoals=1")
}

// --- ensureWorktree --------------------------------------------------------

func TestEnsureWorktree_ParallelGoal_CreatesAndBindsCwd(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 1, fake.count("worktree", "add"), "worktree add must run exactly once")
	assert.True(t, fake.count("-b", worktreeBranch("goal-001")) == 1, "branch -b taskvisor/goal-001")
	assert.Equal(t, wtPath, d.runtime("goal-001").WorktreeDir)
	assert.Equal(t, worktreeBranch("goal-001"), d.runtime("goal-001").Branch)
}

func TestEnsureWorktree_ReusedOnRetry(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")
	require.NoError(t, os.MkdirAll(wtPath, 0o755)) // worktree already exists (prior cycle)

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 0, fake.count("worktree", "add"), "existing worktree must be reused, not re-added")
	assert.Equal(t, wtPath, d.runtime("goal-001").WorktreeDir)
}

func TestEnsureWorktree_NotAGitRepo_FallsBackToBase(t *testing.T) {
	d, _, dir := setupDaemon(t) // t.TempDir() has no .git
	goal := &Goal{ID: "goal-001"}

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err, "non-git repo must never error")

	assert.Equal(t, dir, cwd, "falls back to base tree")
	assert.Equal(t, 0, len(fake.calls), "no git in a non-git project")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir)
}

func TestEnsureWorktree_SymlinksControlPlane(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	// A worker writing under the worktree's .tmux-cli/research must land in the
	// single shared base control plane (the symlink resolves through to base).
	researchDir := filepath.Join(wtPath, ".tmux-cli", "research")
	require.NoError(t, os.MkdirAll(researchDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(researchDir, "report.md"), []byte("hi"), 0o644))

	baseFile := filepath.Join(dir, ".tmux-cli", "research", "report.md")
	got, err := os.ReadFile(baseFile)
	require.NoError(t, err, "file written via worktree cwd must resolve into base .tmux-cli")
	assert.Equal(t, "hi", string(got))
}

// --- mergeWorktreeBack -----------------------------------------------------

// primeWorktree sets a goal's runtime as if ensureWorktree had created a worktree.
func primeWorktree(d *Daemon, goalID string) {
	rt := d.runtime(goalID)
	rt.WorktreeDir = d.worktreePath(goalID)
	rt.Branch = worktreeBranch(goalID)
}

func TestMergeWorktreeBack_CleanMerge_AdvancesAndRemoves(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "M internal/a.go\n", 0
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed)

	assert.Equal(t, 1, fake.count("add", "-A"), "stage all")
	assert.Equal(t, 1, fake.count("commit", "-m"), "commit dirty worktree")
	assert.Equal(t, 1, fake.count("rebase", "main"), "rebase onto base branch")
	assert.Equal(t, 1, fake.count("merge", "--ff-only"), "ff-only merge into base")
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"), "worktree removed after merge")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir, "runtime worktree cleared")
}

func TestMergeWorktreeBack_EmptyDiff_NoCommitNoMerge(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "", 0 // clean
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0 // no commits ahead
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	require.NoError(t, d.mergeWorktreeBack(&Goal{ID: "goal-001"}))

	assert.Equal(t, 0, fake.count("commit", "-m"), "no commit on empty diff")
	assert.Equal(t, 0, fake.count("rebase"), "no rebase on empty diff")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"), "no merge on empty diff")

	// And the worktree is still removed on completion.
	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-001"}))
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"))
}

func TestMergeWorktreeBack_Conflict_FailsGoalCleanly(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "M internal/shared.go\n", 0
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "rebase", "main"):
			return "CONFLICT", 1 // peer advanced base ⇒ conflict
		case argsContain(args, "diff", "--name-only", "--diff-filter=U"):
			return "internal/shared.go\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed, "merge conflict must fail the goal")

	assert.Equal(t, GoalFailed, gf.Goals[0].Status)
	assert.Equal(t, 1, fake.count("rebase", "--abort"), "rebase aborted (no partial state)")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"), "base must NOT be merged into on conflict")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	valSig, ok := sig.(*ValidatorSignal)
	require.True(t, ok)
	assert.Equal(t, VerdictFail, valSig.Verdict)
	assert.Contains(t, valSig.NextAction, "internal/shared.go", "conflicting path surfaced")
}

func TestMergeWorktreeBack_SerializedUnderLock(t *testing.T) {
	_, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)

	var (
		mu      sync.Mutex
		inside  int
		overlap bool
		wg      sync.WaitGroup
	)
	body := func() error {
		mu.Lock()
		inside++
		if inside > 1 {
			overlap = true
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inside--
		mu.Unlock()
		return nil
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = WithMergeLock(dir, body)
		}()
	}
	wg.Wait()

	assert.False(t, overlap, "WithMergeLock must serialize merge-back critical sections")
}

// --- discardWorktree -------------------------------------------------------

func TestDiscardWorktree_OnHardHalt_RemovesBranchAndDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	d.cleanupWorktreeOnHalt(&Goal{ID: "goal-001"})

	assert.Equal(t, 1, fake.count("worktree", "remove", "--force", wtPath), "worktree dir removed")
	assert.Equal(t, 1, fake.count("branch", "-D", worktreeBranch("goal-001")), "branch removed")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir)
}

func TestDiscardWorktree_MaxGoals1_NoGit(t *testing.T) {
	d, _, _ := setupDaemon(t)
	// No worktree primed (WorktreeDir empty) — the MaxGoals=1 shape.
	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-001"}))
	assert.Equal(t, 0, len(fake.calls), "no worktree ⇒ zero git on discard")
}

// --- pruneOrphanWorktrees --------------------------------------------------

func TestPruneOrphanWorktrees_OnActivate_RemovesStale(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	base := filepath.Join(dir, worktreesDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "goal-001"), 0o755)) // running — keep
	require.NoError(t, os.MkdirAll(filepath.Join(base, "goal-002"), 0o755)) // orphan — remove

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	goals := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning},
		{ID: "goal-002", Status: GoalDone},
	}}

	d.pruneOrphanWorktrees(goals)

	assert.Equal(t, 1, fake.count("worktree", "prune"))
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force", filepath.Join(base, "goal-002")), "orphan removed")
	assert.Equal(t, 0, fake.count("worktree", "remove", "--force", filepath.Join(base, "goal-001")), "running goal's worktree kept")
	assert.Equal(t, 1, fake.count("branch", "-D", worktreeBranch("goal-002")))
}

func TestPruneOrphanWorktrees_NoDir_ZeroGit(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir) // git repo, but no .tmux-cli-worktrees dir
	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	d.pruneOrphanWorktrees(&GoalsFile{})
	assert.Equal(t, 0, len(fake.calls), "absent worktrees dir ⇒ zero git (MaxGoals=1 byte-identical)")
}

// TestPruneOrphanWorktrees_NewBaseDir pins the relocated prune base: orphans under
// the <base>/.tmux-cli-worktrees sibling are removed while an in-flight goal's
// worktree is preserved.
func TestPruneOrphanWorktrees_NewBaseDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	base := filepath.Join(dir, worktreesDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "goal-001"), 0o755)) // running — keep
	require.NoError(t, os.MkdirAll(filepath.Join(base, "goal-002"), 0o755)) // orphan — remove

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	goals := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning},
		{ID: "goal-002", Status: GoalDone},
	}}

	d.pruneOrphanWorktrees(goals)

	assert.Equal(t, 1, fake.count("worktree", "prune"))
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force", filepath.Join(base, "goal-002")), "orphan removed")
	assert.Equal(t, 0, fake.count("worktree", "remove", "--force", filepath.Join(base, "goal-001")), "running goal's worktree kept")
}

// --- worktreePath relocation ----------------------------------------------

// TestWorktreePath_UsesSibling asserts the per-goal worktree lives in the in-repo
// sibling <base>/.tmux-cli-worktrees/<id>, NOT nested inside the control plane.
func TestWorktreePath_UsesSibling(t *testing.T) {
	d, _, dir := setupDaemon(t)
	got := d.worktreePath("goal-001")

	assert.Equal(t, filepath.Join(dir, ".tmux-cli-worktrees", "goal-001"), got)
	ctl := filepath.Join(dir, ".tmux-cli") + string(os.PathSeparator)
	assert.False(t, strings.HasPrefix(got, ctl),
		"worktree must not be nested under the control plane (.tmux-cli)")
}

// TestEnsureWorktree_NoSelfReference materializes a worktree (with its
// control-plane back-symlink) and asserts that walking <base>/.tmux-cli never
// re-enters a worktree — i.e. the control plane no longer contains itself, which
// is the ELOOP self-reference this relocation kills.
func TestEnsureWorktree_NoSelfReference(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	// The worktree (and its <wt>/.tmux-cli back-symlink) must NOT be reachable by
	// walking the control plane: walk is Lstat-based, so it cannot follow the
	// symlink, and the worktree is a SIBLING of .tmux-cli, never a descendant.
	ctlDir := filepath.Join(dir, ".tmux-cli")
	walkErr := filepath.Walk(ctlDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		assert.NotContains(t, path, worktreesDirName,
			"walking the control plane must never reach a worktree (%s)", path)
		return nil
	})
	require.NoError(t, walkErr)

	wtCtl := filepath.Join(wtPath, ".tmux-cli")
	assert.False(t, strings.HasPrefix(wtCtl, ctlDir+string(os.PathSeparator)),
		"the worktree's control-plane symlink must live outside .tmux-cli")
}

// --- safeToRemoveWorktree --------------------------------------------------

func TestSafeToRemoveWorktree_Table(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, ".tmux-cli"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, worktreesDirName), 0o755))

	// A symlink under the sibling that resolves back into the control plane must
	// be refused even though its literal path passes the positive allowlist.
	evil := filepath.Join(base, worktreesDirName, "evil")
	require.NoError(t, os.Symlink(filepath.Join(base, ".tmux-cli"), evil))

	cases := []struct {
		name  string
		path  string
		allow bool
	}{
		{"legit worktree", filepath.Join(base, worktreesDirName, "g1"), true},
		{"base itself", base, false},
		{"control plane", filepath.Join(base, ".tmux-cli"), false},
		{"ancestor of base", filepath.Dir(base), false},
		{"empty path", "", false},
		{"outside worktree root", filepath.Join(base, "src"), false},
		{"symlink into control plane", evil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := safeToRemoveWorktree(base, tc.path)
			if tc.allow {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestDiscardWorktree_GuardBlocksControlPlane points a goal's WorktreeDir at the
// control plane (a corruption that must never delete it): discardWorktree must
// refuse the worktree remove (no argv recorded), log loudly, yet still attempt
// the branch delete.
func TestDiscardWorktree_GuardBlocksControlPlane(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	rt := d.runtime("goal-001")
	rt.WorktreeDir = filepath.Join(dir, ".tmux-cli")
	rt.Branch = worktreeBranch("goal-001")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	fake := &fakeGitRunner{}
	d.SetGitRunnerFunc(fake.run)

	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-001"}))

	assert.Equal(t, 0, fake.count("worktree", "remove", "--force"),
		"guard must refuse removing the control plane")
	assert.Equal(t, 1, fake.count("branch", "-D", worktreeBranch("goal-001")),
		"branch delete is still attempted after a refused worktree remove")
	assert.Contains(t, logBuf.String(), "refusing unsafe worktree remove",
		"a refused remove must be logged loudly")
}

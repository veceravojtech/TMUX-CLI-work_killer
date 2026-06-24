package taskvisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// autocommit_test.go — completion-time auto-commit (goal-009). Recording-fake
// tests inject SetGitRunnerFunc and assert exact argv without a repo; real-repo
// tests git-init in t.TempDir() and verify the on-disk commit/staging effects.

// autoCommitDaemon builds a daemon over dir with the fake runner injected and
// the auto-commit gate open (mirrors New()'s default-ON seed).
func autoCommitDaemon(t *testing.T, dir string, fake *fakeGitRunner) *Daemon {
	t.Helper()
	d := New(dir, new(testutil.MockTmuxExecutor))
	d.SetGitRunnerFunc(fake.run)
	d.autoCommit = true
	return d
}

// commitMessages returns the -m argument of every recorded `git commit` call.
func commitMessages(f *fakeGitRunner) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var msgs []string
	for _, c := range f.calls {
		for i := 0; i+1 < len(c); i++ {
			if c[i] == "-m" && argsContain(c, "commit") {
				msgs = append(msgs, c[i+1])
			}
		}
	}
	return msgs
}

// dirtyScopeResponse answers `status --porcelain` with one in-scope dirty file
// so the stage→commit path proceeds; everything else exits 0.
func dirtyScopeResponse(args []string) (string, int) {
	if argsContain(args, "status", "--porcelain") {
		return " M internal/taskvisor/x.go\n", 0
	}
	return "", 0
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// mkRealGitRepo creates a real repo with one in-scope and one out-of-scope file
// committed clean, then dirties both. Returns the repo dir.
func mkRealGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.email", "taskvisor@test.local")
	runGitCmd(t, dir, "config", "user.name", "Taskvisor Test")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "taskvisor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "taskvisor", "x.go"), []byte("package taskvisor\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o644))
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")

	// Dirty one in-scope and one out-of-scope file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "taskvisor", "x.go"), []byte("package taskvisor\n\n// changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme changed\n"), 0o644))
	return dir
}

func scopedGoal() *Goal {
	return &Goal{
		ID:          "goal-009",
		Description: "Auto-commit resolved goals to the current branch",
		Status:      GoalDone,
		Scope:       []string{"internal/taskvisor/**"},
	}
}

// --- real-repo tests --------------------------------------------------------

func TestAutoCommit_StagesScopeOnlyAndCommitsToCurrentBranch(t *testing.T) {
	dir := mkRealGitRepo(t)
	branchBefore := runGitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	d := New(dir, new(testutil.MockTmuxExecutor))
	require.True(t, d.autoCommit, "New() must seed auto-commit ON")
	d.autoCommitGoal(scopedGoal())

	assert.Equal(t, "2", runGitCmd(t, dir, "rev-list", "--count", "HEAD"), "exactly one new commit on top of initial")
	assert.Equal(t, branchBefore, runGitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"), "branch must be unchanged")

	subject := runGitCmd(t, dir, "log", "-1", "--pretty=%s")
	assert.Equal(t, "goal-009: Auto-commit resolved goals to the current branch", subject)

	files := runGitCmd(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	assert.Contains(t, files, "internal/taskvisor/x.go", "in-scope path must be committed")
	assert.NotContains(t, files, "README.md", "out-of-scope path must NOT be committed")
}

func TestAutoCommit_UnrelatedChangesSurviveUnstaged(t *testing.T) {
	dir := mkRealGitRepo(t)

	d := New(dir, new(testutil.MockTmuxExecutor))
	d.autoCommitGoal(scopedGoal())

	unstaged := runGitCmd(t, dir, "diff", "--name-only")
	assert.Contains(t, unstaged, "README.md", "out-of-scope file must remain modified-unstaged")
	assert.NotContains(t, unstaged, "x.go", "in-scope file must be committed clean")
	assert.Empty(t, runGitCmd(t, dir, "diff", "--cached", "--name-only"), "nothing may be left staged after the commit")
}

// TestAutoCommit_ScopelessStagesWholeTreeRealRepo exercises the third fallback
// tier end-to-end: a goal with no scope and no completion report over a real
// dirty repo must `git add -A` and commit the WHOLE tree (both the in-scope and
// the out-of-scope file land in the single commit), with goalCommitMessage(g) as
// the subject and the branch unchanged.
func TestAutoCommit_ScopelessStagesWholeTreeRealRepo(t *testing.T) {
	dir := mkRealGitRepo(t)
	branchBefore := runGitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	d := New(dir, new(testutil.MockTmuxExecutor))
	require.True(t, d.autoCommit, "New() must seed auto-commit ON")
	g := scopedGoal()
	g.Scope = nil
	d.autoCommitGoal(g)

	assert.Equal(t, "2", runGitCmd(t, dir, "rev-list", "--count", "HEAD"), "exactly one new commit on top of initial")
	assert.Equal(t, branchBefore, runGitCmd(t, dir, "rev-parse", "--abbrev-ref", "HEAD"), "branch must be unchanged")
	assert.Equal(t, goalCommitMessage(g), runGitCmd(t, dir, "log", "-1", "--pretty=%s"))

	files := runGitCmd(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	assert.Contains(t, files, "internal/taskvisor/x.go", "in-scope path must be committed")
	assert.Contains(t, files, "README.md", "out-of-scope path must ALSO be committed (whole-tree fallback)")
}

// --- recording-fake tests ----------------------------------------------------

func TestAutoCommit_EmptyDiffSkips(t *testing.T) {
	fake := &fakeGitRunner{} // status --porcelain returns "" by default
	d := autoCommitDaemon(t, t.TempDir(), fake)

	d.autoCommitGoal(scopedGoal())

	assert.Equal(t, 1, fake.count("status", "--porcelain"), "exactly one porcelain probe")
	assert.Equal(t, 0, fake.count("add"), "empty diff must not stage")
	assert.Equal(t, 0, fake.count("commit"), "empty diff must not commit")
}

func TestAutoCommit_DisabledSettingMakesNoGitCalls(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	d := autoCommitDaemon(t, t.TempDir(), fake)
	d.autoCommit = false

	d.autoCommitGoal(scopedGoal())

	assert.Empty(t, fake.calls, "disabled setting must invoke zero git commands")
}

func TestAutoCommit_StagesGlobPathspecsWithWorkDir(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	dir := t.TempDir()
	d := autoCommitDaemon(t, dir, fake)

	d.autoCommitGoal(scopedGoal())

	assert.Equal(t, 1, fake.count("-C", dir, "status", "--porcelain", "--", ":(glob)internal/taskvisor/**"))
	assert.Equal(t, 1, fake.count("-C", dir, "add", "--", ":(glob)internal/taskvisor/**"))
	assert.Equal(t, 1, fake.count("-C", dir, "commit", "-m"))
}

func TestAutoCommit_MessageIncludesBackendTaskSuffix(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	d := autoCommitDaemon(t, t.TempDir(), fake)

	g := scopedGoal()
	g.Acceptance = []string{
		"All TestAutoCommit_* tests pass",
		"Backend task 45 is satisfied: commits land on the current branch",
	}
	d.autoCommitGoal(g)

	msgs := commitMessages(fake)
	require.Len(t, msgs, 1)
	assert.Equal(t, "goal-009: Auto-commit resolved goals to the current branch (backend task 45)", msgs[0])
}

func TestAutoCommit_MessageOmitsSuffixWithoutMapping(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	d := autoCommitDaemon(t, t.TempDir(), fake)

	g := scopedGoal()
	g.Acceptance = []string{"All tests pass"}
	d.autoCommitGoal(g)

	msgs := commitMessages(fake)
	require.Len(t, msgs, 1)
	assert.Equal(t, "goal-009: Auto-commit resolved goals to the current branch", msgs[0])
}

func TestAutoCommit_ScopeAbsentFallsBackToReportFiles(t *testing.T) {
	dir := t.TempDir()
	// Synthetic completion report: goal-009's section names one existing and one
	// missing path; a sibling section names a path that must NOT leak in.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli", "goals"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "named.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644))
	report := "# Taskvisor Completion Report\n\n## Goals\n\n" +
		"### goal-008: sibling\n- **Status:** done\n- touched `other.txt`\n\n" +
		"### goal-009: fallback\n- **Status:** done\n- touched `named.txt` and `missing.txt`\n\n" +
		"### goal-010: after\n- touched `other.txt`\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md"), []byte(report), 0o644))

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "status", "--porcelain") {
			return " M named.txt\n", 0
		}
		return "", 0
	}}
	d := autoCommitDaemon(t, dir, fake)

	g := scopedGoal()
	g.Scope = nil
	d.autoCommitGoal(g)

	assert.Equal(t, 1, fake.count("add", "--", "named.txt"), "exactly the named existing path is staged")
	assert.Equal(t, 0, fake.count("missing.txt"), "missing path must be dropped")
	assert.Equal(t, 0, fake.count("other.txt"), "sibling sections must not leak")
	assert.Equal(t, 1, fake.count("commit"))
}

func TestAutoCommit_ScopeAbsentDirtyStagesWholeTree(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	d := autoCommitDaemon(t, t.TempDir(), fake)

	g := scopedGoal()
	g.Scope = nil
	d.autoCommitGoal(g)

	assert.Equal(t, 1, fake.count("status", "--porcelain"), "exactly one porcelain probe")
	assert.Equal(t, 0, fake.count("--porcelain", "--"), "the fallback probe is UNSCOPED — no `--` pathspec separator")
	assert.Equal(t, 1, fake.count("add", "-A"), "scopeless dirty tree must stage the whole tree with add -A")
	assert.Equal(t, 1, fake.count("commit"), "scopeless dirty tree must commit once")

	msgs := commitMessages(fake)
	require.Len(t, msgs, 1)
	assert.Equal(t, goalCommitMessage(g), msgs[0], "scopeless commit subject must be goalCommitMessage(g)")
}

func TestAutoCommit_ScopeAbsentCleanTreeSkips(t *testing.T) {
	fake := &fakeGitRunner{} // status --porcelain returns "" by default
	d := autoCommitDaemon(t, t.TempDir(), fake)

	g := scopedGoal()
	g.Scope = nil
	d.autoCommitGoal(g)

	assert.Equal(t, 1, fake.count("status", "--porcelain"), "clean tree still probes porcelain exactly once")
	assert.Equal(t, 0, fake.count("add"), "clean tree must not stage")
	assert.Equal(t, 0, fake.count("commit"), "clean tree must not commit")
}

func TestAutoCommit_NeverPushesOrBranches(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	d := autoCommitDaemon(t, dir, fake)

	// Exercise every fake-runner path: happy, no-suffix, empty-scope fallback.
	d.autoCommitGoal(scopedGoal())
	g := scopedGoal()
	g.Acceptance = []string{"Backend task 7"}
	d.autoCommitGoal(g)
	empty := scopedGoal()
	empty.Scope = nil
	d.autoCommitGoal(empty)

	for _, forbidden := range []string{"push", "branch", "checkout", "switch"} {
		assert.Zerof(t, fake.count(forbidden), "auto-commit must never invoke git %s", forbidden)
	}
}

// TestGoalCommitMessage_BothPathsRenderDescriptiveSubject is the anti-drift
// guard: the serial completion auto-commit (autocommit.go) and the parallel
// worktree merge (worktree.go) must render the IDENTICAL descriptive subject
// `<id>: <description> (backend task N)`, so the two commit paths can never
// drift on commit-message format regardless of serial vs parallel execution.
func TestGoalCommitMessage_BothPathsRenderDescriptiveSubject(t *testing.T) {
	const want = "goal-009: Auto-commit resolved goals to the current branch (backend task 71)"

	acceptance := []string{
		"All tests pass",
		"Backend task 71 is satisfied: both commit paths share one message renderer",
	}

	// (a) Serial path — autoCommitGoal commits the in-scope dirty diff.
	serialFake := &fakeGitRunner{respond: dirtyScopeResponse}
	sd := autoCommitDaemon(t, t.TempDir(), serialFake)
	sg := scopedGoal()
	sg.Acceptance = acceptance
	sd.autoCommitGoal(sg)

	serialMsgs := commitMessages(serialFake)
	require.Len(t, serialMsgs, 1, "serial path must commit exactly once")
	serialSubject := serialMsgs[0]

	// (b) Worktree path — mergeWorktreeBack commits the dirty worktree before the
	// ff-merge. Mirror TestMergeWorktreeBack_CleanMerge_AdvancesAndRemoves's
	// runtime + responder so the commit branch is taken (dirty porcelain, one
	// commit ahead, base branch resolvable).
	wd, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(wd, "goal-009")
	wtFake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "M internal/taskvisor/x.go\n", 0
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	wd.SetGitRunnerFunc(wtFake.run)

	wg := scopedGoal()
	wg.Acceptance = acceptance
	_, wmErr := wd.mergeWorktreeBack(wg)
	require.NoError(t, wmErr)

	wtMsgs := commitMessages(wtFake)
	require.Len(t, wtMsgs, 1, "worktree path must commit exactly once")
	worktreeSubject := wtMsgs[0]

	assert.Equal(t, want, serialSubject, "serial path subject")
	assert.Equal(t, want, worktreeSubject, "worktree path subject")
	assert.Equal(t, serialSubject, worktreeSubject, "both commit paths must render the same subject")
}

// TestGoalCommitMessage_OmitsSuffixWithoutBackendTask pins the helper's
// no-mapping branch: with no `Backend task N` acceptance entry the subject is
// the unsuffixed `<id>: <description>` (mirrors the serial path's
// TestAutoCommit_MessageOmitsSuffixWithoutMapping).
func TestGoalCommitMessage_OmitsSuffixWithoutBackendTask(t *testing.T) {
	g := scopedGoal()
	g.Acceptance = []string{"All tests pass"}
	assert.Equal(t, "goal-009: Auto-commit resolved goals to the current branch", goalCommitMessage(g))
}

func TestAutoCommit_GitFailureIsWarnOnly(t *testing.T) {
	g := scopedGoal()
	g.Status = GoalDone

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "status", "--porcelain") {
			return " M internal/taskvisor/x.go\n", 0
		}
		if argsContain(args, "commit") {
			return "", 1 // pre-commit hook rejection / any non-zero exit
		}
		return "", 0
	}}
	d := autoCommitDaemon(t, t.TempDir(), fake)

	assert.NotPanics(t, func() { d.autoCommitGoal(g) })
	assert.Equal(t, 1, fake.count("commit"), "the commit was attempted once")
	assert.Equal(t, GoalDone, g.Status, "a commit failure must never alter the goal status")
}

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
	dir := t.TempDir()
	mkRealGitRepoAt(t, dir)
	return dir
}

// mkRealGitRepoAt is mkRealGitRepo's in-place variant: it stamps an EXISTING dir
// (e.g. a daemon's workDir) as a real repo with one in-scope
// (internal/taskvisor/x.go) and one out-of-scope (README.md) file committed
// clean, then dirties both. Extracted so crash-recovery tests can drive
// autoCommitGoal against the daemon's own workDir instead of a detached temp dir.
func mkRealGitRepoAt(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
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

// --- goal-005: own-changeset staging (scope ∩ goal-start diff) --------------

// TestAutoCommit_ExcludesPreexistingSiblingInScopeEdit is the demonstrated-bug
// guard (real git via a fresh repo): a sibling's uncommitted in-scope edit sits
// in the tree when goal B starts; after goal B captures its start snapshot and
// makes its OWN in-scope edit, auto-commit must commit ONLY goal B's file and
// leave the sibling's edit dirty/unstaged.
func TestAutoCommit_ExcludesPreexistingSiblingInScopeEdit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.email", "taskvisor@test.local")
	runGitCmd(t, dir, "config", "user.name", "Taskvisor Test")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "taskvisor"), 0o755))
	xPath := filepath.Join(dir, "internal", "taskvisor", "x.go")         // goal B's file
	sibPath := filepath.Join(dir, "internal", "taskvisor", "sibling.go") // sibling's file
	require.NoError(t, os.WriteFile(xPath, []byte("package taskvisor\n"), 0o644))
	require.NoError(t, os.WriteFile(sibPath, []byte("package taskvisor\n"), 0o644))
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")

	// Sibling leaves an uncommitted in-scope edit BEFORE goal B starts.
	require.NoError(t, os.WriteFile(sibPath, []byte("package taskvisor\n\n// sibling edit\n"), 0o644))

	d := New(dir, new(testutil.MockTmuxExecutor))
	require.True(t, d.autoCommit, "New() must seed auto-commit ON")
	goalB := scopedGoal()

	// Capture goal B's start snapshot — freezes the sibling's dirty edit.
	d.captureGoalStartSnapshot(goalB)

	// Now goal B makes its OWN in-scope edit.
	require.NoError(t, os.WriteFile(xPath, []byte("package taskvisor\n\n// goal B edit\n"), 0o644))

	require.True(t, d.autoCommitGoal(goalB), "goal B's own changeset must commit")

	files := runGitCmd(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	assert.Contains(t, files, "internal/taskvisor/x.go", "goal's own file must be committed")
	assert.NotContains(t, files, "sibling.go", "sibling's pre-existing edit must NOT be captured")

	status := runGitCmd(t, dir, "status", "--porcelain")
	assert.Contains(t, status, "sibling.go", "sibling's edit must remain dirty/unstaged")
}

// TestAutoCommit_ExcludesPreexistingUntrackedSibling extends the isolation to
// untracked files: a sibling's untracked in-scope file present at goal start is
// recorded in the marker and must NOT be swept into goal B's commit, while a NEW
// untracked in-scope file the goal itself creates IS committed.
func TestAutoCommit_ExcludesPreexistingUntrackedSibling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.email", "taskvisor@test.local")
	runGitCmd(t, dir, "config", "user.name", "Taskvisor Test")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "taskvisor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "taskvisor", "keep.go"), []byte("package taskvisor\n"), 0o644))
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")

	// A sibling's untracked in-scope file exists BEFORE goal B starts.
	sibUntracked := filepath.Join(dir, "internal", "taskvisor", "sibling_new.go")
	require.NoError(t, os.WriteFile(sibUntracked, []byte("package taskvisor\n"), 0o644))

	d := New(dir, new(testutil.MockTmuxExecutor))
	goalB := scopedGoal()
	d.captureGoalStartSnapshot(goalB)

	// Goal B creates its OWN new untracked in-scope file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "taskvisor", "goalb_new.go"), []byte("package taskvisor\n"), 0o644))

	require.True(t, d.autoCommitGoal(goalB), "goal B's new file must commit")

	files := runGitCmd(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	assert.Contains(t, files, "internal/taskvisor/goalb_new.go", "goal's own new file must be committed")
	assert.NotContains(t, files, "sibling_new.go", "sibling's pre-existing untracked file must NOT be captured")

	status := runGitCmd(t, dir, "status", "--porcelain")
	assert.Contains(t, status, "sibling_new.go", "sibling's untracked file must remain untracked")
}

// TestAutoCommit_NoSnapshotMarkerPreservesLegacyStaging pins the fallback: with
// no marker present, the scope-matched tier stages today's scope pathspecs
// unchanged and never attempts a snapshot diff.
func TestAutoCommit_NoSnapshotMarkerPreservesLegacyStaging(t *testing.T) {
	fake := &fakeGitRunner{respond: dirtyScopeResponse}
	dir := t.TempDir()
	d := autoCommitDaemon(t, dir, fake)

	d.autoCommitGoal(scopedGoal())

	assert.Equal(t, 1, fake.count("-C", dir, "add", "--", ":(glob)internal/taskvisor/**"), "no marker ⇒ legacy scope pathspecs staged")
	assert.Equal(t, 0, fake.count("diff", "--name-only"), "no marker ⇒ no snapshot diff attempted")
	assert.Equal(t, 1, fake.count("commit"))
}

// TestAutoCommit_SnapshotEmptyDiffSkipsCommit: a marker is present but the goal
// changed nothing in scope since its snapshot ⇒ empty own-changeset ⇒ clean skip
// (no add, no commit, committed=false).
func TestAutoCommit_SnapshotEmptyDiffSkipsCommit(t *testing.T) {
	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-009")
	require.NoError(t, os.MkdirAll(markerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, "start-snapshot"), []byte("deadbeef\n"), 0o644))

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "status", "--porcelain") {
			return " M internal/taskvisor/x.go\n", 0 // legacy probe still sees dirt
		}
		return "", 0 // diff --name-only vs snapshot returns empty
	}}
	d := autoCommitDaemon(t, dir, fake)

	committed := d.autoCommitGoal(scopedGoal())

	assert.False(t, committed, "empty own-changeset ⇒ no commit")
	assert.Equal(t, 1, fake.count("diff", "--name-only"), "the snapshot diff was consulted")
	assert.Equal(t, 0, fake.count("add"), "nothing staged")
	assert.Equal(t, 0, fake.count("commit"), "no commit issued")
}

// TestAutoCommit_SnapshotDiffRetargetsStaging: a marker is present and the
// snapshot diff names the goal's own file, so add/commit re-target onto that
// path (NOT the scope glob pathspec).
func TestAutoCommit_SnapshotDiffRetargetsStaging(t *testing.T) {
	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-009")
	require.NoError(t, os.MkdirAll(markerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, "start-snapshot"), []byte("deadbeef\n"), 0o644))

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "diff", "--name-only"):
			return "internal/taskvisor/x.go\n", 0
		case argsContain(args, "status", "--porcelain"):
			return " M internal/taskvisor/x.go\n M internal/taskvisor/sibling.go\n", 0
		}
		return "", 0
	}}
	d := autoCommitDaemon(t, dir, fake)

	require.True(t, d.autoCommitGoal(scopedGoal()))

	assert.Equal(t, 1, fake.count("add", "--", "internal/taskvisor/x.go"), "staging re-targets onto the own-changeset path")
	assert.Equal(t, 0, fake.count("add", "--", ":(glob)internal/taskvisor/**"), "the scope glob is NOT used once a snapshot diff exists")
	assert.Equal(t, 1, fake.count("commit"))
}

// TestCaptureGoalStartSnapshot_WritesMarker: a non-empty `git stash create`
// output is recorded as the marker's first line.
func TestCaptureGoalStartSnapshot_WritesMarker(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "stash", "create") {
			return "cafebabe\n", 0
		}
		return "", 0 // status: no untracked
	}}
	d := autoCommitDaemon(t, dir, fake)

	d.captureGoalStartSnapshot(scopedGoal())

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-009", "start-snapshot"))
	require.NoError(t, err)
	first := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	assert.Equal(t, "cafebabe", first, "marker's first line is the stash-create SHA")
}

// TestCaptureGoalStartSnapshot_EmptyStashWritesEmptyMarker: a clean tree yields
// empty `git stash create` output ⇒ an empty marker ⇒ goalOwnInScopePaths falls
// back to legacy staging (ok=false).
func TestCaptureGoalStartSnapshot_EmptyStashWritesEmptyMarker(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{} // stash create returns "" (clean tree)
	d := autoCommitDaemon(t, dir, fake)

	d.captureGoalStartSnapshot(scopedGoal())

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-009", "start-snapshot"))
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(data)), "clean tree ⇒ empty marker")

	_, ok := d.goalOwnInScopePaths(scopedGoal(), scopePathspecs(scopedGoal().Scope))
	assert.False(t, ok, "empty marker ⇒ legacy staging fallback")
}

// TestCaptureGoalStartSnapshot_RecordsStartUntracked: the start-time untracked
// in-scope set is recorded on the marker's trailing lines so a pre-existing
// sibling untracked file can later be excluded.
func TestCaptureGoalStartSnapshot_RecordsStartUntracked(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "stash", "create") {
			return "cafebabe\n", 0
		}
		if argsContain(args, "status", "--porcelain") {
			return "?? internal/taskvisor/sibling_new.go\n", 0
		}
		return "", 0
	}}
	d := autoCommitDaemon(t, dir, fake)

	d.captureGoalStartSnapshot(scopedGoal())

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-009", "start-snapshot"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "internal/taskvisor/sibling_new.go", "start-time untracked in-scope path is recorded")
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

package taskvisor

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// baseRootMarker is a test-local fixture filename used by the existing
// TestEnsureWorktree_* tests to materialize a Go base (go.mod) in a worktree.
// The production const was deleted when baseCheckedOut became project-agnostic
// (it no longer keys on any single language marker); these fixtures keep
// writing go.mod purely to make the worktree dir non-empty → base-present.
const baseRootMarker = "go.mod"

// fakeGitRunner is a recording GitRunnerFunc: it captures every invocation's
// argv (so tests assert the exact git commands without a real repo) and returns
// canned (stdout, exitCode) per a responder. A sideEffect hook lets a test
// materialize the worktree dir on `worktree add` so the control-plane symlink
// path is exercised on the real (temp) filesystem.
type fakeGitRunner struct {
	mu         sync.Mutex
	calls      [][]string
	respond    func(args []string) (stdout string, code int)
	respondErr func(args []string) error // non-nil → simulate a runner TRANSPORT failure (err != nil)
	sideEffect func(args []string)
}

func (f *fakeGitRunner) run(_ context.Context, args ...string) (string, string, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if f.respondErr != nil {
		if err := f.respondErr(args); err != nil {
			return "", "", -1, err
		}
	}
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

// firstIndex returns the index of the first recorded call that contains seq as a
// contiguous subslice, or -1 if none. Used to assert call ordering (e.g. the seed
// commit precedes `worktree add`) without depending on absolute call indices.
func (f *fakeGitRunner) firstIndex(seq ...string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if argsContain(c, seq...) {
			return i
		}
	}
	return -1
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
			_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 1, fake.count("worktree", "add"), "worktree add must run exactly once")
	assert.True(t, fake.count("-B", worktreeBranch("goal-001")) == 1, "branch -B taskvisor/goal-001 (idempotent create-or-reset)")
	assert.Equal(t, wtPath, d.runtime("goal-001").WorktreeDir)
	assert.Equal(t, worktreeBranch("goal-001"), d.runtime("goal-001").Branch)
}

// TestEnsureWorktree_UnbornHeadSeedsInitialCommit: a freshly git-init'd base has
// an unborn HEAD, so `git worktree add … HEAD` fails (exit 128, "invalid
// reference: HEAD") and the goal poll-wedges to failed (backend task 317). The
// pre-add `rev-parse --verify -q HEAD` probe exits non-zero → ensureWorktree
// seeds an `--allow-empty` baseline commit (inline identity) BEFORE the add.
func TestEnsureWorktree_UnbornHeadSeedsInitialCommit(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			if argsContain(args, "rev-parse", "--verify") {
				return "", 1 // unborn HEAD
			}
			return "", 0
		},
		sideEffect: func(args []string) {
			if argsContain(args, "worktree", "add") {
				_ = os.MkdirAll(wtPath, 0o755)
				_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
			}
		},
	}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	commitIdx := fake.firstIndex("commit", "--allow-empty")
	addIdx := fake.firstIndex("worktree", "add")
	require.GreaterOrEqual(t, commitIdx, 0, "unborn HEAD must seed a commit --allow-empty")
	require.GreaterOrEqual(t, addIdx, 0, "worktree add must still run")
	assert.Less(t, commitIdx, addIdx, "seed commit must precede worktree add")
	// Inline identity so the seed never depends on the base repo's ambient git config.
	assert.Equal(t, 1, fake.count("user.email=taskvisor@local"), "seed commit carries inline identity")
}

// TestEnsureWorktree_BornHeadSkipsSeedCommit: a base with a born HEAD (rev-parse
// exits 0) takes the existing add path unchanged — NO seed commit is issued.
func TestEnsureWorktree_BornHeadSkipsSeedCommit(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			return "", 0 // rev-parse --verify HEAD exits 0 → born
		},
		sideEffect: func(args []string) {
			if argsContain(args, "worktree", "add") {
				_ = os.MkdirAll(wtPath, 0o755)
				_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
			}
		},
	}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, 0, fake.count("commit", "--allow-empty"), "born HEAD must NOT seed a commit")
	assert.Equal(t, 1, fake.count("worktree", "add"), "born HEAD takes the existing add path")
}

// TestEnsureWorktree_RevParseTransportErrorPropagates: a non-nil runner err from
// the HEAD probe (git binary missing, ctx timeout) is a real fault, NOT "unborn".
// It must propagate without seeding a commit or proceeding to the add.
func TestEnsureWorktree_RevParseTransportErrorPropagates(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}

	fake := &fakeGitRunner{
		respondErr: func(args []string) error {
			if argsContain(args, "rev-parse", "--verify") {
				return errors.New("git: command not found")
			}
			return nil
		},
	}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.Error(t, err, "a transport error from the HEAD probe must propagate")
	assert.Equal(t, 0, fake.count("commit", "--allow-empty"), "transport error is not 'unborn' — no seed commit")
	assert.Equal(t, 0, fake.count("worktree", "add"), "must not proceed to add after a probe transport error")
}

func TestEnsureWorktree_ReusedOnRetry(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")
	require.NoError(t, os.MkdirAll(wtPath, 0o755))                                                       // worktree already exists (prior cycle)
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)) // base checked out

	// respond reports wtPath as a registered worktree so the reuse fast-path is taken.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "worktree", "list", "--porcelain") {
			return "worktree " + wtPath + "\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 0, fake.count("worktree", "add"), "existing worktree must be reused, not re-added")
	assert.Equal(t, wtPath, d.runtime("goal-001").WorktreeDir)
}

// TestEnsureWorktree_PreExistingBranch: the goal branch taskvisor/<goal> already
// exists (a leftover from a prior broken run, or a branch that survived a reuse-
// check teardown). The OLD `worktree add -b` would hard-fail "fatal: a branch
// named ... already exists" and wedge the goal forever. The `-B` (create-or-
// force-reset) flag reconciles the surviving branch in one idempotent call.
func TestEnsureWorktree_PreExistingBranch(t *testing.T) {
	// preExistingBranchRunner fails any OLD `worktree add -b` (proving it is no
	// longer used) and materializes the worktree on the NEW `-B` add.
	makeFake := func(wtPath string) *fakeGitRunner {
		return &fakeGitRunner{
			respond: func(args []string) (string, int) {
				if argsContain(args, "worktree", "add", "-b") {
					return "fatal: a branch named 'taskvisor/goal-001' already exists", 128
				}
				return "", 0
			},
			sideEffect: func(args []string) {
				if argsContain(args, "worktree", "add", "-B") {
					_ = os.MkdirAll(wtPath, 0o755)
					_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
				}
			},
		}
	}

	t.Run("provision_path_branch_exists", func(t *testing.T) {
		d, _, dir := setupDaemon(t)
		mkGitRepo(t, dir)
		goal := &Goal{ID: "goal-001"}
		wtPath := d.worktreePath("goal-001") // no dir at wtPath

		fake := makeFake(wtPath)
		d.SetGitRunnerFunc(fake.run)

		cwd, err := d.ensureWorktree(goal, true)
		require.NoError(t, err, "pre-existing branch must not collide under -B")

		assert.Equal(t, wtPath, cwd)
		assert.Equal(t, 1, fake.count("worktree", "add"), "worktree add must run exactly once")
		assert.Equal(t, 1, fake.count("-B", worktreeBranch("goal-001")), "must provision with -B (create-or-reset)")
		assert.Equal(t, 0, fake.count("-b", worktreeBranch("goal-001")), "must NOT use the colliding -b")
	})

	t.Run("teardown_then_reprovision_branch_survives", func(t *testing.T) {
		d, _, dir := setupDaemon(t)
		mkGitRepo(t, dir)
		goal := &Goal{ID: "goal-001"}
		wtPath := d.worktreePath("goal-001")
		require.NoError(t, os.MkdirAll(wtPath, 0o755)) // stray UNREGISTERED dir

		fake := makeFake(wtPath)
		// Unregistered: porcelain list returns "" so the reuse fast-path is skipped
		// and the stray dir is torn down before the -B re-provision.
		baseRespond := fake.respond
		fake.respond = func(args []string) (string, int) {
			if argsContain(args, "worktree", "list", "--porcelain") {
				return "", 0
			}
			return baseRespond(args)
		}
		d.SetGitRunnerFunc(fake.run)

		cwd, err := d.ensureWorktree(goal, true)
		require.NoError(t, err, "surviving branch must reconcile via -B after teardown")

		assert.Equal(t, wtPath, cwd)
		assert.Equal(t, 1, fake.count("worktree", "add"), "re-provision via worktree add exactly once")
		assert.GreaterOrEqual(t, fake.count("worktree", "prune"), 1, "teardown must prune half-registrations")
		assert.Equal(t, 1, fake.count("-B", worktreeBranch("goal-001")), "re-provision uses -B")
		assert.True(t, baseCheckedOut(wtPath), "base present after re-provision")
	})
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
			_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
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

func TestEnsureWorktree_CopiesClaudeCommands(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	// Base has the installed command set; .claude is git-excluded so `worktree add`
	// would NOT carry it into the checkout — the daemon must copy it in.
	baseTmux := filepath.Join(dir, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(filepath.Join(baseTmux, "worker"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "supervisor.xml"), []byte("<sup/>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "worker", "investigate-worker.xml"), []byte("<wk/>"), 0o644))

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
			_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	_, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	// The supervisor command (and nested subdir commands) must resolve in the worktree cwd.
	got, err := os.ReadFile(filepath.Join(wtPath, ".claude", "commands", "tmux", "supervisor.xml"))
	require.NoError(t, err, "/tmux:supervisor must be present in the worktree")
	assert.Equal(t, "<sup/>", string(got))
	nested, err := os.ReadFile(filepath.Join(wtPath, ".claude", "commands", "tmux", "worker", "investigate-worker.xml"))
	require.NoError(t, err, "nested subdir commands must be copied too")
	assert.Equal(t, "<wk/>", string(nested))
}

func TestEnsureWorktree_ReuseSelfHealsMissingCommands_PreservesEdits(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	// Base command set.
	baseTmux := filepath.Join(dir, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(baseTmux, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "supervisor.xml"), []byte("<sup-base/>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "task-list.xml"), []byte("<base-tasklist/>"), 0o644))

	// A worktree from before the fix: it exists, lacks supervisor.xml, but already
	// holds a goal-edited task-list.xml mirror that must NOT be clobbered.
	wtTmux := filepath.Join(wtPath, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(wtTmux, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wtTmux, "task-list.xml"), []byte("<goal-EDITED/>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)) // base checked out

	// respond reports wtPath registered so the reuse self-heal path is taken.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "worktree", "list", "--porcelain") {
			return "worktree " + wtPath + "\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)
	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 0, fake.count("worktree", "add"), "existing worktree reused, not re-added")

	// Missing command filled in...
	got, err := os.ReadFile(filepath.Join(wtTmux, "supervisor.xml"))
	require.NoError(t, err, "missing supervisor.xml must be filled on reuse")
	assert.Equal(t, "<sup-base/>", string(got))
	// ...but the goal's edited mirror preserved.
	edited, err := os.ReadFile(filepath.Join(wtTmux, "task-list.xml"))
	require.NoError(t, err)
	assert.Equal(t, "<goal-EDITED/>", string(edited), "reuse must NOT overwrite a goal-edited command file")
}

// --- ensureWorktree stub re-provisioning / fail-loud ----------------------

// TestEnsureWorktree_StubReprovisionsStrayDir: a dir exists at wtPath but is NOT
// a registered worktree (empty porcelain list). It must be torn down (guarded
// remove + RemoveAll + prune) and re-provisioned via `git worktree add`.
func TestEnsureWorktree_StubReprovisionsStrayDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")
	require.NoError(t, os.MkdirAll(wtPath, 0o755)) // stray dir, NOT a registered worktree

	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			if argsContain(args, "worktree", "list", "--porcelain") {
				return "", 0 // empty list ⇒ wtPath not registered
			}
			return "", 0
		},
		sideEffect: func(args []string) {
			if argsContain(args, "worktree", "add") {
				_ = os.MkdirAll(wtPath, 0o755)
				_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
			}
		},
	}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 1, fake.count("worktree", "add"), "stray dir must be re-provisioned via worktree add")
	assert.GreaterOrEqual(t, fake.count("worktree", "prune"), 1, "teardown must prune half-registrations")
	assert.Equal(t, wtPath, d.runtime("goal-001").WorktreeDir)
}

// TestEnsureWorktree_StubBaselessRegisteredReprovisions: a dir IS reported
// registered by the porcelain list but has NO base (go.mod) — a base-less stub.
// It must be torn down and re-provisioned.
func TestEnsureWorktree_StubBaselessRegisteredReprovisions(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")
	require.NoError(t, os.MkdirAll(wtPath, 0o755)) // registered but no go.mod

	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			if argsContain(args, "worktree", "list", "--porcelain") {
				return "worktree " + wtPath + "\n", 0 // registered...
			}
			return "", 0
		},
		sideEffect: func(args []string) {
			if argsContain(args, "worktree", "add") {
				_ = os.MkdirAll(wtPath, 0o755)
				_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
			}
		},
	}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 1, fake.count("worktree", "add"), "base-less registered worktree must be re-provisioned")
	assert.True(t, baseCheckedOut(wtPath), "base present after re-provision")
}

// TestEnsureWorktree_StubFailsLoudWhenBaseAbsentAfterAdd: `git worktree add`
// succeeds (creates the dir) but the base never lands (no go.mod). ensureWorktree
// must fail loud with an explicit provisioning error and return an empty cwd, so
// the goal never runs in a base-less stub.
func TestEnsureWorktree_StubFailsLoudWhenBaseAbsentAfterAdd(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755) // dir created but NO go.mod ⇒ base-less
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "worktree provisioning failed for goal-001")
	assert.Equal(t, "", cwd, "no base-less stub cwd is ever returned")
}

// TestEnsureWorktree_StubReuseValidNoReprovision: a dir that IS registered AND
// has the base checked out is reused as-is — zero `worktree add`, zero teardown.
func TestEnsureWorktree_StubReuseValidNoReprovision(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")
	require.NoError(t, os.MkdirAll(wtPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644))

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "worktree", "list", "--porcelain") {
			return "worktree " + wtPath + "\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd)
	assert.Equal(t, 0, fake.count("worktree", "add"), "valid worktree reused, not re-added")
	assert.Equal(t, 0, fake.count("worktree", "prune"), "no teardown on a valid reuse")
	assert.Equal(t, 0, fake.count("worktree", "remove", "--force"), "no teardown on a valid reuse")
}

// TestEnsureWorktree_NonGoBase_ComposerJSON: a `git worktree add` whose checkout
// materializes a NON-Go base (composer.json + a contexts/ dir, no go.mod) must be
// accepted — the project-agnostic baseCheckedOut treats any non-control-plane
// root entry as base content, so a PHP/Symfony worktree dispatches instead of
// STUCK-looping (regression of task 262's go.mod-only probe).
func TestEnsureWorktree_NonGoBase_ComposerJSON(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	goal := &Goal{ID: "goal-001"}
	wtPath := d.worktreePath("goal-001")

	fake := &fakeGitRunner{sideEffect: func(args []string) {
		if argsContain(args, "worktree", "add") {
			_ = os.MkdirAll(wtPath, 0o755)
			// Non-Go base: composer.json + a contexts/ dir, deliberately NO go.mod.
			_ = os.WriteFile(filepath.Join(wtPath, "composer.json"), []byte("{}\n"), 0o644)
			_ = os.MkdirAll(filepath.Join(wtPath, "contexts"), 0o755)
		}
	}}
	d.SetGitRunnerFunc(fake.run)

	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)

	assert.Equal(t, wtPath, cwd, "a correctly provisioned non-Go worktree is returned, not re-provisioned")
	assert.True(t, baseCheckedOut(wtPath), "composer.json base reads as checked out (no go.mod required)")
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

	_, err := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, err)

	assert.Equal(t, 0, fake.count("commit", "-m"), "no commit on empty diff")
	assert.Equal(t, 0, fake.count("rebase"), "no rebase on empty diff")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"), "no merge on empty diff")

	// And the worktree is still removed on completion.
	require.NoError(t, d.discardWorktree(&Goal{ID: "goal-001"}))
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"))
}

// conflictResponder builds a fakeGitRunner responder for a merge-back that
// rebase-conflicts: dirty status, 1 commit ahead, base "main", the initial
// `rebase main` exits 1, `diff --diff-filter=U` reports unmergedPath, and
// `rebase --continue` exits 0 (a successful out-of-scope auto-resolve). The
// post-merge `merge-base --is-ancestor` assertion falls through to the default
// exit 0 (base contains the branch tip).
func conflictResponder(unmergedPath string) func(args []string) (string, int) {
	return func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "M internal/a.go\n", 0
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "rebase", "--continue"):
			return "", 0 // out-of-scope resolved ⇒ rebase completes
		case argsContain(args, "rebase", "main"):
			return "CONFLICT", 1 // peer advanced base ⇒ conflict
		case argsContain(args, "diff", "--name-only", "--diff-filter=U"):
			return unmergedPath + "\n", 0
		}
		return "", 0
	}
}

// TestMergeWorktreeBack_ConflictOutOfScopeAutoResolves: a conflict only on a path
// OUTSIDE goal.Scope (peer compose churn) is resolved in base's favor and the
// integration still lands — the goal stays Done and the worktree is removed.
func TestMergeWorktreeBack_ConflictOutOfScopeAutoResolves(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: conflictResponder("docker-compose.yaml")}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone, Scope: []string{"internal/taskvisor/**"}}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed, "an out-of-scope conflict auto-resolves — the goal does not fail")

	assert.Equal(t, 1, fake.count("checkout", "--ours", "--", "docker-compose.yaml"), "out-of-scope path resolved in base's favor")
	assert.Equal(t, 1, fake.count("rebase", "--continue"), "rebase continued after auto-resolve")
	assert.Equal(t, 1, fake.count("merge", "--ff-only"), "integration lands via ff-merge")
	assert.Equal(t, 0, fake.count("rebase", "--abort"), "no abort — the conflict was auto-resolved")
	assert.Equal(t, GoalDone, gf.Goals[0].Status, "goal stays Done after a clean auto-resolved integration")
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"), "worktree removed after merge")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir, "runtime worktree cleared")
}

// TestMergeWorktreeBack_ConflictInScopeBlocks: a conflict on a path INSIDE
// goal.Scope touches the goal's own deliverable — never auto-resolve. The rebase
// aborts, base is untouched, and the goal is BLOCKED (needs-merge), not Done, with
// the worktree/branch preserved.
func TestMergeWorktreeBack_ConflictInScopeBlocks(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: conflictResponder("internal/taskvisor/worktree.go")}
	d.SetGitRunnerFunc(fake.run)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor"}}, nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone, Scope: []string{"internal/taskvisor/**"}}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed, "an in-scope conflict blocks integration — failed=true suppresses resumeDownstream")

	assert.Equal(t, 1, fake.count("rebase", "--abort"), "in-scope conflict aborts the rebase (no partial state)")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"), "base must NOT be merged into on an in-scope conflict")
	assert.Equal(t, GoalBlocked, gf.Goals[0].Status, "in-scope conflict ⇒ GoalBlocked, never Done")
	assert.Equal(t, "needs-merge", gf.Goals[0].BlockedBy, "BlockedBy records the needs-merge reason")
	// Worktree + branch are PRESERVED so the un-integrated commit survives.
	assert.Equal(t, 0, fake.count("worktree", "remove", "--force"), "worktree preserved for manual merge")
	assert.Equal(t, 0, fake.count("branch", "-D"), "branch preserved for manual merge")

	markerPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "needs-merge.md")
	marker, err := os.ReadFile(markerPath)
	require.NoError(t, err, "needs-merge.md marker must be written")
	assert.Contains(t, string(marker), "internal/taskvisor/worktree.go", "conflicting path recorded in marker")
}

// TestMergeWorktreeBack_ConflictBlocksNeedsMerge: an in-scope conflict BLOCKS
// without any failure surface — no VerdictFail signal, no [TASKVISOR:GOAL-FAILED
// notify, reportFailedGoals files nothing (the goal is Blocked, not Failed), and a
// dependent does not dispatch (parent not Done ⇒ resume suppressed).
func TestMergeWorktreeBack_ConflictBlocksNeedsMerge(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: conflictResponder("internal/taskvisor/worktree.go")}
	d.SetGitRunnerFunc(fake.run)

	var notifies []string
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{{TmuxWindowID: "@0", Name: "supervisor"}}, nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		notifies = append(notifies, args.String(2))
	}).Maybe()

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone, Scope: []string{"internal/taskvisor/**"}},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed)

	// No VerdictFail signal — a merge-back conflict is not a goal failure.
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	if valSig, ok := sig.(*ValidatorSignal); ok {
		assert.NotEqual(t, VerdictFail, valSig.Verdict, "no VerdictFail signal on merge-back conflict")
	}
	// No GOAL-FAILED notify (preserves the anti-false-critical intent).
	for _, msg := range notifies {
		assert.NotContains(t, msg, "[TASKVISOR:GOAL-FAILED", "must NOT send a GOAL-FAILED notification on a merge-back conflict")
	}

	// reportFailedGoals files nothing — the goal is Blocked, not Failed.
	d.reportFailedGoals(gf)
	d.reportedFailuresMu.Lock()
	reported := d.reportedFailures["goal-001"]
	d.reportedFailuresMu.Unlock()
	assert.False(t, reported, "a Blocked (not Failed) goal is never reported as failed")

	// The dependent does not dispatch: the parent is not Done (resume suppressed).
	assert.NotEqual(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, GoalPending, gf.Goals[1].Status, "soft cascade leaves the dependent pending (not failed)")
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
			_ = os.WriteFile(filepath.Join(wtPath, baseRootMarker), []byte("module x\n"), 0o644)
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

// --- copyClaudeCommands: embedded-template regeneration -------------------
//
// These four tests pin the fix for backend task 325: a goal worktree's
// git-excluded .claude/commands/tmux mirror must be regenerated from the daemon
// binary's compiled-in command templates (injected via SetCommandTemplates), not
// copied from a possibly-stale base on-disk mirror — so the dual-write tests that
// assert embedded==mirror never false-fail during a daemon-run `make test`.

// TestCopyClaudeCommands_CreatePathWritesFromEmbedded: on the create path
// (overwrite=true) with templates injected, the worktree mirror is written from the
// TEMPLATES (byte-identical to embedded), never from the STALE base disk mirror.
func TestCopyClaudeCommands_CreatePathWritesFromEmbedded(t *testing.T) {
	d, _, dir := setupDaemon(t)
	wtPath := filepath.Join(dir, "wt")
	require.NoError(t, os.MkdirAll(wtPath, 0o755))

	// Base disk mirror holds STALE content — must be ignored in favor of templates.
	baseTmux := filepath.Join(dir, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(baseTmux, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "e2e-evaluator.xml"), []byte("STALE"), 0o644))

	d.commandTemplates = map[string]string{"e2e-evaluator.xml": "NEW"}

	require.NoError(t, d.copyClaudeCommands(wtPath, true))

	got, err := os.ReadFile(filepath.Join(wtPath, ".claude", "commands", "tmux", "e2e-evaluator.xml"))
	require.NoError(t, err)
	assert.Equal(t, "NEW", string(got), "create path must write from embedded templates, not stale base disk")
}

// TestCopyClaudeCommands_ReusePreservesEditedFile: on the reuse path
// (overwrite=false) an existing goal-edited mirror file is preserved untouched while
// a template file MISSING from the worktree is filled from templates.
func TestCopyClaudeCommands_ReusePreservesEditedFile(t *testing.T) {
	d, _, dir := setupDaemon(t)
	wtPath := filepath.Join(dir, "wt")
	wtTmux := filepath.Join(wtPath, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(wtTmux, 0o755))
	// An in-flight goal-edited mirror file.
	require.NoError(t, os.WriteFile(filepath.Join(wtTmux, "e2e-evaluator.xml"), []byte("EDITED"), 0o644))

	// Templates provide a DIFFERENT value for the edited file (must NOT clobber) plus
	// a second file MISSING from the worktree (must be filled).
	d.commandTemplates = map[string]string{
		"e2e-evaluator.xml": "FROM-TEMPLATE",
		"supervisor.xml":    "SUP",
	}

	require.NoError(t, d.copyClaudeCommands(wtPath, false))

	edited, err := os.ReadFile(filepath.Join(wtTmux, "e2e-evaluator.xml"))
	require.NoError(t, err)
	assert.Equal(t, "EDITED", string(edited), "reuse must preserve a goal-edited command file untouched")

	filled, err := os.ReadFile(filepath.Join(wtTmux, "supervisor.xml"))
	require.NoError(t, err, "a missing template file must be filled on reuse")
	assert.Equal(t, "SUP", string(filled))
}

// TestCopyClaudeCommands_NilTemplatesFallsBackToDisk: with no templates injected,
// the worktree mirror is copied from the base disk exactly as before — never empty
// or removed.
func TestCopyClaudeCommands_NilTemplatesFallsBackToDisk(t *testing.T) {
	d, _, dir := setupDaemon(t)
	require.Nil(t, d.commandTemplates)
	wtPath := filepath.Join(dir, "wt")
	require.NoError(t, os.MkdirAll(wtPath, 0o755))

	// Populated base disk command set.
	baseTmux := filepath.Join(dir, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(baseTmux, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseTmux, "supervisor.xml"), []byte("<sup-base/>"), 0o644))

	require.NoError(t, d.copyClaudeCommands(wtPath, true))

	got, err := os.ReadFile(filepath.Join(wtPath, ".claude", "commands", "tmux", "supervisor.xml"))
	require.NoError(t, err, "nil templates must fall back to base disk-copy, not empty the mirror")
	assert.Equal(t, "<sup-base/>", string(got))
}

// TestCopyClaudeCommands_NilTemplatesNoBaseIsNoop: nil templates and no base command
// dir ⇒ silent no-op (returns nil, writes nothing).
func TestCopyClaudeCommands_NilTemplatesNoBaseIsNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	require.Nil(t, d.commandTemplates)
	wtPath := filepath.Join(dir, "wt")
	require.NoError(t, os.MkdirAll(wtPath, 0o755))

	require.NoError(t, d.copyClaudeCommands(wtPath, true), "no base command set is a silent no-op")

	_, err := os.Stat(filepath.Join(wtPath, ".claude"))
	assert.True(t, os.IsNotExist(err), "no-op must write nothing (no .claude created)")
}

package taskvisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// integration_gate_test.go — E1 P4: post-merge integration gate. These tests pin
// runIntegrationGate / mergeWorktreeBack's in-lock integration call and
// finalizeWorktreeOnDone's integration-failure branch. They live in a dedicated
// file so the new fmt/syscall imports stay isolated from worktree_test.go's import
// block; all shared helpers (setupDaemon, primeWorktree, fakeGitRunner, mkGitRepo,
// writeGoals, cleanMerge responder shape) come from the package's existing tests.

// writeSettingsIntegrationCmd writes a minimal setting.yaml carrying the P4
// integration_cmd (and max_goals=2 for shape), so d.integrationCmd() resolves it
// via setup.LoadSettings(d.workDir).
func writeSettingsIntegrationCmd(t *testing.T, dir, cmd string) {
	t.Helper()
	content := fmt.Sprintf(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
  max_workers: 4
  cycle_delay: 5
  max_goals: 2
plan:
  auto_approve: true
  auto_execute: true
sudo:
  timeout: 30
taskvisor:
  dispatch_timeout: 3600
  validate_timeout: 300
  poll_interval: 0
  integration_cmd: %q
`, cmd)
	p := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// cleanMergeResponse is the fakeGitRunner responder shape for a happy
// stage→commit→rebase→ff merge: dirty status, 1 commit ahead, base branch "main".
// rebase/merge fall through to the runner's default exit 0.
func cleanMergeResponse(args []string) (string, int) {
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

// mergeLockHeld reports whether the worktree-merge flock is currently held. It
// opens a SECOND fd on the lock file and attempts a non-blocking LOCK_EX: flock
// locks are per-open-file-description, so even within this process a second fd is
// denied (EWOULDBLOCK) while WithMergeLock holds the lock — proving in-lock
// execution. When it CAN acquire, it releases immediately and reports false.
func mergeLockHeld(t *testing.T, dir string) bool {
	t.Helper()
	lockPath := filepath.Join(dir, ".tmux-cli", "worktree-merge.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true // denied ⇒ held by the merge critical section
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func envContains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// --- mergeWorktreeBack integration call -----------------------------------

func TestMergeWorktreeBack_RunsIntegrationCmd_AfterMerge(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)

	var (
		called  int
		gotDir  string
		gotEnv  []string
		gotPath string
	)
	d.SetScriptRunnerFunc(func(_ context.Context, sp, wd string, env []string) (string, string, int, error) {
		called++
		gotPath, gotDir, gotEnv = sp, wd, env
		return "", "", 0, nil
	})

	_, mwbErr := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, mwbErr)

	assert.Equal(t, 1, called, "integration command runs exactly once after a real FF merge")
	assert.Equal(t, dir, gotDir, "integration command must run against the merged base (d.workDir)")
	assert.True(t, strings.HasSuffix(gotPath, ".sh"), "a temp shell script path is passed to the runner: %s", gotPath)
	assert.True(t, envContains(gotEnv, "GOAL_ID=goal-001"), "GOAL_ID must be exported to the integration script")
	assert.Equal(t, 1, fake.count("merge", "--ff-only"), "FF merge still happens before the gate")
}

func TestMergeWorktreeBack_IntegrationNonZero_ReturnsErrIntegrationFailed(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "tests failed\n", 1, nil
	})

	_, err := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.Error(t, err)

	var ifail errIntegrationFailed
	require.True(t, errors.As(err, &ifail), "a red suite must surface as errIntegrationFailed, got %T", err)
	assert.Equal(t, 1, ifail.exit)
	assert.Contains(t, ifail.stderr, "tests failed")
}

func TestMergeWorktreeBack_NoIntegrationCmd_RunnerNotCalled(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir) // no setting.yaml ⇒ integration_cmd defaults to ""
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	called := false
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	})

	_, mwbErr := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, mwbErr)

	assert.False(t, called, "no integration command ⇒ runner never invoked")
	assert.Equal(t, 1, fake.count("merge", "--ff-only"), "base still fast-forwards (byte-identical to today)")
}

func TestMergeWorktreeBack_NoRealMerge_SkipsIntegration(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	// ahead==0 ⇒ early return before the FF/integration block.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "", 0
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)
	called := false
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	})

	_, mwbErr := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, mwbErr)

	assert.False(t, called, "no commits ahead ⇒ no merge ⇒ integration skipped")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"))
}

func TestMergeWorktreeBack_MaxGoals1_SkipsIntegration(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	// No primeWorktree ⇒ WorktreeDir=="" ⇒ the no-worktree guard returns before the lock.

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	called := false
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	})

	_, mwbErr := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, mwbErr)

	assert.False(t, called, "no worktree (MaxGoals=1) ⇒ integration never runs")
	assert.Equal(t, 0, len(fake.calls), "no worktree ⇒ zero git, never reaching the lock")
}

func TestMergeWorktreeBack_IntegrationRunsBeforeLockReleased(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)

	var heldDuringRun bool
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		heldDuringRun = mergeLockHeld(t, dir)
		return "", "", 0, nil
	})

	_, mwbErr := d.mergeWorktreeBack(&Goal{ID: "goal-001"})
	require.NoError(t, mwbErr)
	assert.True(t, heldDuringRun, "integration must run while WithMergeLock is still held (in-lock invariant)")
}

// --- finalizeWorktreeOnDone integration-failure branch --------------------

func TestFinalizeWorktreeOnDone_IntegrationFailed_FlipsFailedAndCascades(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "boom\n", 1, nil
	})

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed, "integration failure must fail the goal")

	assert.Equal(t, GoalFailed, gf.Goals[0].Status)
	assert.NotEmpty(t, gf.Goals[0].FinishedAt, "FinishedAt stamped on the failed goal")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent cascade-blocked")
	assert.Equal(t, "goal-001", gf.Goals[1].BlockedBy)
}

func TestFinalizeWorktreeOnDone_IntegrationFailed_WritesFailSignal(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "suite red\n", 2, nil
	})

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	require.True(t, failed)

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	valSig, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "an integration failure must write a validator signal")
	assert.Equal(t, VerdictFail, valSig.Verdict)
	assert.Equal(t, "human", valSig.Owner)
	assert.Contains(t, valSig.NextAction, "integration command failed")
}

func TestFinalizeWorktreeOnDone_IntegrationFailed_DiscardsWorktree(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "", 1, nil
	})

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	require.True(t, failed)

	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"), "failed goal's worktree is discarded")
	assert.Empty(t, d.runtime("goal-001").WorktreeDir, "runtime worktree cleared after discard")
}

// TestFinalizeWorktreeOnDone_MergeConflict_StillHandled is a regression: adding the
// integration branch must NOT alter the pre-existing errMergeConflict handling.
func TestFinalizeWorktreeOnDone_MergeConflict_StillHandled(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	// integration_cmd set, but the rebase conflicts BEFORE the FF/integration block.
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	integrationCalled := false
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "status", "--porcelain"):
			return "M internal/shared.go\n", 0
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "rebase", "main"):
			return "CONFLICT", 1
		case argsContain(args, "diff", "--name-only", "--diff-filter=U"):
			return "internal/shared.go\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		integrationCalled = true
		return "", "", 0, nil
	})

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed, "a post-validation merge-back conflict does not fail a validated goal")
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, 1, fake.count("rebase", "--abort"), "conflict path still aborts cleanly")
	assert.Equal(t, 0, fake.count("merge", "--ff-only"), "no FF on conflict")
	assert.False(t, integrationCalled, "integration never runs when the rebase conflicts (no FF)")

	// No VerdictFail signal is written on the merge-back conflict path.
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	if valSig, ok := sig.(*ValidatorSignal); ok {
		assert.NotEqual(t, VerdictFail, valSig.Verdict, "no VerdictFail signal on merge-back conflict")
	}
}

// --- advanceToNextGoal resume suppression ---------------------------------

// TestAdvanceToNextGoal_IntegrationFailed_SuppressesResume proves a failed
// integration gate (finalize → failed=true) suppresses the downstream resume: the
// dependent stays cascade-blocked and is not re-pended. An independent running
// sibling keeps the daemon active so no deactivation/tmux path is exercised.
func TestAdvanceToNextGoal_IntegrationFailed_SuppressesResume(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	writeSettingsIntegrationCmd(t, dir, "make test")
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: cleanMergeResponse}
	d.SetGitRunnerFunc(fake.run)
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "red\n", 1, nil
	})

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
			{ID: "goal-003", Status: GoalRunning},
		},
	}
	writeGoals(t, dir, gf)

	require.NoError(t, d.advanceToNextGoal(gf, "goal-001", true))

	// gf is mutated in place by advanceToNextGoal → finalizeWorktreeOnDone.
	g1, _ := gf.GoalByID("goal-001")
	g2, _ := gf.GoalByID("goal-002")
	assert.Equal(t, GoalFailed, g1.Status, "goal-001 flipped to failed by the integration gate")
	assert.Equal(t, GoalBlocked, g2.Status, "downstream stays blocked — resume was suppressed, not re-pended")
	assert.Equal(t, "goal-001", g2.BlockedBy)
}

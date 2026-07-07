package taskvisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// gate_bounce_noncode_test.go — goal-001 / backend task 514: a correct, gate-green
// goal must not burn its code-defect retry budget on infra/ordering flakiness.
// Three localized fixes:
//   Part 1 — inScopeDiffEmpty's worktree branch inspects the working tree (not only
//     commitsAhead) so an uncommitted-but-present in-scope diff never reads as empty.
//   Part 2 — the two supervisor-crash detections route to handleStuckSupervisor
//     (charge StuckRetries, no correction, re-dispatch), not handleFailedCycle.
//   Part 3 — a finding-less failing cycle names its failing command in the correction.

// worktreeGoal is a supervising-phase goal wired for worktree mode with the given
// declared scope, full code budget, and lane=full (so demoteSoloLane never needs
// goal.md).
func worktreeGoal(scope ...string) Goal {
	g := routeGoal("goal-001", 3, 3, 3, 3)
	g.Description = "gate bounce goal"
	g.Lane = LaneFull
	g.Scope = scope
	return g
}

// --- Part 1: inScopeDiffEmpty worktree working-tree probe --------------------

// TestInScopeDiffEmpty_WorktreeUncommittedInScopeEdits_NotEmpty — commitsAhead==0
// but the worktree working tree has uncommitted in-scope edits: the diff is NOT
// empty (the gate must not fire), so a worker that reported DONE with real edits is
// never scored a "no committed in-scope changes" code defect.
func TestInScopeDiffEmpty_WorktreeUncommittedInScopeEdits_NotEmpty(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0 // nothing committed ahead of base
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "status", "--porcelain"):
			return " M internal/taskvisor/statemachine.go\n", 0 // uncommitted in-scope edit
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	goal := worktreeGoal("internal/taskvisor/**")
	assert.False(t, d.inScopeDiffEmpty(&goal), "uncommitted in-scope worktree edits are NOT an empty diff")
	assert.GreaterOrEqual(t, fake.count("status", "--porcelain"), 1, "the worktree working tree is probed when commitsAhead==0")
}

// TestInScopeDiffEmpty_WorktreeTrulyEmpty_Empty — commitsAhead==0 AND the working
// tree is clean in scope: the diff is genuinely empty, so the gate fires (true).
func TestInScopeDiffEmpty_WorktreeTrulyEmpty_Empty(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "status", "--porcelain"):
			return "", 0 // clean working tree in scope
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	goal := worktreeGoal("internal/taskvisor/**")
	assert.True(t, d.inScopeDiffEmpty(&goal), "commitsAhead==0 AND clean working tree ⇒ truly empty")
}

// TestInScopeDiffEmpty_WorktreePorcelainError_FailsOpen — commitsAhead==0 but the
// working-tree probe errors: fail OPEN (false) so a transient git failure never
// gates a real validation.
func TestInScopeDiffEmpty_WorktreePorcelainError_FailsOpen(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "status", "--porcelain"):
			return "fatal: bad revision", 128 // probe error ⇒ fail open
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	goal := worktreeGoal("internal/taskvisor/**")
	assert.False(t, d.inScopeDiffEmpty(&goal), "a working-tree probe error must fail open (not gate)")
}

// TestInScopeDiffEmpty_WorktreeCommitsAhead_NotEmpty — commitsAhead>0: the diff is
// non-empty and the working tree is never probed (short-circuit preserved).
func TestInScopeDiffEmpty_WorktreeCommitsAhead_NotEmpty(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0 // committed ahead ⇒ non-empty
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	goal := worktreeGoal("internal/taskvisor/**")
	assert.False(t, d.inScopeDiffEmpty(&goal), "commitsAhead>0 ⇒ non-empty")
	assert.Equal(t, 0, fake.count("status", "--porcelain"), "the working tree is NOT probed when commitsAhead>0")
}

// --- Part 2: supervisor crash → handleStuckSupervisor -----------------------

// TestSupervisorCrash_WindowVanished_ChargesStuckNotCode — sig==nil, bootConfirmed
// past +5s, and the supervisor window is gone: the transient crash is recovered via
// handleStuckSupervisor (StuckRetries charged, CodeRetries untouched, no correction
// written, re-dispatched), NOT a code-defect re-pend.
func TestSupervisorCrash_WindowVanished_ChargesStuckNotCode(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))
	clk := &fakeClock{t: time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.dispatchTimeout = time.Hour // hard timeout must NOT fire

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 2, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")

	rt := d.runtime("goal-001")
	rt.phase = phaseSupervising
	rt.dispatchTime = clk.t
	rt.bootConfirmedAt = clk.t // arm the crash-detect path
	clk.advance(10 * time.Second)

	empty := []tmux.WindowInfo{}
	booted := []tmux.WindowInfo{{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"}}
	// crash-detect find (empty ⇒ vanished) + handleStuckSupervisor 2 kills +
	// dispatchRetry's 5 kill lookups + collectManagedNames + waitWindowsGone = 10 empty
	exec.On("ListWindows", testSession).Return(empty, nil).Times(10)
	exec.On("ListWindows", testSession).Return(booted, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯", nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil).Maybe()
	var sent []string
	exec.On("SendMessage", testSession, "@1", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		sent = append(sent, args.String(2))
	})

	out := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	g := &gf.Goals[0]
	assert.Equal(t, GoalRunning, g.Status, "vanished supervisor is re-dispatched, not re-pended")
	assert.Equal(t, 2, g.StuckRetries, "stuck budget charged 3->2")
	assert.Equal(t, 2, g.CodeRetries, "code budget untouched (not a code defect)")
	assert.Contains(t, out, "supervisor window vanished — transient crash", "vanished routes to the stuck path")
	require.Len(t, sent, 1)
	assert.Equal(t, "/tmux:supervisor goal-001", sent[0], "re-dispatch via dispatchRetry reuse path")

	_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	assert.True(t, os.IsNotExist(statErr), "no correction file written for a transient supervisor crash")
}

// --- Part 3: finding-less corrections name the failing command --------------

// TestWriteCorrectionFile_NamesFailingCommand — a finding-less signal with a
// non-empty failingCommand writes `Command: <failingCommand>` and NOT the
// "(not reported)" / "did not report the failing command" placeholder.
func TestWriteCorrectionFile_NamesFailingCommand(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	sig := &ValidatorSignal{NextAction: "Implementation completed but failed acceptance criteria.\n\nassertion failed: expected done, got failed"}
	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig, true, "make test"))

	body, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "Command: make test", "the correction names the concrete failing command")
	assert.NotContains(t, s, "not reported", "placeholder must not appear when a command is known")
	assert.NotContains(t, s, "did not report the failing command", "placeholder must not appear when a command is known")
}

// TestWriteCorrectionFile_EmptyFailingCommand_KeepsFallback — an empty
// failingCommand preserves the existing placeholder text verbatim (no regression).
func TestWriteCorrectionFile_EmptyFailingCommand_KeepsFallback(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	sig := &ValidatorSignal{NextAction: "Implementation completed but failed acceptance criteria.\n\nassertion failed: expected done, got failed"}
	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig, true, ""))

	body, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "did not report the failing command", "empty failingCommand keeps the existing fallback text")
}

// TestHandleFailedCycle_EmptyDiff_NamesProbeCommand — the empty-diff gate route
// writes a correction that names the acceptance-diff probe command.
func TestHandleFailedCycle_EmptyDiff_NamesProbeCommand(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001") // goalUsesWorktree == true

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0 // zero commits ahead
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		case argsContain(args, "status", "--porcelain"):
			return "", 0 // clean working tree ⇒ truly empty ⇒ gate fires
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	_ = trackValidatorSpawn(d)

	gf := &GoalsFile{Goals: []Goal{worktreeGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

	body, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md"))
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "Command:", "the empty-diff correction names a Command")
	assert.Contains(t, s, "rev-list --count", "the correction names the acceptance-diff probe command")
	assert.NotContains(t, s, "not reported", "the probe command replaces the placeholder")
}

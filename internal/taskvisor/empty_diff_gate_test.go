package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// empty_diff_gate_test.go — goal-006: a PRE-validation empty-diff gate in
// checkSupervisingPhase. When the implementer reports DONE but produced NO
// in-scope changeset, the daemon must fail-fast the cycle via the code-retry
// path (handleFailedCycle) BEFORE spawning a validator — never emit a
// "phase supervising -> validating" line, never spawn a validator window, never
// let a pass be recorded for a cycle that integrated nothing. The gate measures
// emptiness with the SAME per-mode predicate the completion path uses (inline:
// scope-matched working-tree probe; worktree: commitsAhead(base..HEAD)==0), so a
// diff that clears the gate can never later trip failZeroIntegration. It fails
// OPEN on any git-probe error / unresolvable worktree, and only fires when
// len(Scope) > 0. The post-validation failZeroIntegration backstop is retained.

// gateGoal is a fresh supervising-phase goal with full code budget (consumed==0
// ⇒ CurrentCycle==1 ⇒ corrections/cycle-1.md), a non-empty scope, and lane=full
// so demoteSoloLane takes the tasks.yaml-only branch (no goal.md dependency).
func gateGoal(scope ...string) Goal {
	g := routeGoal("goal-001", 3, 3, 3, 3)
	g.Description = "empty diff gate goal"
	g.Lane = LaneFull
	g.Scope = scope
	return g
}

// primeSupervisingDone puts the daemon in the supervising phase with a delivered
// "done" SupervisorSignal — the exact state checkSupervisingPhase consumes right
// before the supervising→validating transition.
func primeSupervisingDone(t *testing.T, d *Daemon, dir, goalID string) {
	t.Helper()
	d.session = testSession
	d.mode = modeActive
	d.autoCommit = false
	d.validatorSendDelay = 0
	d.runtime(goalID).phase = phaseSupervising
	_, err := EnsureGoalDir(dir, goalID)
	require.NoError(t, err)
	require.NoError(t, SaveSupervisorSignal(dir, goalID, &SupervisorSignal{
		Status: "done", Timestamp: "2026-07-05T14:30:00Z",
	}))
}

// noWorkerWindows makes the two teardown ListWindows lookups (killWindowsByPrefix
// + killWindowByName) no-ops and records whether ANY window was created (the
// validator spawn). Returns a pointer the caller asserts against.
func trackValidatorSpawn(d *Daemon) *bool {
	spawned := new(bool)
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		*spawned = true
		return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
	})
	return spawned
}

// assertRoutedToRetry asserts the gate fired: the goal was re-pended for the
// code-retry path (not terminally failed), the phase stayed supervising, one code
// retry was consumed, a corrections file naming the commit instruction was
// written, the gate log line was emitted, NO transition log was emitted, and NO
// validator window was spawned.
func assertRoutedToRetry(t *testing.T, d *Daemon, dir string, goal *Goal, logs string, validatorSpawned *bool) {
	t.Helper()
	assert.Equal(t, GoalPending, goal.Status, "empty diff routes to the code-retry path (re-pended), not terminal failure")
	assert.Equal(t, phaseSupervising, d.runtime(goal.ID).phase, "phase stays supervising for the implementer respawn")
	assert.Equal(t, 2, goal.CodeRetries, "one code retry consumed (3 -> 2)")
	assert.False(t, *validatorSpawned, "no validator window may be spawned for an empty in-scope diff")

	corr := filepath.Join(dir, ".tmux-cli", "goals", goal.ID, "corrections", "cycle-1.md")
	data, err := os.ReadFile(corr)
	require.NoError(t, err, "corrections/cycle-1.md must be written")
	body := string(data)
	assert.Contains(t, body, "commit", "correction instructs the implementer to commit its work")
	assert.Contains(t, body, "in-scope", "correction names the missing in-scope diff")

	assert.Contains(t, logs, "empty in-scope diff — failing cycle before validation", "gate log line emitted")
	assert.NotContains(t, logs, "phase supervising -> validating", "no supervising->validating transition for a gated cycle")
}

// TestEmptyDiffGate_InlineEmptyRoutesToRetryBeforeValidator — inline mode, no
// scope-matched changes (porcelain empty): gate fires → code-retry, no validator.
func TestEmptyDiffGate_InlineEmptyRoutesToRetryBeforeValidator(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		return "", 0 // status --porcelain empty ⇒ empty in-scope diff
	}}
	d.SetGitRunnerFunc(fake.run)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	validatorSpawned := trackValidatorSpawn(d)

	gf := &GoalsFile{Goals: []Goal{gateGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assertRoutedToRetry(t, d, dir, &gf.Goals[0], logs, validatorSpawned)
}

// TestEmptyDiffGate_InlineNonEmptyProceedsToValidator — inline mode with a real
// scope-matched change (dirty porcelain, no start-snapshot ⇒ legacy staging):
// gate does NOT fire and the validator spawns exactly as before.
func TestEmptyDiffGate_InlineNonEmptyProceedsToValidator(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "status", "--porcelain") {
			return "M internal/taskvisor/statemachine.go\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	// Two teardown kill lookups, then the validator spawn path.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	gf := &GoalsFile{Goals: []Goal{gateGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assert.NotContains(t, logs, "empty in-scope diff — failing cycle before validation", "gate must not fire for a real in-scope diff")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "the goal advances to validating (validator spawned)")
	assert.Contains(t, logs, "phase supervising -> validating", "the non-empty path logs the transition unchanged")
}

// TestEmptyDiffGate_WorktreeZeroAheadRoutesToRetry — worktree mode, zero commits
// ahead of base: gate fires → code-retry, no validator.
func TestEmptyDiffGate_WorktreeZeroAheadRoutesToRetry(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001") // goalUsesWorktree == true

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "0\n", 0 // zero commits ahead ⇒ empty in-scope diff
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	validatorSpawned := trackValidatorSpawn(d)

	gf := &GoalsFile{Goals: []Goal{gateGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assertRoutedToRetry(t, d, dir, &gf.Goals[0], logs, validatorSpawned)
}

// TestEmptyDiffGate_WorktreeAheadProceeds — worktree mode with commits ahead of
// base: gate does NOT fire; the validator spawns as before.
func TestEmptyDiffGate_WorktreeAheadProceeds(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-list", "--count"):
			return "1\n", 0 // one commit ahead ⇒ non-empty in-scope diff
		case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
			return "main\n", 0
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	gf := &GoalsFile{Goals: []Goal{gateGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assert.NotContains(t, logs, "empty in-scope diff — failing cycle before validation", "gate must not fire when the worktree is ahead")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "the goal advances to validating (validator spawned)")
	assert.Contains(t, logs, "phase supervising -> validating")
}

// TestEmptyDiffGate_EmptyScopeSkipsGate — len(scope)==0: the gate is skipped
// entirely (same guard as every failZeroIntegration call site) and the
// transition proceeds to the validator.
func TestEmptyDiffGate_EmptyScopeSkipsGate(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")

	// A git runner that would REPORT emptiness if consulted — it must NOT be, so
	// an empty-scope goal never gates.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) { return "", 0 }}
	d.SetGitRunnerFunc(fake.run)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	gf := &GoalsFile{Goals: []Goal{gateGoal()}} // empty scope
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assert.NotContains(t, logs, "empty in-scope diff — failing cycle before validation", "empty-scope goals are never gated")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "the transition proceeds to the validator")
	assert.Equal(t, 0, fake.count("status", "--porcelain"), "no emptiness probe runs for an empty-scope goal")
}

// TestEmptyDiffGate_GitErrorFailsOpen — inline probe returns a non-zero exit: the
// gate fails OPEN (does not fire) so a transient git failure never blocks a real
// validation; the transition proceeds and the post-validation backstop still guards.
func TestEmptyDiffGate_GitErrorFailsOpen(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	primeSupervisingDone(t, d, dir, "goal-001")

	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		if argsContain(args, "status", "--porcelain") {
			return "fatal: not a git repository", 128 // probe error ⇒ fail open
		}
		return "", 0
	}}
	d.SetGitRunnerFunc(fake.run)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	gf := &GoalsFile{Goals: []Goal{gateGoal("internal/taskvisor/**")}}
	writeGoals(t, dir, gf)

	logs := captureLog(t, func() {
		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))
	})

	assert.NotContains(t, logs, "empty in-scope diff — failing cycle before validation", "a git-probe error must fail open, not gate")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "fail-open lets validation proceed")
}

package taskvisor

import (
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// zero_integration_test.go — the done-without-integration invariant (goal-007):
// a goal with a NON-EMPTY declared Scope that integrates ZERO net committed
// changes into base must surface GoalFailed ("no integrated changes"), never stay
// silently GoalDone. Worktree mode keys on the merge result (integrated); inline
// mode keys on whether autoCommitGoal committed. Empty-scope / genuine no-op
// goals are unaffected (no false positive). The two checks are mutually exclusive
// by mode (worktree branch gated on goalUsesWorktree, inline on !goalUsesWorktree).

// zeroAheadResponse drives mergeWorktreeBack down the ahead==0 (nothing landed)
// path: a clean porcelain, zero commits ahead, a resolvable base branch.
func zeroAheadResponse(args []string) (string, int) {
	switch {
	case argsContain(args, "status", "--porcelain"):
		return "", 0
	case argsContain(args, "rev-list", "--count"):
		return "0\n", 0 // zero commits ahead ⇒ integrated=false
	case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
		return "main\n", 0
	}
	return "", 0
}

// realMergeResponse drives mergeWorktreeBack down the real-merge path: a dirty
// porcelain, one commit ahead, a resolvable base branch ⇒ a true ff-merge.
func realMergeResponse(args []string) (string, int) {
	switch {
	case argsContain(args, "status", "--porcelain"):
		return "M internal/taskvisor/a.go\n", 0
	case argsContain(args, "rev-list", "--count"):
		return "1\n", 0 // one commit ahead ⇒ integrated=true
	case argsContain(args, "rev-parse", "--abbrev-ref", "HEAD"):
		return "main\n", 0
	}
	return "", 0
}

// --- worktree mode: keys on the merge result -------------------------------

// TestZeroIntegration_NonEmptyScopeWorktreeSurfacesFailed: a worktree goal with a
// non-empty Scope whose merge-back lands ZERO commits (ahead==0) is flipped
// GoalDone→GoalFailed with a VerdictFail/owner=human signal, a cascade of
// dependents, and a GOAL-FAILED reason=no-integrated-changes notify.
func TestZeroIntegration_NonEmptyScopeWorktreeSurfacesFailed(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001") // goalUsesWorktree == true

	fake := &fakeGitRunner{respond: zeroAheadResponse}
	d.SetGitRunnerFunc(fake.run)

	// A supervisor window so the GOAL-FAILED notify lands (and is captured).
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Return(nil)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "empty worker output", Status: GoalDone,
			Scope: []string{"internal/taskvisor/**"}},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.True(t, failed, "a non-empty-scope worktree goal that integrated nothing must surface failed")

	assert.Equal(t, GoalFailed, gf.Goals[0].Status, "goal flipped done -> failed")
	assert.NotEmpty(t, gf.Goals[0].FinishedAt, "FinishedAt stamped on the failed goal")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent cascade-blocked")
	assert.Equal(t, "goal-001", gf.Goals[1].BlockedBy)

	// A VerdictFail/owner=human signal mirrors the integration-failure surfacing.
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	valSig, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "zero-integration must write a validator signal")
	assert.Equal(t, VerdictFail, valSig.Verdict)
	assert.Equal(t, "human", valSig.Owner)
	assert.Contains(t, valSig.NextAction, "no integrated changes")

	// The GOAL-FAILED notify carries reason=no-integrated-changes.
	var found bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessageWithDelay" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") && strings.Contains(msg, "reason=no-integrated-changes") {
				found = true
			}
		}
	}
	assert.True(t, found, "must emit GOAL-FAILED with reason=no-integrated-changes")
}

// TestZeroIntegration_RealChangesReachDone: a worktree goal with a non-empty Scope
// whose merge-back lands a real commit (ahead==1) still reaches GoalDone — the
// invariant must not regress goals that did integrate work.
func TestZeroIntegration_RealChangesReachDone(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: realMergeResponse}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "real work", Status: GoalDone,
			Scope: []string{"internal/taskvisor/**"}},
	}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed, "a real ff-merge integrated work — goal stays done")
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, 1, fake.count("merge", "--ff-only"), "the branch fast-forwarded into base")
	assert.Equal(t, 1, fake.count("worktree", "remove", "--force"), "worktree removed after a clean merge")

	// No VerdictFail signal — the goal did not fail.
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	if valSig, ok := sig.(*ValidatorSignal); ok {
		assert.NotEqual(t, VerdictFail, valSig.Verdict, "no VerdictFail signal on a real merge")
	}
}

// TestZeroIntegration_EmptyScopeNoFalsePositive: a worktree goal with an EMPTY
// Scope and ahead==0 is a genuine no-op — it must stay GoalDone (the invariant
// keys strictly on len(Scope)>0, so an empty-scope goal never false-fails).
func TestZeroIntegration_EmptyScopeNoFalsePositive(t *testing.T) {
	d, _, dir := setupDaemon(t)
	mkGitRepo(t, dir)
	primeWorktree(d, "goal-001")

	fake := &fakeGitRunner{respond: zeroAheadResponse}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "scopeless no-op", Status: GoalDone, Scope: nil},
	}}
	writeGoals(t, dir, gf)

	failed, err := d.finalizeWorktreeOnDone(gf, &gf.Goals[0])
	require.NoError(t, err)
	assert.False(t, failed, "an empty-scope goal must NOT surface failed (no false positive)")
	assert.Equal(t, GoalDone, gf.Goals[0].Status, "empty-scope goal stays done")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	assert.Nil(t, sig, "no fail signal written for an empty-scope goal")
}

// --- inline mode: keys on whether autoCommitGoal committed -----------------
//
// The salvage done-site (site C) is the cleanest inline path to exercise in
// isolation: salvageLateVerdicts flips a late-pass GoalFailed back to GoalDone,
// runs autoCommitGoal, then applies the inline guard in place (it does not call
// advanceToNextGoal). A clean working tree makes autoCommitGoal report committed
// =false, so a non-empty-scope inline goal is re-failed by failZeroIntegration.

// TestZeroIntegration_DoneWithoutIntegrationInlineSalvage: an inline (no-worktree)
// goal salvaged to done with a non-empty Scope but zero committed changes is
// surfaced failed in place via the inline guard.
func TestZeroIntegration_DoneWithoutIntegrationInlineSalvage(t *testing.T) {
	d, _, dir := setupDaemon(t)
	// No primeWorktree ⇒ goalUsesWorktree == false (inline mode). Leave d.session
	// empty so the GOAL-FAILED notify is a silent no-op (no ListWindows mock needed).

	// A clean working tree ⇒ autoCommitGoal commits nothing ⇒ committed=false.
	fake := &fakeGitRunner{respond: func(args []string) (string, int) {
		return "", 0 // status --porcelain clean for every probe
	}}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "inline no commit", Status: GoalFailed,
			FailedBy: "validation-timeout", Scope: []string{"internal/taskvisor/**"}},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	// A late PASS verdict on disk, exactly as goal-validation-done writes it.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict:  VerdictPass,
		Findings: []ValidationFinding{{Rule: "go-test", Status: VerdictPass, Detail: "all green"}},
	}))

	require.NoError(t, d.salvageLateVerdicts(gf))

	// The salvaged done is re-failed by the inline zero-integration guard.
	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "inline non-empty-scope goal with zero commits is surfaced failed")
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status, "dependent cascade-blocked")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	valSig, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "a VerdictFail signal replaces the salvaged pass")
	assert.Equal(t, VerdictFail, valSig.Verdict)
	assert.Equal(t, "human", valSig.Owner)
	assert.Contains(t, valSig.NextAction, "no integrated changes")
}

// TestZeroIntegration_EmptyIntegrationSurfacesInlineEmptyScope: an inline goal
// salvaged to done with an EMPTY Scope stays done — the inline guard is gated on
// len(Scope)>0, so a scopeless goal never false-fails even with zero commits.
func TestZeroIntegration_EmptyIntegrationSurfacesInlineEmptyScope(t *testing.T) {
	d, _, dir := setupDaemon(t)

	fake := &fakeGitRunner{respond: func(args []string) (string, int) { return "", 0 }}
	d.SetGitRunnerFunc(fake.run)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "inline scopeless", Status: GoalFailed,
			FailedBy: "validation-timeout", Scope: nil},
		// A live sibling keeps the daemon-shaped state realistic; unused by salvage.
		{ID: "goal-002", Description: "live sibling", Status: GoalRunning},
	}}
	writeGoals(t, dir, gf)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict:  VerdictPass,
		Findings: []ValidationFinding{{Rule: "go-test", Status: VerdictPass, Detail: "all green"}},
	}))

	require.NoError(t, d.salvageLateVerdicts(gf))

	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, g.Status, "empty-scope inline goal stays done (no false positive)")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "no cascade on a no-op empty-scope goal — the sibling stays running")
}

// --- the mode predicate ----------------------------------------------------

// TestZeroIntegration_NonEmptyScopeZeroIntegrationModePredicate pins the
// mutual-exclusion predicate: goalUsesWorktree is true only when a worktree is
// primed, false inline — so each goal routes to exactly ONE of the two checks.
func TestZeroIntegration_NonEmptyScopeZeroIntegrationModePredicate(t *testing.T) {
	d, _, _ := setupDaemon(t)

	inline := &Goal{ID: "goal-001"}
	assert.False(t, d.goalUsesWorktree(inline), "no worktree primed ⇒ inline mode")

	primeWorktree(d, "goal-002")
	wt := &Goal{ID: "goal-002"}
	assert.True(t, d.goalUsesWorktree(wt), "worktree primed ⇒ worktree mode")
}

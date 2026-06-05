package taskvisor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcileBlocks unit tests — ReconcileBlocks is a pure *GoalsFile method, so
// these need no Daemon: build an in-memory GoalsFile, call ReconcileBlocks, and
// assert per-goal Status/BlockedBy plus the changed return.

// TestReconcileBlocks_RependsHardBlockedSubtreeWhenBlockerDone — Bug A core fix:
// B,C hard-blocked behind A; A is now done with all deps satisfied → B,C are
// re-pended (GoalPending, BlockedBy="") and the call reports changed==true.
func TestReconcileBlocks_RependsHardBlockedSubtreeWhenBlockerDone(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "goal-A", DependsOn: []string{"goal-A"}},
		{ID: "goal-C", Status: GoalBlocked, BlockedBy: "goal-A", DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.True(t, changed, "re-pending a recovered subtree is a change")
	b, _ := gf.GoalByID("goal-B")
	c, _ := gf.GoalByID("goal-C")
	assert.Equal(t, GoalPending, b.Status)
	assert.Equal(t, "", b.BlockedBy)
	assert.Equal(t, GoalPending, c.Status)
	assert.Equal(t, "", c.BlockedBy)
}

// TestReconcileBlocks_ReblocksWhenAnotherDepStillFailed — C depends on [A,B]
// (order pinned); A done but B still failed → C is re-blocked behind the
// first-failed-in-array-order dep, B.
func TestReconcileBlocks_ReblocksWhenAnotherDepStillFailed(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalFailed},
		{ID: "goal-C", Status: GoalBlocked, BlockedBy: "goal-A", DependsOn: []string{"goal-A", "goal-B"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	c, _ := gf.GoalByID("goal-C")
	assert.Equal(t, GoalBlocked, c.Status)
	assert.Equal(t, "goal-B", c.BlockedBy, "blocked behind first failed dep in array order")
}

// TestReconcileBlocks_ReblocksFreshFailedDep — a still-pending goal whose dep
// just failed is derived to GoalBlocked,BlockedBy=<dep>.
func TestReconcileBlocks_ReblocksFreshFailedDep(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalFailed},
		{ID: "goal-D", Status: GoalPending, DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	d, _ := gf.GoalByID("goal-D")
	assert.Equal(t, GoalBlocked, d.Status)
	assert.Equal(t, "goal-A", d.BlockedBy)
}

// TestReconcileBlocks_PreservesPreconditionPark_HaltBlockedEnv — an env/infra
// park (BlockedByPrecondition flag set, BlockedBy="env_precondition") with deps
// done is left untouched; reconcile keys on the flag, not BlockedBy.
func TestReconcileBlocks_PreservesPreconditionPark_HaltBlockedEnv(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "env_precondition",
			BlockedByPrecondition: true, DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "env_precondition", b.BlockedBy)
}

// TestReconcileBlocks_PreservesPreflightPark_EmptyBlockedBy — an ops preflight
// park (flag set, BlockedBy empty) with deps done is left untouched.
func TestReconcileBlocks_PreservesPreflightPark_EmptyBlockedBy(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "",
			BlockedByPrecondition: true, DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "", b.BlockedBy)
}

// TestReconcileBlocks_DoesNotClearCircuitBreaker — the convergence circuit
// breaker sentinel is a human gate; reconcile never auto-clears it.
func TestReconcileBlocks_DoesNotClearCircuitBreaker(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "convergence-circuit-breaker",
			DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "convergence-circuit-breaker", b.BlockedBy)
}

// TestReconcileBlocks_LeavesExternalHoldBlocked — a non-dependency "external"
// hold on a zero-dep goal stays blocked: DependsOnSatisfied is vacuously true,
// so the isDependencyBlock guard is what keeps it pinned.
func TestReconcileBlocks_LeavesExternalHoldBlocked(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "external"},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "external", b.BlockedBy)
}

// TestReconcileBlocks_LeavesSpecDefectPreflightParkBlocked — a planner
// spec-defect park (BlockedBy="", flag false) needs a re-plan, not a re-pend;
// isDependencyBlock is false so it stays blocked.
func TestReconcileBlocks_LeavesSpecDefectPreflightParkBlocked(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "",
			BlockedByPrecondition: false, DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "", b.BlockedBy)
}

// TestReconcileBlocks_LeavesDanglingDepBlocked — BlockedBy names a deleted goal
// id not present in the file; it is not in realIDs so the goal stays blocked.
func TestReconcileBlocks_LeavesDanglingDepBlocked(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "goal-deleted"},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalBlocked, b.Status)
	assert.Equal(t, "goal-deleted", b.BlockedBy)
}

// TestReconcileBlocks_RependsDepsUnsatisfiedMarker — the transient
// "deps_unsatisfied" marker is dependency-derived; with deps done it re-pends.
func TestReconcileBlocks_RependsDepsUnsatisfiedMarker(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "deps_unsatisfied",
			DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalPending, b.Status)
	assert.Equal(t, "", b.BlockedBy)
}

// TestReconcileBlocks_SkipsGoalRunning — a live worker is never orphaned: a
// GoalRunning goal whose dep just failed is NOT re-stamped blocked.
func TestReconcileBlocks_SkipsGoalRunning(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalFailed},
		{ID: "goal-B", Status: GoalRunning, DependsOn: []string{"goal-A"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalRunning, b.Status)
}

// TestReconcileBlocks_LeavesTerminalGoalsUntouched — done/failed goals are
// terminal and never mutated.
func TestReconcileBlocks_LeavesTerminalGoalsUntouched(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone, DependsOn: []string{"goal-X"}},
		{ID: "goal-X", Status: GoalFailed},
		{ID: "goal-B", Status: GoalFailed, DependsOn: []string{"goal-X"}},
	}}

	changed := gf.ReconcileBlocks()

	assert.False(t, changed)
	a, _ := gf.GoalByID("goal-A")
	b, _ := gf.GoalByID("goal-B")
	assert.Equal(t, GoalDone, a.Status)
	assert.Equal(t, GoalFailed, b.Status)
}

// TestReconcileBlocks_LeavesRetryBudgetsUntouched — across every reconcile
// branch, no retry-budget field is touched (only Status/BlockedBy).
func TestReconcileBlocks_LeavesRetryBudgetsUntouched(t *testing.T) {
	mk := func(id, status, blockedBy string, deps []string) Goal {
		return Goal{
			ID: id, Status: status, BlockedBy: blockedBy, DependsOn: deps,
			Retries: 2, CodeRetries: 1, SpecRetries: 3, ValidationRetries: 4, BlockRetries: 5,
		}
	}
	gf := &GoalsFile{Goals: []Goal{
		mk("goal-A", GoalDone, "", nil),
		mk("goal-F", GoalFailed, "", nil),
		mk("goal-rep", GoalBlocked, "goal-A", []string{"goal-A"}), // re-pended
		mk("goal-blk", GoalPending, "", []string{"goal-F"}),       // re-blocked
		mk("goal-keep", GoalBlocked, "external", nil),             // untouched
	}}

	gf.ReconcileBlocks()

	for _, g := range gf.Goals {
		assert.Equal(t, 2, g.Retries, "%s Retries", g.ID)
		assert.Equal(t, 1, g.CodeRetries, "%s CodeRetries", g.ID)
		assert.Equal(t, 3, g.SpecRetries, "%s SpecRetries", g.ID)
		assert.Equal(t, 4, g.ValidationRetries, "%s ValidationRetries", g.ID)
		assert.Equal(t, 5, g.BlockRetries, "%s BlockRetries", g.ID)
	}
}

// TestReconcileBlocks_Idempotent — a second call after a converged state is a
// no-op: changed==false and state identical.
func TestReconcileBlocks_Idempotent(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-A", Status: GoalDone},
		{ID: "goal-B", Status: GoalBlocked, BlockedBy: "goal-A", DependsOn: []string{"goal-A"}},
		{ID: "goal-F", Status: GoalFailed},
		{ID: "goal-C", Status: GoalPending, DependsOn: []string{"goal-F"}},
	}}

	first := gf.ReconcileBlocks()
	require.True(t, first, "first call mutates")
	snapshot := make([]Goal, len(gf.Goals))
	copy(snapshot, gf.Goals)

	second := gf.ReconcileBlocks()

	assert.False(t, second, "second call is a no-op")
	for i := range gf.Goals {
		assert.Equal(t, snapshot[i].Status, gf.Goals[i].Status, "%s Status stable", gf.Goals[i].ID)
		assert.Equal(t, snapshot[i].BlockedBy, gf.Goals[i].BlockedBy, "%s BlockedBy stable", gf.Goals[i].ID)
	}
}

// TestTick_SelfRecoversStuckSubtreeOnLoad — a daemon loading an already-stuck
// goals.yaml (subtree GoalBlocked behind a now-done blocker, current_goal a done
// goal) heals it in one tick: the subtree is re-pended and a dependable goal is
// dispatched, with no operator action.
func TestTick_SelfRecoversStuckSubtreeOnLoad(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-003",
		Goals: []Goal{
			{ID: "goal-003", Description: "recovered blocker", Status: GoalDone},
			{ID: "goal-004", Description: "stuck dependent", Status: GoalBlocked,
				BlockedBy: "goal-003", DependsOn: []string{"goal-003"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-004")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0", "supervisor-004")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, "goal-004", gf.CurrentGoal, "re-pended dependent becomes current")
	assert.Equal(t, GoalRunning, gf.Goals[1].Status, "re-pended dependent is dispatched")

	// The heal is persisted: reloading the file shows the dependent is no longer
	// pinned blocked behind the done blocker.
	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rb, _ := reloaded.GoalByID("goal-004")
	assert.NotEqual(t, GoalBlocked, rb.Status, "no operator action needed — heal persisted")
}

// TestTick_SelfRecoverPersistsWhenCurrentGoalRunning — the subtle self-persist:
// when current_goal is a GoalRunning goal mid-flight, the tick's running path
// (checkProgress) returns nil WITHOUT a SaveGoals, so the reconcile mutation
// would be lost on flock release unless the tick self-persists at the top. Prove
// it by reloading the file from disk after one tick.
func TestTick_SelfRecoverPersistsWhenCurrentGoalRunning(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	// the goal's runtime phase is the lazily-created zero value (phaseNone) here, so
	// checkProgress short-circuits to nil with no signal load and no SaveGoals —
	// exactly the mid-flight no-save path.

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "live worker", Status: GoalRunning},
			{ID: "goal-003", Description: "recovered blocker", Status: GoalDone},
			{ID: "goal-004", Description: "stuck dependent", Status: GoalBlocked,
				BlockedBy: "goal-003", DependsOn: []string{"goal-003"}},
		},
	}
	writeGoals(t, dir, gf)

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	assert.Equal(t, GoalRunning, gf.Goals[0].Status, "the live worker is never orphaned")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rb, _ := reloaded.GoalByID("goal-004")
	assert.Equal(t, GoalPending, rb.Status, "reconcile mutation persisted despite no-save running path")
	assert.Equal(t, "", rb.BlockedBy, "re-pend cleared BlockedBy on disk")
}

// TestIncidentReplay_FailResetDone — the real incident: goal-003 hard-fails
// (CascadeFailure blocks its subtree), an operator ResetGoals it, it then
// completes done — reconcile recovers the whole subtree.
func TestIncidentReplay_FailResetDone(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-003", Status: GoalFailed},
		{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-003"}},
		{ID: "goal-005", Status: GoalPending, DependsOn: []string{"goal-004"}},
	}}

	// 1. Hard failure cascades GoalBlocked across the subtree.
	gf.CascadeFailure("goal-003", "fail")
	g4, _ := gf.GoalByID("goal-004")
	require.Equal(t, GoalBlocked, g4.Status)

	// 2. Operator resets goal-003, it later completes done.
	require.True(t, gf.ResetGoal("goal-003"))
	require.True(t, gf.SetStatus("goal-003", GoalDone))

	// 3. Reconcile heals the now-stale GoalBlocked subtree.
	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	g4, _ = gf.GoalByID("goal-004")
	g5, _ := gf.GoalByID("goal-005")
	assert.Equal(t, GoalPending, g4.Status)
	assert.Equal(t, "", g4.BlockedBy)
	// goal-005's dep (goal-004) is now pending (not done), so it re-pends too only
	// once goal-004 completes; here it should also clear since its blocked_by
	// (goal-004) is real but goal-004 is not failed and deps aren't satisfied.
	assert.Equal(t, GoalBlocked, g5.Status, "goal-005 stays blocked until goal-004 completes")
}

// countBlockedByDoneFlagged mirrors checkInvariant's trigger (diagnostics.go:50):
// it counts non-terminal, non-park goals whose BlockedBy names a real GoalDone
// goal — the exact signature the daemon floods INVARIANT VIOLATION on. The A1
// fix must drive this to 0 post-reconcile.
func countBlockedByDoneFlagged(gf *GoalsFile) int {
	n := 0
	for i := range gf.Goals {
		g := &gf.Goals[i]
		switch g.Status {
		case GoalDone, GoalFailed, GoalRunning:
			continue
		}
		if g.BlockedByPrecondition || g.BlockedBy == "convergence-circuit-breaker" || g.BlockedBy == "" {
			continue
		}
		if gf.statusOf(g.BlockedBy) == GoalDone {
			n++
		}
	}
	return n
}

// TestReconcileBlocks_StaleBlockedByDone_RepointsToPendingDep — A1 core: the
// goal-063 replay (BlockedBy names a now-done blocker while a sibling dep is
// still pending) matches neither re-block (no failed dep) nor un-stick (deps
// unsatisfied). The new branch re-points BlockedBy to the first incomplete dep
// and KEEPS the goal GoalBlocked, driving the checkInvariant flag-count to 0.
func TestReconcileBlocks_StaleBlockedByDone_RepointsToPendingDep(t *testing.T) {
	gf := newGoal063Fixture() // goal-001 done, goal-002 pending, goal-063 blocked by goal-001

	require.Equal(t, 1, countBlockedByDoneFlagged(gf), "fixture starts in the flagged state")

	changed := gf.ReconcileBlocks()

	assert.True(t, changed, "re-pointing a stale BlockedBy is a change")
	g, _ := gf.GoalByID("goal-063")
	assert.Equal(t, GoalBlocked, g.Status, "stays blocked — sibling dep still pending")
	assert.Equal(t, "goal-002", g.BlockedBy, "re-pointed to the first still-incomplete dep")
	assert.Equal(t, 0, countBlockedByDoneFlagged(gf), "checkInvariant no longer flags any goal")
}

// TestReconcileBlocks_Idempotent_AfterRepoint — after the re-point, BlockedBy
// names an incomplete dep, so the gate (statusOf(BlockedBy)==GoalDone) is false
// on the next tick → no-op, changed==false, BlockedBy unchanged.
func TestReconcileBlocks_Idempotent_AfterRepoint(t *testing.T) {
	gf := newGoal063Fixture()

	require.True(t, gf.ReconcileBlocks(), "first call re-points")
	g, _ := gf.GoalByID("goal-063")
	require.Equal(t, "goal-002", g.BlockedBy)

	second := gf.ReconcileBlocks()

	assert.False(t, second, "second call is a no-op")
	g, _ = gf.GoalByID("goal-063")
	assert.Equal(t, GoalBlocked, g.Status)
	assert.Equal(t, "goal-002", g.BlockedBy, "BlockedBy unchanged on idempotent re-run")
}

// TestReconcileBlocks_RepointsToFirstIncompleteDepInArrayOrder — with two
// incomplete deps (goal-002 pending then goal-064 running) the re-point picks
// the FIRST incomplete in DependsOn array order, not just any incomplete dep.
func TestReconcileBlocks_RepointsToFirstIncompleteDepInArrayOrder(t *testing.T) {
	gf := newGoal063Fixture()
	gf.Goals = append(gf.Goals, Goal{ID: "goal-064", Status: GoalRunning})
	g, _ := gf.GoalByID("goal-063")
	g.DependsOn = []string{"goal-001", "goal-002", "goal-064"} // done, pending, running

	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	g, _ = gf.GoalByID("goal-063")
	assert.Equal(t, "goal-002", g.BlockedBy, "first incomplete dep in array order, though goal-064 is also incomplete")
	assert.Equal(t, GoalBlocked, g.Status)
}

// TestReconcileBlocks_RepointFallsBackToDepsUnsatisfiedForDanglingDep — when
// DependsOnSatisfied is false only because of a dangling (deleted) dep id, no
// real incomplete dep exists, so the re-point uses the "deps_unsatisfied"
// sentinel and the goal stays GoalBlocked.
func TestReconcileBlocks_RepointFallsBackToDepsUnsatisfiedForDanglingDep(t *testing.T) {
	gf := newGoal063Fixture()
	g, _ := gf.GoalByID("goal-063")
	g.DependsOn = []string{"goal-001", "goal-999"} // goal-999 does not exist

	changed := gf.ReconcileBlocks()

	assert.True(t, changed)
	g, _ = gf.GoalByID("goal-063")
	assert.Equal(t, "deps_unsatisfied", g.BlockedBy, "fallback sentinel when no real incomplete dep exists")
	assert.Equal(t, GoalBlocked, g.Status)
}

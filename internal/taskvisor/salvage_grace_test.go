package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// salvage_grace_test.go — Fix 3 (goal-061 post-mortem follow-up to Fix 2):
// salvage grace window in deactivateOnCompletion.
//
// Incident shape: in the goal-061 topology — timeout-failed goal + ALL remaining
// goals cascade-blocked on it — salvageLateVerdicts (Fix 2) can never fire:
// nothing is runnable, AllResolved counts failed+blocked as resolved,
// HasRecoverableBlock correctly excludes GoalFailed blockers, so
// deactivateOnCompletion tears down EVERY goal window (killing the still-running
// validator the exhausted-timeout branch deliberately left alive) and modeIdle
// poll() never reaches tick()/the salvage scan. The real pass verdict arrived
// 5m51s after the timeout and was lost.
//
// The fix: a third self-guard BEFORE teardown (alongside HasResumablePark /
// HasRecoverableBlock) — while any goal is "salvage-pending" (GoalFailed +
// FailedBy=="validation-timeout" + FinishedAt fresher than salvageGrace) the
// daemon stays active WITHOUT tearing down. On grace expiry the marker is
// cleared + persisted and deactivation proceeds in the same call
// (self-terminating — a validator that never reports cannot wedge the daemon
// active forever).

// freshRFC3339 / staleRFC3339 build FinishedAt stamps relative to now.
func freshRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func staleRFC3339(age time.Duration) string {
	return time.Now().UTC().Add(-age).Format(time.RFC3339)
}

// --- Test 1: salvage-pending goal defers deactivation ------------------------

// TestDeactivateOnCompletion_SalvageGraceOpen_StaysActive — a timeout-marked
// failed goal with a fresh FinishedAt holds the daemon active: no teardown (the
// mock executor would fail on any unexpected ListWindows/KillWindow), no mode
// flip, guard file intact, and the marker is NOT mutated (the salvage scan must
// keep watching it).
func TestDeactivateOnCompletion_SalvageGraceOpen_StaysActive(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	// goal-061 topology: timeout-failed blocker + cascade-blocked dependent.
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed blocker", Status: GoalFailed,
				FailedBy: "validation-timeout", FinishedAt: freshRFC3339()},
			{ID: "goal-002", Description: "cascade-blocked dependent", Status: GoalBlocked,
				BlockedBy: "goal-001", DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)

	out := captureLog(t, func() {
		require.NoError(t, d.deactivateOnCompletion(gf))
	})

	assert.Equal(t, modeActive, d.mode, "daemon must stay active while salvage grace is open")
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.NoError(t, statErr, "taskvisor-active guard must remain")
	assert.Contains(t, out, "salvage grace open for goal-001")

	// No marker mutation, in memory or on disk — the salvage scan keeps watching.
	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "validation-timeout", g.FailedBy, "marker untouched while pending")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "validation-timeout", rg.FailedBy, "no persistence side effect while pending")
}

// --- Test 2: expired grace clears the marker and deactivates -----------------

// TestDeactivateOnCompletion_SalvageGraceExpired_ClearsMarkerAndDeactivates —
// FinishedAt older than salvageGrace: the marker is cleared + persisted and
// normal deactivation proceeds in the same call (modeIdle, guard file removed,
// teardown invoked — the mock sequence is consumed).
func TestDeactivateOnCompletion_SalvageGraceExpired_ClearsMarkerAndDeactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed blocker", Status: GoalFailed,
				FailedBy: "validation-timeout", FinishedAt: staleRFC3339(2 * time.Hour)},
			{ID: "goal-002", Description: "cascade-blocked dependent", Status: GoalBlocked,
				BlockedBy: "goal-001", DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	out := captureLog(t, func() {
		require.NoError(t, d.deactivateOnCompletion(gf))
	})

	assert.Equal(t, modeIdle, d.mode, "daemon must deactivate once grace expired")
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "guard file removed on deactivation")
	assert.Contains(t, out, "salvage grace expired for goal-001")
	exec.AssertExpectations(t)

	// Marker cleared in memory AND persisted.
	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "", g.FailedBy, "expired marker cleared")
	assert.Equal(t, GoalFailed, g.Status, "failure itself stands")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, "", rg.FailedBy, "marker clear persisted to goals.yaml")
}

// --- Test 3: end-to-end goal-061 topology recovery ---------------------------

// TestTick_Goal061Topology_GraceDefersThenSalvages — the full incident replay:
//
//	tick 1: timeout-failed goal (fresh FinishedAt) + everything cascade-blocked,
//	        no verdict yet → deactivateOnCompletion is reached but the grace
//	        guard defers it: daemon stays active, no teardown.
//	tick 2: the late PASS verdict is now on disk → salvage flips failed→done,
//	        ReconcileBlocks re-pends the dependent, and it is dispatched the
//	        SAME tick. The daemon never went idle — goal-061 now recovers.
func TestTick_Goal061Topology_GraceDefersThenSalvages(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "timeout-failed blocker", Status: GoalFailed,
				FailedBy:  "validation-timeout",
				StartedAt: staleRFC3339(30 * time.Minute), FinishedAt: freshRFC3339()},
			{ID: "goal-002", Description: "cascade-blocked dependent", Status: GoalBlocked,
				BlockedBy: "goal-001", DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-002")
	require.NoError(t, err)

	// --- Tick 1: no verdict yet — grace guard must defer deactivation. ---
	// No executor expectations are set: any teardown call would fail the mock.
	out := captureLog(t, func() {
		require.NoError(t, d.tick(context.Background(), gf))
	})
	assert.Equal(t, modeActive, d.mode, "tick 1: daemon stays active inside the grace window")
	assert.Contains(t, out, "salvage grace open for goal-001")
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	_, statErr := os.Stat(guardPath)
	assert.NoError(t, statErr, "tick 1: guard file intact")
	g1, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g1.Status)
	assert.Equal(t, "validation-timeout", g1.FailedBy, "tick 1: still watching")

	// --- The late pass verdict lands (5m51s late in the real incident). ---
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass,
		Findings: []ValidationFinding{{
			Rule: "go-test", Status: VerdictPass, Detail: "all green",
		}},
		Timestamp: freshRFC3339(),
	}))

	// --- Tick 2: salvage + same-tick dispatch of the re-pended dependent. ---
	// Salvage's killWindowByName(validator) consumes one ListWindows before the
	// dispatch mock sequence.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.tick(context.Background(), gf))

	blocker, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalDone, blocker.Status, "tick 2: late pass salvaged failed -> done")
	assert.Equal(t, "", blocker.FailedBy, "tick 2: marker cleared by salvage")

	dep, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, dep.Status, "tick 2: dependent re-pended and dispatched same tick")

	assert.Equal(t, modeActive, d.mode, "daemon stayed active throughout")
}

// --- Test 4: malformed/absent FinishedAt can never wedge the daemon ----------

// TestDeactivateOnCompletion_SalvageMarkerBadFinishedAt_TreatedExpired — a
// marked goal whose FinishedAt is absent or unparseable is treated as EXPIRED:
// marker cleared, deactivation proceeds. The guard must be self-terminating.
func TestDeactivateOnCompletion_SalvageMarkerBadFinishedAt_TreatedExpired(t *testing.T) {
	for _, tc := range []struct {
		name       string
		finishedAt string
	}{
		{name: "absent", finishedAt: ""},
		{name: "malformed", finishedAt: "yesterday-ish"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, exec, dir := setupDaemon(t)
			d.mode = modeActive
			d.session = testSession
			writeGuardFile(t, dir)

			gf := &GoalsFile{
				CurrentGoal: "goal-001",
				Goals: []Goal{
					{ID: "goal-001", Description: "timeout-failed, bad stamp", Status: GoalFailed,
						FailedBy: "validation-timeout", FinishedAt: tc.finishedAt},
				},
			}
			writeGoals(t, dir, gf)

			setupDeactivateOnCompletionMocks(exec, testSession)

			require.NoError(t, d.deactivateOnCompletion(gf))

			assert.Equal(t, modeIdle, d.mode, "bad FinishedAt must not wedge the daemon active")
			g, ok := gf.GoalByID("goal-001")
			require.True(t, ok)
			assert.Equal(t, "", g.FailedBy, "marker cleared on treated-as-expired")
		})
	}
}

// --- splitSalvageMarked unit coverage ----------------------------------------

func TestSplitSalvageMarked_Partition(t *testing.T) {
	now := time.Now().UTC()
	grace := 600 * time.Second
	gf := &GoalsFile{Goals: []Goal{
		// pending: failed 5m ago, inside the 10m grace
		{ID: "goal-001", Status: GoalFailed, FailedBy: "validation-timeout",
			FinishedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)},
		// expired: failed 11m ago
		{ID: "goal-002", Status: GoalFailed, FailedBy: "validation-timeout",
			FinishedAt: now.Add(-11 * time.Minute).Format(time.RFC3339)},
		// expired: marker without a parseable stamp
		{ID: "goal-003", Status: GoalFailed, FailedBy: "validation-timeout"},
		// not in the watch set: unmarked failure
		{ID: "goal-004", Status: GoalFailed, FinishedAt: now.Format(time.RFC3339)},
		// not in the watch set: marker only matters on GoalFailed
		{ID: "goal-005", Status: GoalDone, FailedBy: "validation-timeout",
			FinishedAt: now.Format(time.RFC3339)},
	}}

	pending, expired := gf.splitSalvageMarked(now, grace)

	require.Len(t, pending, 1)
	assert.Equal(t, "goal-001", pending[0].ID)
	require.Len(t, expired, 2)
	assert.Equal(t, "goal-002", expired[0].ID)
	assert.Equal(t, "goal-003", expired[1].ID)
}

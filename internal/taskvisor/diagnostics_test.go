package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- stall watchdog reporting ------------------------------------------------

// TestCheckStall_ReportsOnceOnIdleStall: with one runnable candidate and nothing
// running, the watchdog reaches its threshold on the 3rd idle tick and flips the
// once-per-episode guard (stallReported). Subsequent ticks must NOT re-arm the
// report. Submission is a nil-producer no-op here, so the observable contract is
// the guard transition + idleTicks count at fire time.
func TestCheckStall_ReportsOnceOnIdleStall(t *testing.T) {
	d, _, _ := setupDaemon(t)
	require.Nil(t, d.producer, "test seam: producer must be nil (reporting disabled)")
	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalPending}}}

	require.NotPanics(t, func() {
		d.checkStall(gf)
		assert.Equal(t, 1, d.idleTicks)
		assert.False(t, d.stallReported, "no fire before threshold")

		d.checkStall(gf)
		assert.Equal(t, 2, d.idleTicks)
		assert.False(t, d.stallReported)

		// 3rd idle tick reaches stallWatchdogTicks -> fires exactly once.
		d.checkStall(gf)
		assert.Equal(t, 3, d.idleTicks)
		assert.True(t, d.stallReported, "fires on reaching the watchdog threshold")

		// 4th tick: guard holds, no re-fire.
		d.checkStall(gf)
		assert.True(t, d.stallReported, "guard suppresses repeat reports")
	})
}

// TestCheckStall_NoReportWhenRunningOrNoCandidate: an in-flight worker
// (AnyRunning) or an empty candidate set is a legitimate idle — the guard stays
// false and idleTicks resets (existing reset path), so nothing is reported.
func TestCheckStall_NoReportWhenRunningOrNoCandidate(t *testing.T) {
	d, _, _ := setupDaemon(t)

	running := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalRunning}}}
	d.checkStall(running)
	assert.Equal(t, 0, d.idleTicks)
	assert.False(t, d.stallReported)

	noCandidate := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	d.checkStall(noCandidate)
	assert.Equal(t, 0, d.idleTicks)
	assert.False(t, d.stallReported)
}

// TestStallPayload_Assembly pins the pure payload contract (the assertable seam
// given a nil producer): candidate IDs + idle/watchdog counts under the exact
// keys the backend expects.
func TestStallPayload_Assembly(t *testing.T) {
	p := stallPayload([]string{"goal-001", "goal-002"}, 3, stallWatchdogTicks)
	assert.Equal(t, []string{"goal-001", "goal-002"}, p["runnable_candidates"])
	assert.Equal(t, 3, p["idle_ticks"])
	assert.Equal(t, stallWatchdogTicks, p["stall_watchdog_ticks"])
}

// --- Bug-A invariant reporting -----------------------------------------------

// TestCheckInvariant_ReportsOnceThenGuards: a non-terminal goal BlockedBy a done
// goal arms invariantReported once; a repeat tick is suppressed; clearing the
// violation (empty offending set) resets the guard so a fresh episode reports
// again.
func TestCheckInvariant_ReportsOnceThenGuards(t *testing.T) {
	d, _, _ := setupDaemon(t)
	require.Nil(t, d.producer)
	gf := &GoalsFile{Goals: []Goal{
		{ID: "X", Status: GoalPending, BlockedBy: "Y"},
		{ID: "Y", Status: GoalDone},
	}}

	d.checkInvariant(gf)
	assert.True(t, d.invariantReported, "violation arms the guard")

	d.checkInvariant(gf)
	assert.True(t, d.invariantReported, "repeat tick stays armed (no re-report)")

	// Clear the violation -> guard resets at the len(ids)==0 early return.
	gf.Goals[0].BlockedBy = ""
	d.checkInvariant(gf)
	assert.False(t, d.invariantReported, "cleared violation resets the guard")

	// Re-violate -> reports again on the new episode.
	gf.Goals[0].BlockedBy = "Y"
	d.checkInvariant(gf)
	assert.True(t, d.invariantReported, "new episode re-arms the guard")
}

// TestCheckInvariant_SkipsLegitHolds: a precondition park and the
// convergence-circuit-breaker sentinel are legitimate holds, not Bug-A — the
// guard must stay unset and nothing is reported.
func TestCheckInvariant_SkipsLegitHolds(t *testing.T) {
	d, _, _ := setupDaemon(t)

	precondition := &GoalsFile{Goals: []Goal{
		{ID: "X", Status: GoalPending, BlockedBy: "Y", BlockedByPrecondition: true},
		{ID: "Y", Status: GoalDone},
	}}
	d.checkInvariant(precondition)
	assert.False(t, d.invariantReported, "precondition park is a legit hold")

	breaker := &GoalsFile{Goals: []Goal{
		{ID: "X", Status: GoalPending, BlockedBy: "convergence-circuit-breaker"},
		{ID: "Y", Status: GoalDone},
	}}
	d.checkInvariant(breaker)
	assert.False(t, d.invariantReported, "circuit-breaker sentinel is a legit hold")
}

// TestCheckInvariant_NilProducerNoOp: with reporting disabled the check never
// panics regardless of a live violation.
func TestCheckInvariant_NilProducerNoOp(t *testing.T) {
	d := &Daemon{} // producer == nil
	gf := &GoalsFile{Goals: []Goal{
		{ID: "X", Status: GoalPending, BlockedBy: "Y"},
		{ID: "Y", Status: GoalDone},
	}}
	require.NotPanics(t, func() { d.checkInvariant(gf) })
	assert.True(t, d.invariantReported, "guard still flips even with reporting off")
}

// TestInvariantPayload_Assembly pins the pure payload contract: offending IDs +
// a non-empty YAML dump of all goals under the backend's keys.
func TestInvariantPayload_Assembly(t *testing.T) {
	goals := []Goal{{ID: "X", Description: "alpha"}, {ID: "Y", Description: "beta"}}
	p := invariantPayload([]string{"X"}, goals)
	assert.Equal(t, []string{"X"}, p["offending_goals"])
	dump, ok := p["goals_dump"].(string)
	require.True(t, ok, "goals_dump must be a string")
	assert.Contains(t, dump, "X")
	assert.Contains(t, dump, "Y")
}

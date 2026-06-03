package taskvisor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- checkInvariant -------------------------------------------------------

func TestCheckInvariant_FlagsBlockedByDoneGoal(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalDone},
			{ID: "goal-B", Status: GoalBlocked, BlockedBy: "goal-A"},
		},
	}

	out := captureLog(t, func() { d.checkInvariant(gf) })

	assert.Contains(t, out, "INVARIANT VIOLATION")
	assert.Contains(t, out, "goal-B")
	// Diagnostics only — state must be untouched.
	assert.Equal(t, GoalBlocked, gf.Goals[1].Status)
	assert.Equal(t, "goal-A", gf.Goals[1].BlockedBy)
}

func TestCheckInvariant_IgnoresPreconditionHold(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalDone},
			{ID: "goal-B", Status: GoalBlocked, BlockedBy: "env_precondition", BlockedByPrecondition: true},
		},
	}

	out := captureLog(t, func() { d.checkInvariant(gf) })

	assert.NotContains(t, out, "INVARIANT VIOLATION")
}

func TestCheckInvariant_IgnoresCircuitBreaker(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-B", Status: GoalBlocked, BlockedBy: "convergence-circuit-breaker"},
		},
	}

	out := captureLog(t, func() { d.checkInvariant(gf) })

	assert.NotContains(t, out, "INVARIANT VIOLATION")
}

func TestCheckInvariant_IgnoresPendingDep(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalPending},
			{ID: "goal-B", Status: GoalBlocked, BlockedBy: "goal-A"},
		},
	}

	out := captureLog(t, func() { d.checkInvariant(gf) })

	assert.NotContains(t, out, "INVARIANT VIOLATION")
}

func TestCheckInvariant_QuietWhenClean(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalDone},
			{ID: "goal-B", Status: GoalPending},
		},
	}

	out := captureLog(t, func() { d.checkInvariant(gf) })

	assert.NotContains(t, out, "INVARIANT VIOLATION")
}

// --- checkStall -----------------------------------------------------------

func TestCheckStall_FiresAfterNIdleTicksWithRunnable(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalPending},
		},
	}

	var lines []string
	for i := 0; i < stallWatchdogTicks; i++ {
		out := captureLog(t, func() { d.checkStall(gf) })
		if strings.Contains(out, "STUCK:") {
			lines = append(lines, out)
		}
	}

	require.Len(t, lines, 1, "STUCK: must fire exactly once across N idle ticks")
	assert.Contains(t, lines[0], "goal-A")
	// Diagnostics only — state untouched.
	assert.Equal(t, GoalPending, gf.Goals[0].Status)
}

func TestCheckStall_ResetOnRunningGoal(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalRunning},
			{ID: "goal-B", Status: GoalPending},
		},
	}

	out := captureLog(t, func() {
		for i := 0; i < stallWatchdogTicks+2; i++ {
			d.checkStall(gf)
		}
	})

	assert.NotContains(t, out, "STUCK:")
	assert.Equal(t, 0, d.idleTicks)
}

func TestCheckStall_NoFireWhenNoCandidate(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalDone},
			{ID: "goal-B", Status: GoalBlocked},
		},
	}

	out := captureLog(t, func() {
		for i := 0; i < stallWatchdogTicks+2; i++ {
			d.checkStall(gf)
		}
	})

	assert.NotContains(t, out, "STUCK:")
	assert.Equal(t, 0, d.idleTicks)
}

func TestCheckStall_OncePerEpisode(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-A", Status: GoalPending},
		},
	}

	out := captureLog(t, func() {
		for i := 0; i < stallWatchdogTicks*2; i++ {
			d.checkStall(gf)
		}
	})

	assert.Equal(t, 1, strings.Count(out, "STUCK:"), "exactly one STUCK: per stall episode")
}

func TestCheckStall_ResetOnDispatch(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession

	// Accumulate idleTicks just below the firing threshold.
	gf := &GoalsFile{
		CurrentGoal: "goal-A",
		Goals:       []Goal{{ID: "goal-A", Status: GoalPending}},
	}
	for i := 0; i < stallWatchdogTicks-1; i++ {
		d.checkStall(gf)
	}
	require.Equal(t, stallWatchdogTicks-1, d.idleTicks)

	// A successful dispatch must reset the episode.
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-A")
	require.NoError(t, err)
	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	assert.Equal(t, 0, d.idleTicks)
	assert.False(t, d.stallReported)
}

func TestTick_InvariantAndStallRunPostReconcile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	out := captureLog(t, func() {
		require.NoError(t, d.tick(context.Background(), gf))
	})

	assert.NotContains(t, out, "INVARIANT VIOLATION")
	assert.NotContains(t, out, "STUCK:")
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	// A dispatching tick increments then resets within the same tick — net 0.
	assert.Equal(t, 0, d.idleTicks)
	assert.False(t, d.stallReported)
}

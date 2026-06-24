package taskvisor

import (
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// elaborationTestDaemon builds a Daemon whose window teardown is a no-op (the mock
// lists no windows, so killGoalWindows finds nothing to kill) with an injectable
// fixed clock — enough to exercise driveElaboratingGoals' pure state logic.
func elaborationTestDaemon(now time.Time) *Daemon {
	exec := new(testutil.MockTmuxExecutor)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	return &Daemon{
		executor: exec,
		session:  testSession,
		clock:    func() time.Time { return now },
		runtimes: map[string]*goalRuntime{},
	}
}

// TestDriveElaborating_Completion proves that when the elaborator has flipped the
// goal out of GoalRoadmap (to GoalPending via goal-edit), the episode ends: the
// elaborating runtime is cleared and no goal mutation is reported (the status flip
// itself is the elaborator's, already persisted).
func TestDriveElaborating_Completion(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	d := elaborationTestDaemon(now)
	d.runtimes["goal-001"] = &goalRuntime{phase: phaseElaborating, dispatchTime: now.Add(-time.Minute)}
	goals := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalPending}}} // elaborator already flipped it

	changed, err := d.driveElaboratingGoals(goals)
	require.NoError(t, err)
	assert.False(t, changed, "completion does not mutate goals.yaml (the flip was the elaborator's)")
	_, has := d.runtimes["goal-001"]
	assert.False(t, has, "completed elaboration clears the runtime")
}

// TestDriveElaborating_Timeout proves a goal still GoalRoadmap past elaborationTimeout
// is fail-safe blocked (blocked_by=elaboration-timeout) and its runtime cleared —
// the guard that makes wiring roadmap goals into the live loop safe.
func TestDriveElaborating_Timeout(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	d := elaborationTestDaemon(now)
	d.runtimes["goal-001"] = &goalRuntime{phase: phaseElaborating, dispatchTime: now.Add(-elaborationTimeout - time.Minute)}
	goals := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalRoadmap}}}

	changed, err := d.driveElaboratingGoals(goals)
	require.NoError(t, err)
	assert.True(t, changed, "timeout mutates the goal → caller persists")
	g, _ := goals.GoalByID("goal-001")
	assert.Equal(t, GoalBlocked, g.Status)
	assert.Equal(t, "elaboration-timeout", g.BlockedBy)
	_, has := d.runtimes["goal-001"]
	assert.False(t, has, "timed-out elaboration clears the runtime")
}

// TestDriveElaborating_InProgress proves a goal still GoalRoadmap within the timeout
// is left untouched (no mutation, runtime retained) so it is not re-dispatched.
func TestDriveElaborating_InProgress(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	d := elaborationTestDaemon(now)
	d.runtimes["goal-001"] = &goalRuntime{phase: phaseElaborating, dispatchTime: now.Add(-time.Minute)}
	goals := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalRoadmap}}}

	changed, err := d.driveElaboratingGoals(goals)
	require.NoError(t, err)
	assert.False(t, changed)
	g, _ := goals.GoalByID("goal-001")
	assert.Equal(t, GoalRoadmap, g.Status, "in-progress elaboration is untouched")
	rt, has := d.runtimes["goal-001"]
	require.True(t, has, "in-progress runtime is retained")
	assert.Equal(t, phaseElaborating, rt.phase)
}

// TestElaboratingGoalIDs proves the runtime-map read returns only phaseElaborating
// goals, sorted.
func TestElaboratingGoalIDs(t *testing.T) {
	d := &Daemon{runtimes: map[string]*goalRuntime{
		"goal-003": {phase: phaseElaborating},
		"goal-001": {phase: phaseSupervising},
		"goal-002": {phase: phaseElaborating},
	}}
	assert.Equal(t, []string{"goal-002", "goal-003"}, d.elaboratingGoalIDs())
}

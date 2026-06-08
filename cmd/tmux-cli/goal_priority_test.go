package main

import (
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPriorityGoal creates goal-001 in dir via the shared authoring core so the
// runtime `goal priority` tests have a real goals.yaml to mutate. Priority
// starts at the default 0.
func seedPriorityGoal(t *testing.T, dir string) {
	t.Helper()
	id, _, err := taskvisor.CreateGoal(dir, taskvisor.GoalSpec{
		Description: "Seed goal",
		Validate:    []string{"true"},
	})
	require.NoError(t, err)
	require.Equal(t, "goal-001", id)
}

func TestRunTaskvisorGoalPriority_SetsAndPersists(t *testing.T) {
	withTempCwd(t, func(dir string) {
		seedPriorityGoal(t, dir)

		captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalPriority(nil, []string{"goal-001", "7"}))
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		g, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		assert.Equal(t, 7, g.Priority)
	})
}

func TestRunTaskvisorGoalPriority_Negative(t *testing.T) {
	withTempCwd(t, func(dir string) {
		seedPriorityGoal(t, dir)

		captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalPriority(nil, []string{"goal-001", "-3"}))
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		g, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		assert.Equal(t, -3, g.Priority, "negative priority persists with no clamping")
	})
}

func TestRunTaskvisorGoalPriority_NonInt(t *testing.T) {
	withTempCwd(t, func(dir string) {
		seedPriorityGoal(t, dir)

		err := runTaskvisorGoalPriority(nil, []string{"goal-001", "abc"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid priority value "abc"`)

		// Parse fails BEFORE the lock/load — goal-001's priority is untouched.
		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		g, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		assert.Equal(t, 0, g.Priority)
	})
}

func TestRunTaskvisorGoalPriority_NotFound(t *testing.T) {
	withTempCwd(t, func(dir string) {
		seedPriorityGoal(t, dir)

		err := runTaskvisorGoalPriority(nil, []string{"goal-999", "5"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "goal not found: goal-999")
	})
}

func TestRunTaskvisorGoalPriority_NoGoalsFile(t *testing.T) {
	withTempCwd(t, func(dir string) {
		// No seed: goals.yaml is absent, so LoadGoals returns (nil, nil) and the
		// handler treats it as not found exactly like runTaskvisorGoalReset.
		err := runTaskvisorGoalPriority(nil, []string{"goal-001", "5"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "goal not found")
	})
}

func TestRunTaskvisorGoalAdd_PriorityFlag(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Prioritized goal"
		goalValidate = []string{"true"}
		goalPriority = 4

		captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, gf.Goals, 1)
		assert.Equal(t, 4, gf.Goals[0].Priority, "--priority flag threads into GoalSpec")
	})
}

func TestRunTaskvisorGoalAdd_NoPriorityFlagDefaultsZero(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Default priority goal"
		goalValidate = []string{"true"}

		captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, gf.Goals, 1)
		assert.Equal(t, 0, gf.Goals[0].Priority, "omitted --priority defaults to 0")
	})
}

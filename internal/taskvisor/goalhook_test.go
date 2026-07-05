package taskvisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFireGoalTransitionHook_FiresWithEnv — a configured hook invokes the runner
// exactly once with the command plus the five transition vars layered on top of
// the os.Environ() base (so PATH reaches the hook).
func TestFireGoalTransitionHook_FiresWithEnv(t *testing.T) {
	d, _, _ := setupDaemon(t)
	d.goalTransitionHook = `tmux-cli notify-orchestrator "goal-$GOAL_ID $NEW_STATUS"`

	called := 0
	var gotCmd string
	var gotEnv []string
	d.SetGoalHookRunnerFunc(func(command string, env []string) {
		called++
		gotCmd = command
		gotEnv = env
	})

	d.fireGoalTransitionHook("goal-017", "running", "done", "validating", 1)

	require.Equal(t, 1, called, "runner must be invoked exactly once")
	assert.Equal(t, `tmux-cli notify-orchestrator "goal-$GOAL_ID $NEW_STATUS"`, gotCmd)
	assert.Contains(t, gotEnv, "GOAL_ID=goal-017")
	assert.Contains(t, gotEnv, "OLD_STATUS=running")
	assert.Contains(t, gotEnv, "NEW_STATUS=done")
	assert.Contains(t, gotEnv, "PHASE=validating")
	assert.Contains(t, gotEnv, "CYCLE=1")

	hasPath := false
	for _, e := range gotEnv {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	assert.True(t, hasPath, "os.Environ() base must be included so PATH reaches the hook")
}

// TestFireGoalTransitionHook_NoopWhenUnconfigured — an empty hook string (the
// zero value = disabled contract) never invokes the runner.
func TestFireGoalTransitionHook_NoopWhenUnconfigured(t *testing.T) {
	d, _, _ := setupDaemon(t)
	d.goalTransitionHook = ""

	called := 0
	d.SetGoalHookRunnerFunc(func(command string, env []string) { called++ })

	d.fireGoalTransitionHook("goal-017", "running", "done", "validating", 1)

	assert.Equal(t, 0, called, "unconfigured hook must never invoke the runner")
}

// TestTransition_FiresGoalTransitionHook — integration-style: drive a real
// running->failed transition (rerunValidationOnly exhausts the last
// ValidationRetries) and assert the injected fake captured NEW_STATUS matching
// the durably committed goal.Status.
func TestTransition_FiresGoalTransitionHook(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.goalTransitionHook = `echo hi`

	called := 0
	var gotNewStatus string
	d.SetGoalHookRunnerFunc(func(command string, env []string) {
		called++
		for _, e := range env {
			if strings.HasPrefix(e, "NEW_STATUS=") {
				gotNewStatus = strings.TrimPrefix(e, "NEW_STATUS=")
			}
		}
	})

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		routeGoal("goal-001", 2, 2, 1, 0),
		{ID: "goal-002", Description: "independent", Status: GoalPending},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, nil))

	require.Equal(t, GoalFailed, goal.Status, "exhausted validation budget hard-halts")
	require.GreaterOrEqual(t, called, 1, "hook must fire on the running->failed transition")
	assert.Equal(t, "failed", gotNewStatus)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, rg.Status, gotNewStatus, "hook NEW_STATUS matches the durable goal.Status")
}

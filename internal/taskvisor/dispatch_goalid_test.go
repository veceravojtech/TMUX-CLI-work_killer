package taskvisor

import (
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestDispatchRetry_ShipsGoalIDToSupervisor guards the goal-id handoff: the
// daemon writes the GLOBAL .tmux-cli/taskvisor-current-goal marker on EVERY
// dispatch (last-writer-wins), so a supervisor that re-derives its own goal
// from that marker can mis-resolve to another in-flight goal. The daemon knows
// goal.ID authoritatively at the re-dispatch seam, so it must SHIP that id as a
// leading token in the /tmux:supervisor command rather than emit a bare
// command that forces the supervisor to guess from the marker.
func TestDispatchRetry_ShipsGoalIDToSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-007",
		Goals: []Goal{
			{ID: "goal-007", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	writeGoalTasksYaml(t, dir, "goal-007", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")

	// Mirror setupDispatchMocks but capture every command sent to the new
	// supervisor window so we can assert on the exact /tmux:supervisor payload.
	// 5 kill lookups (killGoalWindows incl. plan-audit).
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(5)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@99", Name: "supervisor-007", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@99").Return("some output ❯ ", nil)

	var sent []string
	exec.On("SendMessage", testSession, "@99", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		sent = append(sent, args.String(2))
	})

	d.createWindowFn = mockCreateWindowFn("@99")

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	require.Len(t, sent, 1, "dispatchRetry should send exactly one command to the supervisor window")
	assert.Equal(t, "/tmux:supervisor goal-007", sent[0],
		"dispatchRetry must ship the goal id as a leading token so the supervisor never re-derives it from the stale global marker")
}

package taskvisor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// dispatch_supwin_marker_test.go — the daemon publishes the EXACT supervisor
// window name it computes (supervisorWindow(goal.ID, mg)) to a per-goal marker
// .tmux-cli/goals/<id>/supervisor-window, written from BOTH dispatch paths via
// one shared helper so they can never drift. The marker value is bound to the
// SAME supWin local already handed to createWindow, so marker == created window
// name by construction. dispatch()'s plan command additionally ships goal.ID so
// plan.xml gets an explicit goal binding.

func supwinMarkerPath(dir, goalID string) string {
	return filepath.Join(dir, ".tmux-cli", "goals", goalID, "supervisor-window")
}

// retrySupwinTasksYaml is a minimal per-goal tasks.yaml so dispatchRetry's
// resetTaskStatuses succeeds and does NOT fall back to the full dispatch() path.
const retrySupwinTasksYaml = `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx-supwin.md
`

func TestDispatch_WritesSupervisorWindowMarker_MaxGoals1(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-007",
		Goals: []Goal{
			{ID: "goal-007", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)

	_ = markerCaptureMocks(exec, "@99", "supervisor-007")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	assert.Equal(t, "supervisor-007", readMarker(t, supwinMarkerPath(dir, "goal-007")),
		"goal windows are always namespaced: marker == supervisorWindow(goal-007,1) == supervisor-007")
}

func TestDispatch_WritesSupervisorWindowMarker_MaxGoals3(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-007",
		Goals: []Goal{
			{ID: "goal-007", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	writeSettingsMaxGoals(t, dir, 3)
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)

	// temp dir has no .git → ensureWorktree falls back to base with zero git;
	// a no-op git runner makes that explicit.
	d.SetGitRunnerFunc((&fakeGitRunner{}).run)

	setupNamespacedDispatchMocks(exec, testSession, "supervisor-007", "@99")
	exec.On("CaptureWindowOutput", testSession, "@99").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@99", mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	assert.Equal(t, "supervisor-007", readMarker(t, supwinMarkerPath(dir, "goal-007")),
		"mg>1 marker must equal supervisorWindow(goal-007,3) == supervisor-007")
}

func TestDispatchRetry_WritesSupervisorWindowMarker_MaxGoals1(t *testing.T) {
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
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)
	// per-goal tasks.yaml + ctx so resetTaskStatuses does NOT fall back to dispatch().
	writeGoalTasksYaml(t, dir, "goal-007", retrySupwinTasksYaml)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-supwin.md", "# Task ctx")

	_ = markerCaptureMocks(exec, "@99", "supervisor-007")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	assert.Equal(t, "supervisor-007", readMarker(t, supwinMarkerPath(dir, "goal-007")),
		"retry: goal windows are always namespaced == supervisorWindow(goal-007,1) == supervisor-007")
}

func TestDispatchRetry_WritesSupervisorWindowMarker_MaxGoals3(t *testing.T) {
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
	writeSettingsMaxGoals(t, dir, 3)
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-007", retrySupwinTasksYaml)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-supwin.md", "# Task ctx")

	d.SetGitRunnerFunc((&fakeGitRunner{}).run)

	setupNamespacedDispatchMocks(exec, testSession, "supervisor-007", "@99")
	exec.On("CaptureWindowOutput", testSession, "@99").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@99", mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	assert.Equal(t, "supervisor-007", readMarker(t, supwinMarkerPath(dir, "goal-007")),
		"retry mg>1 marker must equal supervisorWindow(goal-007,3) == supervisor-007")
}

func TestDispatch_PlanCommandCarriesGoalID(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-007",
		Goals: []Goal{
			{ID: "goal-007", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)

	sent := markerCaptureMocks(exec, "@99", "supervisor-007")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "dispatch should send exactly one command to the supervisor window")
	dispatchPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-007", "dispatch.md")
	assert.Equal(t, "/tmux:plan "+dispatchPath+" goal-007", (*sent)[0],
		"dispatch must ship goal.ID as a trailing token so plan.xml gets an explicit goal binding")
	assert.True(t, strings.HasPrefix((*sent)[0], "/tmux:plan "))
	assert.True(t, strings.HasSuffix((*sent)[0], " goal-007"))
}

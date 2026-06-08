package taskvisor

import (
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// dispatch_failloud_test.go — P5 fixes #1 (call-site swaps to waitForPromptOrFail)
// and #2 (injectCorrections-failure fallback in dispatchRetry).
//
// Fix #1: a supervisor/validator window whose pane never shows ❯ must make the
// dispatch path RETURN an error (so it surfaces through tick() immediately)
// instead of shoving a command into an unready window and idle-hanging to the
// 1h dispatchTimeout. The proof is twofold: the call returns an error AND no
// command was ever sent into the window.
//
// Fix #2: when corrections cannot be injected on a retry, dispatchRetry must
// fall back to the full d.dispatch (re-plan) path — never re-run the UNCORRECTED
// spec via the /tmux:supervisor skip-planning route.

const failloudTasksYaml = `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx-failloud.md
`

// captureSends registers a SendMessage stub on the given window that records the
// commands it receives, so a test can assert NOTHING was sent into an unready
// window. Returns the backing slice.
func captureSends(exec *testutil.MockTmuxExecutor, winID string) *[]string {
	sent := &[]string{}
	exec.On("SendMessage", testSession, winID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		*sent = append(*sent, args.String(2))
	})
	return sent
}

func TestDispatch_PromptNeverArrives_ReturnsError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.clock = autoClock(time.Hour)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	empty := []tmux.WindowInfo{}
	claude := []tmux.WindowInfo{{TmuxWindowID: "@99", Name: "supervisor-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(empty, nil).Times(7) // 5 kills (killGoalWindows incl. plan-audit) + collect + gone
	exec.On("ListWindows", testSession).Return(claude, nil)         // waitClaudeBoot + findWindowByName
	exec.On("CaptureWindowOutput", testSession, "@99").Return("booting...", nil)
	sent := captureSends(exec, "@99")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	derr := d.dispatch(&gf.Goals[0], gf)

	require.Error(t, derr, "an unready supervisor window must make dispatch return, not proceed")
	assert.Contains(t, derr.Error(), "wait for supervisor prompt")
	assert.Empty(t, *sent, "no command may be sent into a window that never showed the prompt")
}

func TestDispatchRetry_PromptNeverArrives_ReturnsError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.clock = autoClock(time.Hour)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	// Valid per-goal tasks.yaml so resetTaskStatuses succeeds and we exercise the
	// retry path proper (not its reset-failure fallback to dispatch).
	writeGoalTasksYaml(t, dir, "goal-001", failloudTasksYaml)

	empty := []tmux.WindowInfo{}
	claude := []tmux.WindowInfo{{TmuxWindowID: "@99", Name: "supervisor-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(empty, nil).Times(7) // 5 kills (killGoalWindows incl. plan-audit) + collect + gone
	exec.On("ListWindows", testSession).Return(claude, nil)
	exec.On("CaptureWindowOutput", testSession, "@99").Return("booting...", nil)
	sent := captureSends(exec, "@99")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	derr := d.dispatchRetry(&gf.Goals[0], gf)

	require.Error(t, derr, "an unready supervisor window must make dispatchRetry return")
	assert.Contains(t, derr.Error(), "wait for supervisor prompt")
	assert.Empty(t, *sent, "no /tmux:supervisor command may be sent into an unready retry window")
}

func TestCreateValidatorAndSendPayload_PromptNeverArrives_ReturnsError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.clock = autoClock(time.Hour)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	claude := []tmux.WindowInfo{{TmuxWindowID: "@77", Name: "validator-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(claude, nil) // waitClaudeBoot + findWindowByName
	exec.On("CaptureWindowOutput", testSession, "@77").Return("booting...", nil)
	sent := captureSends(exec, "@77")
	d.SetWindowCreateFunc(mockCreateWindowFn("@77"))

	derr := d.createValidatorAndSendPayload(&gf.Goals[0])

	require.Error(t, derr, "an unready validator window must make createValidatorAndSendPayload return")
	assert.Contains(t, derr.Error(), "wait for prompt")
	assert.Empty(t, *sent, "no /tmux:investigate command may be sent into an unready validator window")
}

// TestDispatchRetry_InjectCorrectionsFails_FallsBackToFullDispatch — when a
// retry cannot proceed with the corrected spec (the per-goal tasks state is
// unreadable/malformed), the daemon must fall back to the FULL d.dispatch
// re-plan path (/tmux:plan), NEVER re-run the UNCORRECTED spec via the
// /tmux:supervisor skip-planning route.
//
// NOTE on coverage: resetTaskStatuses runs immediately before injectCorrections
// over the SAME per-goal tasks file, with a SUPERSET of injectCorrections'
// failure conditions (both ReadFile + yaml.Unmarshal it; reset additionally
// requires a tasks list). So a corrupt-tasks-state retry trips reset's
// (pre-existing) fallback first — injectCorrections' new fallback is the
// consistent defense-in-depth sibling for the TOCTOU/independent-failure case
// that file-state alone cannot deterministically isolate. This test asserts the
// SYSTEM-LEVEL acceptance criterion (corrupt retry state ⇒ full re-plan, never
// uncorrected re-run); see the report for the structural detail.
func TestDispatchRetry_InjectCorrectionsFails_FallsBackToFullDispatch(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	// Malformed per-goal tasks.yaml: corrections cannot be applied on top of it.
	writeGoalTasksYaml(t, dir, "goal-001", "{ this : is : not : valid ][ yaml")

	// The fallback runs the full d.dispatch, which succeeds here (window ready).
	sent := markerCaptureMocks(exec, "@99", "supervisor-001")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "fallback full dispatch sends exactly one command")
	assert.Contains(t, (*sent)[0], "/tmux:plan",
		"corrupt retry state must fall back to the full re-plan path (/tmux:plan)")
	assert.NotContains(t, (*sent)[0], "/tmux:supervisor",
		"the uncorrected spec must NEVER be re-run via the skip-planning route")
}

// TestDispatchRetry_InjectCorrectionsSucceeds_ContinuesRetryPath guards the
// happy retry path against the new fallback branch: with a valid per-goal tasks
// file, dispatchRetry must proceed to the /tmux:supervisor skip-planning route
// (NOT fall back to /tmux:plan).
func TestDispatchRetry_InjectCorrectionsSucceeds_ContinuesRetryPath(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-001", failloudTasksYaml)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-failloud.md", "# Task ctx")

	sent := markerCaptureMocks(exec, "@99", "supervisor-001")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "retry path sends exactly one command")
	assert.Contains(t, (*sent)[0], "/tmux:supervisor",
		"a clean retry proceeds to the skip-planning route, not a full re-plan")
	assert.Contains(t, (*sent)[0], "goal-001",
		"the retry ships goal.ID as the leading token")
}

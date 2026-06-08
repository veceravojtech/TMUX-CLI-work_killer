package taskvisor

import (
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// dispatch_test.go — goal-008 regression: BOTH re-dispatch kill sequences in
// dispatch.go (dispatch = Site A, dispatchRetry = Site B) MUST route through
// killGoalWindows so the plan-audit window (plan-audit-<ns>) is killed alongside
// the supervisor / validator / execute- / investigator- windows.
//
// Root cause (web project goal-009 daemon wedge, 2026-06-08): each site ran an
// inline 4-kill block that OMITTED the plan-audit window, while collectManagedNames
// (the very next line, which builds the wait-set waitWindowsGone then awaits)
// INCLUDES plan-audit. So waitWindowsGone blocked forever on a plan-audit window
// nobody killed — repeating "poll error: waitWindowsGone: timeout … [plan-audit-NNN]"
// and wedging the daemon permanently. killGoalWindows (windows.go) is the canonical
// 5-kill sequence (… + planAuditWindow), making kill-set ⊇ wait-set.
//
// Each test injects a LIVE plan-audit window during the kill phase and asserts
// KillWindow was invoked on it. On the OLD inline 4-kill code the 5th plan-audit
// kill never fires, so KillWindow(@PA) is never called and the assertion fails.

// planAuditKillMocks programs the ListWindows phasing for a (re-)dispatch whose
// kill phase must see — and kill — a live plan-audit window. The kill phase is the
// 5 killGoalWindows lookups (supervisor, execute-prefix, validator, inv-prefix,
// plan-audit), each returning the live plan-audit window; only the 5th
// (planAuditWindow) name-matches, so exactly one ClosePipePane+KillWindow on @PA.
// Then 2 empty lookups (collectManagedNames + waitWindowsGone) and an unbounded
// booted-supervisor window for waitClaudeBoot + waitForPrompt + the trailing
// notifySupervisor lookup. This mirrors markerCaptureMocks / dispatch_marker_test.go
// phasing, but seeds the plan-audit window into the kill phase.
func planAuditKillMocks(exec *testutil.MockTmuxExecutor, supWinID, supName string) {
	planAuditLive := []tmux.WindowInfo{{TmuxWindowID: "@PA", Name: "plan-audit-064"}}
	// 5 kill lookups (killGoalWindows incl. plan-audit) — each sees the live plan-audit window
	exec.On("ListWindows", testSession).Return(planAuditLive, nil).Times(5)
	// collectManagedNames + waitWindowsGone — plan-audit killed, nothing managed remains
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	// waitClaudeBoot + waitForPrompt + trailing notify lookup — booted supervisor window
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: supWinID, Name: supName, CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, supWinID).Return("some output ❯ ", nil)
	exec.On("ClosePipePane", testSession, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)
	exec.On("SendMessage", testSession, supWinID, mock.Anything).Return(nil)
}

// TestDispatch_KillsPlanAuditWindow — Site A (full dispatch). A live plan-audit
// window present at dispatch time must be killed by the killGoalWindows sweep, so
// the subsequent waitWindowsGone (whose wait-set includes plan-audit) cannot wedge.
func TestDispatch_KillsPlanAuditWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-064",
		Goals:       []Goal{{ID: "goal-064", Description: "site-A dispatch", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)

	planAuditKillMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatch(&gf.Goals[0], gf))

	exec.AssertCalled(t, "KillWindow", testSession, "@PA")
}

// TestDispatchRetry_KillsPlanAuditWindow — Site B (retry). The retry kill block
// must also route through killGoalWindows and kill the live plan-audit window.
// A per-goal tasks.yaml + NextDispatch=implementer keep us on the retry path
// (resetTaskStatuses succeeds, so dispatchRetry does not fall back to dispatch).
func TestDispatchRetry_KillsPlanAuditWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-064",
		Goals: []Goal{{
			ID: "goal-064", Description: "site-B retry", Status: GoalPending,
			MaxCodeRetries: 3, CodeRetries: 2,
			NextDispatch: dispatchImplementer,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-064", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx-064.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-064.md", "# Task ctx")

	planAuditKillMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	exec.AssertCalled(t, "KillWindow", testSession, "@PA")
}

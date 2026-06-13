package taskvisor

import (
	"context"
	"errors"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// taskresolve_test.go — TDD coverage for push-based backend task resolution
// (goal-032). The daemon PUSHes a mapped backend task's terminal state when its
// goal reaches a durable terminal state, then rewrites task-goals.yaml without
// the consumed entry. Best-effort: an API error/disabled leaves the mapping for
// /tmux:task-list reconcile (the fallback actor). Every test swaps the
// updateTaskStatusFn seam (mirroring submitReportFn) and restores it via defer.

func seedLedger(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, SaveTaskGoals(dir, &TaskGoalsFile{Mappings: []TaskGoalMapping{
		{TaskID: "51", GoalID: "goal-x", Title: "t", ClaimedAt: "2026-06-13T00:00:00Z"},
	}}))
}

// A done goal with a mapping and a live API: the task is PATCHed resolved with
// {goal_id, finished_at, summary, findings} and the entry is removed from disk.
func TestTaskResolveOnDone_ResolvesWithFindings(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.ctx = context.Background()
	seedLedger(t, dir)

	var (
		gotID, gotStatus string
		gotResolution    map[string]any
	)
	orig := updateTaskStatusFn
	defer func() { updateTaskStatusFn = orig }()
	updateTaskStatusFn = func(_ *Daemon, _ context.Context, id, status string, resolution map[string]any) error {
		gotID, gotStatus, gotResolution = id, status, resolution
		return nil
	}

	sig := &ValidatorSignal{Findings: []ValidationFinding{
		{Rule: "build", Status: "pass", OutputExcerpt: "ok"},
		{Rule: "grep", Status: "pass", Detail: "found"},
	}}
	goal := &Goal{ID: "goal-x", FinishedAt: "2026-06-13T01:00:00Z"}

	d.resolveTaskOnTerminal(goal, "resolved", doneResolution(goal, sig))

	assert.Equal(t, "51", gotID)
	assert.Equal(t, "resolved", gotStatus)
	require.NotNil(t, gotResolution)
	assert.Equal(t, "goal-x", gotResolution["goal_id"])
	assert.Equal(t, "2026-06-13T01:00:00Z", gotResolution["finished_at"])
	assert.NotEmpty(t, gotResolution["summary"])

	findings, ok := gotResolution["findings"].([]map[string]any)
	require.True(t, ok, "findings must be []map[string]any")
	require.Len(t, findings, 2)
	assert.Equal(t, "build", findings[0]["name"])
	assert.Equal(t, "pass", findings[0]["verdict"])
	assert.Equal(t, "ok", findings[0]["evidence"], "evidence prefers OutputExcerpt")
	assert.Equal(t, "found", findings[1]["evidence"], "evidence falls back to Detail")

	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, tgf)
	assert.Equal(t, -1, tgf.indexOf("goal-x"), "mapping removed after successful resolve")
}

// The failed path PATCHes status=failed and threads the failed_by reason; the
// mapping is removed on success.
func TestTaskResolveOnDone_FailedPath(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.ctx = context.Background()
	seedLedger(t, dir)

	var (
		gotStatus     string
		gotResolution map[string]any
	)
	orig := updateTaskStatusFn
	defer func() { updateTaskStatusFn = orig }()
	updateTaskStatusFn = func(_ *Daemon, _ context.Context, _, status string, resolution map[string]any) error {
		gotStatus, gotResolution = status, resolution
		return nil
	}

	goal := &Goal{ID: "goal-x", FailedBy: "validation-timeout"}
	d.resolveTaskOnTerminal(goal, "failed", failResolution(goal, "code-defect"))

	assert.Equal(t, "failed", gotStatus)
	assert.Equal(t, "validation-timeout", gotResolution["reason"], "failed_by wins over the class fallback")

	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	assert.Equal(t, -1, tgf.indexOf("goal-x"), "mapping removed after failed resolve")
}

// An API error must leave the mapping in place (reconcile fallback) and never
// propagate — resolveTaskOnTerminal returns nothing.
func TestTaskResolveOnDone_APIDownLeavesMapping(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.ctx = context.Background()
	seedLedger(t, dir)

	called := false
	orig := updateTaskStatusFn
	defer func() { updateTaskStatusFn = orig }()
	updateTaskStatusFn = func(_ *Daemon, _ context.Context, _, _ string, _ map[string]any) error {
		called = true
		return errors.New("boom")
	}

	goal := &Goal{ID: "goal-x", FinishedAt: "2026-06-13T01:00:00Z"}
	d.resolveTaskOnTerminal(goal, "resolved", doneResolution(goal, nil))

	assert.True(t, called, "the seam must be invoked even when it errors")
	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, tgf)
	assert.GreaterOrEqual(t, tgf.indexOf("goal-x"), 0, "API error leaves the mapping for reconcile")
}

// With reporting disabled (d.producer == nil) the real updateTaskStatus returns
// errReportingDisabled — no panic, no network, mapping untouched.
func TestTaskResolveOnDone_DisabledLeavesMapping(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.ctx = context.Background()
	require.Nil(t, d.producer, "setupDaemon leaves reporting disabled")
	seedLedger(t, dir)

	// Do NOT swap the seam — exercise the real (*Daemon).updateTaskStatus.
	goal := &Goal{ID: "goal-x", FinishedAt: "2026-06-13T01:00:00Z"}
	d.resolveTaskOnTerminal(goal, "resolved", doneResolution(goal, nil))

	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, tgf)
	assert.GreaterOrEqual(t, tgf.indexOf("goal-x"), 0, "disabled reporting leaves the mapping")
}

// No mapping for the goal (or absent ledger) → no PATCH, no error, no panic.
func TestTaskResolveOnDone_NoMappingNoop(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.ctx = context.Background()
	// Ledger present but with NO entry for goal-x.
	require.NoError(t, SaveTaskGoals(dir, &TaskGoalsFile{Mappings: []TaskGoalMapping{
		{TaskID: "99", GoalID: "goal-other", Title: "t", ClaimedAt: "2026-06-13T00:00:00Z"},
	}}))

	count := 0
	orig := updateTaskStatusFn
	defer func() { updateTaskStatusFn = orig }()
	updateTaskStatusFn = func(_ *Daemon, _ context.Context, _, _ string, _ map[string]any) error {
		count++
		return nil
	}

	goal := &Goal{ID: "goal-x", FinishedAt: "2026-06-13T01:00:00Z"}
	d.resolveTaskOnTerminal(goal, "resolved", doneResolution(goal, nil))
	assert.Equal(t, 0, count, "no mapping ⇒ no backend call")

	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, tgf.indexOf("goal-other"), 0, "unrelated mapping is preserved")

	// Absent ledger (fresh workDir, no task-goals.yaml) is also a clean no-op.
	d2, _, _ := setupDaemon(t)
	d2.ctx = context.Background()
	assert.NotPanics(t, func() {
		d2.resolveTaskOnTerminal(goal, "resolved", doneResolution(goal, nil))
	})
	assert.Equal(t, 0, count, "absent ledger ⇒ still no backend call")
}

// A validator PASS that the daemon gate downgrades (declared validate +
// scriptPassed=false, no validate.sh) takes the error/ops re-validate branch:
// the goal does NOT reach done and the resolver never fires, so no backend call
// is made and the mapping is preserved. Mirrors
// TestCheckValidatingPhase_DeclaredValidate_LLMPassNoScript_DowngradesAndRevalidates.
func TestTaskResolveOnDone_GateRejectedNeverResolves(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.ctx = context.Background()
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0

	require.NoError(t, SaveTaskGoals(dir, &TaskGoalsFile{Mappings: []TaskGoalMapping{
		{TaskID: "51", GoalID: "goal-001", Title: "t", ClaimedAt: "2026-06-13T00:00:00Z"},
	}}))

	goal := routeGoal("goal-001", 3, 2, 2, 0)
	goal.Validate = []string{"go test ./..."} // declares validate ⇒ gate armed
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{goal}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	d.runtime("goal-001").phase = phaseValidating

	// LLM validator returns pass; no validate.sh exists ⇒ scriptPassed stays false.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: "2026-06-13T14:35:00Z",
	}))

	count := 0
	orig := updateTaskStatusFn
	defer func() { updateTaskStatusFn = orig }()
	updateTaskStatusFn = func(_ *Daemon, _ context.Context, _, _ string, _ map[string]any) error {
		count++
		return nil
	}

	const valID = "@2"
	val := []tmux.WindowInfo{{TmuxWindowID: valID, Name: "validator-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(val, nil)
	exec.On("CaptureWindowOutput", testSession, valID).Return("❯ ", nil)
	exec.On("KillWindow", testSession, valID).Return(nil)
	exec.On("SendMessage", testSession, valID, mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn(valID))

	g := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(g, gf))

	assert.NotEqual(t, GoalDone, g.Status, "a gate-downgraded LLM pass must NOT reach done")
	assert.Equal(t, 0, count, "the resolver never fires from a gate-rejected pass")

	tgf, err := LoadTaskGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, tgf)
	assert.GreaterOrEqual(t, tgf.indexOf("goal-001"), 0, "gate-rejected pass leaves the mapping for reconcile")
}

package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// dispatch_marker_test.go — RC-D: explicit next-dispatch marker (F3).
//
// dispatchCandidate's legacy heuristic keys on codeBudgetConsumed
// (CodeRetries < MaxCodeRetries) — a STICKY historical fact. Once any code
// retry was ever burned, every later dispatch — including a spec-defect
// "bounce to generation" — took dispatchRetry (reuse tasks.yaml, SKIP
// planning), so the defective spec was re-executed verbatim and the planner
// never ran (observed live: test-project goal-064, 2026-06-04). The explicit
// Goal.NextDispatch marker (persisted in goals.yaml, set at the verdict seam,
// consumed/cleared on dispatch) overrides the heuristic so a planner bounce
// always reaches the planner.

// markerCaptureMocks mirrors setupDispatchMocks but captures every command
// sent to the new supervisor window so tests can assert which dispatch path
// (/tmux:plan = full dispatch vs /tmux:supervisor = retry) was chosen. The
// goal supervisor window is ALWAYS namespaced (supervisor-<ns>); supName names
// the window waitClaudeBoot/waitForPrompt must resolve for this goal.
func markerCaptureMocks(exec *testutil.MockTmuxExecutor, newWindowID, supName string) *[]string {
	// 5 kill lookups (killGoalWindows incl. plan-audit) + 1 collectManagedNames + 1 waitWindowsGone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
	// waitClaudeBoot: supervisor window up and running claude
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: newWindowID, Name: supName, CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, newWindowID).Return("some output ❯ ", nil)

	sent := &[]string{}
	exec.On("SendMessage", testSession, newWindowID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		*sent = append(*sent, args.String(2))
	})
	return sent
}

// markerGoalsFile builds a single-goal GoalsFile whose budgets are explicit
// live+Max pairs so a disk round-trip survives the LoadGoals zero-budget
// re-seed (the fixtures_test.go trap).
func markerGoalsFile(goal Goal) *GoalsFile {
	return &GoalsFile{CurrentGoal: goal.ID, Goals: []Goal{goal}}
}

const markerTasksYaml = `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx-marker.md
`

// TestDispatchCandidate_SpecBounceForcesFullDispatch is the RC-D regression:
// consumed code budget AND an existing per-goal tasks.yaml — the exact state
// where the sticky heuristic always chose dispatchRetry — must still route to
// a FULL dispatch (/tmux:plan, planner re-generation) when the spec-bounce
// marker NextDispatch=generation is set.
func TestDispatchCandidate_SpecBounceForcesFullDispatch(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "spec-bounced goal", Status: GoalPending,
		CodeRetries: 2, MaxCodeRetries: 3, // code budget consumed — sticky heuristic says retry
		SpecRetries: 1, MaxSpecRetries: 2,
		NextDispatch: dispatchGeneration,
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)

	sent := markerCaptureMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "dispatch should send exactly one command")
	assert.Contains(t, (*sent)[0], "/tmux:plan",
		"NextDispatch=generation must force the full-plan path regardless of consumed code budget")
	assert.NotContains(t, (*sent)[0], "/tmux:supervisor",
		"a spec bounce must never skip planning via dispatchRetry")
}

// TestDispatchCandidate_ImplementerMarkerUsesRetry: NextDispatch=implementer
// plus an existing tasks.yaml routes to dispatchRetry. The fixture has FULL
// code budget and zero legacy retries, so the legacy heuristic alone would
// have chosen a full dispatch — proving the marker, not the heuristic, drives
// the decision.
func TestDispatchCandidate_ImplementerMarkerUsesRetry(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "code-defect retry", Status: GoalPending,
		CodeRetries: 3, MaxCodeRetries: 3, // full budget — heuristic alone would full-dispatch
		NextDispatch: dispatchImplementer,
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")

	sent := markerCaptureMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "dispatchRetry should send exactly one command")
	assert.Equal(t, "/tmux:supervisor goal-064", (*sent)[0],
		"NextDispatch=implementer with an existing tasks.yaml must take the retry path")
}

// TestDispatchCandidate_EmptyMarkerLegacyHeuristic: legacy mid-flight
// goals.yaml entries carry no marker — the existing heuristic must reproduce
// today's behavior both ways (consumed budget + tasks.yaml → retry; fresh
// goal → full dispatch).
func TestDispatchCandidate_EmptyMarkerLegacyHeuristic(t *testing.T) {
	t.Run("budget consumed and tasks.yaml exists routes to retry", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "legacy consumed budget", Status: GoalPending,
			CodeRetries: 2, MaxCodeRetries: 3, // consumed
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)
		writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
		writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")

		sent := markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		require.Len(t, *sent, 1)
		assert.Equal(t, "/tmux:supervisor goal-064", (*sent)[0],
			"empty marker + consumed budget + tasks.yaml must keep today's retry route")
	})

	t.Run("fresh goal routes to full dispatch", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "legacy fresh goal", Status: GoalPending,
			CodeRetries: 3, MaxCodeRetries: 3, // full budget, Retries 0
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)
		// tasks.yaml present, so ONLY the untouched budget keeps this on the full path.
		writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)

		sent := markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		require.Len(t, *sent, 1)
		assert.Contains(t, (*sent)[0], "/tmux:plan",
			"empty marker + untouched budget must keep today's full-dispatch route")
	})
}

// TestDispatchMarker_ClearedOnDispatch: the marker is consume-once — after a
// dispatch (either path) goals.yaml on disk must carry no next_dispatch for
// the goal, so a stale marker can never leak into the next cycle's decision.
func TestDispatchMarker_ClearedOnDispatch(t *testing.T) {
	t.Run("full dispatch clears generation marker", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "bounced goal", Status: GoalPending,
			CodeRetries: 2, MaxCodeRetries: 3,
			NextDispatch: dispatchGeneration,
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)

		_ = markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		loaded, err := LoadGoals(dir)
		require.NoError(t, err)
		lg, ok := loaded.GoalByID("goal-064")
		require.True(t, ok)
		assert.Empty(t, lg.NextDispatch, "consumed marker must not survive the dispatch")

		raw, err := os.ReadFile(GoalsFilePath(dir))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "next_dispatch",
			"omitempty must drop the cleared marker from goals.yaml entirely")
	})

	t.Run("retry dispatch clears implementer marker", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "retried goal", Status: GoalPending,
			CodeRetries: 2, MaxCodeRetries: 3,
			NextDispatch: dispatchImplementer,
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)
		writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
		writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")

		_ = markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		loaded, err := LoadGoals(dir)
		require.NoError(t, err)
		lg, ok := loaded.GoalByID("goal-064")
		require.True(t, ok)
		assert.Empty(t, lg.NextDispatch, "consumed marker must not survive the retry dispatch")

		raw, err := os.ReadFile(GoalsFilePath(dir))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "next_dispatch")
	})
}

// TestResetGoal_ClearsNextDispatch extends the AGENTS.md "zero + re-seed"
// ResetGoal invariant: a reset goal starts fresh, so the routing marker is
// cleared alongside the live retry counters — the next dispatch falls back to
// the (fresh-goal) legacy heuristic, i.e. a full planner dispatch.
func TestResetGoal_ClearsNextDispatch(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:           "goal-001",
				Status:       GoalFailed,
				Retries:      2,
				NextDispatch: dispatchGeneration,
			},
		},
	}
	ok := gf.ResetGoal("goal-001")
	assert.True(t, ok)
	assert.Equal(t, GoalPending, gf.Goals[0].Status)
	assert.Empty(t, gf.Goals[0].NextDispatch, "a reset goal starts fresh — no routing marker")
}

// writeEscalationMd writes a goal-scoped escalation.md mirroring the supervisor's
// exact schema (see supervisor.xml <escalation-routing>): a "## Escalations"
// header, an "escalation_count: <N>" line, and one "- need: \"...\"" entry block
// per need. This is the marker the daemon's pendingPrereqEscalation gate reads.
func writeEscalationMd(t *testing.T, dir, goalID string, count int, needs []string) {
	t.Helper()
	goalDir, err := EnsureGoalDir(dir, goalID)
	require.NoError(t, err)
	var b strings.Builder
	b.WriteString("## Escalations\n")
	b.WriteString(fmt.Sprintf("escalation_count: %d\n", count))
	for i, n := range needs {
		b.WriteString(fmt.Sprintf("- need: %q\n  from: execute-1  cycle: %d\n", n, i+2))
	}
	p := filepath.Join(goalDir, "escalation.md")
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o644))
}

// TestPrereqEscalationRoutesThroughPlan is the task-159 regression: when an
// UNHANDLED prerequisite-goal escalation is pending (escalation.md count >
// Goal.EscalationCount with a concrete need), dispatchCandidate must route the
// next cycle through the full plan/generation path (/tmux:plan) instead of the
// skip-plan dispatchRetry (/tmux:supervisor) — even with NextDispatch=implementer
// and an existing tasks.yaml, the EXACT bug state that re-ran the doomed validate
// until code-exhaustion. Mirrors TestDispatchCandidate_ImplementerMarkerUsesRetry.
func TestPrereqEscalationRoutesThroughPlan(t *testing.T) {
	t.Run("unhandled escalation overrides implementer skip-plan route", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		// Exact bug state: code-defect implementer re-pend that WOULD skip-plan.
		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "green deliverable, doomed broad validate", Status: GoalPending,
			CodeRetries: 2, MaxCodeRetries: 3, // code budget consumed
			NextDispatch:    dispatchImplementer,
			EscalationCount: 0, // nothing wired yet — the file count is strictly greater
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)
		writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
		writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")
		writeEscalationMd(t, dir, "goal-064", 1, []string{"remediate pre-existing baseline debt"})

		sent := markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		require.Len(t, *sent, 1, "dispatch should send exactly one command")
		assert.Contains(t, (*sent)[0], "/tmux:plan",
			"a pending prerequisite escalation must force the full-plan path, not skip-plan retry")
		assert.NotContains(t, (*sent)[0], "/tmux:supervisor",
			"the doomed code-retry/skip-plan route must be suppressed while an escalation is unhandled")
	})

	t.Run("no escalation file keeps retry route", func(t *testing.T) {
		d, exec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive

		// Identical fixture WITHOUT escalation.md — the gate is dormant and the
		// existing implementer/skip-plan route is byte-identical to today.
		gf := markerGoalsFile(Goal{
			ID: "goal-064", Description: "code-defect retry", Status: GoalPending,
			CodeRetries: 2, MaxCodeRetries: 3,
			NextDispatch: dispatchImplementer,
		})
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-064")
		require.NoError(t, err)
		writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
		writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")

		sent := markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		require.Len(t, *sent, 1)
		assert.Equal(t, "/tmux:supervisor goal-064", (*sent)[0],
			"with no escalation.md the implementer marker must keep today's retry route")
	})
}

// TestBounceToGeneration_SetsGenerationMarker covers the marker's WRITE side
// at the spec-defect seam: a re-pending bounce must stamp
// NextDispatch=generation and persist it (it survives a daemon restart via
// goals.yaml, not daemon runtime).
func TestBounceToGeneration_SetsGenerationMarker(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "defective spec", Status: GoalRunning,
		CodeRetries: 2, MaxCodeRetries: 3,
		SpecRetries: 2, MaxSpecRetries: 2, // decrements to 1 → re-pend, not exhaustion
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)

	// nil signal = findingless bounce (timeout-synthesized) — the spec breaker
	// never fires on an empty signature set, so the re-pend tail is reached.
	require.NoError(t, d.bounceToGeneration(&gf.Goals[0], gf, nil))

	assert.Equal(t, GoalPending, gf.Goals[0].Status)
	assert.Equal(t, dispatchGeneration, gf.Goals[0].NextDispatch,
		"spec bounce must stamp the generation marker for the next dispatch")

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	lg, ok := loaded.GoalByID("goal-064")
	require.True(t, ok)
	assert.Equal(t, dispatchGeneration, lg.NextDispatch, "marker must persist in goals.yaml (restart-safe)")
}

// TestHandleFailedCycle_SetsImplementerMarker covers the marker's WRITE side
// at the code-defect seam: a re-pending failed cycle must stamp
// NextDispatch=implementer and persist it.
func TestHandleFailedCycle_SetsImplementerMarker(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "code defect", Status: GoalRunning,
		CodeRetries: 2, MaxCodeRetries: 3, // decrements to 1 → re-pend, not exhaustion
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)

	require.NoError(t, d.handleFailedCycle(&gf.Goals[0], gf, "tests failed", "code-defect"))

	assert.Equal(t, GoalPending, gf.Goals[0].Status)
	assert.Equal(t, dispatchImplementer, gf.Goals[0].NextDispatch,
		"code-defect re-pend must stamp the implementer marker for the next dispatch")

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	lg, ok := loaded.GoalByID("goal-064")
	require.True(t, ok)
	assert.Equal(t, dispatchImplementer, lg.NextDispatch, "marker must persist in goals.yaml (restart-safe)")
}

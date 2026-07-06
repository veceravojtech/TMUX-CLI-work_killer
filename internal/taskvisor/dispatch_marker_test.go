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

// TestDispatchCandidate_SpecBounceWithTasksYamlDowngradesToSpecRepair is the
// plan-once inversion of the former RC-D regression (backend task 490): a
// spec-bounce marker (NextDispatch=generation) with an existing per-goal
// tasks.yaml must NO LONGER re-run the full /tmux:plan planner. The first plan
// already produced tasks.yaml, so resolveDispatchKind downgrades DispatchPlan to
// DispatchSpecRepair, which routes the spec repair to /tmux:supervisor (which
// amends tasks.yaml in place, supervisor.xml step 1d) — never a second full plan.
// The RC-D guarantee (a spec bounce never diverts into the skip-plan dispatchRetry)
// survives: this still goes through dispatch()/DispatchSpecRepair, not
// dispatchRetry's DispatchImplement.
func TestDispatchCandidate_SpecBounceWithTasksYamlDowngradesToSpecRepair(t *testing.T) {
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
	assert.Contains(t, (*sent)[0], "/tmux:supervisor",
		"plan-once: a spec bounce with an existing tasks.yaml must route spec repair to the supervisor")
	assert.NotContains(t, (*sent)[0], "/tmux:plan",
		"plan-once: the full planner must never run a second time once tasks.yaml exists")
}

// TestDispatchCandidate_SpecBounceNoTasksYamlStillPlans is the plan-once sibling:
// a spec bounce (NextDispatch=generation) with NO per-goal tasks.yaml — a crashed
// or never-run first plan — stays retryable and still routes to the full /tmux:plan
// planner (the downgrade keys on artifact existence, not a dispatch counter).
func TestDispatchCandidate_SpecBounceNoTasksYamlStillPlans(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "spec-bounced goal, crashed first plan", Status: GoalPending,
		CodeRetries: 2, MaxCodeRetries: 3,
		SpecRetries: 1, MaxSpecRetries: 2,
		NextDispatch: dispatchGeneration,
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)
	// Deliberately DO NOT writeGoalTasksYaml — no plan artifact exists yet.

	sent := markerCaptureMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "dispatch should send exactly one command")
	assert.Contains(t, (*sent)[0], "/tmux:plan",
		"plan-once: with no tasks.yaml the first plan stays retryable — still a full /tmux:plan")
	assert.NotContains(t, (*sent)[0], "/tmux:supervisor",
		"a never-run first plan must not be downgraded to spec-repair")
}

// TestRecordPlanRunCounter covers the audit-only plan-run counter marker: it
// increments and persists .tmux-cli/goals/<id>/plan-runs across calls, returning
// the running count (1 then 2). The counter is observability + a defensive
// assertion; the plan-once guarantee itself is structural (resolveDispatchKind).
func TestRecordPlanRunCounter(t *testing.T) {
	d, _, dir := setupDaemon(t)

	if got := d.recordPlanRun("goal-070"); got != 1 {
		t.Fatalf("first recordPlanRun = %d, want 1", got)
	}
	markerPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-070", "plan-runs")
	raw, err := os.ReadFile(markerPath)
	require.NoError(t, err, "the marker must persist after the first run")
	assert.Equal(t, "1", strings.TrimSpace(string(raw)), "marker holds the running count")

	if got := d.recordPlanRun("goal-070"); got != 2 {
		t.Fatalf("second recordPlanRun = %d, want 2 (the >1 case WARN-logs)", got)
	}
	raw, err = os.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, "2", strings.TrimSpace(string(raw)), "marker persists the incremented count")
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

// TestDispatchCandidate_GatePhaseNeverRetriesToSupervisor is the regression for
// the live "no /tmux:gate was run" miss: a gate goal carrying BOTH an existing
// tasks.yaml (e.g. left by a pre-DispatchGate supervisor run) AND the
// implementer marker + consumed code budget — the exact state that forces every
// other phase into dispatchRetry's hardcoded /tmux:supervisor — must instead
// re-dispatch its dedicated /tmux:gate executor and NEVER the supervisor.
func TestDispatchCandidate_GatePhaseNeverRetriesToSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := markerGoalsFile(Goal{
		ID: "goal-001", Description: "env gate", Status: GoalPending, Phase: PhaseGate,
		CodeRetries: 2, MaxCodeRetries: 3, // code budget consumed — sticky heuristic says retry
		NextDispatch: dispatchImplementer, // marker that would force dispatchRetry for any other phase
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-001", markerTasksYaml) // stale tasks.yaml present

	sent := markerCaptureMocks(exec, "@99", "supervisor-001")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

	require.Len(t, *sent, 1, "gate dispatch should send exactly one command")
	assert.Equal(t, "/tmux:gate goal-001", (*sent)[0],
		"a gate goal must re-dispatch /tmux:gate even with a stale tasks.yaml + implementer marker")
	assert.NotContains(t, (*sent)[0], "/tmux:supervisor",
		"the gate phase must never divert into the supervisor-retry path")
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
		// A genuinely fresh goal has NO plan artifact yet: with untouched budget AND
		// no tasks.yaml the empty-marker heuristic routes to the full plan path, and
		// the plan-once guard leaves it a real DispatchPlan (no artifact to downgrade
		// against). (Pre-plan-once this wrote a valid tasks.yaml to prove the budget
		// tie-break; under plan-once a valid tasks.yaml would correctly downgrade to
		// spec-repair, so the "fresh goal → full dispatch" intent is now expressed by
		// the true no-artifact state — the budget-consumed+tasks.yaml→retry case is
		// covered by the sibling subtest above.)

		sent := markerCaptureMocks(exec, "@99", "supervisor-064")
		d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

		require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

		require.Len(t, *sent, 1)
		assert.Contains(t, (*sent)[0], "/tmux:plan",
			"empty marker + untouched budget + no artifact must keep today's full-dispatch route")
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

// TestPrereqEscalationRoutesThroughPlan is the task-159 regression, updated for
// plan-once (backend task 490): when an UNHANDLED prerequisite-goal escalation is
// pending (escalation.md count > Goal.EscalationCount with a concrete need),
// dispatchCandidate must still route the next cycle through dispatch() (the
// full-plan choke point), NEVER the doomed skip-plan dispatchRetry that re-ran the
// identical validate until code-exhaustion. The routing through dispatch() is
// preserved; what changed is dispatch()'s RESULT when a per-goal tasks.yaml already
// exists: resolveDispatchKind's plan-once guard downgrades the DispatchPlan the
// escalation route would have produced to DispatchSpecRepair, so the second full
// /tmux:plan is suppressed and spec repair routes to /tmux:supervisor (which runs
// the step-1d spec-repair intake / in-run micro-prerequisite resolution, goal-004).
// The crashed-first-plan variant (no tasks.yaml) still full-plans — see the sibling
// TestDispatchCandidate_SpecBounceNoTasksYamlStillPlans.
func TestPrereqEscalationRoutesThroughPlan(t *testing.T) {
	t.Run("unhandled escalation with tasks.yaml downgrades to spec-repair (plan-once), never doomed skip-plan re-run", func(t *testing.T) {
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
		assert.NotContains(t, (*sent)[0], "/tmux:plan",
			"plan-once: with an existing tasks.yaml the escalation route must NOT re-run the full planner a second time")
		assert.Contains(t, (*sent)[0], "/tmux:supervisor",
			"the escalation route now downgrades to spec-repair — dispatch() → /tmux:supervisor (step-1d intake), not a doomed skip-plan re-run")
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

// escEntry is one escalation.md "- need:" block, optionally carrying the
// supervisor-written `resolved:` continuation line (goal-004): a resolved block
// must NOT count toward pendingPrereqEscalation's `needs` so the gate can go
// dormant once every prerequisite was landed inside the supervising cycle.
type escEntry struct {
	need     string
	resolved bool
}

// writeEscalationMdResolved writes a goal-scoped escalation.md like
// writeEscalationMd but lets each need block optionally carry a
// `resolved: in-run (cycle N, commit deadbeef)` continuation line — the
// byte-exact marker the supervisor writes when it lands a prerequisite in-run.
// Kept as a separate helper (not a change to writeEscalationMd's signature) so
// the existing callers — TestPrereqEscalationRoutesThroughPlan at :351 — stay
// green.
func writeEscalationMdResolved(t *testing.T, dir, goalID string, count int, entries []escEntry) {
	t.Helper()
	goalDir, err := EnsureGoalDir(dir, goalID)
	require.NoError(t, err)
	var b strings.Builder
	b.WriteString("## Escalations\n")
	b.WriteString(fmt.Sprintf("escalation_count: %d\n", count))
	for i, e := range entries {
		b.WriteString(fmt.Sprintf("- need: %q\n  from: execute-1  cycle: %d\n", e.need, i+2))
		if e.resolved {
			b.WriteString(fmt.Sprintf("  resolved: in-run (cycle %d, commit deadbeef)\n", i+2))
		}
	}
	p := filepath.Join(goalDir, "escalation.md")
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o644))
}

// escalationMdPath mirrors pendingPrereqEscalation's path construction so parse
// tests read the exact file the daemon gate reads.
func escalationMdPath(dir, goalID string) string {
	return filepath.Join(dir, ".tmux-cli", "goals", goalID, "escalation.md")
}

// TestParseEscalationMd_SkipsResolvedEntry: a single concrete "- need:" block
// carrying a `resolved:` line must count toward escalation_count but NOT toward
// needs — so pendingPrereqEscalation's needs>0 signal self-clears.
func TestParseEscalationMd_SkipsResolvedEntry(t *testing.T) {
	dir := t.TempDir()
	writeEscalationMdResolved(t, dir, "goal-100", 1, []escEntry{
		{need: "JWT auth middleware goal: validate Bearer token", resolved: true},
	})
	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-100"))
	require.NoError(t, err)
	assert.Equal(t, 1, count, "escalation_count is still the supervisor-written monotonic value")
	assert.Equal(t, 0, needs, "a resolved block must not count toward needs")
}

// TestParseEscalationMd_MixedResolvedAndUnresolved: two concrete blocks where
// only the first carries `resolved:` — the resolved one is excluded, the
// unresolved one still counts.
func TestParseEscalationMd_MixedResolvedAndUnresolved(t *testing.T) {
	dir := t.TempDir()
	writeEscalationMdResolved(t, dir, "goal-101", 2, []escEntry{
		{need: "JWT auth middleware goal", resolved: true},
		{need: "rate-limit middleware goal", resolved: false},
	})
	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-101"))
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, 1, needs, "only the unresolved concrete block counts")
}

// TestParseEscalationMd_UnresolvedEntryStillCounts: a concrete "- need:" with no
// `resolved:` line counts exactly as today (backward-compat).
func TestParseEscalationMd_UnresolvedEntryStillCounts(t *testing.T) {
	dir := t.TempDir()
	writeEscalationMdResolved(t, dir, "goal-102", 1, []escEntry{
		{need: "JWT auth middleware goal", resolved: false},
	})
	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-102"))
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, 1, needs, "an unresolved concrete block counts, byte-identical to today")
}

// TestParseEscalationMd_ResolvedAtEOF: the final block carries `resolved:` before
// EOF with no trailing "- need:" — the post-loop flush must exclude it. First
// block is unresolved to prove the EOF flush excludes ONLY the resolved final
// block (not a blanket drop).
func TestParseEscalationMd_ResolvedAtEOF(t *testing.T) {
	dir := t.TempDir()
	writeEscalationMdResolved(t, dir, "goal-103", 2, []escEntry{
		{need: "rate-limit middleware goal", resolved: false},
		{need: "JWT auth middleware goal", resolved: true},
	})
	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-103"))
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, 1, needs, "the resolved final block must be excluded by the EOF flush")
}

// TestParseEscalationMd_ResolvedMarkerIgnoresParenthetical: ANY trimmed line
// beginning `resolved:` is the marker — the daemon must NOT hard-match the exact
// `in-run (cycle …, commit …)` parenthetical the supervisor happens to write.
func TestParseEscalationMd_ResolvedMarkerIgnoresParenthetical(t *testing.T) {
	dir := t.TempDir()
	goalDir, err := EnsureGoalDir(dir, "goal-104")
	require.NoError(t, err)
	// Hand-written escalation.md with a bare `resolved:` line (no parenthetical)
	// and tolerated leading indentation on the continuation line.
	raw := "## Escalations\n" +
		"escalation_count: 1\n" +
		"- need: \"JWT auth middleware goal\"\n" +
		"  from: execute-1  cycle: 2\n" +
		"    resolved: done\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "escalation.md"), []byte(raw), 0o644))

	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-104"))
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, 0, needs, "a bare `resolved:` line (no parenthetical, indented) still marks the block resolved")
}

// TestParseEscalationMd_MissingFileFailSafe: an absent path returns zero counts
// and a non-nil err so the caller (pendingPrereqEscalation) treats the gate as
// not-pending and never panics.
func TestParseEscalationMd_MissingFileFailSafe(t *testing.T) {
	dir := t.TempDir()
	count, needs, err := parseEscalationMd(escalationMdPath(dir, "goal-does-not-exist"))
	require.Error(t, err, "a missing file must return a non-nil err (fail-safe)")
	assert.Equal(t, 0, count)
	assert.Equal(t, 0, needs)
}

// TestPrereqEscalation_ResolvedEntryKeepsRetryRoute: the routing-level proof —
// an escalation.md whose every concrete entry is resolved makes needs==0, so
// pendingPrereqEscalation returns false and dispatchCandidate keeps today's
// implementer skip-plan route (/tmux:supervisor), NOT the full-plan bounce. This
// is what lets the gate go dormant once all escalations are resolved in-run.
func TestPrereqEscalation_ResolvedEntryKeepsRetryRoute(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	// Same fixture as TestPrereqEscalationRoutesThroughPlan's routing case: a
	// code-defect implementer re-pend that WOULD skip-plan, with the file count
	// strictly greater than EscalationCount — but every entry is resolved.
	gf := markerGoalsFile(Goal{
		ID: "goal-064", Description: "green deliverable, escalation resolved in-run", Status: GoalPending,
		CodeRetries: 2, MaxCodeRetries: 3,
		NextDispatch:    dispatchImplementer,
		EscalationCount: 0,
	})
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-064")
	require.NoError(t, err)
	writeGoalTasksYaml(t, dir, "goal-064", markerTasksYaml)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx-marker.md", "# Task ctx")
	writeEscalationMdResolved(t, dir, "goal-064", 1, []escEntry{
		{need: "remediate pre-existing baseline debt", resolved: true},
	})

	sent := markerCaptureMocks(exec, "@99", "supervisor-064")
	d.SetWindowCreateFunc(mockCreateWindowFn("@99"))

	require.NoError(t, d.dispatchCandidate(&gf.Goals[0], gf))

	require.Len(t, *sent, 1)
	assert.Equal(t, "/tmux:supervisor goal-064", (*sent)[0],
		"a fully-resolved escalation.md leaves needs==0 → gate dormant → today's retry route survives")
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

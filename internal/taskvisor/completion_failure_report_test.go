package taskvisor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests run with d.producer == nil (the dev/test default: no signing key
// is embedded, so producer.New returns nil). reportFailure is therefore a no-op
// on the wire, but reportFailedGoals' iteration / status-filter / dedup logic
// runs unchanged, and d.reportedFailures is the faithful, synchronous record of
// which terminally-failed goals were submitted (the map is marked iff
// reportFailure was invoked for that goal). Payload/category/severity assembly
// is verified directly against the pure buildFailedGoalReport helper, which
// composes execute-1's reporting.go helpers without any network.

func failedGoal(id string) Goal {
	return Goal{ID: id, Description: "do the thing", Status: GoalFailed}
}

// TestReportFailedGoals_StatusFilter is the table-driven status gate: only
// GoalFailed goals are reported; GoalDone (success AND SkipGoal), GoalBlocked,
// GoalPending and GoalRunning are never reported.
func TestReportFailedGoals_StatusFilter(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		reported bool
	}{
		{"failed is reported", GoalFailed, true},
		{"done is not reported", GoalDone, false},
		{"blocked is not reported", GoalBlocked, false},
		{"pending is not reported", GoalPending, false},
		{"running is not reported", GoalRunning, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := setupDaemon(t)
			gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: tc.status}}}

			d.reportFailedGoals(gf)

			assert.Equal(t, tc.reported, d.reportedFailures["goal-001"])
		})
	}
}

// TestReportFailedGoals_FailedGoalReportsOnce: a single GoalFailed goal with a
// validator signal present is recorded exactly once.
func TestReportFailedGoals_FailedGoalReportsOnce(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeFixtureSignal(t, dir, "goal-001", &ValidatorSignal{
		Verdict:  VerdictFail,
		Findings: []ValidationFinding{{Rule: "build", Status: VerdictFail, Detail: "compile error", Correction: "fix the import"}},
	})
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001")}}

	d.reportFailedGoals(gf)

	assert.True(t, d.reportedFailures["goal-001"])
	assert.Len(t, d.reportedFailures, 1)
}

// TestReportFailedGoals_SkipGoalNotReported: a goal killed via SkipGoal becomes
// GoalDone and is indistinguishable from a success — correctly NOT reported.
func TestReportFailedGoals_SkipGoalNotReported(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalRunning}}}
	require.True(t, gf.SkipGoal("goal-001"))
	require.Equal(t, GoalDone, gf.Goals[0].Status)

	d.reportFailedGoals(gf)

	assert.Empty(t, d.reportedFailures)
}

// TestReportFailedGoals_DedupAcrossInvocations: two reportFailedGoals calls over
// the same GoalFailed goal record it once total.
func TestReportFailedGoals_DedupAcrossInvocations(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001")}}

	d.reportFailedGoals(gf)
	require.True(t, d.reportedFailures["goal-001"])
	require.Len(t, d.reportedFailures, 1)

	// Second invocation must hit the dedup guard: no change to the set.
	d.reportFailedGoals(gf)
	assert.Len(t, d.reportedFailures, 1)
	assert.True(t, d.reportedFailures["goal-001"])
}

// TestReportFailedGoals_MultipleFailedGoals: two failed goals each recorded once.
func TestReportFailedGoals_MultipleFailedGoals(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001"), {ID: "goal-002", Status: GoalDone}, failedGoal("goal-003")}}

	d.reportFailedGoals(gf)

	assert.True(t, d.reportedFailures["goal-001"])
	assert.False(t, d.reportedFailures["goal-002"])
	assert.True(t, d.reportedFailures["goal-003"])
	assert.Len(t, d.reportedFailures, 2)
}

// TestReportFailedGoals_NilProducer: with reporting disabled (the test default)
// iterating over failed goals never panics; reportFailure is a silent no-op.
func TestReportFailedGoals_NilProducer(t *testing.T) {
	d, _, _ := setupDaemon(t)
	require.Nil(t, d.producer, "test env has no embedded key, so producer must be nil")
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001")}}

	assert.NotPanics(t, func() { d.reportFailedGoals(gf) })
	assert.True(t, d.reportedFailures["goal-001"])
}

// TestReportFailedGoals_MissingSignal: a failed goal with no signal.json is
// still reported; the report carries an empty proposedFix and empty verdict.
func TestReportFailedGoals_MissingSignal(t *testing.T) {
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001")}}

	require.NotPanics(t, func() { d.reportFailedGoals(gf) })
	assert.True(t, d.reportedFailures["goal-001"])

	// No signal on disk → empty-signal assembly.
	r := buildFailedGoalReport(&gf.Goals[0], nil)
	assert.Empty(t, r.proposedFix)
	assert.Empty(t, r.payload["verdict"])
}

// TestReportFailedGoals_LazyInit: reportedFailures starts nil on a fresh Daemon
// and is initialized on first use without a New() change.
func TestReportFailedGoals_LazyInit(t *testing.T) {
	d := &Daemon{} // no New(): reportedFailures is nil, producer is nil
	gf := &GoalsFile{Goals: []Goal{failedGoal("goal-001")}}

	assert.NotPanics(t, func() { d.reportFailedGoals(gf) })
	assert.True(t, d.reportedFailures["goal-001"])
}

// TestBuildFailedGoalReport_PayloadContents pins the assembled report contract:
// severity always critical, category inferred from the signal, payload carries
// the goal YAML + verdict + failed_by + cycle, proposedFix derives from the
// finding correction, and expectedGreenState derives from goal.Acceptance.
func TestBuildFailedGoalReport_PayloadContents(t *testing.T) {
	g := Goal{
		ID:          "goal-042",
		Description: "ship the widget",
		Acceptance:  []string{"go build passes", "tests green"},
		Status:      GoalFailed,
		FailedBy:    "code-defect",
	}
	sig := &ValidatorSignal{
		Verdict: VerdictFail,
		Findings: []ValidationFinding{
			{Rule: "build", Status: VerdictFail, Detail: "undefined symbol", Correction: "import the package", FailureClass: "code-defect", Owner: "implementer"},
		},
	}

	r := buildFailedGoalReport(&g, sig)

	assert.Equal(t, "critical", r.severity)
	assert.Equal(t, "execute", r.category) // inferred from code-defect / implementer
	assert.Contains(t, r.title, "goal-042")
	assert.Contains(t, r.description, "code-defect")
	assert.Equal(t, "import the package", r.proposedFix)
	assert.Equal(t, "go build passes; tests green", r.expected)

	assert.Equal(t, VerdictFail, r.payload["verdict"])
	assert.Equal(t, "code-defect", r.payload["failed_by"])
	assert.Equal(t, CurrentCycle(&g), r.payload["cycle"])
	yaml, _ := r.payload["goal"].(string)
	assert.Contains(t, yaml, "goal-042", "payload must carry the goal YAML")
	findings, _ := r.payload["findings"].(string)
	assert.Contains(t, findings, "undefined symbol")
}

// TestBuildFailedGoalReport_NilSignal: with no signal, severity stays critical,
// category defaults to execute, proposedFix is empty, verdict is empty.
func TestBuildFailedGoalReport_NilSignal(t *testing.T) {
	g := Goal{ID: "goal-007", Description: "x", Status: GoalFailed}

	r := buildFailedGoalReport(&g, nil)

	assert.Equal(t, "critical", r.severity)
	assert.Equal(t, "execute", r.category)
	assert.Empty(t, r.proposedFix)
	assert.Empty(t, r.payload["verdict"])
	assert.Equal(t, "", r.payload["failed_by"])
}

// --- Integration via deactivateOnCompletion -------------------------------

// TestDeactivateOnCompletion_ReportsFailedGoalsAfterSalvage: a resolved mixed
// set (Done + Failed) flows through every teardown guard, reports the failed
// goal exactly once, and still completes teardown (modeIdle). Mirrors
// TestDeactivateOnCompletion_AllResolved.
func TestDeactivateOnCompletion_ReportsFailedGoalsAfterSalvage(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalFailed},
			{ID: "goal-003", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.deactivateOnCompletion(gf))

	assert.Equal(t, modeIdle, d.mode, "teardown must still reach modeIdle")
	assert.True(t, d.reportedFailures["goal-002"], "the failed goal is reported")
	assert.False(t, d.reportedFailures["goal-001"])
	assert.False(t, d.reportedFailures["goal-003"])
	assert.Len(t, d.reportedFailures, 1)
}

// TestDeactivateOnCompletion_InGraceTimeoutNotReported: a timeout-synthesized
// failure still inside the salvage grace returns early — the report site is
// never reached, so the goal is NOT reported (it may yet salvage to GoalDone).
func TestDeactivateOnCompletion_InGraceTimeoutNotReported(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	fresh := time.Now().UTC().Format(time.RFC3339)
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed, FailedBy: "validation-timeout", FinishedAt: fresh},
		},
	}
	writeGoals(t, dir, gf)

	require.NoError(t, d.deactivateOnCompletion(gf))

	assert.NotEqual(t, modeIdle, d.mode, "salvage grace keeps the daemon active")
	assert.False(t, d.reportedFailures["goal-001"], "in-grace timeout goal must not be reported")
	assert.Empty(t, d.reportedFailures)
}

// TestDeactivateOnCompletion_FailedGoalReportDedupAcrossDeactivates: two
// deactivate passes over the same failed goal report it once total.
func TestDeactivateOnCompletion_FailedGoalReportDedupAcrossDeactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalFailed}}}
	writeGoals(t, dir, gf)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.deactivateOnCompletion(gf))
	require.True(t, d.reportedFailures["goal-001"])
	require.Len(t, d.reportedFailures, 1)

	d.mode = modeActive // simulate a later re-entry
	require.NoError(t, d.deactivateOnCompletion(gf))
	assert.Len(t, d.reportedFailures, 1, "no double-report across deactivations")
}

// guard against accidental literal drift in the title/description.
func TestBuildFailedGoalReport_TitleStable(t *testing.T) {
	g := Goal{ID: "goal-009", Status: GoalFailed}
	r := buildFailedGoalReport(&g, nil)
	assert.True(t, strings.HasPrefix(r.title, "Goal goal-009 failed"), "title=%q", r.title)
}

package taskvisor

import (
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the backend's NotBlank contract (web CreateTaskRequest DTO:
// proposedFix/expectedGreenState must never be blank) for EVERY daemon-built
// failure-report path. A blank field is a guaranteed 422 rejection, so each
// production call site (worker crash, breaker trip, invariant violation, stall
// watchdog, failed goal) must yield non-blank values, and buildRequest's
// choke-point backfill must catch any future call site that forgets.

// swapSubmitReport replaces the submitReportFn seam with fn for the test's
// duration, restoring the production default on cleanup. Mirrors
// swapCrashReporter (recovery_test.go): producer.Client is a concrete type with
// an unexported constructor, so the package-var seam is the only way to observe
// daemon-built submissions without a live backend.
func swapSubmitReport(t *testing.T, fn func(d *Daemon, req producer.TaskRequest, onResult func(error))) {
	t.Helper()
	orig := submitReportFn
	submitReportFn = fn
	t.Cleanup(func() { submitReportFn = orig })
}

// captureSubmittedRequests swaps the seam for a synchronous success recorder:
// every submitted request is appended to the returned slice and onResult(nil)
// fires inline (delivered), so tests assert request contents deterministically.
func captureSubmittedRequests(t *testing.T) *[]producer.TaskRequest {
	t.Helper()
	captured := &[]producer.TaskRequest{}
	swapSubmitReport(t, func(_ *Daemon, req producer.TaskRequest, onResult func(error)) {
		*captured = append(*captured, req)
		if onResult != nil {
			onResult(nil)
		}
	})
	return captured
}

// assertContractFields asserts the two backend NotBlank fields are non-blank.
func assertContractFields(t *testing.T, req producer.TaskRequest) {
	t.Helper()
	assert.NotEmpty(t, strings.TrimSpace(req.ProposedFix), "proposedFix must be non-blank (backend NotBlank)")
	assert.NotEmpty(t, strings.TrimSpace(req.ExpectedGreenState), "expectedGreenState must be non-blank (backend NotBlank)")
}

// TestReportContract_WorkerCrash: both recovery actions submit a request with
// non-blank contract fields; the terminal "fail" action points the consumer at
// the goal-reset remediation.
func TestReportContract_WorkerCrash(t *testing.T) {
	for _, action := range []string{"re-dispatch", "fail"} {
		t.Run(action, func(t *testing.T) {
			captured := captureSubmittedRequests(t)
			d, _, _ := setupDaemon(t)
			g := &Goal{ID: "goal-001", Description: "do the thing", Status: GoalRunning}

			d.reportWorkerCrash(g, 1, action, false, nil)

			require.Len(t, *captured, 1)
			assertContractFields(t, (*captured)[0])
			if action == "fail" {
				assert.Contains(t, (*captured)[0].ProposedFix, "taskvisor goal reset",
					"a terminally-failed crash report must name the reset remediation")
			}
		})
	}
}

// TestReportContract_BreakerTrip: a circuit-breaker trip submits non-blank
// contract fields whether or not the goal carries acceptance criteria; with
// acceptance present, expectedGreenState derives from it.
func TestReportContract_BreakerTrip(t *testing.T) {
	cases := []struct {
		name       string
		acceptance []string
	}{
		{"with acceptance", []string{"go build passes", "tests green"}},
		{"without acceptance", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captured := captureSubmittedRequests(t)
			d, _, _ := setupDaemon(t)
			g := Goal{ID: "goal-002", Description: "converge", Status: GoalBlocked, Acceptance: tc.acceptance}

			d.reportBreakerTrip(&g, "code", []string{"sig-a", "sig-b"}, 3, 3)

			require.Len(t, *captured, 1)
			assertContractFields(t, (*captured)[0])
			if tc.acceptance != nil {
				assert.Equal(t, "go build passes; tests green", (*captured)[0].ExpectedGreenState,
					"acceptance criteria win over the derived fallback")
			}
		})
	}
}

// TestReportContract_InvariantViolation: the Bug-A invariant report carries
// non-blank contract fields.
func TestReportContract_InvariantViolation(t *testing.T) {
	captured := captureSubmittedRequests(t)
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{
		{ID: "X", Status: GoalPending, BlockedBy: "Y"},
		{ID: "Y", Status: GoalDone},
	}}

	d.checkInvariant(gf)

	require.Len(t, *captured, 1)
	assertContractFields(t, (*captured)[0])
}

// TestReportContract_StallWatchdog: the idle-stall report (fires on reaching
// stallWatchdogTicks with a runnable candidate) carries non-blank contract fields.
func TestReportContract_StallWatchdog(t *testing.T) {
	captured := captureSubmittedRequests(t)
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalPending}}}

	for i := 0; i < stallWatchdogTicks; i++ {
		d.checkStall(gf)
	}

	require.Len(t, *captured, 1)
	assertContractFields(t, (*captured)[0])
}

// TestReportContract_FailedGoalNilSignal: a terminally-failed goal with no
// signal.json on disk and nil Acceptance — the worst case for derivable content —
// still submits non-blank contract fields, and the fix names the goal.
func TestReportContract_FailedGoalNilSignal(t *testing.T) {
	captured := captureSubmittedRequests(t)
	d, _, _ := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "do the thing", Status: GoalFailed}}}

	d.reportFailedGoals(gf)

	require.Len(t, *captured, 1)
	assertContractFields(t, (*captured)[0])
	assert.Contains(t, (*captured)[0].ProposedFix, "goal-001",
		"the fallback fix must name the failed goal")
}

// TestBuildRequest_BackfillsBlankFields: the choke-point safety net — a request
// built with NO options (a future call site that forgets the fields) leaves
// buildRequest with both contract fields backfilled from its own title/category.
func TestBuildRequest_BackfillsBlankFields(t *testing.T) {
	d := &Daemon{}
	req := d.buildRequest("general", "warning", "some failure title", "desc", nil)

	assertContractFields(t, req)
	assert.Contains(t, req.ProposedFix, "some failure title",
		"the backfilled fix is derived from the request's own title")
}

// TestBuildRequest_PreservesExplicitFields: the backfill only fills blanks —
// explicitly supplied fields pass through untouched.
func TestBuildRequest_PreservesExplicitFields(t *testing.T) {
	d := &Daemon{}
	req := d.buildRequest("general", "warning", "t", "d", nil,
		withProposedFix("explicit fix"),
		withExpectedGreenState("explicit green"))

	assert.Equal(t, "explicit fix", req.ProposedFix)
	assert.Equal(t, "explicit green", req.ExpectedGreenState)
}

package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportFailure_NilProducerIsNoOp pins the disabled-reporting contract: with
// d.producer == nil the method returns immediately, never panics, and spawns no
// submission goroutine (the early return precedes any go func()).
func TestReportFailure_NilProducerIsNoOp(t *testing.T) {
	d := &Daemon{} // producer == nil (reporting disabled)
	require.NotPanics(t, func() {
		d.reportFailure("execute", "warning", "title", "desc", map[string]any{"k": "v"})
	})
}

// TestBuildRequest_PopulatesFields covers the deterministic, network-free request
// assembly: every TaskRequest field, applied options, and SystemInfo collected
// with d.vcsRevision as the CLI version.
func TestBuildRequest_PopulatesFields(t *testing.T) {
	d := &Daemon{vcsRevision: "abc1234"}
	payload := map[string]any{"goal_id": "goal-009"}

	req := d.buildRequest("execute", "warning", "the title", "the description", payload,
		withProposedFix("do the fix"),
		withExpectedGreenState("tests green"))

	assert.Equal(t, "execute", req.Category)
	assert.Equal(t, "warning", req.Severity)
	assert.Equal(t, "the title", req.Title)
	assert.Equal(t, "the description", req.Description)
	assert.Equal(t, payload, req.Payload)
	assert.Equal(t, "do the fix", req.ProposedFix)
	assert.Equal(t, "tests green", req.ExpectedGreenState)
	assert.Equal(t, "abc1234", req.SystemInfo.CLIVersion)
}

// TestBuildRequest_NormalizesCategoryAndSeverity is defense-in-depth: an unknown
// category/severity coerces to general/info so an out-of-contract caller can never
// put an invalid enum on the wire.
func TestBuildRequest_NormalizesCategoryAndSeverity(t *testing.T) {
	d := &Daemon{vcsRevision: "v"}
	req := d.buildRequest("bogus", "nonsense", "t", "d", nil)
	assert.Equal(t, "general", req.Category)
	assert.Equal(t, "info", req.Severity)
}

func TestProposedFixFromSignal_NilSignal(t *testing.T) {
	assert.Equal(t, "", proposedFixFromSignal(nil))
}

func TestProposedFixFromSignal_NoCorrections(t *testing.T) {
	sig := &ValidatorSignal{Findings: []ValidationFinding{{Rule: "r1"}, {Rule: "r2"}}}
	assert.Equal(t, "", proposedFixFromSignal(sig))
}

// TestProposedFixFromSignal_FirstNonEmpty: when the first finding carries a
// correction, that one wins alone.
func TestProposedFixFromSignal_FirstNonEmpty(t *testing.T) {
	sig := &ValidatorSignal{Findings: []ValidationFinding{
		{Rule: "r1", Correction: "fix one"},
		{Rule: "r2", Correction: "fix two"},
	}}
	assert.Equal(t, "fix one", proposedFixFromSignal(sig))
}

// TestProposedFixFromSignal_JoinsMultiple: when the first finding has no
// correction, every non-empty correction is joined with newlines.
func TestProposedFixFromSignal_JoinsMultiple(t *testing.T) {
	sig := &ValidatorSignal{Findings: []ValidationFinding{
		{Rule: "r1"},
		{Rule: "r2", Correction: "fix two"},
		{Rule: "r3", Correction: "fix three"},
	}}
	assert.Equal(t, "fix two\nfix three", proposedFixFromSignal(sig))
}

func TestExpectedGreenState_JoinsAcceptance(t *testing.T) {
	g := Goal{Acceptance: []string{"a", "b", "c"}}
	assert.Equal(t, "a; b; c", expectedGreenState(g))
}

func TestExpectedGreenState_EmptyAcceptance(t *testing.T) {
	assert.Equal(t, "", expectedGreenState(Goal{}))
}

func TestGoalToYAML_RoundTrips(t *testing.T) {
	g := Goal{ID: "goal-009", Description: "demo"}
	out := goalToYAML(g)
	assert.Contains(t, out, "id:")
	assert.Contains(t, out, "goal-009")
}

func TestGoalToYAML_NeverPanics(t *testing.T) {
	require.NotPanics(t, func() { _ = goalToYAML(Goal{}) })
}

func TestInferCategory(t *testing.T) {
	tests := []struct {
		name string
		sig  *ValidatorSignal
		goal Goal
		want string
	}{
		{"code-defect class", &ValidatorSignal{Findings: []ValidationFinding{{FailureClass: "code-defect"}}}, Goal{}, "execute"},
		{"implementer owner", &ValidatorSignal{Owner: "implementer"}, Goal{}, "execute"},
		{"spec-defect class", &ValidatorSignal{Findings: []ValidationFinding{{FailureClass: "spec-defect"}}}, Goal{}, "plan"},
		{"planner owner", &ValidatorSignal{Owner: "planner"}, Goal{}, "plan"},
		{"ops owner", &ValidatorSignal{Owner: "ops"}, Goal{}, "general"},
		{"env-config class", &ValidatorSignal{Findings: []ValidationFinding{{FailureClass: "env-config"}}}, Goal{}, "general"},
		{"infra-flake class", &ValidatorSignal{Findings: []ValidationFinding{{FailureClass: "infra-flake"}}}, Goal{}, "general"},
		{"validator-error class", &ValidatorSignal{Findings: []ValidationFinding{{FailureClass: "validator-error"}}}, Goal{}, "validator"},
		{"nil signal, NextDispatch generation", nil, Goal{NextDispatch: dispatchGeneration}, "plan"},
		{"nil signal, NextDispatch implementer", nil, Goal{NextDispatch: dispatchImplementer}, "execute"},
		{"nil signal, empty NextDispatch", nil, Goal{}, "execute"},
		{"empty signal, empty goal", &ValidatorSignal{}, Goal{}, "execute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, inferCategory(tt.sig, tt.goal))
		})
	}
}

func TestNormalizeCategory_UnknownToGeneral(t *testing.T) {
	assert.Equal(t, "general", normalizeCategory("bogus"))
	assert.Equal(t, "general", normalizeCategory(""))
	for _, c := range []string{"plan", "supervisor", "validator", "execute", "general"} {
		assert.Equal(t, c, normalizeCategory(c))
	}
}

func TestNormalizeSeverity_UnknownToInfo(t *testing.T) {
	assert.Equal(t, "info", normalizeSeverity("bogus"))
	assert.Equal(t, "info", normalizeSeverity(""))
	for _, s := range []string{"critical", "warning", "info"} {
		assert.Equal(t, s, normalizeSeverity(s))
	}
}

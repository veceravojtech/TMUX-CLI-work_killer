package taskvisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSignalDir(t *testing.T, root, goalID string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli", "goals", goalID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
}

func TestLoadSignal_Missing(t *testing.T) {
	root := t.TempDir()
	sig, err := LoadSignal(root, "goal-001")
	assert.Nil(t, sig)
	assert.NoError(t, err)
}

func TestLoadSignal_Supervisor(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"supervisor","status":"done","timestamp":"2026-05-20T14:30:00Z"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	ss, ok := sig.(*SupervisorSignal)
	require.True(t, ok, "expected *SupervisorSignal, got %T", sig)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, "done", ss.Status)
	assert.Equal(t, "2026-05-20T14:30:00Z", ss.Timestamp)
}

func TestLoadSignal_Validator(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data, err := json.Marshal(map[string]any{
		"source":      "validator",
		"verdict":     "fail",
		"findings":    []map[string]string{{"rule": "price check", "status": "fail", "detail": "mismatch"}},
		"next_action": "fix prices",
		"timestamp":   "2026-05-20T14:35:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), data, 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", sig)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, "fail", vs.Verdict)
	assert.Equal(t, "fix prices", vs.NextAction)
	assert.Equal(t, "2026-05-20T14:35:00Z", vs.Timestamp)
	require.Len(t, vs.Findings, 1)
	assert.Equal(t, "price check", vs.Findings[0].Rule)
	assert.Equal(t, "fail", vs.Findings[0].Status)
	assert.Equal(t, "mismatch", vs.Findings[0].Detail)
}

func TestLoadSignal_UnknownSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"foo","status":"done"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	assert.Nil(t, sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "foo")
}

func TestSaveSupervisorSignal(t *testing.T) {
	root := t.TempDir()
	sig := &SupervisorSignal{
		Status:    "done",
		Timestamp: "2026-05-20T14:30:00Z",
	}
	err := SaveSupervisorSignal(root, "goal-001", sig)
	require.NoError(t, err)

	assert.Equal(t, "supervisor", sig.Source)

	data, err := os.ReadFile(SignalPath(root, "goal-001"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "supervisor", raw["source"])
	assert.Equal(t, "done", raw["status"])

	tmpPath := SignalPath(root, "goal-001") + ".tmp"
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain")
}

func TestSaveValidatorSignal(t *testing.T) {
	root := t.TempDir()
	sig := &ValidatorSignal{
		Verdict: "pass",
		Findings: []ValidationFinding{
			{Rule: "price check", Status: "pass", Detail: "matched"},
		},
		NextAction: "",
		Timestamp:  "2026-05-20T14:35:00Z",
	}
	err := SaveValidatorSignal(root, "goal-001", sig)
	require.NoError(t, err)

	assert.Equal(t, "validator", sig.Source)

	data, err := os.ReadFile(SignalPath(root, "goal-001"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "validator", raw["source"])
	assert.Equal(t, "pass", raw["verdict"])

	findings, ok := raw["findings"].([]any)
	require.True(t, ok)
	require.Len(t, findings, 1)
}

func TestSignal_Roundtrip(t *testing.T) {
	root := t.TempDir()
	original := &SupervisorSignal{
		Status:    "stopped",
		Timestamp: "2026-05-20T15:00:00Z",
	}
	require.NoError(t, SaveSupervisorSignal(root, "goal-002", original))

	loaded, err := LoadSignal(root, "goal-002")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	ss, ok := loaded.(*SupervisorSignal)
	require.True(t, ok)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, original.Status, ss.Status)
	assert.Equal(t, original.Timestamp, ss.Timestamp)
}

func TestReadSignal_SupervisorSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"supervisor","status":"done","timestamp":"2026-05-20T14:30:00Z"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	ss, ok := sig.(*SupervisorSignal)
	require.True(t, ok)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, "done", ss.Status)
}

func TestReadSignal_ValidatorSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data, err := json.Marshal(map[string]any{
		"source":      "validator",
		"verdict":     "fail",
		"next_action": "fix X",
		"findings":    []map[string]string{},
		"timestamp":   "2026-05-20T14:35:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), data, 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, "fail", vs.Verdict)
	assert.Equal(t, "fix X", vs.NextAction)
}

func TestReadSignal_FileNotFound(t *testing.T) {
	root := t.TempDir()
	sig, err := LoadSignal(root, "goal-999")
	assert.Nil(t, sig)
	assert.NoError(t, err)
}

func TestDeleteSignal(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	require.NoError(t, SaveSupervisorSignal(root, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	err = DeleteSignal(root, "goal-001")
	require.NoError(t, err)

	sig, err = LoadSignal(root, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestSignal_RoundtripValidator(t *testing.T) {
	root := t.TempDir()
	original := &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{Rule: "api test", Status: "fail", Detail: "404 error", Correction: "fix endpoint"},
			{Rule: "ui check", Status: "pass", Detail: "looks good"},
		},
		NextAction: "fix the API endpoint",
		Timestamp:  "2026-05-20T15:05:00Z",
	}
	require.NoError(t, SaveValidatorSignal(root, "goal-003", original))

	loaded, err := LoadSignal(root, "goal-003")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	vs, ok := loaded.(*ValidatorSignal)
	require.True(t, ok)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, original.Verdict, vs.Verdict)
	assert.Equal(t, original.NextAction, vs.NextAction)
	assert.Equal(t, original.Timestamp, vs.Timestamp)
	require.Len(t, vs.Findings, 2)
	assert.Equal(t, original.Findings[0], vs.Findings[0])
	assert.Equal(t, original.Findings[1], vs.Findings[1])
}

// --- C6 convergence circuit-breaker: signature primitives ---

func TestNormalizeFailureCause_CollapsesVariableParts(t *testing.T) {
	c1 := "task failed at 2026-06-01T14:30:00Z reading /var/log/app.log pid 1234"
	c2 := "task failed at 2026-05-20T09:15:42Z reading /tmp/other/app.log pid 9999"
	assert.Equal(t, NormalizeFailureCause(c1), NormalizeFailureCause(c2),
		"causes differing only in TS/PATH/PID must normalize identically")
}

func TestNormalizeFailureCause_DistinctCausesDiverge(t *testing.T) {
	c1 := "connection refused on /var/run/db.sock"
	c2 := "permission denied on /var/run/db.sock"
	assert.NotEqual(t, NormalizeFailureCause(c1), NormalizeFailureCause(c2),
		"genuinely distinct causes sharing a path must stay distinct")
}

func TestNormalizeFailureCause_ExtractsCodeBeforePathStrip(t *testing.T) {
	got := NormalizeFailureCause("open /tmp/exit-137.log failed")
	assert.Contains(t, got, "CODE=EXIT_137", "code embedded in path is captured first")
	assert.Contains(t, got, "<PATH>", "the path is still stripped to a distinct token")
}

func TestNormalizeFailureCause_DistinctTokens(t *testing.T) {
	got := NormalizeFailureCause("worker /opt/app/bin crashed pid=4242")
	assert.Contains(t, got, "<PATH>", "path token present")
	assert.Contains(t, got, "<PID>", "pid token present")
	assert.NotContains(t, got, "/opt/app/bin")
	assert.NotContains(t, got, "4242")
}

func TestFailureSignature_AntiCollision(t *testing.T) {
	a := ValidationFinding{Rule: "alpha", FailureClass: "beta", Status: "fail", Detail: "some cause"}
	b := ValidationFinding{Rule: "beta", FailureClass: "alpha", Status: "fail", Detail: "some cause"}
	c := ValidationFinding{Rule: "gamma", FailureClass: "delta", Status: "fail", Detail: "other cause"}

	sa, sb, sc := failureSignature(a), failureSignature(b), failureSignature(c)
	assert.NotEqual(t, sa, sb, "swapping rule/failure_class yields a different hash (NUL-separator proof)")
	assert.NotEqual(t, sa, sc, "distinct triples yield distinct hashes")
	assert.NotEqual(t, sb, sc, "distinct triples yield distinct hashes")
	assert.Equal(t, sa, failureSignature(a), "deterministic: identical input -> identical hash")
}

func TestComputeSignatures_SortedDeterministic(t *testing.T) {
	f1 := ValidationFinding{Rule: "r1", Status: "fail", Detail: "cause one"}
	f2 := ValidationFinding{Rule: "r2", Status: "fail", Detail: "cause two"}
	fp := ValidationFinding{Rule: "r3", Status: "pass", Detail: "ok"}

	got1 := ComputeSignatures([]ValidationFinding{f1, f2, fp})
	got2 := ComputeSignatures([]ValidationFinding{fp, f2, f1})
	assert.Equal(t, got1, got2, "order-independent: same findings any order -> identical sorted slice")
	assert.Len(t, got1, 2, "pass-status findings are excluded")
	assert.True(t, sort.StringsAreSorted(got1), "result is sorted ascending")
}

// --- C10 incremental re-validation ---------------------------------------

func TestComputeInputFingerprint_Stability(t *testing.T) {
	f := ValidationFinding{
		Rule:          "pricing correct",
		Scope:         []string{"internal/pricing/calc.go", "internal/pricing/rules.go"},
		Preconditions: []string{"env:DATABASE_URL"},
	}
	// Same finding, changedFiles supplied in two different orders.
	h1 := ComputeInputFingerprint(f, []string{"internal/pricing/calc.go", "internal/pricing/rules.go"})
	h2 := ComputeInputFingerprint(f, []string{"internal/pricing/rules.go", "internal/pricing/calc.go"})
	assert.Equal(t, h1, h2, "shuffled changedFiles must yield an identical hash (sort + determinism)")
	assert.Equal(t, h1, ComputeInputFingerprint(f, []string{"internal/pricing/calc.go", "internal/pricing/rules.go"}), "deterministic across calls")
}

func TestComputeInputFingerprint_FileChange(t *testing.T) {
	f := ValidationFinding{
		Rule:  "pricing correct",
		Scope: []string{"internal/pricing/calc.go"},
	}
	base := ComputeInputFingerprint(f, nil)
	inScope := ComputeInputFingerprint(f, []string{"internal/pricing/calc.go"})
	outOfScope := ComputeInputFingerprint(f, []string{"cmd/other/main.go"})

	assert.NotEqual(t, base, inScope, "a touched in-scope file must change the hash (regression ⇒ RE-RUN)")
	assert.Equal(t, base, outOfScope, "an out-of-scope changed file must NOT change the hash")
}

func TestComputeInputFingerprint_PreconditionChange(t *testing.T) {
	f1 := ValidationFinding{Rule: "db reachable", Scope: []string{"internal/db/conn.go"}, Preconditions: []string{"env:DATABASE_URL"}}
	f2 := f1
	f2.Preconditions = []string{"env:DATABASE_URL", "service:localhost:5432"}

	assert.NotEqual(t,
		ComputeInputFingerprint(f1, nil),
		ComputeInputFingerprint(f2, nil),
		"adding a precondition must change the hash")
}

func TestPlanRevalidation_MixedStatuses(t *testing.T) {
	findings := []ValidationFinding{
		{Rule: "alpha", Scope: []string{"a.go"}},
		{Rule: "beta", Scope: []string{"b.go"}},
		{Rule: "gamma", Scope: []string{"c.go"}},
	}

	// nil prev ⇒ every finding RERUN.
	for _, p := range PlanRevalidation(nil, findings, nil, false, false) {
		assert.Equal(t, ActionRerun, p.Action, "nil prev forces RERUN for %s", p.FindingID)
	}

	// force-full and final-cycle ⇒ every finding RERUN even with a matching ledger.
	fpAlpha := ComputeInputFingerprint(findings[0], nil)
	prev := &Results{Results: map[string]ResultEntry{
		"alpha": {FindingID: "alpha", Status: VerdictPass, InputFingerprint: fpAlpha, CycleNumber: 1},
	}}
	for _, p := range PlanRevalidation(prev, findings, nil, true, false) {
		assert.Equal(t, ActionRerun, p.Action, "force-full forces RERUN for %s", p.FindingID)
	}
	for _, p := range PlanRevalidation(prev, findings, nil, false, true) {
		assert.Equal(t, ActionRerun, p.Action, "final-cycle forces RERUN for %s", p.FindingID)
	}

	// Mixed state: alpha prior pass unchanged ⇒ REUSE; beta prior fail ⇒ RERUN;
	// gamma prior pass but in-scope file changed ⇒ RERUN.
	fpBeta := ComputeInputFingerprint(findings[1], nil)
	fpGamma := ComputeInputFingerprint(findings[2], nil)
	prev = &Results{Results: map[string]ResultEntry{
		"alpha": {FindingID: "alpha", Status: VerdictPass, InputFingerprint: fpAlpha, CycleNumber: 2},
		"beta":  {FindingID: "beta", Status: VerdictFail, InputFingerprint: fpBeta, CycleNumber: 2},
		"gamma": {FindingID: "gamma", Status: VerdictPass, InputFingerprint: fpGamma, CycleNumber: 2},
	}}
	plans := PlanRevalidation(prev, findings, []string{"c.go"}, false, false)
	byID := map[string]FindingPlan{}
	for _, p := range plans {
		byID[p.FindingID] = p
	}
	assert.Equal(t, ActionReuse, byID["alpha"].Action, "unchanged prior pass ⇒ REUSE")
	assert.Equal(t, 2, byID["alpha"].ReusedFromCycle, "REUSE carries prior cycle number")
	assert.Equal(t, ActionRerun, byID["beta"].Action, "prior fail ⇒ RERUN")
	assert.Equal(t, ActionRerun, byID["gamma"].Action, "in-scope change ⇒ RERUN")
	// Deterministic sort by finding id.
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, []string{plans[0].FindingID, plans[1].FindingID, plans[2].FindingID})
}

func TestResults_Roundtrip(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")

	// Absent file ⇒ nil, no error.
	got, err := LoadResults(root, "goal-001")
	require.NoError(t, err)
	assert.Nil(t, got, "absent results.json returns nil with no error")

	want := &Results{Results: map[string]ResultEntry{
		"alpha": {FindingID: "alpha", Status: VerdictPass, InputFingerprint: "abc", CycleNumber: 1},
		"beta": {FindingID: "beta", Status: VerdictPass, InputFingerprint: "def", CycleNumber: 2,
			ReusedFromCycle: 1, ReusedFingerprint: "def"},
	}}
	require.NoError(t, SaveResults(root, "goal-001", want))

	got, err = LoadResults(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.Results, got.Results, "roundtrip preserves all entries including reuse fields")

	// Corrupt file ⇒ safe degrade to nil, no error.
	require.NoError(t, os.WriteFile(ResultsPath(root, "goal-001"), []byte("{not json"), 0o644))
	got, err = LoadResults(root, "goal-001")
	require.NoError(t, err)
	assert.Nil(t, got, "corrupt results.json degrades to nil (full re-validation), never errors")
}

// TestHasSubstantiveSpecDefect_Table pins predicate A: a finding is substantive
// iff it is non-pass, planner-owned blocked/spec-defect AND carries a non-stub
// Detail OR Correction. Anything that lacks concrete content (or is not a
// planner-owned blocked/spec-defect) returns false so the daemon re-validates
// instead of burning the single SpecRetries.
func TestHasSubstantiveSpecDefect_Table(t *testing.T) {
	tests := []struct {
		name     string
		findings []ValidationFinding
		want     bool
	}{
		{
			name:     "no findings (top-level fallback)",
			findings: nil,
			want:     false,
		},
		{
			name: "stub-only planner blocked spec-defect",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: ""},
			},
			want: false,
		},
		{
			name: "stub literals in both fields",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "none", Correction: "tbd"},
			},
			want: false,
		},
		{
			name: "stub with surrounding whitespace/case",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "  None  ", Correction: " N/A "},
			},
			want: false,
		},
		{
			name: "non-stub Detail",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "criteria contradict", Correction: ""},
			},
			want: true,
		},
		{
			name: "non-stub Correction only",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: "acceptance #3 requires X but precondition forbids X"},
			},
			want: true,
		},
		{
			name: "blocked owner=ops (not planner)",
			findings: []ValidationFinding{
				{Rule: "env", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops", Detail: "missing secret", Correction: "export X"},
			},
			want: false,
		},
		{
			name: "code-defect implementer",
			findings: []ValidationFinding{
				{Rule: "ut", Status: VerdictFail, FailureClass: "code-defect", Owner: "implementer", Detail: "test red", Correction: "fix it"},
			},
			want: false,
		},
		{
			name: "pass only",
			findings: []ValidationFinding{
				{Rule: "a1", Status: VerdictPass, Owner: "planner"},
			},
			want: false,
		},
		{
			name: "spec-defect status!=blocked with content (validateFindings bypass)",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictFail, FailureClass: "spec-defect", Owner: "planner", Detail: "acceptance contradicts precondition"},
			},
			want: true,
		},
		{
			name: "spec-defect status!=blocked, both fields stub",
			findings: []ValidationFinding{
				{Rule: "a3", Status: VerdictFail, FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: ""},
			},
			want: false,
		},
		{
			name: "mixed: one stub planner-blocked + one substantive",
			findings: []ValidationFinding{
				{Rule: "a1", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "", Correction: ""},
				{Rule: "a2", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner", Detail: "real contradiction", Correction: ""},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HasSubstantiveSpecDefect(tt.findings))
		})
	}
}

// TestContentlessDetail_ParityWithMCP pins the taskvisor mirror of
// mcp.contentlessCorrections (internal/mcp/tools_taskvisor.go:220-229). taskvisor
// cannot import internal/mcp (cycle), so the source map cannot be compared at
// runtime; this frozen key set IS the parity contract — if either map drifts, a
// maintainer must update both, and this test fails on any taskvisor-side drift.
func TestContentlessDetail_ParityWithMCP(t *testing.T) {
	wantKeys := map[string]bool{
		"":                 true,
		"fix it":           true,
		"none":             true,
		"n/a":              true,
		"na":               true,
		"not applicable":   true,
		"to be determined": true,
		"tbd":              true,
	}
	gotKeys := make(map[string]bool, len(contentlessDetail))
	for k := range contentlessDetail {
		gotKeys[k] = true
	}
	assert.Equal(t, wantKeys, gotKeys, "contentlessDetail must mirror mcp.contentlessCorrections key-for-key")
}

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

// --- B5a structured correction_edit schema ---

// TestValidationFinding_CorrectionEditRoundTrips: a finding carrying two
// CorrectionEdits survives a JSON marshal → unmarshal field-for-field.
func TestValidationFinding_CorrectionEditRoundTrips(t *testing.T) {
	original := ValidationFinding{
		Rule:       "rename symbol",
		Status:     "fail",
		Detail:     "old name still referenced",
		Correction: "rename Foo to Bar everywhere",
		CorrectionEdits: []CorrectionEdit{
			{File: "internal/a/a.go", Line: 5, Old: "Foo", New: "Bar"},
			{File: "internal/b/b.go", Line: 0, Old: "", New: "import x"},
		},
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var back ValidationFinding
	require.NoError(t, json.Unmarshal(data, &back))
	require.Len(t, back.CorrectionEdits, 2)
	assert.Equal(t, original.CorrectionEdits, back.CorrectionEdits)
	// json tag name is correction_edit (not correction_edits).
	assert.Contains(t, string(data), `"correction_edit"`)
}

// TestValidationFinding_OmitsCorrectionEditWhenAbsent: a prose-only finding
// marshals with NO correction_edit key (back-compat byte shape).
func TestValidationFinding_OmitsCorrectionEditWhenAbsent(t *testing.T) {
	f := ValidationFinding{Rule: "r", Status: "pass", Detail: "ok"}
	data, err := json.Marshal(f)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "correction_edit",
		"prose-only finding must not emit a correction_edit key")
}

// TestComputeSignatures_IgnoresCorrectionEdit: two findings identical except for
// CorrectionEdits must hash to the same signature (corrections are remedy, not
// failure identity — the C6 breaker must not see them).
func TestComputeSignatures_IgnoresCorrectionEdit(t *testing.T) {
	base := ValidationFinding{Rule: "r1", Status: "fail", Detail: "cause", FailureClass: "code-defect"}
	withEdit := base
	withEdit.CorrectionEdits = []CorrectionEdit{{File: "a.go", Line: 1, Old: "x", New: "y"}}

	assert.Equal(t,
		ComputeSignatures([]ValidationFinding{base}),
		ComputeSignatures([]ValidationFinding{withEdit}),
		"CorrectionEdits must not affect the failure signature")
}

// TestHasSubstantiveSpecDefect_UnaffectedByCorrectionEdit: a planner/blocked
// finding with stub Detail+Correction but a populated CorrectionEdits must NOT
// be treated as substantive (no spec-defect inflation from code edits).
func TestHasSubstantiveSpecDefect_UnaffectedByCorrectionEdit(t *testing.T) {
	f := ValidationFinding{
		Rule:            "spec contradiction",
		Status:          VerdictBlocked,
		Owner:           "planner",
		Detail:          "tbd",
		Correction:      "fix it",
		CorrectionEdits: []CorrectionEdit{{File: "a.go", Line: 1, Old: "x", New: "y"}},
	}
	assert.False(t, HasSubstantiveSpecDefect([]ValidationFinding{f}),
		"CorrectionEdits must not make a stub-prose finding substantive")
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

// --- C3 runtime-container guard regression tests ------------------------------
// These pin the EXISTING taskvisor routing the C3 prompt layer relies on: an
// investigator that emits a dead-container finding as failure_class=infra-flake
// owner=ops must roll up to (blocked, ops) and park via haltBlockedEnv WITHOUT
// charging any code-retry budget. signal.go / statemachine.go are UNCHANGED — the
// fix is purely in the prompt/template layer that EMITS the class; these tests
// guard that the route those prompts depend on cannot silently regress.

// TestClassifyVerdict_InfraFlakeOwnerOps_RoutesBlockedOps — a lone
// infra-flake/ops finding rolls up to (blocked, ops): the route a down runtime
// container takes so haltBlockedEnv parks the goal instead of bouncing to code.
func TestClassifyVerdict_InfraFlakeOwnerOps_RoutesBlockedOps(t *testing.T) {
	findings := []ValidationFinding{
		{
			Rule:         "phpunit:smoke",
			Status:       VerdictBlocked,
			FailureClass: "infra-flake",
			Owner:        "ops",
			Detail:       "runtime container test-project-php-1 is not running",
		},
	}
	verdict, owner := ClassifyVerdict(findings)
	assert.Equal(t, VerdictBlocked, verdict, "infra-flake must roll up to blocked, not fail")
	assert.Equal(t, "ops", owner, "a dead-container infra-flake is owned by ops, not the implementer")
}

// TestClassifyVerdict_InfraFlakeNotDowngradedToCodeDefect — a pass finding
// alongside an infra-flake/ops non-pass finding must NOT leak into a code-defect
// fail; the verdict stays blocked. This locks the precedence the guard depends on:
// an environment fault must never be re-classified as a code defect.
func TestClassifyVerdict_InfraFlakeNotDowngradedToCodeDefect(t *testing.T) {
	findings := []ValidationFinding{
		{Rule: "ecs:style", Status: VerdictPass},
		{
			Rule:         "vendor/bin/phpstan",
			Status:       VerdictBlocked,
			FailureClass: "infra-flake",
			Owner:        "ops",
			Detail:       "no such object: test-project-php-1",
		},
	}
	verdict, owner := ClassifyVerdict(findings)
	assert.Equal(t, VerdictBlocked, verdict, "infra-flake must not be downgraded to a code-defect fail")
	assert.NotEqual(t, VerdictFail, verdict, "a down container is never a code defect")
	assert.Equal(t, "ops", owner)
}

// TestHaltBlockedEnv_InfraFlake_ChargesNoBudget — a blocked/infra-flake/ops
// ValidatorSignal parks the goal with ZERO budget charged (all four counters
// unchanged) and arms BlockedByPrecondition for §5 auto-resume. Mirrors
// TestBlockedEnvHold_NoBudget but with the dead-container infra-flake class.
func TestHaltBlockedEnv_InfraFlake_ChargesNoBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "ops", Remedy: "start the runtime container test-project-php-1, then resume",
		Findings: []ValidationFinding{{
			Rule: "vendor/bin/phpunit", Status: "blocked", FailureClass: "infra-flake", Owner: "ops",
			Detail: "runtime container test-project-php-1 is not running (State.Running=false)",
		}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(goal, gf))

	assert.Equal(t, 2, goal.CodeRetries, "no code budget charged on a down-container infra-flake")
	assert.Equal(t, 2, goal.SpecRetries, "no spec budget charged")
	assert.Equal(t, 1, goal.ValidationRetries, "no validation budget charged")
	assert.Equal(t, 1, goal.BlockRetries, "no block budget charged")
	assert.Equal(t, GoalBlocked, goal.Status, "goal parked on infra-flake")
	assert.True(t, goal.BlockedByPrecondition, "infra-flake park must arm §5 auto-resume")
}

// TestHaltBlockedEnv_WritesRunbookNamingContainer — the operator runbook names
// the EXACT container from the blocking finding, so the operator knows which
// container to bring back up.
func TestHaltBlockedEnv_WritesRunbookNamingContainer(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 1)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "blocked", Owner: "ops",
		Findings: []ValidationFinding{{
			Rule: "vendor/bin/phpunit", Status: "blocked", FailureClass: "infra-flake", Owner: "ops",
			Detail: "runtime container test-project-php-1 is down — docker inspect reported State.Running=false",
		}},
		Timestamp: "2026-05-20T14:35:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(goal, gf))

	runbook := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	data, readErr := os.ReadFile(runbook)
	require.NoError(t, readErr, "haltBlockedEnv must write an operator runbook")
	assert.Contains(t, string(data), "test-project-php-1",
		"runbook must name the exact down container so ops knows what to restart")
}

// TestClassifyVerdict_OwnSuiteRedRollsToFail — a red own-suite-green gate arrives
// as a {Status:fail, FailureClass:"code-defect"} finding (the gate's Fail text
// classifies a non-zero phpunit exit as code-defect/implementer). ClassifyVerdict
// must roll it up to (fail, implementer) so the goal cannot reach done. This is a
// REGRESSION lock on the B2b wiring: signal.go's code-defect tier is unchanged;
// the test pins that a gate red routes through it correctly.
func TestClassifyVerdict_OwnSuiteRedRollsToFail(t *testing.T) {
	findings := []ValidationFinding{
		{Rule: "build sanity", Status: VerdictPass},
		{
			Rule:         "vendor/bin/phpunit tests/Integration/Catalog tests/Functional/Catalog",
			Status:       VerdictFail,
			FailureClass: "code-defect",
			Owner:        "implementer",
			Detail:       "non-zero phpunit exit for the goal's integration+functional scope",
		},
	}
	verdict, owner := ClassifyVerdict(findings)
	assert.Equal(t, VerdictFail, verdict, "a red own-suite gate must roll up to fail")
	assert.Equal(t, "implementer", owner, "a code-defect gate red is owned by the implementer")
}

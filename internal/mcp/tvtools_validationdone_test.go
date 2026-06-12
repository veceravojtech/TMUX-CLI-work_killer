package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

func TestGoalValidationDone_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{
		{Rule: "price check", Status: "pass"},
	}
	output, err := server.GoalValidationDone("goal-001", "pass", findings, "", nil)

	require.NoError(t, err)
	assert.True(t, output.Written)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json")
	data, err := os.ReadFile(signalPath)
	require.NoError(t, err)

	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "validator", sig["source"])
	assert.Equal(t, "pass", sig["verdict"])
	assert.NotEmpty(t, sig["timestamp"])
}

// TestM04b_TransientFailureOwnerOps (meta-acceptance M-04b) proves that when the
// investigate.xml transient-retry loop exhausts its budget and the EMITTER sets a
// blocked/infra-flake finding with owner=ops, GoalValidationDone persists that
// owner UNCHANGED to signal.json — the emitter's owner is never overwritten.

func TestM04b_TransientFailureOwnerOps(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{
		{Rule: "transient probe", Status: "blocked", FailureClass: "infra-flake", Owner: "ops", Correction: "Wait for the transient dependency to recover, then resume the goal."},
	}
	output, err := server.GoalValidationDone("goal-001", "blocked", findings, "", nil)

	require.NoError(t, err)
	assert.True(t, output.Written)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json")
	data, err := os.ReadFile(signalPath)
	require.NoError(t, err)

	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "blocked", sig["verdict"])

	rawFindings, ok := sig["findings"].([]any)
	require.True(t, ok, "findings must be present in signal.json")
	require.Len(t, rawFindings, 1)
	f0, ok := rawFindings[0].(map[string]any)
	require.True(t, ok)
	// The emitter's owner/class persist UNCHANGED — never overwritten downstream.
	assert.Equal(t, "ops", f0["owner"])
	assert.Equal(t, "infra-flake", f0["failure_class"])
}

func TestGoalValidationDone_FailVerdict(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	output, err := server.GoalValidationDone("goal-001", "fail", nil, "retry with fix", nil)

	require.NoError(t, err)
	assert.True(t, output.Written)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)

	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "fail", sig["verdict"])
	assert.Equal(t, "retry with fix", sig["next_action"])
}

// TestGoalValidationDone_RejectsMissingFields: a non-pass finding missing one of
// the four mandatory detail fields is rejected before any I/O; the error names
// all four required fields and no signal.json is written.

func TestGoalValidationDone_RejectsMissingFields(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	findings := []ValidationFinding{{
		Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
		FailingCommand: "go test ./pricing",
		OutputExcerpt:  "want 1000 got 100",
		// ExpectedState intentionally omitted.
		Correction: "multiply by 100",
	}}

	_, err := server.GoalValidationDone("goal-001", "fail", findings, "fix it", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	// Error names all four required fields.
	assert.Contains(t, err.Error(), "failing_command")
	assert.Contains(t, err.Error(), "output_excerpt")
	assert.Contains(t, err.Error(), "expected_state")
	assert.Contains(t, err.Error(), "correction")

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	assert.True(t, os.IsNotExist(statErr), "no signal.json must be written on rejection")
}

// TestGoalValidationDone_RejectsStubCorrection: a non-pass finding whose other
// fields are set but whose correction is a contentless stub is rejected as a
// non-correction; the error names correction and no signal.json is written.

func TestGoalValidationDone_RejectsStubCorrection(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	findings := []ValidationFinding{{
		Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
		FailingCommand: "go test ./pricing",
		OutputExcerpt:  "want 1000 got 100",
		ExpectedState:  "total in cents matches API",
		Correction:     "fix it", // contentless stub
	}}

	_, err := server.GoalValidationDone("goal-001", "fail", findings, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "correction")

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	assert.True(t, os.IsNotExist(statErr), "no signal.json must be written on rejection")
}

// TestGoalValidationDone_AcceptsCompleteFinding: a non-pass finding with all
// four detail fields non-empty is accepted and persisted, with the new fields
// written into signal.json.

func TestGoalValidationDone_AcceptsCompleteFinding(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{{
		Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
		FailingCommand: "go test ./pricing -run TestTotal",
		OutputExcerpt:  "want 1000 got 100",
		ExpectedState:  "total in cents matches the API",
		Correction:     "multiply dollars by 100 before formatting",
	}}

	output, err := server.GoalValidationDone("goal-001", "fail", findings, "fix pricing", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	got := sig["findings"].([]any)[0].(map[string]any)
	assert.Equal(t, "go test ./pricing -run TestTotal", got["failing_command"])
	assert.Equal(t, "want 1000 got 100", got["output_excerpt"])
	assert.Equal(t, "total in cents matches the API", got["expected_state"])
	assert.Equal(t, "multiply dollars by 100 before formatting", got["correction"])
}

// TestGoalValidationDone_AcceptsPassFindingEmptyFields: a pass finding may leave
// all four extra fields empty and is accepted unchanged.

func TestGoalValidationDone_AcceptsPassFindingEmptyFields(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{{Rule: "smoke", Status: "pass"}}

	output, err := server.GoalValidationDone("goal-001", "pass", findings, "", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "pass", sig["verdict"])
}

// TestValidateFindings_BlockedRequiresCorrection: a blocked finding must carry a
// concrete, non-stub correction (remedy) so the parked goal's runbook is
// actionable. Empty/whitespace/stub corrections (case-folded) are rejected with
// ErrInvalidInput naming "blocked" and "correction"; a real remedy is accepted.

func TestValidateFindings_BlockedRequiresCorrection(t *testing.T) {
	cases := []struct {
		name       string
		correction string
		wantErr    bool
	}{
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"tbd", "tbd", true},
		{"none", "none", true},
		{"na", "n/a", true},
		{"casefold fix it", "Fix It", true},
		{"real remedy", "Start postgres on :5432, then resume.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := []ValidationFinding{{
				Rule:       "db-precondition",
				Status:     taskvisor.VerdictBlocked,
				Correction: tc.correction,
			}}
			err := validateFindings("blocked", findings)
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidInput)
				assert.Contains(t, err.Error(), "blocked")
				assert.Contains(t, err.Error(), "correction")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateFindings_BlockedDoesNotRequireFailingCommand: COMMAND-shaped fields
// (failing_command/output_excerpt/expected_state) stay fail-only. A blocked
// finding with a real remedy but no command fields is accepted — guards against
// over-enforcement.

func TestValidateFindings_BlockedDoesNotRequireFailingCommand(t *testing.T) {
	findings := []ValidationFinding{{
		Rule:       "db-precondition",
		Status:     taskvisor.VerdictBlocked,
		Correction: "Start postgres on :5432, then resume.",
		// FailingCommand / OutputExcerpt / ExpectedState intentionally empty.
	}}
	err := validateFindings("blocked", findings)
	require.NoError(t, err)
}

// TestGoalValidationDone_BlockedWithRemedy_WritesSignal: a blocked finding with a
// concrete correction and failure_class rides the full E2E path and persists a
// signal.json carrying verdict=="blocked".

func TestGoalValidationDone_BlockedWithRemedy_WritesSignal(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{{
		Rule: "db-precondition", Status: "blocked", FailureClass: "env-config", Owner: "ops",
		Correction: "Start postgres on :5432, then resume.",
	}}

	output, err := server.GoalValidationDone("goal-001", "blocked", findings, "start postgres", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "blocked", sig["verdict"])
}

// TestGoalValidationDone_BlockedStubCorrection_NoSignal: a blocked finding with a
// stub correction is rejected with ErrInvalidInput before any I/O — no
// signal.json is written, proving the no-signal contract end-to-end.

func TestGoalValidationDone_BlockedStubCorrection_NoSignal(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	findings := []ValidationFinding{{
		Rule: "db-precondition", Status: "blocked", FailureClass: "env-config", Owner: "ops",
		Correction: "tbd", // contentless stub
	}}

	_, err := server.GoalValidationDone("goal-001", "blocked", findings, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "correction")

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	assert.True(t, os.IsNotExist(statErr), "no signal.json must be written on rejection")
}

func TestGoalValidationDone_WrongUUID(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-wrong")

	server := newTestServer(mockExec, tmpDir)
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "not the validator window")
}

func TestGoalValidationDone_NoValidatorWindow(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	server := newTestServer(mockExec, tmpDir)
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "no validator window")
}

// TestGoalValidationDone_NamespacedValidatorWindow reproduces the MaxGoals>1
// concurrency defect: when two goals run in parallel the daemon spawns the
// validator with a per-goal suffixed name ("validator-046", see
// internal/taskvisor/window_names.go), but the authorization lookup used to
// hard-code Name == "validator" and rejected with "no validator window found",
// stranding the verdict. The lookup must accept the per-goal "validator-<ns>"
// form and still gate the write on the UUID match.

func TestGoalValidationDone_NamespacedValidatorWindow(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-046
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-046"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-045"},
		{TmuxWindowID: "@1", Name: "validator-046"},
		{TmuxWindowID: "@2", Name: "supervisor-046"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-046", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-046")

	server := newTestServer(mockExec, tmpDir)
	output, err := server.GoalValidationDone("goal-046", "pass", []ValidationFinding{{Rule: "x", Status: "pass"}}, "", nil)

	require.NoError(t, err)
	assert.True(t, output.Written)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-046", "signal.json")
	data, err := os.ReadFile(signalPath)
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "pass", sig["verdict"])
}

func TestGoalValidationDone_UnknownGoalID(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)

	mockExec := new(testutil.MockTmuxExecutor)
	server := newTestServer(mockExec, tmpDir)
	_, err := server.GoalValidationDone("goal-999", "pass", nil, "", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "goal not found")
}

// --- GoalPrune tests ---

func TestGoalValidationDone_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)

	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "", nil)
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
}

func TestGoalValidationDone_WritesResultsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	findings := []ValidationFinding{{Rule: "alpha", Status: "pass"}, {Rule: "beta", Status: "pass"}}
	results := []FindingResult{
		{ID: "alpha", Status: "pass", ScopeFiles: []string{"a.go"}, ChangedFiles: []string{"a.go"}},
		{ID: "beta", Status: "pass", ScopeFiles: []string{"b.go"}, ChangedFiles: []string{"out-of-scope.go"}},
	}

	output, err := server.GoalValidationDone("goal-001", "pass", findings, "", results)
	require.NoError(t, err)
	assert.True(t, output.Written)

	// signal.json still written (preserved behavior).
	_, err = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)

	// results.json ledger written via taskvisor schema.
	ledger, err := taskvisor.LoadResults(tmpDir, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, ledger)
	require.Len(t, ledger.Results, 2)

	alpha := ledger.Results["alpha"]
	beta := ledger.Results["beta"]
	assert.Equal(t, "alpha", alpha.FindingID)
	assert.Equal(t, "pass", alpha.Status)
	assert.NotEmpty(t, alpha.InputFingerprint)
	assert.GreaterOrEqual(t, alpha.CycleNumber, 1)

	// beta's only change is out-of-scope, so its fingerprint equals the baseline
	// (no in-scope change) — the server computed scope∩changed = ∅.
	betaBaseline := taskvisor.ComputeInputFingerprint(taskvisor.ValidationFinding{Rule: "beta", Scope: []string{"b.go"}}, nil)
	assert.Equal(t, betaBaseline, beta.InputFingerprint, "out-of-scope change must not alter the fingerprint")
}

// TestGoalValidationDone_NoResultsLeavesLedgerAbsent: omitting results leaves
// results.json untouched (signal.json is still written).

func TestGoalValidationDone_NoResultsLeavesLedgerAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	server := newTestServer(mockExec, tmpDir)
	_, err := server.GoalValidationDone("goal-001", "pass", []ValidationFinding{{Rule: "x", Status: "pass"}}, "", nil)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "results.json"))
	assert.True(t, os.IsNotExist(statErr), "no results.json when no per-finding results supplied")
}

// --- C8 / M-06 spec-defect detection tests ---
//
// These prove the binding-cross-reference contract: when the
// investigate.xml preflight detects a Config artifact that contradicts a
// declared binding (e.g. composer.json under a greenfield binding), it emits a
// finding {status:blocked, class:spec-defect, owner:planner} that round-trips
// through goal-validation-done into signal.json unchanged. The struct field for
// the failure CLASS is FailureClass (json: failure_class) — C1's owner-priority
// resolution puts planner at the top, so a binding contradiction is always
// owned by the planner and never the implementer/ops. No production Go change is
// needed here: C1 added the Class/Owner fields; C8 only guarantees the emitter
// produces this shape and that the MCP write path preserves it.

// newSpecDefectTestServer wires the validator-window mock + UUID env the
// goal-validation-done authorization path requires and returns a ready server.

//go:build c1_gate
// +build c1_gate

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonTagNames returns the ordered list of json tag names (the token before any
// comma) for every field of the given struct type.
func jsonTagNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		names = append(names, name)
	}
	return names
}

// TestValidationFindingStructsInSync guarantees the two ValidationFinding
// definitions (taskvisor.ValidationFinding, the signal.json shape, and
// mcp.ValidationFinding, the tool input shape) never drift. The signal.json
// wire contract is the json tag set, so we compare json tag names field by
// field and name the first divergence on mismatch.
func TestValidationFindingStructsInSync(t *testing.T) {
	tvType := reflect.TypeOf(taskvisor.ValidationFinding{})
	mcpType := reflect.TypeOf(ValidationFinding{})

	tvTags := jsonTagNames(tvType)
	mcpTags := jsonTagNames(mcpType)

	require.Equalf(t, len(tvTags), len(mcpTags),
		"field count differs: taskvisor=%v mcp=%v", tvTags, mcpTags)

	for i := range tvTags {
		require.Equalf(t, tvTags[i], mcpTags[i],
			"ValidationFinding structs diverge at field %d: taskvisor json tag %q != mcp json tag %q",
			i, tvTags[i], mcpTags[i])
	}

	// Set equality as a belt-and-braces check independent of field order.
	assert.ElementsMatch(t, tvTags, mcpTags)
}

// TestCorrectionEditStructsInSync_TagsMatch guarantees the two mirrored
// CorrectionEdit types (taskvisor.CorrectionEdit, mcp.CorrectionEdit) never
// drift: the json tag set is the signal.json wire contract for nested edits.
func TestCorrectionEditStructsInSync_TagsMatch(t *testing.T) {
	tvTags := jsonTagNames(reflect.TypeOf(taskvisor.CorrectionEdit{}))
	mcpTags := jsonTagNames(reflect.TypeOf(CorrectionEdit{}))

	require.Equalf(t, len(tvTags), len(mcpTags),
		"CorrectionEdit field count differs: taskvisor=%v mcp=%v", tvTags, mcpTags)
	for i := range tvTags {
		require.Equalf(t, tvTags[i], mcpTags[i],
			"CorrectionEdit structs diverge at field %d: taskvisor %q != mcp %q", i, tvTags[i], mcpTags[i])
	}
	assert.ElementsMatch(t, tvTags, mcpTags)
}

// TestValidationFindingStructsInSync_IncludesCorrectionEdit verifies both
// ValidationFinding types carry the correction_edit json tag at the same index.
func TestValidationFindingStructsInSync_IncludesCorrectionEdit(t *testing.T) {
	tvTags := jsonTagNames(reflect.TypeOf(taskvisor.ValidationFinding{}))
	mcpTags := jsonTagNames(reflect.TypeOf(ValidationFinding{}))

	indexOf := func(tags []string, want string) int {
		for i, n := range tags {
			if n == want {
				return i
			}
		}
		return -1
	}
	tvIdx := indexOf(tvTags, "correction_edit")
	mcpIdx := indexOf(mcpTags, "correction_edit")
	require.GreaterOrEqual(t, tvIdx, 0, "taskvisor ValidationFinding must carry correction_edit")
	require.GreaterOrEqual(t, mcpIdx, 0, "mcp ValidationFinding must carry correction_edit")
	assert.Equal(t, tvIdx, mcpIdx, "correction_edit must sit at the same field index in both structs")
	assert.Equal(t, len(tvTags), len(mcpTags), "both ValidationFinding structs must have equal field counts")
}

// TestValidateFindings_RejectsCorrectionEditWithEmptyFile: a fail finding whose
// CorrectionEdits has an entry with empty file is rejected with ErrInvalidInput.
func TestValidateFindings_RejectsCorrectionEditWithEmptyFile(t *testing.T) {
	findings := []ValidationFinding{{
		Rule:           "rename",
		Status:         taskvisor.VerdictFail,
		Detail:         "stale name",
		FailingCommand: "go build ./...",
		OutputExcerpt:  "undefined: Foo",
		ExpectedState:  "compiles",
		Correction:     "rename Foo to Bar",
		CorrectionEdits: []CorrectionEdit{
			{File: "  ", Line: 1, Old: "Foo", New: "Bar"},
		},
	}}
	err := validateFindings(taskvisor.VerdictFail, findings)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "correction_edit.file")
}

// TestValidateFindings_AcceptsValidCorrectionEdit: a fail finding with valid
// prose Correction and a well-formed CorrectionEdits entry passes.
func TestValidateFindings_AcceptsValidCorrectionEdit(t *testing.T) {
	findings := []ValidationFinding{{
		Rule:           "rename",
		Status:         taskvisor.VerdictFail,
		Detail:         "stale name",
		FailingCommand: "go build ./...",
		OutputExcerpt:  "undefined: Foo",
		ExpectedState:  "compiles",
		Correction:     "rename Foo to Bar",
		CorrectionEdits: []CorrectionEdit{
			{File: "internal/a/a.go", Line: 5, Old: "Foo", New: "Bar"},
		},
	}}
	assert.NoError(t, validateFindings(taskvisor.VerdictFail, findings))
}

// TestGoalValidationDone_PersistsCorrectionEditToSignal: a goal-validation-done
// call carrying correction_edit is written to signal.json and reloaded via
// LoadSignal with the edits intact.
func TestGoalValidationDone_PersistsCorrectionEditToSignal(t *testing.T) {
	tmpDir := t.TempDir()
	server := newValidatorServer(t, tmpDir)

	findings := []ValidationFinding{{
		Rule:           "rename",
		Status:         taskvisor.VerdictFail,
		FailureClass:   "code-defect",
		Owner:          "implementer",
		Detail:         "stale name",
		FailingCommand: "go build ./...",
		OutputExcerpt:  "undefined: Foo",
		ExpectedState:  "compiles",
		Correction:     "rename Foo to Bar",
		CorrectionEdits: []CorrectionEdit{
			{File: "internal/a/a.go", Line: 5, Old: "Foo", New: "Bar"},
			{File: "internal/b/b.go", New: "import x"},
		},
	}}
	_, err := server.GoalValidationDone("goal-001", taskvisor.VerdictFail, findings, "fix it", nil)
	require.NoError(t, err)

	loaded, err := taskvisor.LoadSignal(tmpDir, "goal-001")
	require.NoError(t, err)
	sig, ok := loaded.(*taskvisor.ValidatorSignal)
	require.True(t, ok)
	require.Len(t, sig.Findings, 1)
	require.Len(t, sig.Findings[0].CorrectionEdits, 2)
	assert.Equal(t, "internal/a/a.go", sig.Findings[0].CorrectionEdits[0].File)
	assert.Equal(t, 5, sig.Findings[0].CorrectionEdits[0].Line)
	assert.Equal(t, "Bar", sig.Findings[0].CorrectionEdits[0].New)
	assert.Equal(t, "import x", sig.Findings[0].CorrectionEdits[1].New)
}

func newValidatorServer(t *testing.T, tmpDir string) *Server {
	t.Helper()
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

	return newTestServer(mockExec, tmpDir)
}

func readSignal(t *testing.T, tmpDir, goalID string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", goalID, "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	return sig
}

func TestGoalValidationDone_RejectsOutOfEnumVerdict(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalValidationDone("goal-001", "done", nil, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	// names the allowed enum
	assert.Contains(t, err.Error(), "pass")
	assert.Contains(t, err.Error(), "fail")
	assert.Contains(t, err.Error(), "blocked")
	assert.Contains(t, err.Error(), "error")

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	assert.True(t, os.IsNotExist(statErr), "no signal.json must be written on rejection")
}

func TestGoalValidationDone_RejectsNonPassMissingClass(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
`)
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	findings := []ValidationFinding{{Rule: "price check", Status: "fail"}}
	_, err := server.GoalValidationDone("goal-001", "fail", findings, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "failure_class")

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	assert.True(t, os.IsNotExist(statErr), "no signal.json must be written on rejection")
}

func TestGoalValidationDone_BlockedEnvConfigAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	server := newValidatorServer(t, tmpDir)

	findings := []ValidationFinding{
		// Blocked findings require a substantive remedy (validateFindings
		// rejects empty/stub corrections).
		{Rule: "secret present", Status: taskvisor.VerdictBlocked, FailureClass: "env-config", Owner: "ops", Correction: "set API_SECRET in .env and restart the service"},
	}
	output, err := server.GoalValidationDone("goal-001", taskvisor.VerdictBlocked, findings, "set the secret", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	sig := readSignal(t, tmpDir, "goal-001")
	assert.Equal(t, "blocked", sig["verdict"])
	fs, ok := sig["findings"].([]any)
	require.True(t, ok)
	require.Len(t, fs, 1)
	f := fs[0].(map[string]any)
	assert.Equal(t, "env-config", f["failure_class"])
	assert.Equal(t, "ops", f["owner"])
	assert.Equal(t, "set API_SECRET in .env and restart the service", f["correction"])
}

func TestGoalValidationDone_EmptyVerdictSynthesizesError(t *testing.T) {
	tmpDir := t.TempDir()
	server := newValidatorServer(t, tmpDir)

	output, err := server.GoalValidationDone("goal-001", "", nil, "", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	sig := readSignal(t, tmpDir, "goal-001")
	assert.Equal(t, "error", sig["verdict"])
	fs, ok := sig["findings"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(fs), 1)
	f := fs[0].(map[string]any)
	assert.Equal(t, "ops", f["owner"], "synthesized error finding must be owned by ops")
}

// TestC1_FieldsPropagateEndToEnd: a worker-emitted finding carrying
// failure_class/owner is persisted by the MCP tool into signal.json, read back
// by the daemon via taskvisor.LoadSignal, and the fields survive the round-trip
// so taskvisor.ClassifyVerdict can route on them. Exercises worker-emit → MCP
// persist → daemon read.
func TestC1_FieldsPropagateEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	server := newValidatorServer(t, tmpDir)

	findings := []ValidationFinding{
		{
			Rule:         "DB reachable",
			Status:       taskvisor.VerdictBlocked,
			FailureClass: "env-config",
			Owner:        "ops",
			Detail:       "DATABASE_URL unset",
			// Blocked findings require a substantive remedy (validateFindings
			// rejects empty/stub corrections) — and it must round-trip too.
			Correction: "ensure DB container is up; export DATABASE_URL; run migrations",
		},
	}
	_, err := server.GoalValidationDone("goal-001", taskvisor.VerdictBlocked, findings, "set DATABASE_URL", nil)
	require.NoError(t, err)

	// Daemon-side read.
	loaded, err := taskvisor.LoadSignal(tmpDir, "goal-001")
	require.NoError(t, err)
	sig, ok := loaded.(*taskvisor.ValidatorSignal)
	require.True(t, ok, "expected a validator signal")
	require.Len(t, sig.Findings, 1)

	// Fields survived persist → read.
	assert.Equal(t, "env-config", sig.Findings[0].FailureClass)
	assert.Equal(t, "ops", sig.Findings[0].Owner)
	assert.Equal(t, "DATABASE_URL unset", sig.Findings[0].Detail)
	assert.Equal(t, "ensure DB container is up; export DATABASE_URL; run migrations", sig.Findings[0].Correction)

	// And the daemon can route on them.
	verdict, owner := taskvisor.ClassifyVerdict(sig.Findings)
	assert.Equal(t, taskvisor.VerdictBlocked, verdict)
	assert.Equal(t, "ops", owner)
}

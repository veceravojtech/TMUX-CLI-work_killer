package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

func writeTestGoalsYaml(t *testing.T, dir string, content string) {
	t.Helper()
	goalsDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(goalsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalsDir, "goals.yaml"), []byte(content), 0o644))
}

// --- TaskvisorStart tests ---

func TestTaskvisorStart_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Fix prices
  status: pending
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.TaskvisorStart()

	require.NoError(t, err)
	assert.True(t, output.Started)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start")
	_, statErr := os.Stat(signalPath)
	assert.NoError(t, statErr)
}

func TestTaskvisorStart_NoPendingGoals(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Done
  status: done
- id: goal-002
  description: Failed
  status: failed
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.TaskvisorStart()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "no pending goals")
}

func TestTaskvisorStart_NoGoalsFile(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.TaskvisorStart()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "goals.yaml not found")
}

func TestTaskvisorStart_SignalFileAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Fix
  status: pending
`)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start")
	require.NoError(t, os.WriteFile(signalPath, []byte("old"), 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.TaskvisorStart()

	require.NoError(t, err)
	assert.True(t, output.Started)

	data, err := os.ReadFile(signalPath)
	require.NoError(t, err)
	assert.Equal(t, "start", string(data))
}

// --- GoalCreate tests ---

func TestGoalCreate_FirstGoal(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Fix prices", []string{"Price matches API"}, []string{"Check price"}, "", "", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "Fix prices", gf.Goals[0].Description)
	assert.Equal(t, "pending", gf.Goals[0].Status)
	assert.Equal(t, 0, gf.Goals[0].Retries)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
	assert.Empty(t, gf.Goals[0].Acceptance, "acceptance should not be in goals.yaml")
	assert.Empty(t, gf.Goals[0].Validate, "validate should not be in goals.yaml")

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	_, statErr := os.Stat(goalDir)
	assert.NoError(t, statErr)

	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "# Fix prices")
	assert.Contains(t, mdContent, "- Price matches API")
	assert.Contains(t, mdContent, "- Check price")
}

func TestGoalCreate_SequentialID(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: done
- id: goal-002
  description: Second
  status: running
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Third", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)
}

func TestGoalCreate_ExplicitMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Custom retries", nil, []string{"check"}, "", "", "", 5, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_DefaultMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Default retries", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

// TestGoalCreateDefaultMaxRetriesIsFive: a fresh MCP goal-create with
// max_retries omitted (0) persists max_retries=5, which LoadGoals migrates
// into per-class budgets Code 5 / Spec 3 / Val 2 / Block 0.
func TestGoalCreateDefaultMaxRetriesIsFive(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Default budgets", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	// Persisted single counter is the new default.
	raw, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, raw.Goals, 1)
	assert.Equal(t, 5, raw.Goals[0].MaxRetries)

	// The migrating loader derives the per-class budgets Code 5 / Spec 3 / Val 2.
	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
}

func TestGoalCreate_EmptyDescription(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("", nil, nil, "", "", "", 0, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "description cannot be empty")
}

func TestGoalCreate_DescriptionTooLong(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	longDesc := strings.Repeat("a", 121)
	_, err := server.GoalCreate(longDesc, nil, nil, "", "", "", 0, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "120")
	assert.Contains(t, err.Error(), "--acceptance")
}

func TestGoalCreate_DescriptionExactlyAtLimit(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	exactDesc := strings.Repeat("b", 120)
	output, err := server.GoalCreate(exactDesc, nil, []string{"check"}, "", "", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, exactDesc, gf.Goals[0].Description)
}

func TestGoalCreate_AppendsToExisting(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: pending
  max_retries: 3
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "First", gf.Goals[0].Description)
	assert.Equal(t, "goal-002", gf.Goals[1].ID)
	assert.Equal(t, "Second", gf.Goals[1].Description)
}

func TestGoalCreate_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Test atomic", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
}

func TestGoalCreate_WithContext(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Refactor auth", []string{"Tests pass"}, []string{"check"}, "Legacy code", "Performance", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "Legacy code")
	assert.Contains(t, mdContent, "## Not In Scope")
	assert.Contains(t, mdContent, "Performance")
}

func TestGoalCreate_WithPhase(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "infrastructure", gf.Goals[0].Phase)
}

func TestGoalCreate_NoAcceptance(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple task", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.NotContains(t, mdContent, "## Context")
	assert.NotContains(t, mdContent, "## Not In Scope")
}

func TestGoalCreate_EmptyValidate(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Valid desc", nil, nil, "", "", "", 0, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "validation rule")
}

func TestGoalCreate_EmptyValidateSlice(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Valid desc", nil, []string{}, "", "", "", 0, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "validation rule")
}

func TestGoalCreate_InvalidPhase(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Bad phase goal", nil, []string{"check"}, "", "", "nonexistent", 0, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "invalid phase")
}

func TestGoalCreate_WithDependsOn(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("First goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, []string{"goal-001"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-002", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, []string{"goal-001"}, gf.Goals[1].DependsOn)
}

func TestGoalCreate_DependsOnNonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Orphan goal", nil, []string{"check"}, "", "", "", 0, []string{"goal-999"}, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "non-existent goal")
}

func TestGoalCreate_WithPhaseAndDependsOn(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Prereq goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Domain goal", nil, []string{"check"}, "", "", "domain", 0, []string{"goal-001"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-002", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "domain", gf.Goals[1].Phase)
	assert.Equal(t, []string{"goal-001"}, gf.Goals[1].DependsOn)
}

func TestGoalCreate_DependsOnEmptySlice(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("No deps goal", nil, []string{"check"}, "", "", "", 0, []string{}, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)
}

func TestGoalCreate_DependsOnMultiple(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Goal A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)
	_, err = server.GoalCreate("Goal B", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Goal C", nil, []string{"check"}, "", "", "", 0, []string{"goal-001", "goal-002"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 3)
	assert.Equal(t, []string{"goal-001", "goal-002"}, gf.Goals[2].DependsOn)
}

// --- GoalValidationDone tests ---

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

func TestGoalPrune_WithGoals(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: done
  max_retries: 3
- id: goal-002
  description: Second
  status: pending
  max_retries: 3
`)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "corrections"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-002", "corrections"), 0o755))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)
	assert.Equal(t, 2, output.GoalsRemoved)

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals.yaml"))
	assert.True(t, os.IsNotExist(statErr), "goals.yaml should be removed")

	_, statErr = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "goals/ dir should be removed")
}

func TestGoalPrune_NoGoals(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)
	assert.Equal(t, 0, output.GoalsRemoved)
}

func TestGoalPrune_DaemonActive(t *testing.T) {
	tmpDir := t.TempDir()
	tmuxDir := filepath.Join(tmpDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-active"), nil, 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalPrune()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "active")
}

func TestGoalPrune_CleansSignalFiles(t *testing.T) {
	tmpDir := t.TempDir()
	tmuxDir := filepath.Join(tmpDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-current-goal"), []byte("goal-001"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "taskvisor-start"), nil, 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalPrune()

	require.NoError(t, err)
	assert.True(t, output.Pruned)

	_, statErr := os.Stat(filepath.Join(tmuxDir, "taskvisor-current-goal"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-current-goal should be removed")

	_, statErr = os.Stat(filepath.Join(tmuxDir, "taskvisor-start"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-start should be removed")
}

func TestGoalCreate_LockFileCreated(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Lock test", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	lockPath := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.lock")
	_, statErr := os.Stat(lockPath)
	assert.NoError(t, statErr, "lock file should exist after GoalCreate")
}

func TestGoalCreate_Concurrent_AllSucceed(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make([]error, goroutines)
	ids := make([]string, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			output, err := server.GoalCreate(
				fmt.Sprintf("Goal %d", idx),
				[]string{fmt.Sprintf("criterion-%d", idx)},
				[]string{"check"}, "", "", "",
				0, nil, nil, nil,
			)
			errs[idx] = err
			if output != nil {
				ids[idx] = output.ID
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < goroutines; i++ {
		assert.NoError(t, errs[i], "goroutine %d should succeed", i)
	}

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err, "goals.yaml must be valid YAML (no corruption)")
	require.NotNil(t, gf)
	assert.Equal(t, goroutines, len(gf.Goals), "all %d goals should be persisted", goroutines)

	idSet := make(map[string]bool, goroutines)
	for _, id := range ids {
		if id != "" {
			idSet[id] = true
		}
	}
	assert.Equal(t, goroutines, len(idSet), "all goal IDs should be unique")
}

func TestGoalCreate_LockCoversLoadSaveSpan(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error
	go func() {
		defer wg.Done()
		_, err1 = server.GoalCreate("First concurrent", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	}()
	go func() {
		defer wg.Done()
		_, err2 = server.GoalCreate("Second concurrent", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	}()

	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 2, len(gf.Goals), "both goals must appear — no lost writes")
}

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

func TestGoalCreate_InfraGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Doctrine XML mapping exists for every aggregate root and entity",
		"XML mapping lives in src/{BC}/Infrastructure/Persistence/Doctrine/Mapping/",
		"No Doctrine annotations/attributes anywhere in Domain or Application layers",
		"Repository implementation implements Domain repository interface",
		"Repository implementation lives in src/{BC}/Infrastructure/Persistence/",
		"Write repository dispatches domain events after flush (flush-then-dispatch pattern)",
		"Read model repository implements Application read model interface",
		"Custom DBAL types created for value objects that need DB storage",
		"doctrine:schema:validate passes",
		"Migration generated and applies cleanly",
		"Integration tests use real test database, not mocks",
		"Integration tests reset DB state via fixtures before each test",
		"Integration tests cover: persist + retrieve aggregate, query methods, edge cases",
		"Integration tests run green",
		"Service configuration wires implementations to interfaces",
		"ACL adapters exist for each cross-BC dependency from context-map.md",
	}
	validate := []string{
		"bin/console doctrine:schema:validate",
		"vendor/bin/phpunit --filter=Booking\\Infrastructure",
	}
	ctx := "Booking BC infrastructure layer. flush-then-dispatch pattern required for domain event publishing. Custom DBAL types needed for Money and BookingStatus value objects. ACL adapters required for cross-BC dependencies with Pricing BC."

	output, err := server.GoalCreate(
		"Implement Booking infrastructure: Doctrine mappings, repos, migration, integration tests",
		acceptance, validate, ctx, "", "infrastructure", 0, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "flush-then-dispatch")
	assert.Contains(t, mdContent, "DBAL types")
	assert.Contains(t, mdContent, "ACL adapters")

	assert.Contains(t, mdContent, "- Write repository dispatches domain events after flush (flush-then-dispatch pattern)")
	assert.Contains(t, mdContent, "- Custom DBAL types created for value objects that need DB storage")
	assert.Contains(t, mdContent, "- ACL adapters exist for each cross-BC dependency from context-map.md")

	assert.Contains(t, mdContent, "- bin/console doctrine:schema:validate")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --filter=Booking\\Infrastructure")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_ActionGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Controller class has maximum ~20 lines in action method",
		"Controller action: deserialize → dispatch → serialize (nothing else)",
		"Request DTO has Symfony validation constraints matching business rules",
		"Response DTO serializes data, does not expose internal domain structure",
		"Route is configured with correct method + path",
		"Controller imports only from Application layer, never Domain directly",
		"Playwright E2E test exists at tests/E2E/Booking/CreateBookingTest.ts",
		"Playwright test resets fixtures before running",
		"Playwright test uses credentials from docs/architecture/test-environment.md",
		"Playwright test verifies: correct status code",
		"Playwright test verifies: response body structure and values",
		"Playwright test verifies: error cases (422 for invalid input, 401 for no auth)",
		"Playwright test verifies: state change in DB (if applicable)",
		"Playwright test passes when run individually",
		"PHPStan level 9 passes on controller file",
		"ECS passes on all new files",
		"Deptrac passes — controller imports Application layer only",
	}
	validate := []string{
		"bin/console debug:router | grep /api/bookings",
		"npx playwright test tests/E2E/Booking/CreateBookingTest.ts",
	}
	ctx := `Deliverables per GM-12:
- Request DTO: src/Booking/Infrastructure/Http/Dto/CreateBookingRequest.php
- Response DTO: src/Booking/Infrastructure/Http/Dto/CreateBookingResponse.php
- Controller: src/Booking/Infrastructure/Http/Action/CreateBookingAction.php
- Route: POST /api/bookings
- E2E test: tests/E2E/Booking/CreateBookingTest.ts`

	output, err := server.GoalCreate(
		"POST /api/bookings — Booking controller action",
		acceptance, validate, ctx, "", "action", 0, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "Request DTO")
	assert.Contains(t, mdContent, "Response DTO")
	assert.Contains(t, mdContent, "Controller")
	assert.Contains(t, mdContent, "Route")
	assert.Contains(t, mdContent, "E2E test")

	assert.Contains(t, mdContent, "CreateBookingAction.php")
	assert.Contains(t, mdContent, "CreateBookingTest.ts")
	assert.Contains(t, mdContent, "POST /api/bookings")

	assert.Contains(t, mdContent, "Playwright E2E test exists")
	assert.Contains(t, mdContent, "Playwright test passes when run individually")

	assert.Contains(t, mdContent, "- npx playwright test tests/E2E/Booking/CreateBookingTest.ts")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_ErrorHandlingGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Symfony exception listener registered and catches all unhandled exceptions",
		"All error responses use RFC 7807 Problem Details format",
		"DomainException maps to 422",
		"EntityNotFoundException maps to 404",
		"AccessDeniedException maps to 403",
		"ValidationException maps to 422 with field-level errors",
		"Unexpected exceptions map to 500 with no internal details exposed",
		"PHPStan clean, ECS clean on error handling files",
	}
	validate := []string{
		"bin/console debug:event-dispatcher | grep ExceptionListener",
		"vendor/bin/phpunit --filter=ErrorHandling",
	}
	ctx := "Global error handling infrastructure. Symfony exception listener catches all unhandled exceptions. RFC 7807 Problem Details format for all error responses. DomainException maps to 422, EntityNotFoundException maps to 404, AccessDeniedException maps to 403, unexpected exceptions map to 500."

	output, err := server.GoalCreate(
		"Implement global error handling: exception listener, RFC 7807, status code mapping",
		acceptance, validate, ctx, "", "cross-cutting", 0, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "exception listener")
	assert.Contains(t, mdContent, "RFC 7807")
	assert.Contains(t, mdContent, "DomainException")
	assert.Contains(t, mdContent, "EntityNotFoundException")
	assert.Contains(t, mdContent, "AccessDeniedException")

	assert.Contains(t, mdContent, "422")
	assert.Contains(t, mdContent, "404")
	assert.Contains(t, mdContent, "403")
	assert.Contains(t, mdContent, "500")

	assert.Contains(t, mdContent, "- bin/console debug:event-dispatcher | grep ExceptionListener")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --filter=ErrorHandling")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_DeptracFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-01: Deptrac zero violations across entire codebase — vendor/bin/deptrac analyse exit code 0",
		"FG-02: Domain + Share layer depends on nothing (pure, no framework imports)",
		"FG-03: Application layer depends only on Domain + Share layer",
		"FG-04: Infrastructure layer depends on Domain + Share + Application only",
		"FG-05: Controllers (inbound adapters) import only Application layer",
		"FG-06: No cross-BC imports in Domain or Application layers",
		"FG-07: ACL adapters are the only cross-BC touchpoints in Infrastructure",
	}
	validate := []string{"vendor/bin/deptrac analyse"}
	ctx := `Final gate: validates that the entire codebase passes Deptrac layer dependency analysis after all BC goals, fixtures, actions, auth, and cross-cutting goals are complete.

## Investigation Config

### Investigator 1: Layer Structure Verifier
- Type: architecture-check
- Commands: vendor/bin/deptrac analyse, vendor/bin/deptrac analyse --formatter=json
- Pass: Exit 0, zero violations — all 4 DDD layers (Domain, Application, Infrastructure, Share) respect dependency rules
- Fail: Any layer violation detected — dependency flows upward or crosses BC boundary

### Investigator 2: Cross-BC Boundary Checker
- Type: architecture-check
- Commands: grep -rn 'use App\\' src/*/Domain/ | grep -v 'use App\\Share\\' | grep -v "$(basename $(dirname $f))" (per-BC), vendor/bin/deptrac analyse --filter=cross-bc
- Pass: Zero cross-BC imports in Domain/Application layers; only ACL adapters in Infrastructure cross BC boundaries
- Fail: Direct cross-BC import found outside ACL adapter`
	notInScope := "PHPStan, ECS, unit/integration tests, Playwright E2E, coverage, console boot, schema validation, migrations"

	output, err := server.GoalCreate(
		"Final gate: Deptrac full codebase layer dependency verification",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "## Not In Scope")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}

	assert.Contains(t, mdContent, "zero violations across entire codebase")
	assert.Contains(t, mdContent, "Domain + Share layer depends on nothing")
	assert.Contains(t, mdContent, "Application layer depends only on Domain + Share")
	assert.Contains(t, mdContent, "Infrastructure layer depends on Domain + Share + Application")
	assert.Contains(t, mdContent, "Controllers (inbound adapters) import only Application layer")
	assert.Contains(t, mdContent, "No cross-BC imports in Domain or Application")
	assert.Contains(t, mdContent, "ACL adapters are the only cross-BC touchpoints")

	assert.Contains(t, mdContent, "- vendor/bin/deptrac analyse")
	assert.Contains(t, mdContent, "Layer Structure Verifier")
	assert.Contains(t, mdContent, "Cross-BC Boundary Checker")
	assert.Contains(t, mdContent, "PHPStan, ECS")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_E2EFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-08: All Playwright E2E tests pass when run together (not just individually) — npx playwright test all green",
		"FG-09: No test isolation issues (order-dependent failures) — run Playwright tests in random order",
		"G-08: Last 3 goals in goals.yaml are final gates (Deptrac, E2E, Quality) — verify ordering",
	}
	validate := []string{"npx playwright test", "npx playwright test --shard=random"}
	ctx := `Final gate: validates that all Playwright E2E tests pass when run together as a full suite, detecting isolation issues that per-action test runs miss.

## Investigation Config

### Investigator 1: Full Suite Runner
- Type: test-execution
- Commands: npx playwright test
- Pass: Exit 0, all E2E tests green when run as a single suite — no failures from shared state or missing fixtures
- Fail: Any test failure when run together that passed in isolation

### Investigator 2: Isolation Checker
- Type: test-execution
- Commands: npx playwright test --shard=random
- Pass: Exit 0, all tests pass in randomized order — no order-dependent failures
- Fail: Test failure in randomized order indicates shared mutable state between tests`
	notInScope := "Individual endpoint tests, Deptrac, PHPStan, ECS, unit tests, schema validation"

	output, err := server.GoalCreate(
		"Final gate: Playwright E2E regression — all tests run together",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "FG-08: All Playwright E2E tests pass when run together (not just individually)")
	assert.Contains(t, mdContent, "FG-09: No test isolation issues (order-dependent failures)")
	assert.Contains(t, mdContent, "G-08: Last 3 goals in goals.yaml are final gates")
	assert.Contains(t, mdContent, "- npx playwright test")
	assert.Contains(t, mdContent, "- npx playwright test --shard=random")
	assert.Contains(t, mdContent, "Full Suite Runner")
	assert.Contains(t, mdContent, "Isolation Checker")
	assert.Contains(t, mdContent, "Individual endpoint tests, Deptrac, PHPStan")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_QualityFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-10: PHPStan level 9 zero errors across entire codebase — vendor/bin/phpstan analyse exit code 0",
		"FG-11: ECS zero violations across entire codebase — vendor/bin/ecs check exit code 0",
		"FG-12: Unit test suite all green — vendor/bin/phpunit --testsuite=unit passes",
		"FG-13: Integration test suite all green — vendor/bin/phpunit --testsuite=integration passes",
		"FG-14: Test coverage meets threshold (configurable, default 80%) — coverage report generated, threshold met",
		"FG-15: No TODO/FIXME/HACK comments in codebase — grep check, zero results",
		"FG-16: bin/console boots without errors — exit code 0",
		"FG-17: doctrine:schema:validate passes — exit code 0",
		"FG-18: All migrations applied cleanly — doctrine:migrations:status shows no pending",
	}
	validate := []string{
		"vendor/bin/phpstan analyse src/ --level=9",
		"vendor/bin/ecs check src/",
		"vendor/bin/phpunit --testsuite=unit",
		"vendor/bin/phpunit --testsuite=integration",
		"vendor/bin/phpunit --coverage-text",
		"grep -rn 'TODO\\|FIXME\\|HACK' src/ | wc -l",
		"bin/console",
		"bin/console doctrine:schema:validate",
		"bin/console doctrine:migrations:status",
	}
	ctx := `Final gate: validates full codebase quality — PHPStan, ECS, all test suites, coverage threshold, and Doctrine schema/migration health after all goals complete.

## Investigation Config

### Investigator 1: Static Analysis Verifier
- Type: quality-gate
- Commands: vendor/bin/phpstan analyse src/ --level=9, vendor/bin/ecs check src/
- Pass: Both exit 0 — zero PHPStan errors at level 9, zero ECS violations
- Fail: Any static analysis error or coding standard violation

### Investigator 2: Test Suite Runner
- Type: test-execution
- Commands: vendor/bin/phpunit --testsuite=unit, vendor/bin/phpunit --testsuite=integration, vendor/bin/phpunit --coverage-text
- Pass: All suites green, coverage meets threshold (default 80%)
- Fail: Test failure or coverage below threshold

### Investigator 3: Runtime Health Checker
- Type: environment-check
- Commands: bin/console, bin/console doctrine:schema:validate, bin/console doctrine:migrations:status, grep -rn 'TODO\|FIXME\|HACK' src/ | wc -l
- Pass: Console boots, schema valid, no pending migrations, zero stale comment markers
- Fail: Boot failure, schema mismatch, pending migrations, or stale comments found`
	notInScope := "Deptrac analysis, Playwright E2E, new feature code, refactoring"

	output, err := server.GoalCreate(
		"Final gate: PHPStan, ECS, test suites, coverage, schema, migrations",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "## Not In Scope")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}

	assert.Contains(t, mdContent, "- vendor/bin/phpstan analyse src/ --level=9")
	assert.Contains(t, mdContent, "- vendor/bin/ecs check src/")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --testsuite=unit")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --testsuite=integration")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --coverage-text")
	assert.Contains(t, mdContent, "grep -rn")
	assert.Contains(t, mdContent, "- bin/console")
	assert.Contains(t, mdContent, "- bin/console doctrine:schema:validate")
	assert.Contains(t, mdContent, "- bin/console doctrine:migrations:status")

	assert.Contains(t, mdContent, "Static Analysis Verifier")
	assert.Contains(t, mdContent, "Test Suite Runner")
	assert.Contains(t, mdContent, "Runtime Health Checker")
	assert.Contains(t, mdContent, "Deptrac analysis, Playwright E2E")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_PlaywrightActionWithFlakeRetry(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Playwright E2E test exists at tests/E2E/Booking/CreateBookingTest.ts",
		"Playwright test passes when run individually",
	}
	validate := []string{
		"npx playwright test tests/E2E/Booking/CreateBookingTest.ts",
	}
	ctx := `Deliverables per GM-12:
- E2E test: tests/E2E/Booking/CreateBookingTest.ts

## Investigation Config

### Investigator 4: Playwright E2E Verifier
- Type: test-execution
- Commands: npx playwright test tests/E2E/Booking/CreateBookingTest.ts
- Retry: 3 total attempts (1 initial + 2 retries) for flake detection
- Pass: Test passes on any of 3 attempts — if first run fails but second/third passes, the test is flaky but acceptable
- Fail: Test fails all 3 attempts — genuine failure, not a flake`

	output, err := server.GoalCreate(
		"POST /api/bookings — Booking controller action",
		acceptance, validate, ctx, "", "action", 0, nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "3 total attempts")
	assert.Contains(t, mdContent, "1 initial + 2 retries")
	assert.Contains(t, mdContent, "flake detection")
	assert.Contains(t, mdContent, "Test passes on any of 3 attempts")
	assert.Contains(t, mdContent, "Test fails all 3 attempts")
	assert.Contains(t, mdContent, "Playwright E2E test exists")
	assert.Contains(t, mdContent, "Playwright test passes when run individually")
}

func TestTvGoalsFile_GlobalMaxRetries_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `global_max_retries: 10
goals:
- id: goal-001
  description: Test
  status: pending
  max_retries: 3
`)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 10, gf.GlobalMaxRetries)

	require.NoError(t, tvSaveGoals(tmpDir, gf))

	reloaded, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, 10, reloaded.GlobalMaxRetries)
}

func TestGoalCreate_PreservesGlobalMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `global_max_retries: 7
goals:
- id: goal-001
  description: First
  status: done
  max_retries: 3
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 7, gf.GlobalMaxRetries, "global_max_retries must survive GoalCreate round-trip")
	require.Len(t, gf.Goals, 2)
}

func TestTvGoal_TimingFields_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
  max_retries: 3
  started_at: "2026-05-29T10:00:00Z"
  finished_at: "2026-05-29T11:30:00Z"
`)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "2026-05-29T10:00:00Z", gf.Goals[0].StartedAt)
	assert.Equal(t, "2026-05-29T11:30:00Z", gf.Goals[0].FinishedAt)

	require.NoError(t, tvSaveGoals(tmpDir, gf))

	reloaded, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Len(t, reloaded.Goals, 1)
	assert.Equal(t, "2026-05-29T10:00:00Z", reloaded.Goals[0].StartedAt)
	assert.Equal(t, "2026-05-29T11:30:00Z", reloaded.Goals[0].FinishedAt)
}

func TestGoalCreate_PreservesGoalTimingFields(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Running goal
  status: running
  max_retries: 3
  started_at: "2026-05-29T10:00:00Z"
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "2026-05-29T10:00:00Z", gf.Goals[0].StartedAt,
		"started_at must survive GoalCreate round-trip")
}

func TestGoalCreate_WritesPhaseToGoalMD(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Domain goal", nil, []string{"check"}, "", "", "domain", 0, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Phase")
	assert.Contains(t, mdContent, "domain")
}

func TestGoalCreate_NoPhaseOmitsSection(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)

	assert.NotContains(t, string(mdData), "## Phase")
}

// TestGoalValidationDone_WritesResultsJSON: when per-finding re-validation
// inputs are supplied, the orchestrator-owned results.json ledger is written
// alongside signal.json with a stable input fingerprint per finding, keyed by
// finding id (the rule). Out-of-scope changes do not alter the fingerprint.
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
// These prove the binding-cross-reference contract: when the validate.xml /
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
func newSpecDefectTestServer(t *testing.T, tmpDir, goalID string) *Server {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", goalID), 0o755))

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

// readSignalFindings reads back signal.json and returns its verdict plus the
// findings array decoded as generic maps (so we assert against the persisted
// JSON keys: rule, status, failure_class, owner, ...).
func readSignalFindings(t *testing.T, tmpDir, goalID string) (verdict string, findings []map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", goalID, "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	verdict, _ = sig["verdict"].(string)
	raw, _ := sig["findings"].([]any)
	for _, f := range raw {
		if m, ok := f.(map[string]any); ok {
			findings = append(findings, m)
		}
	}
	return verdict, findings
}

// TestM06_SpecDefectDetection: a Config that references composer.json under a
// greenfield binding produces a blocked / spec-defect / planner finding. The
// load-bearing assertion is owner==planner (and NOT implementer/ops) — it proves
// C1's owner-priority resolved the contradiction to the planner so the C2 daemon
// switch bounces it to goal-generation and decrements spec_retries, never charging
// the implementer with an unwinnable code retry.
func TestM06_SpecDefectDetection(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	findings := []ValidationFinding{{
		Rule:         "binding-cross-reference: composer.json vs greenfield",
		Status:       "blocked",
		FailureClass: "spec-defect",
		Owner:        "planner",
		Detail:       "composer.json contradicts greenfield binding",
		Correction:   "regenerate Config without composer.json",
	}}

	output, err := server.GoalValidationDone("goal-005", "blocked", findings, "regenerate Config without composer.json", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "blocked", verdict, "binding contradiction rolls up to a blocked verdict")

	var specDefect map[string]any
	for _, f := range persisted {
		if f["failure_class"] == "spec-defect" {
			specDefect = f
			break
		}
	}
	require.NotNil(t, specDefect, "a spec-defect finding must be present in signal.json")

	// Load-bearing: owner resolved to planner, never the implementer or ops.
	assert.Equal(t, "planner", specDefect["owner"])
	assert.NotEqual(t, "implementer", specDefect["owner"])
	assert.NotEqual(t, "ops", specDefect["owner"])
	assert.Equal(t, "spec-defect", specDefect["failure_class"])
	assert.Equal(t, "blocked", specDefect["status"])
}

// TestM06_NoBindingNoDefect: when no binding is declared, a Config that mentions
// composer.json produces an ordinary pass-through with NO spec-defect finding.
// Proves the matcher is binding-gated, not a blanket ban on composer.json.
func TestM06_NoBindingNoDefect(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	// No binding ⇒ empty forbidden set ⇒ the preflight is a no-op and the
	// ordinary rule check passes; no spec-defect is emitted.
	findings := []ValidationFinding{{
		Rule:   "composer.json present",
		Status: "pass",
	}}

	output, err := server.GoalValidationDone("goal-005", "pass", findings, "", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "pass", verdict, "no binding ⇒ verdict unaffected by C8")
	for _, f := range persisted {
		assert.NotEqual(t, "spec-defect", f["failure_class"], "no spec-defect finding when no binding is declared")
	}
}

// TestM06_MultiBindingUnion: greenfield + no-orm both declared; a Config that
// references an ORM-config artifact (doctrine.yaml, in the no-orm pattern set)
// is flagged. Proves the forbidden set is the UNION of every detected binding's
// patterns — a ref matching either set is caught with owner=planner.
func TestM06_MultiBindingUnion(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	// doctrine.yaml matches the no-orm pattern set; under the union of
	// greenfield+no-orm it is forbidden and emitted as a spec-defect.
	findings := []ValidationFinding{{
		Rule:         "binding-cross-reference: doctrine.yaml vs no-orm",
		Status:       "blocked",
		FailureClass: "spec-defect",
		Owner:        "planner",
		Detail:       "doctrine.yaml contradicts no-orm binding",
		Correction:   "regenerate Config without doctrine.yaml",
	}}

	output, err := server.GoalValidationDone("goal-005", "blocked", findings, "regenerate Config without doctrine.yaml", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "blocked", verdict)

	var specDefect map[string]any
	for _, f := range persisted {
		if f["failure_class"] == "spec-defect" {
			specDefect = f
			break
		}
	}
	require.NotNil(t, specDefect, "ORM artifact under no-orm (unioned) binding must emit a spec-defect")
	assert.Equal(t, "planner", specDefect["owner"])
	assert.Contains(t, specDefect["rule"], "no-orm")
}

// --- GoalCreate preconditions write-path (WS3b) tests ---

func TestGoalCreate_WithPreconditions_PersistsToGoalsYaml(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN=postgres://..."},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	require.Len(t, gf.Goals[0].Preconditions, 2)
	assert.Equal(t, taskvisor.Precondition{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN=postgres://..."}, gf.Goals[0].Preconditions[0])
	assert.Equal(t, taskvisor.Precondition{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"}, gf.Goals[0].Preconditions[1])
}

func TestGoalCreate_WithPreconditions_PersistsToGoalMD(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Preconditions")
	assert.Contains(t, mdContent, "- [env] DB_DSN — export DB_DSN")
}

func TestGoalCreate_PreconditionsRoundTripToEvaluate(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN"},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil)
	require.NoError(t, err)

	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	g, ok := gf.GoalByID(output.ID)
	require.True(t, ok)
	require.Len(t, g.Preconditions, 2)
	assert.Equal(t, preconds[0], g.Preconditions[0])
	assert.Equal(t, preconds[1], g.Preconditions[1])
}

func TestGoalCreate_NoPreconditions_OmitsYamlKeyAndSection(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple task", nil, []string{"check"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	rawYaml, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(rawYaml), "preconditions:")

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(mdData), "## Preconditions")
}

func TestGoalCreate_PreconditionEmptyRemedy_RendersSpecOnly(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "service", Spec: "localhost:5432", Remedy: ""},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "- [service] localhost:5432")
	assert.NotContains(t, mdContent, "localhost:5432 —")
}

// --- GoalCreate Investigation Config (M2) tests ---

// validInvestigatorSet returns n fully-valid investigators for the happy-path
// and boundary tests. Names are distinctive ("Custom Investigator N") so a test
// can prove the explicit config — not deriveInvestigators — reached goal.md.
func validInvestigatorSet(n int) []taskvisor.Investigator {
	types := []string{"static-analysis", "quality-gate", "test-execution", "architecture-check"}
	invs := make([]taskvisor.Investigator, n)
	for i := 0; i < n; i++ {
		invs[i] = taskvisor.Investigator{
			Name:     fmt.Sprintf("Custom Investigator %d", i+1),
			Type:     types[i%len(types)],
			Commands: []string{fmt.Sprintf("make check-%d", i+1)},
			Pass:     "exit 0",
		}
	}
	return invs
}

func TestGoalCreate_AcceptsValidInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.GoalCreate("Valid investigators", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(2))
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Investigation Config")
	// Distinctive names prove the explicit config (not the derived fallback)
	// reached WriteGoalMD.
	assert.Contains(t, mdContent, "Custom Investigator 1")
	assert.Contains(t, mdContent, "Custom Investigator 2")
}

func TestGoalCreate_AcceptsFourInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.GoalCreate("Four investigators", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(4))
	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)
}

func TestGoalCreate_RejectsTooFewInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Too few", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(1))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "2–4")
	// Rejection happens before the goals-file lock: no goal dir is created.
	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestGoalCreate_RejectsTooManyInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Too many", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(5))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "2–4")
}

func TestGoalCreate_RejectsEmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[1].Name = ""
	_, err := server.GoalCreate("Empty name", nil, []string{"check"}, "", "", "", 0, nil, nil, invs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "name")
	require.ErrorContains(t, err, "investigator[2]") // 1-based index
}

func TestGoalCreate_RejectsBadType(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Type = "bogus"
	_, err := server.GoalCreate("Bad type", nil, []string{"check"}, "", "", "", 0, nil, nil, invs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "invalid type")
}

// TestGoalCreate_AcceptsPlannerEmittedTypes proves the M4 superset fix: each
// investigator type the planner (task-plan-generate.xml) emits but that M2's
// enum originally lacked must now be accepted. Each newly-added type is paired
// with a known-good type ("static-analysis") to satisfy the 2–4 floor.
func TestGoalCreate_AcceptsPlannerEmittedTypes(t *testing.T) {
	newTypes := []string{
		"command",
		"environment-check",
		"file-check",
		"implementation-check",
		"integration-check",
	}

	for _, plannerType := range newTypes {
		t.Run(plannerType, func(t *testing.T) {
			tmpDir := t.TempDir()
			server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

			invs := []taskvisor.Investigator{
				{
					Name:     "Planner Investigator",
					Type:     plannerType,
					Commands: []string{"make check"},
					Pass:     "exit 0",
				},
				{
					Name:     "Known Good Investigator",
					Type:     "static-analysis",
					Commands: []string{"make analyse"},
					Pass:     "exit 0",
				},
			}

			output, err := server.GoalCreate("Planner type "+plannerType, nil, []string{"check"}, "", "", "", 0, nil, nil, invs)
			require.NoError(t, err)

			goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
			mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
			require.NoError(t, err)
			mdContent := string(mdData)
			assert.Contains(t, mdContent, "## Investigation Config")
			assert.Contains(t, mdContent, "Planner Investigator")
		})
	}
}

func TestGoalCreate_RejectsMissingCommand(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Commands = nil
	_, err := server.GoalCreate("Missing command", nil, []string{"check"}, "", "", "", 0, nil, nil, invs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "command")
}

func TestGoalCreate_RejectsEmptyPass(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Pass = ""
	_, err := server.GoalCreate("Empty pass", nil, []string{"check"}, "", "", "", 0, nil, nil, invs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "pass")
}

func TestGoalCreate_FallbackWhenNoInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// nil investigators → M1's deriveInvestigators fallback must still render a
	// valid 2–4 section.
	output, err := server.GoalCreate("Fallback", nil, []string{"PHPStan level 9 passes", "Unit tests pass"}, "", "", "", 0, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Investigation Config")
	assert.GreaterOrEqual(t, strings.Count(mdContent, "### Investigator "), 2)
	// The distinctive explicit-config names never appear via the fallback path,
	// confirming nil (not an explicit set) was threaded through.
	assert.NotContains(t, mdContent, "Custom Investigator")
}

func TestGoalCreate_UnchangedGuardsStillFire(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// Empty description still rejects even with a valid investigator set — the
	// new trailing param did not reorder the existing guards.
	_, err := server.GoalCreate("", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(2))
	assert.ErrorIs(t, err, ErrInvalidInput)

	// Empty validate still rejects.
	_, err = server.GoalCreate("Valid desc", nil, nil, "", "", "", 0, nil, nil, validInvestigatorSet(2))
	assert.ErrorIs(t, err, ErrInvalidInput)
}

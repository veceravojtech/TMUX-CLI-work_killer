package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	output, err := server.GoalCreate("Fix prices", []string{"Price matches API"}, []string{"Check price"}, 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "Fix prices", gf.Goals[0].Description)
	assert.Equal(t, "pending", gf.Goals[0].Status)
	assert.Equal(t, 0, gf.Goals[0].Retries)
	assert.Equal(t, 3, gf.Goals[0].MaxRetries)
	assert.Equal(t, []string{"Price matches API"}, gf.Goals[0].Acceptance)
	assert.Equal(t, []string{"Check price"}, gf.Goals[0].Validate)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	_, statErr := os.Stat(goalDir)
	assert.NoError(t, statErr)
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
	output, err := server.GoalCreate("Third", nil, nil, 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)
}

func TestGoalCreate_ExplicitMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Custom retries", nil, nil, 5)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_DefaultMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Default retries", nil, nil, 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 3, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_EmptyDescription(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("", nil, nil, 0)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "description cannot be empty")
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
	_, err := server.GoalCreate("Second", nil, nil, 0)
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
	_, err := server.GoalCreate("Test atomic", nil, nil, 0)
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
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
	output, err := server.GoalValidationDone("goal-001", "pass", findings, "")

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
	output, err := server.GoalValidationDone("goal-001", "fail", nil, "retry with fix")

	require.NoError(t, err)
	assert.True(t, output.Written)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json"))
	require.NoError(t, err)

	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	assert.Equal(t, "fail", sig["verdict"])
	assert.Equal(t, "retry with fix", sig["next_action"])
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
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "")

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
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "")

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
	_, err := server.GoalValidationDone("goal-999", "pass", nil, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "goal not found")
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
	_, err := server.GoalValidationDone("goal-001", "pass", nil, "")
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001", "signal.json.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
}

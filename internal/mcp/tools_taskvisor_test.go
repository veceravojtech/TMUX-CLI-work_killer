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
	output, err := server.GoalCreate("Fix prices", []string{"Price matches API"}, []string{"Check price"}, "", "", 0)

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
	output, err := server.GoalCreate("Third", nil, nil, "", "", 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)
}

func TestGoalCreate_ExplicitMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Custom retries", nil, nil, "", "", 5)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_DefaultMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Default retries", nil, nil, "", "", 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 3, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_EmptyDescription(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("", nil, nil, "", "", 0)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "description cannot be empty")
}

func TestGoalCreate_DescriptionTooLong(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	longDesc := strings.Repeat("a", 121)
	_, err := server.GoalCreate(longDesc, nil, nil, "", "", 0)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "120")
	assert.Contains(t, err.Error(), "--acceptance")
}

func TestGoalCreate_DescriptionExactlyAtLimit(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	exactDesc := strings.Repeat("b", 120)
	output, err := server.GoalCreate(exactDesc, nil, nil, "", "", 0)

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
	_, err := server.GoalCreate("Second", nil, nil, "", "", 0)
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
	_, err := server.GoalCreate("Test atomic", nil, nil, "", "", 0)
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
}

func TestGoalCreate_WithContext(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Refactor auth", []string{"Tests pass"}, nil, "Legacy code", "Performance", 0)

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

func TestGoalCreate_NoAcceptance(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple task", nil, nil, "", "", 0)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.NotContains(t, mdContent, "## Validation Rules")
	assert.NotContains(t, mdContent, "## Context")
	assert.NotContains(t, mdContent, "## Not In Scope")
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

func TestGoalCreate_Concurrent(t *testing.T) {
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
				nil, "", "",
				0,
			)
			errs[idx] = err
			if output != nil {
				ids[idx] = output.ID
			}
		}(i)
	}

	wg.Wait()

	successCount := 0
	for i := 0; i < goroutines; i++ {
		if errs[i] == nil {
			successCount++
		}
	}
	assert.Greater(t, successCount, 0, "at least one goroutine should succeed")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err, "goals.yaml must be valid YAML (no corruption)")
	require.NotNil(t, gf)
	assert.GreaterOrEqual(t, len(gf.Goals), 1, "at least 1 goal should be persisted")
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

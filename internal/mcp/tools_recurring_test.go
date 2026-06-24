package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
)

// stageActiveRecurring writes an active recurring.yaml to tmpDir and returns it.
func stageActiveRecurring(t *testing.T, tmpDir string) *taskvisor.RecurringFile {
	t.Helper()
	rf := &taskvisor.RecurringFile{Task: &taskvisor.RecurringTask{
		ID:              "recurring-001",
		Prompt:          "p",
		TotalCycles:     10,
		CompletedCycles: 0,
		Status:          taskvisor.RecurringActive,
		CurrentCycle:    taskvisor.RecurringCycle{Index: 1, Phase: "dispatching"},
	}}
	require.NoError(t, taskvisor.SaveRecurring(tmpDir, rf))
	return rf
}

// stageMarker writes the .tmux-cli/recurring-active marker to tmpDir.
func stageMarker(t *testing.T, tmpDir string) string {
	t.Helper()
	marker := filepath.Join(tmpDir, ".tmux-cli", "recurring-active")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o755))
	require.NoError(t, os.WriteFile(marker, []byte("active"), 0o644))
	return marker
}

func TestRecurringCreate_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.RecurringCreate(RecurringCreateInput{Prompt: "p", Cycles: 10})

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Created)

	rf, loadErr := taskvisor.LoadRecurring(tmpDir)
	require.NoError(t, loadErr)
	require.NotNil(t, rf)
	require.NotNil(t, rf.Task)
	assert.Equal(t, taskvisor.RecurringActive, rf.Task.Status)
	assert.Equal(t, 10, rf.Task.TotalCycles)
	assert.Equal(t, 0, rf.Task.CompletedCycles)
	assert.Equal(t, 1, rf.Task.CurrentCycle.Index)
	assert.Equal(t, "dispatching", rf.Task.CurrentCycle.Phase)

	marker := filepath.Join(tmpDir, ".tmux-cli", "recurring-active")
	_, statErr := os.Stat(marker)
	assert.NoError(t, statErr)
}

func TestRecurringCreate_NoExecutorCall(t *testing.T) {
	tmpDir := t.TempDir()
	executor := new(testutil.MockTmuxExecutor)
	server := newTestServer(executor, tmpDir)

	_, _ = server.RecurringCreate(RecurringCreateInput{Prompt: "p", Cycles: 10})

	executor.AssertExpectations(t)
}

func TestRecurringCreate_EmptyPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.RecurringCreate(RecurringCreateInput{Prompt: "", Cycles: 5})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)

	_, statErr := os.Stat(taskvisor.RecurringFilePath(tmpDir))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRecurringCreate_ZeroCycles(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.RecurringCreate(RecurringCreateInput{Prompt: "p", Cycles: 0})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)

	_, statErr := os.Stat(taskvisor.RecurringFilePath(tmpDir))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRecurringCreate_AlreadyActive(t *testing.T) {
	tmpDir := t.TempDir()
	stageActiveRecurring(t, tmpDir)
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.RecurringCreate(RecurringCreateInput{Prompt: "p", Cycles: 10})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "already active")
}

func TestRecurringStatus_Active(t *testing.T) {
	tmpDir := t.TempDir()
	stageActiveRecurring(t, tmpDir)
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.RecurringStatus()

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, output.Active)
	require.NotNil(t, output.Task)
	assert.Equal(t, 1, output.Task.CurrentCycle.Index)
	assert.Equal(t, "dispatching", output.Task.CurrentCycle.Phase)
}

func TestRecurringStatus_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.RecurringStatus()

	require.NoError(t, err)
	require.NotNil(t, output)
	assert.False(t, output.Active)
}

func TestRecurringStop_Active(t *testing.T) {
	tmpDir := t.TempDir()
	stageActiveRecurring(t, tmpDir)
	marker := stageMarker(t, tmpDir)
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.RecurringStop()

	require.NoError(t, err)

	rf, loadErr := taskvisor.LoadRecurring(tmpDir)
	require.NoError(t, loadErr)
	require.NotNil(t, rf)
	require.NotNil(t, rf.Task)
	assert.Equal(t, taskvisor.RecurringStopped, rf.Task.Status)

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr))
}

func TestRecurringStop_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.RecurringStop()

	require.NoError(t, err)
}

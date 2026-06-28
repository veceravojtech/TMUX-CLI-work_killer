package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

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
	assert.Contains(t, err.Error(), "no pending or roadmap goals")
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

// --- Streaming goal-generation: taskvisor-start admits roadmap/bootstrap plans (execute-1) ---
//
// These lock the contract that the early-start signal succeeds on the two plan
// shapes the streaming generator emits before the whole roadmap exists — a
// roadmap-only skeleton plan and a bootstrap-only pending plan — and still errors
// when there is no non-terminal work. They mirror TaskvisorStart at
// tools_taskvisor.go:197-210 (the gate counts GoalPending OR GoalRoadmap).

// TestTaskvisorStart_AdmitsRoadmapOnlyPlan: a goals.yaml of only `status: roadmap`
// skeletons (what the director's Stage-1 generator emits) is startable — the
// daemon elaborates them JIT.
func TestTaskvisorStart_AdmitsRoadmapOnlyPlan(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Skeleton one
  status: roadmap
- id: goal-002
  description: Skeleton two
  status: roadmap
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.TaskvisorStart()

	require.NoError(t, err)
	assert.True(t, output.Started)

	signalPath := filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start")
	_, statErr := os.Stat(signalPath)
	assert.NoError(t, statErr)
}

// TestTaskvisorStart_AdmitsBootstrapPendingPlan: a goals.yaml of only born-pending
// bootstrap goals (the early-start trigger) is startable — the daemon dispatches
// goal-001 next tick while later skeletons are still being authored.
func TestTaskvisorStart_AdmitsBootstrapPendingPlan(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Bootstrap gate0
  status: pending
- id: goal-002
  description: Bootstrap scaffold
  status: pending
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.TaskvisorStart()

	require.NoError(t, err)
	assert.True(t, output.Started)
}

// TestTaskvisorStart_ErrorsOnNoNonTerminalGoals: an all-terminal (or empty) plan
// has no work the daemon can advance, so the gate errors `no pending or roadmap
// goals` — the negative side of the admit contract.
func TestTaskvisorStart_ErrorsOnNoNonTerminalGoals(t *testing.T) {
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
	assert.Contains(t, err.Error(), "no pending or roadmap goals")
}

// --- GoalCreate tests ---

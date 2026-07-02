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

// --- Incremental planning: taskvisor-start admits an EMPTY ledger (execute-1) ---
//
// The daemon's incremental loop (internal/taskvisor/plannext.go) authors
// goal-001 ITSELF after activation — daemon.go's idle poll synthesizes an empty
// in-memory GoalsFile when goals.yaml is missing in incremental mode. So the
// MCP start gate must not refuse an empty ledger when
// setup.LoadSettings(...).Taskvisor.PlanningMode == setup.PlanningModeIncremental;
// roadmap mode keeps both refusals byte-identical.

// writeTestSettingYaml seeds .tmux-cli/setting.yaml with the given planning
// mode so TaskvisorStart's setup.LoadSettings read sees it.
func writeTestSettingYaml(t *testing.T, dir, planningMode string) {
	t.Helper()
	confDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	content := "taskvisor:\n  planning_mode: " + planningMode + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "setting.yaml"), []byte(content), 0o644))
}

// TestTaskvisorStart_IncrementalAllowsEmptyLedger: with planning_mode
// incremental, both empty-ledger shapes — no goals.yaml at all (the daemon's
// documented valid start state) and a ledger with zero startable goals — write
// the start signal instead of refusing.
func TestTaskvisorStart_IncrementalAllowsEmptyLedger(t *testing.T) {
	t.Run("missing goals.yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeTestSettingYaml(t, tmpDir, "incremental")

		server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
		output, err := server.TaskvisorStart()

		require.NoError(t, err)
		assert.True(t, output.Started)

		data, readErr := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start"))
		require.NoError(t, readErr)
		assert.Equal(t, "start", string(data))
	})

	t.Run("zero startable goals", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeTestSettingYaml(t, tmpDir, "incremental")
		writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Done
  status: done
- id: goal-002
  description: Failed
  status: failed
`)

		server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
		output, err := server.TaskvisorStart()

		require.NoError(t, err)
		assert.True(t, output.Started)

		_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start"))
		assert.NoError(t, statErr)
	})
}

// TestTaskvisorStart_RoadmapStillRefusesEmptyLedger: an explicit
// planning_mode roadmap keeps BOTH existing refusals byte-identical — the
// missing-file error and the no-startable-goals error.
func TestTaskvisorStart_RoadmapStillRefusesEmptyLedger(t *testing.T) {
	t.Run("missing goals.yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeTestSettingYaml(t, tmpDir, "roadmap")

		server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
		_, err := server.TaskvisorStart()

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidInput)
		assert.Contains(t, err.Error(), "goals.yaml not found")
	})

	t.Run("zero startable goals", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeTestSettingYaml(t, tmpDir, "roadmap")
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

		_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "taskvisor-start"))
		assert.True(t, os.IsNotExist(statErr), "roadmap refusal must not write the start signal")
	})
}

// --- GoalCreate tests ---

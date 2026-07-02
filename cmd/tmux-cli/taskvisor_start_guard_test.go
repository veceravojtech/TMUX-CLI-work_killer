package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
)

// seedCLIPlanningMode writes .tmux-cli/setting.yaml with the given planning
// mode so runTaskvisorStart's shared guard sees it.
func seedCLIPlanningMode(t *testing.T, dir, mode string) {
	t.Helper()
	confDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	content := "taskvisor:\n  planning_mode: " + mode + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "setting.yaml"), []byte(content), 0o644))
}

// TestTaskvisorStartCmd_IncrementalAllowsEmptyLedger: CLI parity with the MCP
// taskvisor-start relaxation — in incremental planning mode both empty-ledger
// shapes (no goals.yaml at all; a ledger with zero startable goals) write the
// start signal instead of refusing, because the daemon authors goal-001 itself.
func TestTaskvisorStartCmd_IncrementalAllowsEmptyLedger(t *testing.T) {
	t.Run("missing goals.yaml", func(t *testing.T) {
		withTempCwd(t, func(dir string) {
			seedCLIPlanningMode(t, dir, "incremental")

			err := runTaskvisorStart(nil, nil)
			require.NoError(t, err)

			_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-start"))
			assert.NoError(t, statErr, "signal file should exist")
		})
	})

	t.Run("zero startable goals", func(t *testing.T) {
		withTempCwd(t, func(dir string) {
			seedCLIPlanningMode(t, dir, "incremental")
			gf := &taskvisor.GoalsFile{
				Goals: []taskvisor.Goal{
					{ID: "goal-001", Description: "Done goal", Status: taskvisor.GoalDone, MaxRetries: 3},
				},
			}
			require.NoError(t, taskvisor.SaveGoals(dir, gf))

			err := runTaskvisorStart(nil, nil)
			require.NoError(t, err)

			_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-start"))
			assert.NoError(t, statErr, "signal file should exist")
		})
	})
}

// TestTaskvisorStartCmd_RoadmapRefusesEmptyLedger: explicit roadmap mode keeps
// BOTH CLI refusals byte-identical to the pre-relaxation messages and never
// writes the start signal.
func TestTaskvisorStartCmd_RoadmapRefusesEmptyLedger(t *testing.T) {
	t.Run("missing goals.yaml", func(t *testing.T) {
		withTempCwd(t, func(dir string) {
			seedCLIPlanningMode(t, dir, "roadmap")

			err := runTaskvisorStart(nil, nil)
			require.Error(t, err)
			assert.Equal(t, "no goals.yaml found — add goals first with 'taskvisor goal add'", err.Error())

			_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-start"))
			assert.True(t, os.IsNotExist(statErr), "roadmap refusal must not write the start signal")
		})
	})

	t.Run("zero startable goals", func(t *testing.T) {
		withTempCwd(t, func(dir string) {
			seedCLIPlanningMode(t, dir, "roadmap")
			gf := &taskvisor.GoalsFile{
				Goals: []taskvisor.Goal{
					{ID: "goal-001", Description: "Done goal", Status: taskvisor.GoalDone, MaxRetries: 3},
				},
			}
			require.NoError(t, taskvisor.SaveGoals(dir, gf))

			err := runTaskvisorStart(nil, nil)
			require.Error(t, err)
			assert.Equal(t, "no pending or recoverable goals — all goals are done or failed", err.Error())

			_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-start"))
			assert.True(t, os.IsNotExist(statErr), "roadmap refusal must not write the start signal")
		})
	})
}

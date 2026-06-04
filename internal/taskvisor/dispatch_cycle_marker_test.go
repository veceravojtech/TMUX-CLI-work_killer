package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cycleMarkerGoal builds a goal whose CurrentCycle equals consumed+1: the live
// per-class counters hold REMAINING budget, so consuming `consumed` from the
// code class (CodeRetries = Max - consumed) advances the cycle number.
func cycleMarkerGoal(id string, consumed int) *Goal {
	return &Goal{
		ID:                   id,
		MaxCodeRetries:       5,
		CodeRetries:          5 - consumed,
		MaxSpecRetries:       2,
		SpecRetries:          2,
		MaxValidationRetries: 3,
		ValidationRetries:    3,
	}
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// The isolation property the global marker cannot provide: goal B's dispatch
// must not clobber goal A's cycle number. At mg>1 each goal gets its own
// goals/<id>/current-cycle marker fed by the SAME CurrentCycle computation
// that feeds the (documented last-writer, unused at mg>1) global marker.
func TestWriteCycleMarker_PerGoalIsolation_TwoGoals(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{workDir: dir}

	goalA := cycleMarkerGoal("goal-045", 1) // CurrentCycle = 2
	goalB := cycleMarkerGoal("goal-046", 3) // CurrentCycle = 4
	require.Equal(t, 2, CurrentCycle(goalA))
	require.Equal(t, 4, CurrentCycle(goalB))

	require.NoError(t, d.writeCycleMarker(goalA, 2))
	require.NoError(t, d.writeCycleMarker(goalB, 2))

	// A's per-goal marker still reads 2 AFTER B's write — no clobber.
	assert.Equal(t, "2", readMarker(t, filepath.Join(dir, ".tmux-cli", "goals", "goal-045", "current-cycle")))
	assert.Equal(t, "4", readMarker(t, filepath.Join(dir, ".tmux-cli", "goals", "goal-046", "current-cycle")))
	// Global marker is last-writer-wins (B), kept for MaxGoals<=1 fallback.
	assert.Equal(t, "4", readMarker(t, filepath.Join(dir, ".tmux-cli", "taskvisor-current-cycle")))
}

// MaxGoals<=1 must produce an artifact set byte-identical to the pre-change
// build: NO per-goal marker (a stale one from a prior mg>1 run is removed),
// global marker carries the cycle, research/cycle-<N>/ pre-created.
func TestWriteCycleMarker_MaxGoals1_NoPerGoalArtifact(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{workDir: dir}

	goal := cycleMarkerGoal("goal-045", 2) // CurrentCycle = 3
	require.Equal(t, 3, CurrentCycle(goal))

	// Stale per-goal marker from an earlier mg>1 run (max_goals flipped >1 -> 1).
	perGoal := filepath.Join(dir, ".tmux-cli", "goals", "goal-045", "current-cycle")
	require.NoError(t, os.MkdirAll(filepath.Dir(perGoal), 0o755))
	require.NoError(t, os.WriteFile(perGoal, []byte("9"), 0o644))

	require.NoError(t, d.writeCycleMarker(goal, 1))

	_, err := os.Stat(perGoal)
	assert.True(t, os.IsNotExist(err), "stale per-goal marker must be removed at mg<=1")
	assert.Equal(t, "3", readMarker(t, filepath.Join(dir, ".tmux-cli", "taskvisor-current-cycle")))
	cycleDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-045", "research", fmt.Sprintf("cycle-%d", CurrentCycle(goal)))
	info, err := os.Stat(cycleDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// The global marker is written unconditionally — at mg>1 too — preserving the
// standalone/MaxGoals<=1 fallback contract.
func TestWriteCycleMarker_GlobalAlwaysWritten(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{workDir: dir}

	goal := cycleMarkerGoal("goal-046", 0) // CurrentCycle = 1
	require.NoError(t, d.writeCycleMarker(goal, 2))

	assert.Equal(t, "1", readMarker(t, filepath.Join(dir, ".tmux-cli", "taskvisor-current-cycle")))
	assert.Equal(t, "1", readMarker(t, filepath.Join(dir, ".tmux-cli", "goals", "goal-046", "current-cycle")))
}

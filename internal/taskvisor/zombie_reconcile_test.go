package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeZombieMarkers writes the four stale runtime markers a dead session leaves
// behind for goalID, plus taskvisor-active, and returns their paths so a test can
// assert the four are deleted and taskvisor-active survives. Mirrors the marker
// setup in TestCrashRecovery_OrphanedSignalResume_DeadWindow_RePends.
func writeZombieMarkers(t *testing.T, dir, goalID string) (stale []string, active string) {
	t.Helper()
	goalDir := filepath.Join(dir, ".tmux-cli", "goals", goalID)
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	stale = []string{
		filepath.Join(goalDir, "supervisor-window"),
		filepath.Join(goalDir, "current-cycle"),
		filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"),
		filepath.Join(dir, ".tmux-cli", "taskvisor-current-cycle"),
	}
	for _, p := range stale {
		require.NoError(t, os.WriteFile(p, []byte("stale"), 0o644))
	}
	active = filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	require.NoError(t, os.WriteFile(active, nil, 0o644))
	return stale, active
}

// TestPreflightReconcilesZombieRunningGoal is the REQUIRED case (validate greps
// for this name): a GoalRunning goal whose worker window is absent from the live
// session (an empty/nil survey == dead session) is re-pended, its stale markers
// are deleted, taskvisor-active survives, its ID is returned, and the queue is
// unblocked — a previously head-of-line-blocked pending goal becomes runnable and
// no GoalRunning remains.
func TestPreflightReconcilesZombieRunningGoal(t *testing.T) {
	dir := t.TempDir()
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-020",
		Goals: []Goal{
			{ID: "goal-020", Description: "zombie", Status: GoalRunning, StartedAt: "2026-06-14T10:25:51Z", Retries: 0, MaxRetries: 3},
			{ID: "goal-021", Description: "ready next", Status: GoalPending, Retries: 0, MaxRetries: 3},
		},
	})
	stale, active := writeZombieMarkers(t, dir, "goal-020")

	// nil window slice == dead session: goal-020 has no live worker window.
	reconciled, err := PreflightReconcileZombieGoals(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-020"}, reconciled, "the zombie goal's ID must be returned")

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g20, ok := goals.GoalByID("goal-020")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g20.Status, "zombie running goal must be re-pended")
	assert.Empty(t, g20.StartedAt, "started_at must be cleared on re-pend")

	for _, p := range stale {
		_, statErr := os.Stat(p)
		assert.True(t, os.IsNotExist(statErr), "stale marker must be deleted: %s", p)
	}
	_, activeErr := os.Stat(active)
	assert.NoError(t, activeErr, "taskvisor-active must survive — the daemon stays active")

	// Queue unblocked: a candidate is now dispatchable and no goal is still running.
	assert.NotEmpty(t, goals.RunnableCandidates(), "a previously blocked pending goal must now be runnable")
	assert.False(t, goals.AnyRunning(), "no GoalRunning may remain after reconcile")
}

// TestPreflightLeavesLiveRunningGoalUntouched: a GoalRunning goal whose
// supervisor-<ns> window IS present in the survey is the normal in-flight case
// and must never be disturbed.
func TestPreflightLeavesLiveRunningGoalUntouched(t *testing.T) {
	dir := t.TempDir()
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-020",
		Goals: []Goal{
			{ID: "goal-020", Description: "live", Status: GoalRunning, StartedAt: "2026-06-14T10:25:51Z", Retries: 0, MaxRetries: 3},
		},
	})

	windows := []tmux.WindowInfo{{TmuxWindowID: "@7", Name: "supervisor-020"}}
	reconciled, err := PreflightReconcileZombieGoals(dir, windows)
	require.NoError(t, err)
	assert.Empty(t, reconciled, "a live running goal must not be reconciled")

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-020")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status, "live running goal stays running")
	assert.Equal(t, "2026-06-14T10:25:51Z", g.StartedAt, "started_at must be unchanged")
}

// TestPreflightFailsZombieWhenRetriesExhausted mirrors crash recovery's exhausted
// branch: a dead-window running goal with Retries == MaxRetries is failed (with
// FinishedAt set), not re-pended.
func TestPreflightFailsZombieWhenRetriesExhausted(t *testing.T) {
	dir := t.TempDir()
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-030",
		Goals: []Goal{
			{ID: "goal-030", Description: "exhausted", Status: GoalRunning, StartedAt: "2026-06-14T10:25:51Z", Retries: 3, MaxRetries: 3},
		},
	})

	reconciled, err := PreflightReconcileZombieGoals(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-030"}, reconciled)

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-030")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "retries exhausted -> failed")
	assert.NotEmpty(t, g.FinishedAt, "finished_at must be set on fail")
}

// TestClearGoalRuntimeMarkersDelegation guards the recovery.go extract-and-
// delegate refactor: the free clearGoalRuntimeMarkers removes the same four
// stale markers and leaves taskvisor-active intact.
func TestClearGoalRuntimeMarkersDelegation(t *testing.T) {
	dir := t.TempDir()
	stale, active := writeZombieMarkers(t, dir, "goal-020")

	clearGoalRuntimeMarkers(dir, "goal-020")

	for _, p := range stale {
		_, statErr := os.Stat(p)
		assert.True(t, os.IsNotExist(statErr), "stale marker must be deleted: %s", p)
	}
	_, activeErr := os.Stat(active)
	assert.NoError(t, activeErr, "taskvisor-active must NOT be removed")
}

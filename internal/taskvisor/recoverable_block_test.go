package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- HasRecoverableBlock unit tests (the recoverable-frontier predicate) ---

func TestHasRecoverableBlock_TrueForBlockedByDoneWithSatisfiedDeps(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-003", Status: GoalDone}, // the recovered cascade blocker
		// goal-015 hard-cascaded behind goal-003; goal-003 is now Done and its
		// own deps (goal-001) are satisfied → immediately recoverable.
		{ID: "goal-015", Status: GoalBlocked, BlockedBy: "goal-003", DependsOn: []string{"goal-001"}},
	}}
	assert.True(t, gf.HasRecoverableBlock())
}

func TestHasRecoverableBlock_FalseWhenBlockerFailed(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-003", Status: GoalFailed}, // genuine hard block: blocker failed
		{ID: "goal-015", Status: GoalBlocked, BlockedBy: "goal-003", DependsOn: []string{"goal-001", "goal-003"}},
	}}
	assert.False(t, gf.HasRecoverableBlock())
}

func TestHasRecoverableBlock_FalseForPreconditionPark(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		// Bug C class: parked on a precondition — guarded by HasResumablePark, not here.
		{ID: "goal-015", Status: GoalBlocked, BlockedBy: "goal-001", BlockedByPrecondition: true, DependsOn: []string{"goal-001"}},
	}}
	assert.False(t, gf.HasRecoverableBlock())
}

func TestHasRecoverableBlock_FalseForCircuitBreaker(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-015", Status: GoalBlocked, BlockedBy: "convergence-circuit-breaker", DependsOn: []string{"goal-001"}},
	}}
	assert.False(t, gf.HasRecoverableBlock())
}

func TestHasRecoverableBlock_FalseWhenDepUnsatisfied(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-003", Status: GoalDone},    // blocker recovered...
		{ID: "goal-009", Status: GoalRunning}, // ...but another dep is still running
		{ID: "goal-015", Status: GoalBlocked, BlockedBy: "goal-003", DependsOn: []string{"goal-001", "goal-009"}},
	}}
	assert.False(t, gf.HasRecoverableBlock())
}

// --- deactivateOnCompletion guard tests ---

func TestDeactivateOnCompletion_StaysActiveAndReconcilesRecoverableBlock(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.mode = modeActive

	// Plant the active guard the daemon would normally hold.
	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	require.NoError(t, os.MkdirAll(filepath.Dir(guardPath), 0o755))
	require.NoError(t, os.WriteFile(guardPath, nil, 0o644))

	// goal-015 repro: only done goals + one recoverable cascade block, 0 pending/running.
	gf := &GoalsFile{
		CurrentGoal: "goal-015",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalDone},
			{ID: "goal-003", Status: GoalDone}, // the recovered hard-cascade blocker
			// blocked_by goal-003 (a CASCADE artifact, NOT in depends_on); own deps all done.
			{ID: "goal-015", Status: GoalBlocked, BlockedBy: "goal-003", DependsOn: []string{"goal-001", "goal-002"}},
		},
	}
	writeGoals(t, dir, gf)

	out := captureLog(t, func() {
		require.NoError(t, d.deactivateOnCompletion(gf))
	})

	// Daemon must NOT have torn down.
	assert.Equal(t, modeActive, d.mode, "daemon should stay active")
	_, statErr := os.Stat(guardPath)
	assert.NoError(t, statErr, "taskvisor-active guard must remain")
	assert.Contains(t, out, "recoverable cascade block(s) outstanding")

	// The recoverable block was un-stuck by ReconcileBlocks, in-memory and persisted.
	g, ok := gf.GoalByID("goal-015")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g.Status)
	assert.Equal(t, "", g.BlockedBy)

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-015")
	require.True(t, ok)
	assert.Equal(t, GoalPending, rg.Status, "un-stick must be persisted")
	assert.Equal(t, "", rg.BlockedBy)
}

func TestDeactivateOnCompletion_DeactivatesWhenNoRecoverableFrontier(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.mode = modeActive

	guardPath := filepath.Join(dir, ".tmux-cli", "taskvisor-active")
	require.NoError(t, os.MkdirAll(filepath.Dir(guardPath), 0o755))
	require.NoError(t, os.WriteFile(guardPath, nil, 0o644))

	// No recoverable frontier: the only block sits behind a GoalFailed blocker
	// (genuine hard block). Deactivation must proceed exactly as before.
	gf := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalFailed},
			{ID: "goal-003", Status: GoalBlocked, BlockedBy: "goal-002", DependsOn: []string{"goal-002"}},
		},
	}
	writeGoals(t, dir, gf)

	require.NoError(t, d.deactivateOnCompletion(gf))

	assert.Equal(t, modeIdle, d.mode, "daemon should deactivate")
	_, statErr := os.Stat(guardPath)
	assert.True(t, os.IsNotExist(statErr), "taskvisor-active guard must be removed on deactivation")

	// The genuine hard block must stay blocked (never resumed).
	g, ok := gf.GoalByID("goal-003")
	require.True(t, ok)
	assert.Equal(t, GoalBlocked, g.Status)
	assert.Equal(t, "goal-002", g.BlockedBy)
}

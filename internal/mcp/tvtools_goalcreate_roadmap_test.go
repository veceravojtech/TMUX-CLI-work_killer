package mcp

import (
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalCreateRoadmap_Skeleton proves the roadmap-skeleton creator persists a
// GoalRoadmap goal with deliverable_area and NO validate (the param the roadmap
// generator + director self-heal rely on).
func TestGoalCreateRoadmap_Skeleton(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	out, err := server.GoalCreateRoadmap("Implement error handling", "auth", nil, "projects/api/src/Http/ErrorHandling/", 0)
	require.NoError(t, err)
	require.NotNil(t, out)

	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, ok := gf.GoalByID(out.ID)
	require.True(t, ok)
	assert.Equal(t, taskvisor.GoalRoadmap, g.Status)
	assert.Equal(t, "projects/api/src/Http/ErrorHandling/", g.DeliverableArea)
	assert.Empty(t, g.Validate)
}

// TestGoalCreateRoadmap_RejectsBadPhase proves the thin MCP phase enum check fires.
func TestGoalCreateRoadmap_RejectsBadPhase(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreateRoadmap("bad phase", "not-a-phase", nil, "x/", 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// TestTaskvisorStart_AdmitsRoadmapOnly proves a roadmap-only goals.yaml (every goal
// a skeleton, as the director's Stage-1 generator emits) is startable — the gate
// counts roadmap goals as real (elaboratable) work.
func TestTaskvisorStart_AdmitsRoadmapOnly(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreateRoadmap("only a skeleton", "domain", nil, "internal/x/", 0)
	require.NoError(t, err)

	out, err := server.TaskvisorStart()
	require.NoError(t, err)
	assert.True(t, out.Started)
}

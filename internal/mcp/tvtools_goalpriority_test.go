package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
)

// TestGoalCreateHandler_Priority proves the goal-create MCP wire param threads
// through: a GoalCreateInput with Priority set persists it, and an omitted
// priority defaults to 0 (omitempty zero value).
func TestGoalCreateHandler_Priority(t *testing.T) {
	t.Run("given priority persists", func(t *testing.T) {
		tmpDir := t.TempDir()
		server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

		_, out, err := server.GoalCreateHandler(context.Background(), nil, GoalCreateInput{
			Description: "Prioritized goal",
			Validate:    []string{"check"},
			Priority:    6,
		})
		require.NoError(t, err)

		gf, err := taskvisor.LoadGoals(tmpDir)
		require.NoError(t, err)
		g, ok := gf.GoalByID(out.ID)
		require.True(t, ok)
		assert.Equal(t, 6, g.Priority)
	})

	t.Run("omitted priority defaults to zero", func(t *testing.T) {
		tmpDir := t.TempDir()
		server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

		_, out, err := server.GoalCreateHandler(context.Background(), nil, GoalCreateInput{
			Description: "Default goal",
			Validate:    []string{"check"},
		})
		require.NoError(t, err)

		gf, err := taskvisor.LoadGoals(tmpDir)
		require.NoError(t, err)
		g, ok := gf.GoalByID(out.ID)
		require.True(t, ok)
		assert.Equal(t, 0, g.Priority)
	})
}

// TestGoalPriority_Integration proves the dual-struct round-trip: a goal created
// via the MCP surface with a non-default priority survives a CLI-style mutation
// (the exact WithGoalsLock→LoadGoals→GoalByID→SaveGoals shape of
// runTaskvisorGoalPriority) AND a subsequent MCP-side load via the tvGoal mirror.
// If the tvGoal mirror lacked the priority field (M1 hazard), the final MCP read
// would silently observe 0 instead of 9.
func TestGoalPriority_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// Create via MCP with a non-default priority.
	out, err := server.GoalCreate("Round-trip goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 6, "", false)
	require.NoError(t, err)

	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, ok := gf.GoalByID(out.ID)
	require.True(t, ok)
	require.Equal(t, 6, g.Priority, "MCP-created priority must persist")

	// Mutate via the CLI handler's exact persistence shape.
	require.NoError(t, taskvisor.WithGoalsLock(tmpDir, func() error {
		gf, err := taskvisor.LoadGoals(tmpDir)
		require.NoError(t, err)
		g, ok := gf.GoalByID(out.ID)
		require.True(t, ok)
		g.Priority = 9
		return taskvisor.SaveGoals(tmpDir, gf)
	}))

	// Read back through the MCP dual-struct mirror (tvGoal): the field must
	// survive both structs, not zero out on the load-resave path.
	tvgf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	var found bool
	for _, tg := range tvgf.Goals {
		if tg.ID == out.ID {
			found = true
			assert.Equal(t, 9, tg.Priority, "tvGoal mirror must preserve priority")
		}
	}
	require.True(t, found, "goal must be present in tvGoal load")
}

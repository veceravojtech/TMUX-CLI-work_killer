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

func TestGoalCreate_InvalidLaneRejected(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Bad lane", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "fast")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "lane")
	// Rejected before any side effect: no goals.yaml, no goal dir.
	_, statErr := os.Stat(tvGoalsFilePath(tmpDir))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be created on invalid lane")
	_, statErr = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "no goal dir may be created on invalid lane")
}

func TestGoalCreate_LaneSoloPersistsAndSurfaces(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Solo lane goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "solo")
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "solo", gf.Goals[0].Lane, "lane must persist to goals.yaml via the tvGoal mirror")

	md, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "## Lane\n\nsolo\n", "goal.md must surface the bare lane string")
}

// TestGoalAddPrerequisite_PreservesLane: the load-resave erase hazard — wiring a
// prerequisite rewrites the WHOLE goals file via tvGoal, which must not strip
// lane: solo from any goal in the file.
func TestGoalAddPrerequisite_PreservesLane(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Solo goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "solo")
	require.NoError(t, err)
	_, err = server.GoalCreate("Prerequisite", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "")
	require.NoError(t, err)

	_, err = server.GoalAddPrerequisite("goal-001", "goal-002")
	require.NoError(t, err)

	// Re-read via the CANONICAL taskvisor loader: the MCP resave must carry lane.
	canonical, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, ok := canonical.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, taskvisor.LaneSolo, g.Lane, "lane: solo must survive the MCP load-resave round-trip")
	g2, ok := canonical.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, "", g2.Lane, "lane-absent goal must stay lane-absent through the resave")
}

package mcp

import (
	"bytes"
	"log"
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

func TestGoalCreate_LaneSoloEmptyValidateRejected(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Solo without validate", nil, nil, "", "", "", 0, nil, nil, nil, nil, 0, "solo")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "solo-lane creation cross-check")
	// Rejected before any side effect: no goals.yaml, no goal dir.
	_, statErr := os.Stat(tvGoalsFilePath(tmpDir))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be created on solo empty-validate rejection")
	_, statErr = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "no goal dir may be created on solo empty-validate rejection")
}

func TestGoalCreate_LaneSoloEmptyValidateSliceRejected(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Solo with empty validate slice", nil, []string{}, "", "", "", 0, nil, nil, nil, nil, 0, "solo")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "solo-lane creation cross-check")
	_, statErr := os.Stat(tvGoalsFilePath(tmpDir))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be created on solo empty-validate rejection")
	_, statErr = os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "no goal dir may be created on solo empty-validate rejection")
}

// No t.Parallel(): log.SetOutput mutates global logger state.
func TestGoalCreate_LaneSoloMultiTopDirScope_WarnsButCreates(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Solo spanning two top dirs", nil, []string{"go build ./..."}, "", "", "", 0, nil, nil, nil, []string{"internal/mcp/", "cmd/tmux-cli/"}, 0, "solo")
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "solo", gf.Goals[0].Lane, "the warn-only cross-check must still create the goal with lane persisted")

	logged := buf.String()
	assert.Contains(t, logged, "solo-lane creation cross-check", "multi-top-dir solo scope must warn-log the cross-check")
	assert.Contains(t, logged, "cmd", "warning must name the offending top-level directories")
	assert.Contains(t, logged, "internal", "warning must name the offending top-level directories")
}

// No t.Parallel(): log.SetOutput mutates global logger state.
func TestGoalCreate_LaneSoloSingleTopDirScope_NoWarn(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Solo within one top dir", nil, []string{"go build ./..."}, "", "", "", 0, nil, nil, nil, []string{"internal/mcp/", "internal/taskvisor/"}, 0, "solo")
	require.NoError(t, err)

	assert.NotContains(t, buf.String(), "solo-lane creation cross-check", "single-top-dir solo scope must not warn")
}

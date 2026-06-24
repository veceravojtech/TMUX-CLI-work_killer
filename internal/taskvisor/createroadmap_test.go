package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateGoal_RoadmapSkeleton proves a status=roadmap spec is created WITHOUT a
// validate rule (the create-time "≥1 validate" mandate is skipped for skeletons),
// persists GoalRoadmap + DeliverableArea, and writes NO validate.sh.
func TestCreateGoal_RoadmapSkeleton(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description:     "Implement global error handling",
		Phase:           "error_handling",
		DeliverableArea: "projects/api/src/Http/ErrorHandling/",
		Status:          GoalRoadmap,
		// deliberately NO Validate / Acceptance / Scope
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := gf.GoalByID(id)
	require.True(t, ok)
	assert.Equal(t, GoalRoadmap, g.Status)
	assert.Equal(t, "projects/api/src/Http/ErrorHandling/", g.DeliverableArea)
	assert.Empty(t, g.Validate)

	// A skeleton carries no validate, so no validate.sh artifact is authored.
	_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "goals", id, "validate.sh"))
	assert.True(t, os.IsNotExist(statErr), "roadmap skeleton must not write validate.sh")
}

// TestCreateGoal_NormalStillRequiresValidate proves the roadmap relaxation did NOT
// weaken the normal path: a non-roadmap goal with no validate is still rejected.
func TestCreateGoal_NormalStillRequiresValidate(t *testing.T) {
	dir := t.TempDir()
	_, _, err := CreateGoal(dir, GoalSpec{Description: "no validate goal"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one validation rule is required")
}

// TestCreateGoal_RejectsUnknownStatus proves only ""/roadmap are creatable.
func TestCreateGoal_RejectsUnknownStatus(t *testing.T) {
	dir := t.TempDir()
	_, _, err := CreateGoal(dir, GoalSpec{
		Description: "bad status",
		Validate:    []string{"go build ./..."},
		Status:      GoalRunning,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid creation status")
}

package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestElaborationCandidates_AdmitsRoadmapDepsSatisfied proves a GoalRoadmap goal
// whose deps are all GoalDone is admitted (ready to be specced against the now-real
// tree), while one with an unfinished producer is held.
func TestElaborationCandidates_AdmitsRoadmapDepsSatisfied(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-002", Status: GoalRoadmap, DependsOn: []string{"goal-001"}},
		{ID: "goal-003", Status: GoalRoadmap, DependsOn: []string{"goal-002"}}, // producer not done
	}}
	got := gf.ElaborationCandidates()
	require.Len(t, got, 1)
	assert.Equal(t, "goal-002", got[0].ID)
}

// TestElaborationCandidates_IgnoresNonRoadmapStatuses proves the selector admits
// ONLY GoalRoadmap — pending/running/blocked/done are all excluded, so it never
// overlaps RunnableCandidates (which admits GoalPending).
func TestElaborationCandidates_IgnoresNonRoadmapStatuses(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending},
		{ID: "goal-002", Status: GoalRunning},
		{ID: "goal-003", Status: GoalBlocked},
		{ID: "goal-004", Status: GoalDone},
		{ID: "goal-005", Status: GoalRoadmap},
	}}
	got := gf.ElaborationCandidates()
	require.Len(t, got, 1)
	assert.Equal(t, "goal-005", got[0].ID)
}

// TestElaborationCandidates_SkipsPreconditionParked proves a precondition-parked
// roadmap goal is held back exactly as RunnableCandidates holds a parked pending
// goal.
func TestElaborationCandidates_SkipsPreconditionParked(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRoadmap, BlockedByPrecondition: true},
		{ID: "goal-002", Status: GoalRoadmap},
	}}
	got := gf.ElaborationCandidates()
	require.Len(t, got, 1)
	assert.Equal(t, "goal-002", got[0].ID)
}

// TestElaborationCandidates_PriorityOrdering mirrors the RunnableCandidates sort:
// higher Priority first, equal priorities retain file order (SliceStable tiebreak).
func TestElaborationCandidates_PriorityOrdering(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRoadmap, Priority: 0},
		{ID: "goal-002", Status: GoalRoadmap, Priority: 5},
		{ID: "goal-003", Status: GoalRoadmap, Priority: 5},
	}}
	got := gf.ElaborationCandidates()
	require.Len(t, got, 3)
	assert.Equal(t, []string{"goal-002", "goal-003", "goal-001"},
		[]string{got[0].ID, got[1].ID, got[2].ID})
}

// TestElaborationCandidates_EmptyForLegacyPlan proves the selector is inert on a
// fully-specced legacy goals.yaml (no GoalRoadmap rows) — the safety property that
// lets the foundation ship dark before the scheduler flip.
func TestElaborationCandidates_EmptyForLegacyPlan(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Validate: []string{"go build ./..."}},
		{ID: "goal-002", Status: GoalDone},
	}}
	assert.Empty(t, gf.ElaborationCandidates())
}

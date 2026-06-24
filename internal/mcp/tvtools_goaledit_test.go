package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
)

// editPtr is the tri-state helper for the GoalEdit pointer arguments.
func editPtr[T any](v T) *T { return &v }

// TestGoalEdit_SetsFieldsAndFlipsStatus proves the goal-edit MCP tool authors a
// roadmap skeleton's concrete fields and flips it roadmap→pending in one call —
// the Tier-2 elaboration write-back path.
func TestGoalEdit_SetsFieldsAndFlipsStatus(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Skeleton
  status: roadmap
  deliverable_area: internal/api/
`)
	s := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	out, err := s.GoalEdit("goal-001",
		editPtr([]string{"Returns 200"}),
		editPtr([]string{"go test ./internal/api/..."}),
		editPtr([]string{"internal/api/**"}),
		editPtr(taskvisor.GoalPending),
		editPtr("internal/api/v2/"),
		editPtr("application"),
	)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", out.ID)
	assert.True(t, out.Edited)
	assert.Equal(t, taskvisor.GoalPending, out.Status)

	// Re-read via the CANONICAL loader the daemon uses.
	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, []string{"Returns 200"}, g.Acceptance)
	assert.Equal(t, []string{"go test ./internal/api/..."}, g.Validate)
	assert.Equal(t, []string{"internal/api/**"}, g.Scope)
	assert.Equal(t, "internal/api/v2/", g.DeliverableArea)
	assert.Equal(t, "application", g.Phase)
	assert.Equal(t, taskvisor.GoalPending, g.Status)
}

// TestGoalEdit_AbsentGoalErrors proves an unknown goal id is a typed
// ErrInvalidInput and writes nothing.
func TestGoalEdit_AbsentGoalErrors(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Exists
  status: roadmap
`)
	s := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := s.GoalEdit("goal-404", nil, nil, nil, editPtr(taskvisor.GoalPending), nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "goal not found")
}

// TestGoalEdit_RejectsDaemonOwnedStatus proves running/done/failed are rejected
// with a typed ErrInvalidInput before any persistence.
func TestGoalEdit_RejectsDaemonOwnedStatus(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Skeleton
  status: roadmap
`)
	s := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	for _, bad := range []string{taskvisor.GoalRunning, taskvisor.GoalDone, taskvisor.GoalFailed, "bogus"} {
		_, err := s.GoalEdit("goal-001", nil, nil, nil, editPtr(bad), nil, nil)
		require.Error(t, err, "status %q must be rejected", bad)
		assert.ErrorIs(t, err, ErrInvalidInput)
		assert.Contains(t, err.Error(), "not editable")
	}

	// Status unchanged on disk.
	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, _ := gf.GoalByID("goal-001")
	assert.Equal(t, taskvisor.GoalRoadmap, g.Status)
}

// TestGoalEdit_RejectsInvalidPhase proves the thin MCP phase enum check fires.
func TestGoalEdit_RejectsInvalidPhase(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Skeleton
  status: roadmap
`)
	s := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := s.GoalEdit("goal-001", nil, nil, nil, nil, nil, editPtr("not-a-phase"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "invalid phase")
}

// TestGoalEdit_PreservesUntouchedDurableFields is the dual-struct silent-erase
// regression at the MCP seam: editing acceptance on a fully-loaded goal must not
// zero ANY daemon-owned durable field. goal-edit delegates to the canonical
// full-Goal load-resave, so every field survives.
func TestGoalEdit_PreservesUntouchedDurableFields(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, fullyLoadedGoalYaml)
	s := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// Baseline the daemon's pre-edit view (LoadGoals re-seed already applied).
	before, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	wantG, ok := before.GoalByID("goal-001")
	require.True(t, ok)
	want := *wantG

	// Edit acceptance ONLY (and not the status — goal-001 is in_progress, a
	// daemon-owned status we must not pass; leaving status nil keeps it as-is).
	_, err = s.GoalEdit("goal-001", editPtr([]string{"new acceptance"}), nil, nil, nil, nil, nil)
	require.NoError(t, err)

	after, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	g, _ := after.GoalByID("goal-001")

	assert.Equal(t, []string{"new acceptance"}, g.Acceptance, "acceptance was edited")
	assert.Equal(t, want.CodeRetries, g.CodeRetries, "code_retries erased")
	assert.Equal(t, want.SpecRetries, g.SpecRetries, "spec_retries erased")
	assert.Equal(t, want.ValidationRetries, g.ValidationRetries, "validation_retries erased")
	assert.Equal(t, want.BlockRetries, g.BlockRetries, "block_retries erased")
	assert.Equal(t, want.MaxCodeRetries, g.MaxCodeRetries, "max_code_retries erased")
	assert.Equal(t, want.ConvergenceSignatures, g.ConvergenceSignatures, "convergence_signatures erased")
	assert.Equal(t, want.ConvergenceStreak, g.ConvergenceStreak, "convergence_streak erased")
	assert.Equal(t, want.SpecConvergenceSignatures, g.SpecConvergenceSignatures, "spec_convergence_signatures erased")
	assert.Equal(t, want.DependsOn, g.DependsOn, "depends_on erased")
	assert.Equal(t, want.EscalationCount, g.EscalationCount, "escalation_count erased")
	assert.Equal(t, want.Migrates, g.Migrates, "migrates erased")
	assert.Equal(t, want.FailedBy, g.FailedBy, "failed_by erased")
	assert.Equal(t, want.BlockedByPrecondition, g.BlockedByPrecondition, "blocked_by_precondition erased")
	assert.Equal(t, want.Scope, g.Scope, "scope erased")
}

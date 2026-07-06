package mcp

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
)

func TestGoalAddPrerequisite_WiresExistingGoalDependsOn(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// Goal A (the bounced dependent) and freshly-created prerequisite P.
	_, err := server.GoalCreate("Dependent A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)
	_, err = server.GoalCreate("Prerequisite P", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	out, err := server.GoalAddPrerequisite("goal-001", "goal-002")
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-002"}, out.DependsOn)
	assert.Equal(t, 1, out.EscalationCount)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, []string{"goal-002"}, gf.Goals[0].DependsOn, "A.depends_on must round-trip via goals.yaml")
	assert.Equal(t, 1, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_RejectsNonExistentPrerequisite(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Dependent A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	_, err = server.GoalAddPrerequisite("goal-001", "goal-999")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "non-existent goal")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, gf.Goals[0].DependsOn, "no edit on rejection")
	assert.Equal(t, 0, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_RejectsNonExistentTargetGoal(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Prerequisite P", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	_, err = server.GoalAddPrerequisite("goal-999", "goal-001")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "non-existent goal")
}

func TestGoalAddPrerequisite_RejectsSelfDependency(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Goal A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	_, err = server.GoalAddPrerequisite("goal-001", "goal-001")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, gf.Goals[0].DependsOn)
	assert.Equal(t, 0, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_RejectsCycle(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// A exists; P is created depending on A. Wiring A -> P would close the cycle
	// A -> P -> A.
	_, err := server.GoalCreate("Goal A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)
	_, err = server.GoalCreate("Goal P", nil, []string{"check"}, "", "", "", 0, []string{"goal-001"}, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	_, err = server.GoalAddPrerequisite("goal-001", "goal-002")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, gf.Goals[0].DependsOn, "no edit persists on cycle rejection")
	assert.Equal(t, 0, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_IdempotentWhenAlreadyPresent(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Dependent A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)
	_, err = server.GoalCreate("Prerequisite P", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	out1, err := server.GoalAddPrerequisite("goal-001", "goal-002")
	require.NoError(t, err)
	assert.Equal(t, 1, out1.EscalationCount)

	// Re-wiring the same edge is a no-op success: no duplicate, no double-increment.
	out2, err := server.GoalAddPrerequisite("goal-001", "goal-002")
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-002"}, out2.DependsOn)
	assert.Equal(t, 1, out2.EscalationCount, "EscalationCount must not double-increment")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-002"}, gf.Goals[0].DependsOn)
	assert.Equal(t, 1, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_EnforcesEscalationCap(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// A already at escalation_count == cap; another wire must be rejected.
	writeTestGoalsYaml(t, tmpDir, fmt.Sprintf(`goals:
- id: goal-001
  description: Dependent A
  status: pending
  escalation_count: %d
- id: goal-002
  description: Prerequisite P
  status: pending
`, escalationCap))

	_, err := server.GoalAddPrerequisite("goal-001", "goal-002")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "escalation cap")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, gf.Goals[0].DependsOn, "A.depends_on unchanged at cap")
	assert.Equal(t, escalationCap, gf.Goals[0].EscalationCount)
}

func TestGoalAddPrerequisite_ConcurrentCallsSerializeUnderLock(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// One dependent A and two prerequisites; two concurrent wires (within the
	// cap of 2) must both land without corrupting goals.yaml.
	_, err := server.GoalCreate("Dependent A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)
	_, err = server.GoalCreate("Prerequisite P1", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)
	_, err = server.GoalCreate("Prerequisite P2", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil, 0, "", false)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	prereqs := []string{"goal-002", "goal-003"}
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = server.GoalAddPrerequisite("goal-001", prereqs[idx])
		}(i)
	}
	wg.Wait()

	for i := 0; i < 2; i++ {
		assert.NoError(t, errs[i], "wire %d should succeed under the lock", i)
	}

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err, "goals.yaml must remain valid YAML (no corruption)")
	require.NotNil(t, gf)
	assert.ElementsMatch(t, []string{"goal-002", "goal-003"}, gf.Goals[0].DependsOn, "both prerequisites must be wired")
	assert.Equal(t, 2, gf.Goals[0].EscalationCount, "each wire increments exactly once")
}

// --- tvGoal Migrates mirror tests (dual-struct sync with taskvisor.Goal.Migrates) ---

// TestTvGoal_MigratesRoundTrips: migrates:true must survive the MCP tvGoalsFile
// load → tvSaveGoals round-trip. Without the tvGoal mirror field, every MCP
// load-resave silently erases the flag and disarms the daemon's migration
// co-scheduling exclusion at MaxGoals>1.

func TestGoalAddPrerequisite_PreservesMigratesFlag(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Dependent A with schema migration
  status: pending
  max_retries: 3
  migrates: true
- id: goal-002
  description: Prerequisite P
  status: pending
  max_retries: 3
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.GoalAddPrerequisite("goal-001", "goal-002")
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-002"}, out.DependsOn)

	canonical, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, canonical.Goals, 2)
	assert.True(t, canonical.Goals[0].Migrates,
		"GoalAddPrerequisite must not erase migrates: true on the rewired goal")
	assert.Equal(t, []string{"goal-002"}, canonical.Goals[0].DependsOn)
}

// --- tvGoal full durable-field parity tests (audit-6: dual-struct sync with taskvisor.Goal) ---

// fullyLoadedGoalYaml populates EVERY durable taskvisor.Goal field with a
// non-zero value: legacy + per-class retry counters, per-class Max… budgets,
// both convergence breaker states, escalation/scope/migrates, blocked_by,
// blocked_by_precondition, and timestamps. Live counters are non-zero and
// Max… budgets are non-zero ON PURPOSE so neither LoadGoals normalization
// (budget derivation, live-counter re-seed) fires — the file round-trips
// byte-for-byte semantically and any field tvGoal drops shows up as a diff.

func TestGoalAddPrerequisite_PreservesAllDurableFields(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, fullyLoadedGoalYaml)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.GoalAddPrerequisite("goal-002", "goal-003")
	require.NoError(t, err)
	assert.Equal(t, []string{"goal-003"}, out.DependsOn)

	canonical, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, canonical.Goals, 3)

	g := canonical.Goals[0] // goal-001: untouched by the wire, must be fully intact
	assert.Equal(t, 3, g.CodeRetries, "code_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 2, g.SpecRetries, "spec_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 1, g.ValidationRetries, "validation_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 1, g.BlockRetries, "block_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 5, g.MaxCodeRetries, "max_code_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 3, g.MaxSpecRetries, "max_spec_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 2, g.MaxValidationRetries, "max_validation_retries erased by GoalAddPrerequisite")
	assert.Equal(t, 1, g.MaxBlockRetries, "max_block_retries erased by GoalAddPrerequisite")
	assert.Equal(t, []string{"code-sig-a", "code-sig-b"}, g.ConvergenceSignatures, "convergence_signatures erased by GoalAddPrerequisite")
	assert.Equal(t, 2, g.ConvergenceStreak, "convergence_streak erased by GoalAddPrerequisite")
	assert.Equal(t, []string{"spec-sig-a"}, g.SpecConvergenceSignatures, "spec_convergence_signatures erased by GoalAddPrerequisite")
	assert.Equal(t, 1, g.SpecConvergenceStreak, "spec_convergence_streak erased by GoalAddPrerequisite")
	assert.True(t, g.BlockedByPrecondition, "blocked_by_precondition erased by GoalAddPrerequisite")
	assert.True(t, g.Migrates, "migrates erased by GoalAddPrerequisite")
	assert.Equal(t, 1, g.EscalationCount, "escalation_count erased by GoalAddPrerequisite")
	assert.Equal(t, "validation-timeout", g.FailedBy, "failed_by erased by GoalAddPrerequisite")

	// The wired goal itself keeps its durable state and gains the edge.
	p := canonical.Goals[1]
	assert.Equal(t, []string{"goal-003"}, p.DependsOn)
	assert.Equal(t, 3, p.MaxCodeRetries, "max_code_retries erased on the rewired goal")
	assert.Equal(t, 2, p.MaxSpecRetries, "max_spec_retries erased on the rewired goal")
}

// TestGoalTvGoalYamlTagParity: REFLECTION parity guard — every yaml-persisted
// field on the canonical taskvisor.Goal must exist on the MCP mirror tvGoal
// with the SAME yaml tag (key + omitempty) and the SAME Go type
// (tvGoal ⊇ Goal's persisted set). This is the permanent class guard: the next
// field added to Goal without a tvGoal mirror fails THIS test instead of
// silently being erased by the MCP load-resave paths.

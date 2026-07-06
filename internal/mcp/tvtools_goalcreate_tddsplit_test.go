package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

// task 473: goal-create rejects a red/green TDD pair split of an existing
// PENDING goal's same unit (one test-only + one impl scope linked by a
// depends_on edge), with an explicit allow_split_tdd escape hatch.

// TestGoalCreate_RejectsTDDPairSplit seeds a pending impl goal and then attempts
// to create a test-only goal on the same unit that depends_on it. The gate must
// reject with ErrInvalidInput and persist NO new goal.
func TestGoalCreate_RejectsTDDPairSplit(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Implement foo
  status: pending
  scope:
  - internal/foo/**
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Add foo tests", nil, []string{"go test ./internal/foo"}, "", "", "", 0,
		[]string{"goal-001"}, nil, nil, []string{"internal/foo/*_test.go"}, 0, "", false)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "allow_split_tdd")

	// Nothing new persisted — the seeded goal is the only one.
	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1, "no goal may persist when the split is rejected")
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
}

// TestGoalCreate_AllowSplitTDDBypasses proves the escape hatch is load-bearing:
// the identical split with allow_split_tdd=true creates the second goal. This
// keeps TestGoalCreate_RejectsTDDPairSplit non-vacuous (the reject is caused by
// the gate, not some unrelated error).
func TestGoalCreate_AllowSplitTDDBypasses(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Implement foo
  status: pending
  scope:
  - internal/foo/**
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.GoalCreate("Add foo tests", nil, []string{"go test ./internal/foo"}, "", "", "", 0,
		[]string{"goal-001"}, nil, nil, []string{"internal/foo/*_test.go"}, 0, "", true)

	require.NoError(t, err)
	assert.Equal(t, "goal-002", out.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2, "allow_split_tdd=true must bypass the gate and create the goal")
}

// TestGoalCreate_NonSplitStillCreates proves the gate does not over-reject: a
// candidate that depends_on a goal with DISJOINT scope is created normally.
func TestGoalCreate_NonSplitStillCreates(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Implement bar
  status: pending
  scope:
  - internal/bar/**
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.GoalCreate("Add baz tests", nil, []string{"go test ./internal/baz"}, "", "", "", 0,
		[]string{"goal-001"}, nil, nil, []string{"internal/baz/*_test.go"}, 0, "", false)

	require.NoError(t, err)
	assert.Equal(t, "goal-002", out.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
}

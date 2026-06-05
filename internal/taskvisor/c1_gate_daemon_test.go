//go:build c1_gate
// +build c1_gate

package taskvisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestM01_UnsetSecret: a validator that reports a blocked/env-config finding is
// classified (blocked, ops). The daemon does NOT advance the goal and does NOT
// charge a code retry — it routes through the correction path. (Full precondition
// preflight is C3; here we assert classification + routing given a blocked finding.)
func TestM01_UnsetSecret(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "needs SECRET", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictBlocked,
		Findings: []ValidationFinding{
			{Rule: "SECRET set", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
		},
		NextAction: "set the SECRET env var",
		Timestamp:  "2026-06-01T15:00:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	// Sanity: the finding classifies to (blocked, ops).
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "SECRET set", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
	})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "ops", o)

	// blocked/ops parks the goal for operator action via haltBlockedEnv (C2-routing):
	// no advance, no code retry charged, an operator runbook is written, and NO
	// code-defect correction file is produced.
	assert.Equal(t, GoalBlocked, goal.Status, "blocked/ops parks the goal via haltBlockedEnv")
	assert.Equal(t, "goal-001", gf.CurrentGoal, "blocked goal must not advance")
	assert.Equal(t, 0, goal.CodeRetries, "blocked must not charge a code retry")

	runbookPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "runbook.md")
	_, rbErr := os.Stat(runbookPath)
	assert.NoError(t, rbErr, "blocked/ops writes an operator runbook")

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(correctionPath)
	assert.True(t, os.IsNotExist(statErr), "blocked/ops must not write a code-defect correction")
}

// TestValidateTimeout_SynthesizesError: when the validator never reports before
// the deadline, the watchdog synthesizes verdict=error/owner=ops and re-runs
// validation only — ValidationRetries moves while CodeRetries/SpecRetries and the
// legacy Retries counter stay put, and a fresh validator is dispatched.
func TestValidateTimeout_SynthesizesError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.validateTimeout = 300 * time.Second
	d.runtime("goal-001").validateTime = time.Now().Add(-301 * time.Second)
	d.createWindowFn = mockCreateWindowFn("@7")

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			// ValidationRetries seeded to its full budget (2): under C2-routing's
			// decrement-toward-zero model the watchdog's rerunValidationOnly drops it
			// 2→1 and re-runs validation (the seeding-fix gives real goals live=Max).
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, CodeRetries: 0, SpecRetries: 0, ValidationRetries: 2, MaxValidationRetries: 2},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@7", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@7").Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@7").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, "@7", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	err = d.checkProgress(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 1, goal.ValidationRetries, "validation retry must move")
	assert.Equal(t, 0, goal.CodeRetries, "code retries must not move")
	assert.Equal(t, 0, goal.SpecRetries, "spec retries must not move")
	assert.Equal(t, 0, goal.Retries, "legacy code-defect retry must not move")
	assert.Equal(t, GoalRunning, goal.Status)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	// A fresh validator was dispatched (re-run validation only).
	exec.AssertCalled(t, "SendMessage", testSession, "@7", mock.Anything)

	correctionPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, statErr := os.Stat(correctionPath)
	assert.True(t, os.IsNotExist(statErr), "timeout must not write a code-defect correction")
}

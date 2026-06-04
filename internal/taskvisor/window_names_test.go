package taskvisor

import (
	"testing"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- goalNamespace: the short, stable token embedded in per-goal window names.

func TestGoalNamespace_StripsGoalPrefixAndZeros(t *testing.T) {
	// Leading zeros are PRESERVED (spec I/O matrix: supervisor-020) so the token
	// is a faithful, reversible slice of the goal ID.
	assert.Equal(t, "020", goalNamespace("goal-020"))
}

func TestGoalNamespace_NonGoalIDPassThrough(t *testing.T) {
	assert.Equal(t, "custom-id", goalNamespace("custom-id"))
}

func TestGoalNamespace_NoZeroPadSingleDigit(t *testing.T) {
	assert.Equal(t, "7", goalNamespace("goal-7"))
}

func TestGoalNamespace_EmptySuffixFallsBackToRawID(t *testing.T) {
	// An id of exactly "goal-" leaves an empty suffix; fall back to the raw id so
	// a window name is never just "supervisor-".
	assert.Equal(t, "goal-", goalNamespace("goal-"))
}

// --- supervisorWindow / validatorWindow.

func TestSupervisorWindow_SingleGoalIsBare(t *testing.T) {
	assert.Equal(t, "supervisor", supervisorWindow("goal-020", 1))
	assert.Equal(t, "supervisor", supervisorWindow("goal-020", 0))
}

func TestSupervisorWindow_MultiGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "supervisor-020", supervisorWindow("goal-020", 2))
}

func TestValidatorWindow_SingleGoalIsBare(t *testing.T) {
	assert.Equal(t, "validator", validatorWindow("goal-020", 1))
}

func TestValidatorWindow_MultiGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "validator-7", validatorWindow("goal-7", 3))
}

// ValidatorWindowNames returns every name a goal's validator window may carry,
// most-specific first, so the MCP authorization lookup accepts both modes
// without re-reading max_goals.
func TestValidatorWindowNames_NamespacedThenBare(t *testing.T) {
	assert.Equal(t, []string{"validator-046", "validator"}, ValidatorWindowNames("goal-046"))
}

// --- executePrefix / invPrefix.

func TestExecutePrefix_SingleGoalUnchanged(t *testing.T) {
	assert.Equal(t, "execute-", executePrefix("goal-020", 1))
}

func TestExecutePrefix_MultiGoalNamespaced(t *testing.T) {
	assert.Equal(t, "execute-020-", executePrefix("goal-020", 2))
}

// ExecutePrefixForGoal exposes the namespaced (MaxGoals>1) execute prefix to the
// MCP layer (mirrors the ValidatorWindowNames export pattern) so recover-workers
// can scope recovery to one goal's worker pool without re-reading max_goals.
func TestExecutePrefixForGoal(t *testing.T) {
	tests := []struct {
		goalID string
		want   string
	}{
		{"goal-020", "execute-020-"},
		{"goal-", "execute-goal--"}, // namespace fallback: empty suffix -> raw id
		{"goal-7", "execute-7-"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ExecutePrefixForGoal(tt.goalID))
		// Parity with the unexported helper at maxGoals=2 — can never drift.
		assert.Equal(t, executePrefix(tt.goalID, 2), ExecutePrefixForGoal(tt.goalID))
	}
}

func TestInvPrefix_SingleGoalUnchanged(t *testing.T) {
	assert.Equal(t, "inv-", invPrefix("goal-020", 1))
}

func TestInvPrefix_MultiGoalNamespaced(t *testing.T) {
	assert.Equal(t, "inv-020-", invPrefix("goal-020", 2))
}

// --- maxGoals(): the lone impurity, reading setting.yaml with a default of 1.

func TestMaxGoals_DefaultsToOneWhenUnset(t *testing.T) {
	dir := t.TempDir()
	d := New(dir, new(testutil.MockTmuxExecutor))
	// No setting.yaml written: LoadSettings seeds the default (MaxGoals=1).
	assert.Equal(t, 1, d.maxGoals())
}

func TestMaxGoals_ReadsConfiguredValue(t *testing.T) {
	dir := t.TempDir()
	s := setup.DefaultSettings()
	s.Supervisor.MaxGoals = 3
	require.NoError(t, setup.SaveSettings(dir, s))

	d := New(dir, new(testutil.MockTmuxExecutor))
	assert.Equal(t, 3, d.maxGoals())
}

// --- collectManagedNames: goal-scoped enumeration of windows to await-gone.

func TestCollectManagedNames_MultiGoalScopedToOneGoal(t *testing.T) {
	dir := t.TempDir()
	s := setup.DefaultSettings()
	s.Supervisor.MaxGoals = 2
	require.NoError(t, setup.SaveSettings(dir, s))

	exec := new(testutil.MockTmuxExecutor)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-020"},
		{TmuxWindowID: "@2", Name: "execute-020-1"},
		{TmuxWindowID: "@3", Name: "execute-021-1"},
	}, nil)

	d := New(dir, exec)
	d.session = testSession

	names := d.collectManagedNames("goal-020")

	assert.Contains(t, names, "supervisor-020")
	assert.Contains(t, names, "validator-020")
	assert.Contains(t, names, "execute-020-1")
	assert.NotContains(t, names, "execute-021-1", "sibling goal's namespaced worker must not be collected")
}

func TestCollectManagedNames_SingleGoalUnchanged(t *testing.T) {
	dir := t.TempDir()
	// No setting.yaml → MaxGoals defaults to 1 → bare names, pre-change behavior.

	exec := new(testutil.MockTmuxExecutor)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@2", Name: "inv-1"},
		{TmuxWindowID: "@3", Name: "unrelated"},
	}, nil)

	d := New(dir, exec)
	d.session = testSession

	names := d.collectManagedNames("goal-020")

	assert.Equal(t, []string{"supervisor", "validator", "execute-1", "inv-1"}, names)
}

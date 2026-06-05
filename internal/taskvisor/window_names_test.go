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

// Goal windows are ALWAYS namespaced now (P1): the maxGoals<=1 bare branch is
// retired. supervisorWindow returns supervisor-<ns> at every maxGoals; bare
// "supervisor" belongs to window-0 only and is never produced here.
func TestSupervisorWindow_SingleGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "supervisor-020", supervisorWindow("goal-020", 1))
	assert.Equal(t, "supervisor-020", supervisorWindow("goal-020", 0))
}

func TestSupervisorWindow_MultiGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "supervisor-020", supervisorWindow("goal-020", 2))
}

func TestValidatorWindow_SingleGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "validator-020", validatorWindow("goal-020", 1))
}

func TestValidatorWindow_MultiGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "validator-7", validatorWindow("goal-7", 3))
}

// ValidatorWindowNames returns the always-emitted namespaced name first, then
// bare "validator" as a one-release fallback for a pre-upgrade live window, so
// the MCP authorization lookup accepts both without re-reading max_goals.
func TestValidatorWindowNames_NamespacedFirstThenBareFallback(t *testing.T) {
	assert.Equal(t, []string{"validator-046", "validator"}, ValidatorWindowNames("goal-046"))
}

// --- executePrefix / invPrefix.

func TestExecutePrefix_SingleGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "execute-020-", executePrefix("goal-020", 1))
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

func TestInvPrefix_SingleGoalIsNamespaced(t *testing.T) {
	assert.Equal(t, "inv-020-", invPrefix("goal-020", 1))
}

func TestInvPrefix_MultiGoalNamespaced(t *testing.T) {
	assert.Equal(t, "inv-020-", invPrefix("goal-020", 2))
}

// InvPrefixForGoal / SupervisorWindowForGoal expose the namespaced names to the
// package-main goal-skip sweep, derived from the same unexported helpers the
// daemon spawns with so they can never drift.
func TestInvPrefixForGoal(t *testing.T) {
	assert.Equal(t, "inv-7-", InvPrefixForGoal("goal-7"))
	assert.Equal(t, invPrefix("goal-7", 2), InvPrefixForGoal("goal-7"))
}

func TestSupervisorWindowForGoal(t *testing.T) {
	assert.Equal(t, "supervisor-7", SupervisorWindowForGoal("goal-7"))
	assert.Equal(t, supervisorWindow("goal-7", 2), SupervisorWindowForGoal("goal-7"))
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

func TestCollectManagedNames_SingleGoalIsNamespaced(t *testing.T) {
	dir := t.TempDir()
	// No setting.yaml → MaxGoals defaults to 1, but goal windows are ALWAYS
	// namespaced now: collected names are supervisor-020 / validator-020 plus the
	// goal's namespaced workers; a sibling goal's worker is not collected.

	exec := new(testutil.MockTmuxExecutor)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-020-1"},
		{TmuxWindowID: "@2", Name: "inv-020-1"},
		{TmuxWindowID: "@3", Name: "execute-021-1"},
		{TmuxWindowID: "@4", Name: "unrelated"},
	}, nil)

	d := New(dir, exec)
	d.session = testSession

	names := d.collectManagedNames("goal-020")

	assert.Equal(t, []string{"supervisor-020", "validator-020", "execute-020-1", "inv-020-1"}, names)
}

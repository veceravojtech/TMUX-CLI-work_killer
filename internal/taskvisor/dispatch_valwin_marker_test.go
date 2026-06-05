package taskvisor

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatch_valwin_marker_test.go — symmetric with the supervisor-window marker:
// createValidatorAndSendPayload publishes the EXACT validator window name it
// computes (validatorWindow(goal.ID, mg)) to .tmux-cli/goals/<id>/validator-window
// byte-exact (no trailing newline), so investigate.xml can self-identify
// VALIDATOR_WID by reading the marker verbatim instead of guessing the bare name.

func valwinMarkerPath(dir, goalID string) string {
	return filepath.Join(dir, ".tmux-cli", "goals", goalID, "validator-window")
}

func TestCreateValidator_WritesValidatorWindowMarker(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.validatorSendDelay = 0

	_, err := EnsureGoalDir(dir, "goal-007")
	require.NoError(t, err)

	// The validator window is always namespaced (validator-007 at every MaxGoals).
	setupValidatorMocks(exec, testSession, "@5", "validator-007")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	require.NoError(t, d.createValidatorAndSendPayload(&Goal{ID: "goal-007"}))

	assert.Equal(t, "validator-007", readMarker(t, valwinMarkerPath(dir, "goal-007")),
		"validator-window marker must equal validatorWindow(goal-007, mg) byte-exact")
}

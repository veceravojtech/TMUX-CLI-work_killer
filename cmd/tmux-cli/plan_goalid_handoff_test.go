package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanXml_AutoExecuteShipsGoalID guards the plan→supervisor handoff arm of
// the goal-id fix. The daemon's first-cycle dispatch sends /tmux:plan with a
// goal-scoped dispatch path (.tmux-cli/goals/goal-<NNN>/dispatch.md); plan.xml
// then auto-executes the supervisor. That handoff previously sent
// `/tmux:supervisor .tmux-cli/tasks.yaml`, dropping the goal id and forcing the
// fresh supervisor to re-derive its goal from the last-writer-wins global
// marker (the goal-046→goal-045 misroute). In GOAL_MODE the handoff MUST ship
// the goal id as a leading `goal-<GOAL_ID>` token, and plan.xml MUST resolve
// GOAL_ID from the goal-scoped dispatch path it was handed — not from the
// global marker.
func TestPlanXml_AutoExecuteShipsGoalID(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")

	// --- Step 11 auto-execute handoff ships the goal id ---
	step11 := strings.Index(content, `<step n="11"`)
	require.NotEqual(t, -1, step11, "plan.xml must have a step 11 (Finalize/auto-execute)")
	flowEnd := strings.Index(content[step11:], `</step>`)
	require.NotEqual(t, -1, flowEnd, "step 11 must be well-formed")
	step11Body := content[step11 : step11+flowEnd]

	// {GOAL_ID} already carries the goal- prefix, so the command must be
	// `/tmux:supervisor {GOAL_ID}` (NOT `goal-{GOAL_ID}`, which would expand to
	// the doubled `goal-goal-046`).
	assert.Contains(t, step11Body, "/tmux:supervisor {GOAL_ID}",
		"plan.xml step 11 GOAL_MODE auto-execute must ship the goal id as a leading token to the fresh supervisor")
	assert.NotContains(t, step11Body, "/tmux:supervisor goal-{GOAL_ID}",
		"must not double the goal- prefix ({GOAL_ID} already includes it)")
	assert.Contains(t, step11Body, "GOAL_MODE",
		"plan.xml step 11 must gate the goal-id handoff on GOAL_MODE")

	// --- Step 0b resolves GOAL_ID from the shipped dispatch path ---
	step0b := strings.Index(content, `<step n="0b"`)
	step1 := strings.Index(content, `<step n="1" title="MCP precondition"`)
	require.NotEqual(t, -1, step0b, "plan.xml must have a step 0b")
	require.NotEqual(t, -1, step1, "plan.xml must have a step 1")
	require.Less(t, step0b, step1, "step 0b must precede step 1")
	step0bBody := content[step0b:step1]

	for _, marker := range []string{
		// GOAL_ID is derived from the goal-scoped dispatch path in $ARGUMENTS.
		"$ARGUMENTS",
		".tmux-cli/goals/",
		// Rationale: why the global marker is not trusted here.
		"last-writer-wins",
	} {
		assert.Contains(t, step0bBody, marker,
			"plan.xml step 0b must derive GOAL_ID from the dispatch path (marker %q)", marker)
	}

	// The dispatch-path derivation must precede the marker fallback.
	pathIdx := strings.Index(step0bBody, ".tmux-cli/goals/")
	markerIdx := strings.Index(step0bBody, "taskvisor-current-goal")
	require.NotEqual(t, -1, pathIdx, "step 0b must reference the goal-scoped dispatch path")
	require.NotEqual(t, -1, markerIdx, "step 0b must still describe the marker fallback")
	assert.Less(t, pathIdx, markerIdx,
		"the dispatch-path GOAL_ID derivation must be resolved BEFORE the global-marker fallback")
}

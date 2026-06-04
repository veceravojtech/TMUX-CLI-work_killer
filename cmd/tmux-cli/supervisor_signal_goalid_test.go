package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupervisorXml_Step9bSignalUsesResolvedGoalID guards the audit-5
// completion-path GOAL_ID handoff: audit-3 made step 0b derive GOAL_ID from
// the supervisor's OWN window name (per-goal-safe under MaxGoals>1), but step
// 9b still re-read the GLOBAL .tmux-cli/taskvisor-current-goal marker to pick
// the signal.json target. The daemon writes that marker on EVERY dispatch
// (last-writer-wins), so under concurrent dispatch supervisor-020 could write
// its done/stopped signal into goal-021's folder — corrupting which goal the
// daemon believes finished. Both step 9b signal.json writes must therefore
// REUSE the GOAL_ID already resolved in step 0b and never instruct a fresh
// global-marker read at write time. For the bare `supervisor` window
// (MaxGoals=1) step 0b resolved GOAL_ID from the marker anyway, so reuse is
// byte-identical to the legacy behavior.
func TestSupervisorXml_Step9bSignalUsesResolvedGoalID(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	step9b := strings.Index(content, `<step n="9b"`)
	flowEnd := strings.Index(content, `</flow>`)
	require.NotEqual(t, -1, step9b, "supervisor.xml must have a step 9b")
	require.NotEqual(t, -1, flowEnd, "supervisor.xml must close its <flow>")
	require.Less(t, step9b, flowEnd, "step 9b must sit inside <flow>")
	body := content[step9b:flowEnd]

	// Both signal.json writes must reuse the step-0b GOAL_ID, gated on
	// GOAL_MODE, instead of re-reading the global marker at write time.
	for _, marker := range []string{
		// Reuse instruction tied to the step-0b resolution.
		"GOAL_MODE",
		"resolved in step 0b",
		// Rationale marker: why a fresh global-marker read is unsafe.
		"last-writer-wins",
	} {
		assert.Contains(t, body, marker,
			"supervisor.xml step 9b must carry resolved-GOAL_ID signal marker %q", marker)
	}

	// The signal payloads must stay byte-identical (daemon contract).
	assert.Contains(t, body, `{"source":"supervisor","status":"done"}`,
		"step 9b must keep the done signal payload verbatim")
	assert.Contains(t, body, `{"source":"supervisor","status":"stopped","reason":"max_cycles_reached"}`,
		"step 9b must keep the max_cycles_reached signal payload verbatim")

	// No fresh global-marker read at signal-write time — the marker is
	// last-writer-wins under concurrent dispatch (MaxGoals>1).
	assert.NotContains(t, body, "taskvisor-current-goal",
		"step 9b must not re-read the global current-goal marker for signal.json writes")
	assert.NotContains(t, body, "read GOAL_ID from it",
		"step 9b must not instruct a fresh GOAL_ID read at signal-write time")

	// Destaled framing outside step 9b: the <directory> requirement and the
	// step 0 parenthetical must describe GOAL_ID as window-name-first (step
	// 0b derivation), with the marker only for the bare `supervisor` window.
	require.NotEqual(t, -1, strings.Index(content, "<directory>"),
		"supervisor.xml must have a <directory> requirement")
	dirStart := strings.Index(content, "<directory>")
	dirEnd := strings.Index(content, "</directory>")
	require.Less(t, dirStart, dirEnd, "<directory> must be well-formed")
	dirBody := content[dirStart:dirEnd]
	assert.Contains(t, dirBody, "window-name",
		"<directory> requirement must reference the step-0b window-name-first GOAL_ID derivation")
	assert.NotContains(t, dirBody, "(.tmux-cli/taskvisor-current-goal exists)",
		"<directory> requirement must not present the global marker as the goal-mode trigger")

	step0 := strings.Index(content, `<step n="0" title="Clean slate"`)
	step0b := strings.Index(content, `<step n="0b"`)
	require.NotEqual(t, -1, step0, "supervisor.xml must have a step 0")
	require.Less(t, step0, step0b, "step 0 must precede step 0b")
	step0Body := content[step0:step0b]
	assert.Contains(t, step0Body, "window-name",
		"step 0 parenthetical must reference the step-0b window-name-first derivation")
	assert.NotContains(t, step0Body, "from .tmux-cli/taskvisor-current-goal,",
		"step 0 parenthetical must not name the global marker as the sole GOAL_ID source")
}

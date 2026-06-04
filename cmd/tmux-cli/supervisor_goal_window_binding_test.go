package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupervisorXml_Step0bDerivesGoalIDFromWindowName guards the audit-3
// per-goal GOAL_ID handoff: the daemon writes the GLOBAL marker
// .tmux-cli/taskvisor-current-goal on EVERY dispatch (last-writer-wins), so at
// MaxGoals>1 a supervisor-020 window reading the marker could resolve
// GOAL_ID=goal-021 and fan out into the wrong goal's tasks.yaml. Step 0b must
// therefore derive GOAL_ID from the supervisor's OWN window name when it is
// namespaced (supervisor-{ns} → goal-{ns}, mirroring parseGoalBinding in
// internal/mcp/tools.go so MCP research-root routing and the supervisor prompt
// always agree), and read the global marker ONLY for the bare `supervisor`
// window (MaxGoals=1 — single writer, byte-identical legacy path).
func TestSupervisorXml_Step0bDerivesGoalIDFromWindowName(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	step0b := strings.Index(content, `<step n="0b"`)
	step1 := strings.Index(content, `<step n="1" title="MCP precondition"`)
	require.NotEqual(t, -1, step0b, "supervisor.xml must have a step 0b")
	require.NotEqual(t, -1, step1, "supervisor.xml must have a step 1 (MCP precondition)")
	require.Less(t, step0b, step1, "step 0b must precede step 1")
	body := content[step0b:step1]

	for _, marker := range []string{
		// How the supervisor learns its own window name before any MCP call.
		"tmux display-message -p '#W'",
		// Namespaced window form and the goal id it derives to.
		"supervisor-{ns}",
		"goal-{ns}",
		// The derivation must be pinned to the Go-side binding scheme so MCP
		// routing (resolveResearchRoot) and the supervisor GOAL_ID agree.
		"parseGoalBinding",
		// Rationale marker: why the global marker is unsafe at MaxGoals>1.
		"last-writer-wins",
	} {
		assert.Contains(t, body, marker,
			"supervisor.xml step 0b must carry window-name GOAL_ID derivation marker %q", marker)
	}

	// The bare-`supervisor` (MaxGoals=1) fallback must keep the legacy
	// global-marker check byte-identical.
	assert.Contains(t, body, "Check whether .tmux-cli/taskvisor-current-goal exists and is non-empty.",
		"supervisor.xml step 0b must retain the global-marker fallback for the bare supervisor window")
}

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupervisorXml_Step0bConsumesGoalIDArgument guards the goal-id handoff
// (companion to the daemon's dispatchRetry change and plan.xml's auto-execute
// handoff): when the daemon ships the goal id as a leading `goal-<NNN>` token
// in the /tmux:supervisor invocation, step 0b MUST treat that argument as the
// AUTHORITATIVE GOAL_ID — taking precedence over both window-name derivation
// and the global .tmux-cli/taskvisor-current-goal marker (which is
// last-writer-wins under concurrent dispatch and was the source of the
// goal-046→goal-045 misroute). The supplied id also pins SUPERVISOR_WID to the
// namespaced form so windows-spawn-worker routes the worker report path and
// reply target to the correct goal even when the MCP server's own
// window-name resolution fails.
func TestSupervisorXml_Step0bConsumesGoalIDArgument(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	step0b := strings.Index(content, `<step n="0b"`)
	step1 := strings.Index(content, `<step n="1" title="MCP precondition"`)
	require.NotEqual(t, -1, step0b, "supervisor.xml must have a step 0b")
	require.NotEqual(t, -1, step1, "supervisor.xml must have a step 1 (MCP precondition)")
	require.Less(t, step0b, step1, "step 0b must precede step 1")
	body := content[step0b:step1]

	for _, marker := range []string{
		// The argument-derived GOAL_ID branch must read $ARGUMENTS for a
		// leading goal-<NNN> token.
		"$ARGUMENTS",
		"goal-{ns}",
		// It must be the highest-precedence GOAL_ID source: authoritative over
		// both the window name and the global marker.
		"AUTHORITATIVE",
	} {
		assert.Contains(t, body, marker,
			"supervisor.xml step 0b must carry argument-GOAL_ID marker %q", marker)
	}

	// The argument branch must be ordered before the window-name and marker
	// branches: precedence is arg > window-name > marker.
	argIdx := strings.Index(body, "$ARGUMENTS")
	markerIdx := strings.Index(body, "taskvisor-current-goal")
	require.NotEqual(t, -1, argIdx, "step 0b must reference $ARGUMENTS")
	require.NotEqual(t, -1, markerIdx, "step 0b must still describe the marker fallback")
	assert.Less(t, argIdx, markerIdx,
		"the $ARGUMENTS goal-id branch must be resolved BEFORE the global-marker fallback")

	// Scope to just the argument branch and assert SUPERVISOR_WID is taken from
	// the supervisor's REAL window name (tmux display-message), NOT synthesized
	// as supervisor-{ns} from the goal id. Synthesizing it breaks MaxGoals=1
	// (the real window is the bare `supervisor`): worker [EXECUTE:DONE] replies
	// would route to a non-existent supervisor-{ns} window. The goal id governs
	// scoping (research-root / tasks-path); the real window name governs routing.
	argBranchStart := strings.Index(body, `<branch condition="ARG_GOAL_ID is set`)
	require.NotEqual(t, -1, argBranchStart, "step 0b must have an ARG_GOAL_ID-is-set branch")
	argBranchEnd := strings.Index(body[argBranchStart:], "</branch>")
	require.NotEqual(t, -1, argBranchEnd, "the ARG_GOAL_ID branch must be well-formed")
	argBranch := body[argBranchStart : argBranchStart+argBranchEnd]

	assert.Contains(t, argBranch, "display-message",
		"the arg branch must derive SUPERVISOR_WID from the real window name (tmux display-message), not synthesize it")
	assert.Contains(t, argBranch, "supervisorWid",
		"the arg branch must instruct passing SUPERVISOR_WID as the supervisorWid argument to windows-spawn-worker")
	assert.Contains(t, argBranch, "MaxGoals=1",
		"the arg branch must call out the MaxGoals=1 bare-`supervisor` case so SUPERVISOR_WID is never wrongly namespaced")
}

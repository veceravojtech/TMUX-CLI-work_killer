package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanXml_PerGoalTasksPath guards the per-goal planning-queue fix. plan.xml
// previously hardcoded the planning queue at the TOP-LEVEL .tmux-cli/tasks.yaml
// in every mode ("the taskvisor daemon reads it from that fixed path" — stale:
// the daemon's retry path reads only the per-goal GoalTasksFilePath). In
// GOAL_MODE the supervisor and daemon both operate on
// .tmux-cli/goals/{GOAL_ID}/tasks.yaml, so a planner writing top-level leaked a
// permanently-`ready` file behind every goal, and the NEXT goal's planner hit
// the clean-slate confirm prompt — headless, unanswered, a full cycle timeout
// misclassified as a code defect (the goal-051/057 deadlock; goals 025/031/045
// burned budgets this way). plan.xml MUST resolve tasks-path per-goal in
// GOAL_MODE and never park a daemon-dispatched plan on user confirmation.
func TestPlanXml_PerGoalTasksPath(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")

	// --- The stale fixed-path claims are gone ---
	assert.NotContains(t, content, "tasks.yaml is ALWAYS .tmux-cli/tasks.yaml",
		"plan.xml must not claim the planning queue lives at a fixed top-level path regardless of mode")
	assert.NotContains(t, content, "always stays at .tmux-cli/tasks.yaml",
		"plan.xml must not claim tasks.yaml always stays at the top-level path")

	// --- tasks-path is a first-class term resolved per-goal in GOAL_MODE ---
	assert.Contains(t, content, `<term name="tasks-path">`,
		"plan.xml must define the tasks-path glossary term (mirroring supervisor.xml)")
	assert.Contains(t, content, ".tmux-cli/goals/{GOAL_ID}/tasks.yaml",
		"plan.xml must resolve tasks-path to the per-goal file in GOAL_MODE")

	// --- Clean slate runs AFTER goal resolution and operates on tasks-path ---
	step0b := strings.Index(content, `<step n="0b"`)
	step0c := strings.Index(content, `<step n="0c" title="Clean slate"`)
	step1 := strings.Index(content, `<step n="1" title="MCP precondition"`)
	require.NotEqual(t, -1, step0b, "plan.xml must have a step 0b")
	require.NotEqual(t, -1, step0c, "plan.xml must have a step 0c Clean slate")
	require.NotEqual(t, -1, step1, "plan.xml must have a step 1")
	require.Less(t, step0b, step0c,
		"clean slate must run AFTER step 0b so tasks-path is already resolved")
	require.Less(t, step0c, step1, "step 0c must precede step 1")
	step0cBody := content[step0c:step1]
	assert.Contains(t, step0cBody, "tasks-path",
		"the clean-slate gate must operate on the resolved tasks-path, not a hardcoded top-level path")

	// --- GOAL_MODE never waits for a human ---
	assert.Contains(t, step0cBody, "GOAL_MODE",
		"the clean-slate gate must branch on GOAL_MODE")
	assert.Contains(t, step0cBody, "WITHOUT user confirmation",
		"in GOAL_MODE a stale finalized plan must be auto-archived WITHOUT user confirmation — "+
			"a daemon-dispatched planner has no human to answer and the silence burns retry budget")

	// --- step 3b writes tasks-path, not the hardcoded top-level file ---
	step3b := strings.Index(content, `<step n="3b"`)
	require.NotEqual(t, -1, step3b, "plan.xml must have a step 3b")
	step3bEnd := strings.Index(content[step3b:], "</step>")
	require.NotEqual(t, -1, step3bEnd, "step 3b must be well-formed")
	step3bBody := content[step3b : step3b+step3bEnd]
	assert.Contains(t, step3bBody, "tasks-path",
		"step 3b must write the fan-out to the resolved tasks-path")
}

// TestSupervisorXml_PrePlannedNoteMatchesPerGoalHandoff: supervisor.xml's tasks
// gate already operated on the per-goal tasks-path in GOAL_MODE, but its note
// claimed /tmux:plan handoffs were only detectable in standalone (plan wrote
// top-level). With plan.xml now writing per-goal, the note must say PRE_PLANNED
// detection works in BOTH modes on tasks-path.
func TestSupervisorXml_PrePlannedNoteMatchesPerGoalHandoff(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	gate := strings.Index(content, `<step n="1c" title="Tasks gate">`)
	require.NotEqual(t, -1, gate, "supervisor.xml must have a step 1c Tasks gate")
	gateEnd := strings.Index(content[gate:], "</step>")
	require.NotEqual(t, -1, gateEnd, "step 1c must be well-formed")
	gateBody := content[gate : gate+gateEnd]

	assert.NotContains(t, gateBody, "in standalone, tasks-path IS .tmux-cli/tasks.yaml, so /tmux:plan handoffs are detected exactly as before",
		"the note must not imply plan handoffs only land top-level — plan.xml now writes tasks-path per-goal in GOAL_MODE")
	assert.Contains(t, gateBody, "BOTH modes",
		"the note must state PRE_PLANNED handoffs are detected on tasks-path in BOTH modes")
}

// TestSupervisorXml_GoalHandoffPreservesReadyStatus: with plan.xml finalizing
// the per-goal tasks-path as status:ready, supervisor.xml step 0's blanket
// "set status to planning on non-continuation invocations" would clobber the
// handoff before step 1c's PRE_PLANNED check (which requires status=ready) —
// masking the plan and re-planning an already-planned goal. The daemon's
// dispatchRetry has the same contract: resetTaskStatuses writes status:ready
// then sends `/tmux:supervisor goal-{ns}`. A leading goal-{ns} argument must
// therefore exempt the flip, and a GOAL_MODE supervisor must never park on the
// unfinished-tasks prompt (headless daemon window — silence burns budget).
func TestSupervisorXml_GoalHandoffPreservesReadyStatus(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	// --- step 0: the planning flip is exempted on goal handoff ---
	step0 := strings.Index(content, `<step n="0" title="Clean slate"`)
	step0b := strings.Index(content, `<step n="0b"`)
	require.NotEqual(t, -1, step0, "supervisor.xml must have a step 0")
	require.Less(t, step0, step0b, "step 0 must precede step 0b")
	step0Body := content[step0:step0b]
	assert.Contains(t, step0Body, "do NOT flip",
		"step 0 must exempt the status→planning flip when invoked with a goal-{ns} handoff token")
	assert.Contains(t, step0Body, "PRE_PLANNED",
		"step 0 must explain the exemption protects step 1c's PRE_PLANNED (status=ready) detection")

	// --- step 1c: GOAL_MODE never stops on the unfinished-tasks prompt ---
	gate := strings.Index(content, `<step n="1c" title="Tasks gate">`)
	require.NotEqual(t, -1, gate, "supervisor.xml must have a step 1c Tasks gate")
	gateEnd := strings.Index(content[gate:], "</step>")
	require.NotEqual(t, -1, gateEnd, "step 1c must be well-formed")
	gateBody := content[gate : gate+gateEnd]
	assert.Contains(t, gateBody, `GOAL_MODE is false`,
		"the interactive unfinished-tasks STOP gate must be confined to standalone mode")
	assert.Contains(t, gateBody, "GOAL_MODE is true",
		"step 1c must have a non-interactive GOAL_MODE branch for unfinished tasks")
}

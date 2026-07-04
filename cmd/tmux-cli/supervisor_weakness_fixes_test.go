package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sliceSupervisorSection returns content[startMarker:endMarker], failing the
// test if either marker is missing or out of order.
func sliceSupervisorSection(t *testing.T, content, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(content, startMarker)
	end := strings.Index(content, endMarker)
	require.NotEqual(t, -1, start, "supervisor.xml must contain %q", startMarker)
	require.NotEqual(t, -1, end, "supervisor.xml must contain %q", endMarker)
	require.Less(t, start, end, "%q must precede %q", startMarker, endMarker)
	return content[start:end]
}

// TestSupervisorXml_Step2DoesNotRederiveSupervisorWid guards weakness S1:
// step 2 used to re-derive SUPERVISOR_WID from the windows-list active:true
// entry, silently overwriting the authoritative step-0b binding (per-goal
// supervisor-window marker / bare `supervisor`) — the active-window probe is
// unreliable at MaxGoals>1. Step 2 must now be a verification that KEEPS the
// step-0b value, with the old fallback chain only for the unset-binding case.
func TestSupervisorXml_Step2DoesNotRederiveSupervisorWid(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	body := sliceSupervisorSection(t, content,
		`<step n="2"`, `<step n="3" title="Fan-out decision"`)

	assert.Contains(t, body, "Do NOT re-derive it here",
		"step 2 must forbid re-deriving SUPERVISOR_WID over the step-0b binding")
	assert.Contains(t, body, "KEEP the step-0b value",
		"step 2 must keep the 0b binding even when the window is absent from windows-list")
	assert.Contains(t, body, "should not happen",
		"the active:true fallback chain must be guarded as a last resort for an unset 0b binding")
}

// TestSupervisorXml_Step4ReconcilesWidAfterSpawn guards weakness S2:
// windows-spawn-worker picks the next free execute-N itself, so under
// concurrent goals the pre-recorded wid in tasks-path can differ from the
// spawned workerName — breaking step-5 routing and liveness checks. Step 4
// must reconcile the task's wid (and dependent depends_on references) to the
// returned workerName.
func TestSupervisorXml_Step4ReconcilesWidAfterSpawn(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	body := sliceSupervisorSection(t, content, `<step n="4"`, `<step n="5"`)

	assert.Contains(t, body, "WID RECONCILE",
		"step 4 must carry the wid reconciliation action after each spawn")
	assert.Contains(t, body, "workerName differs",
		"step 4 must compare the returned workerName against the recorded wid")
	assert.Contains(t, body, "depends_on",
		"the reconciliation must also rewrite depends_on references to the old wid")
}

// TestSupervisorXml_Step4WindowIdLedger guards weakness S3: execute-N names
// are recycled after kills, so a live window with the right name can belong
// to ANOTHER goal. Step 4 must record a sidecar worker-windows.yaml ledger
// (never extra tasks.yaml fields) and define name+window-id matching, with a
// recycled-name mismatch counting as dead.
func TestSupervisorXml_Step4WindowIdLedger(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	body := sliceSupervisorSection(t, content, `<step n="4"`, `<step n="5"`)

	assert.Contains(t, body, "worker-windows.yaml",
		"step 4 must append spawned window ids to the worker-windows.yaml ledger")
	assert.Contains(t, body, "SIDECAR ledger",
		"the ledger must be a sidecar file, never extra fields on tasks.yaml entries")
	assert.Contains(t, body, "RECYCLED name",
		"a name-match with a different window id must be treated as a recycled name (dead worker)")
}

// TestSupervisorXml_Step5EventTriggeredOrphanReconcile guards weakness S4:
// step 5 used to idle forever when a worker died silently mid-session. On
// every inbound worker message it must now reconcile in_progress tasks
// against windows-list (event-triggered, explicitly NOT idle polling) and
// reset orphans to pending for respawn; step 1c continuation branches mirror
// plan.xml step 0c's IN_PROGRESS RECOVERY.
func TestSupervisorXml_Step5EventTriggeredOrphanReconcile(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	step5 := sliceSupervisorSection(t, content,
		`<step n="5" title="Idle and route"`, `<step n="5a"`)

	assert.Contains(t, step5, "ORPHAN RECONCILE",
		"step 5 must reconcile orphaned in_progress tasks on inbound messages")
	assert.Contains(t, step5, "EVERY inbound worker message",
		"the reconcile must run on every inbound worker message, before handling it")
	assert.Contains(t, step5, "event-triggered",
		"the reconcile must be declared event-triggered so it does not weaken the no-idle-polling rule")
	assert.Contains(t, step5, "NEVER poll windows-list while idle",
		"the no-idle-polling rule itself must remain intact")

	step1c := sliceSupervisorSection(t, content,
		`<step n="1c" title="Tasks gate">`, `<step n="2"`)
	assert.Contains(t, step1c, "IN_PROGRESS RECOVERY",
		"step 1c continuation branches must recover orphaned in_progress tasks at startup")
	assert.Contains(t, step1c, "reset NOTHING",
		"a failed windows-list call must reset nothing (fail-safe), mirroring plan.xml step 0c")
}

// TestSupervisorXml_SelfWaveCapBoundsSynthesisLoop guards weakness S5: the
// step-9 synthesis loop plus exhaustive-coverage and max_cycles default 0 let
// the supervisor generate work from its own output forever. A SELF_WAVE
// counter (glossary) caps purely self-generated waves at 2 per invocation, is
// a real stop-condition, and the step-9 / not-stop / critical-rules text must
// reference it instead of contradicting it.
func TestSupervisorXml_SelfWaveCapBoundsSynthesisLoop(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	assert.Contains(t, content, `<term name="SELF_WAVE">`,
		"the glossary must define the SELF_WAVE counter")

	stop := sliceSupervisorSection(t, content, "<stop-conditions>", "</stop-conditions>")
	assert.Contains(t, stop, "SELF_WAVE cap reached",
		"stop-conditions must carry the SELF_WAVE cap as a real stop entry")
	assert.Contains(t, stop, "RECOMMENDED NEXT WORK",
		"at the cap, remaining self-generated follow-ups are listed, not executed")

	step9 := sliceSupervisorSection(t, content, `<step n="9" title=`, `<step n="9b"`)
	assert.Contains(t, step9, "SELF_WAVE",
		"the step-9 synthesis loop action must classify waves against the SELF_WAVE cap")
}

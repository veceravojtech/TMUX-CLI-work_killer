package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// planStepBody slices the body of the step whose tag starts with the given
// prefix (e.g. `<step n="1b"`), from the tag to its closing </step>.
func planStepBody(t *testing.T, content, stepPrefix string) string {
	t.Helper()
	start := strings.Index(content, stepPrefix)
	require.NotEqual(t, -1, start, "plan.xml must contain %q", stepPrefix)
	end := strings.Index(content[start:], "</step>")
	require.NotEqual(t, -1, end, "step %q must be well-formed", stepPrefix)
	return content[start : start+end]
}

// TestPlanXml_GoalModeForcesAutoApprove guards P1: with plan.auto_approve=false
// a daemon-dispatched (GOAL_MODE) planner used to idle at the interactive
// approval gate (steps 9-10) for an "approve" no human would ever send,
// burning the goal's entire cycle timeout. Step 1b must FORCE AUTO_APPROVE=true
// in GOAL_MODE regardless of setting.yaml.
func TestPlanXml_GoalModeForcesAutoApprove(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="1b"`)

	assert.Contains(t, body, "FORCE AUTO_APPROVE=true regardless of setting.yaml",
		"step 1b must force auto-approve in GOAL_MODE")
	assert.Contains(t, body, "GOAL_MODE: forcing auto-approve",
		"step 1b must log the forced auto-approve with its rationale")
	assert.Contains(t, body, "no human at the approval gate (steps 9–10)",
		"the log must name the deadlocked gate")
}

// TestPlanXml_SelfSpecAuditFailsClosed guards P2: the step 11a malformed-verdict
// retry used to fail OPEN even on SELF_SPEC plans, where the blind audit is the
// ONLY independent review — a twice-failed audit sub-agent silently shipped a
// plan nobody but its author checked. On SELF_SPEC the branch must fail CLOSED
// (escalate + STOP); non-SELF_SPEC keeps fail-open but logged prominently.
func TestPlanXml_SelfSpecAuditFailsClosed(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="11a" title="Blind audit gate">`)

	assert.Contains(t, body, "FAIL CLOSED",
		"a twice-failed audit on a SELF_SPEC plan must fail closed")
	assert.Contains(t, body, "failed twice on a SELF_SPEC plan",
		"the escalation message must name the SELF_SPEC no-review condition")
	assert.Contains(t, body, "not finalizing autonomously",
		"the SELF_SPEC branch must escalate to a human instead of finalizing")
	// Non-SELF_SPEC keeps the fail-open path, prominently logged.
	assert.Contains(t, body, "accepting plan without audit (fail-open)",
		"non-SELF_SPEC plans keep the fail-open acceptance")
	assert.Contains(t, body, "step 9 summary table",
		"the fail-open acceptance must be carried into the summary output")
}

// TestPlanXml_AuditForcedOnUnresolvedGaps guards P3: under AUTO_APPROVE the
// author-side caps (3-pushback accept-as-is, 2-pass coverage) could exhaust
// with unresolved gaps no human would ever see, and plan.audit=false left no
// audit either. The step 11a condition must gain a third disjunct forcing the
// audit when an autonomous run finalizes with unresolved gaps.
func TestPlanXml_AuditForcedOnUnresolvedGaps(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="11a" title="Blind audit gate">`)

	assert.Contains(t, body, "OR (AUTO_APPROVE is true AND",
		"step 11a condition must gain the autonomous-unresolved-gaps disjunct")
	assert.Contains(t, body, "unresolved gaps at the 3-pushback cap",
		"the disjunct must cover specs accepted at the pushback cap")
	assert.Contains(t, body, "uncovered requirements remain from step 8b",
		"the disjunct must cover exhausted coverage passes")
	assert.Contains(t, body, "Audit forced: autonomous run finalizing with unresolved gaps",
		"the forced audit must be logged")
}

// TestPlanXml_Step5EventTriggeredLivenessReconcile guards P4: a worker window
// dying MID-session (after step 0c recovery, before its DONE) used to leave its
// task in_progress forever — step 5 idles on messages that can never arrive.
// Every inbound worker message must trigger a ONE-shot windows-list reconcile
// of the other in_progress tasks, without reintroducing idle polling.
func TestPlanXml_Step5EventTriggeredLivenessReconcile(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="5" title="Idle and route">`)

	assert.Contains(t, body, "EVERY inbound worker message BEFORE handling it",
		"the reconcile must run on every inbound worker message")
	assert.Contains(t, body, "EVENT-TRIGGERED reconciliation",
		"the rule must state this is event-triggered, not idle polling")
	assert.Contains(t, body, "NEVER poll windows-list while idle",
		"the no-idle-polling rule must stand")
	assert.Contains(t, body, "respawn it per step 4",
		"orphaned tasks must be reset and respawned")
}

// TestPlanXml_Step4WidReconcileAndLedger guards P5+P6: (P5) when
// windows-spawn-worker returns a workerName different from the task's recorded
// wid, the task and its depends_on referrers must be rewritten or messages from
// the real window are unroutable; (P6) each spawn must append a wid→window_id
// row to the worker-windows.yaml SIDECAR so liveness checks can tell this
// goal's worker from a recycled execute-N name of another goal.
func TestPlanXml_Step4WidReconcileAndLedger(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="4" title="Spawn spec workers`)

	// P5: wid reconcile after spawn.
	assert.Contains(t, body, "workerName differs from the task's recorded wid",
		"step 4 must reconcile the returned workerName against the recorded wid")
	assert.Contains(t, body, "rewrite any depends_on references to the old wid",
		"the reconcile must also rewrite depends_on referrers")

	// P6: sidecar ledger, never extra tasks.yaml fields.
	assert.Contains(t, body, "worker-windows.yaml",
		"each spawn must append to the worker-windows.yaml ledger")
	assert.Contains(t, body, "SIDECAR file ONLY",
		"the ledger must stay a sidecar — tasks-validate rejects extra task fields")

	// P6 wiring: both step 0c liveness judgments consult the ledger.
	step0c := planStepBody(t, content, `<step n="0c" title="Clean slate">`)
	assert.GreaterOrEqual(t, strings.Count(step0c, "worker-windows.yaml"), 2,
		"both step 0c liveness judgments (IN_PROGRESS RECOVERY and LIVENESS CHECK) must id-qualify name matches via the ledger")
	assert.Contains(t, step0c, "recycled execute-N name from another goal",
		"a name-match with a different window id must count as DEAD, not live")
}

// TestPlanXml_SpecWorkersInheritWorktree guards P7: supervisor.xml threads
// workingDirectory=WORKTREE_DIR so implementation workers edit the goal's git
// worktree, but plan.xml spawns omitted it — spec workers read the BASE tree
// and produced Code Maps that don't match what implementation workers see.
func TestPlanXml_SpecWorkersInheritWorktree(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="4" title="Spawn spec workers`)

	assert.Contains(t, body, "resolve WORKTREE_DIR",
		"step 4 must resolve WORKTREE_DIR before the spawn loop")
	assert.Contains(t, body, "/.tmux-cli/worktrees/",
		"resolution must check the planner's own cwd for the worktree path")
	assert.Contains(t, body, "taskvisor-current-worktree",
		"resolution must fall back to the worktree marker (GOAL_ID-guarded)")
	assert.Contains(t, body, "workingDirectory: WORKTREE_DIR if set",
		"spawn params must thread workingDirectory")
	assert.Contains(t, body, "EVERY windows-spawn-worker call in this flow",
		"all spawn sites (4, 8b, 8d, 10, 11b) must pass the same workingDirectory")
	assert.Contains(t, body, "worktree symlinks .tmux-cli",
		"report/context save paths must be stated unchanged (control plane is shared)")
}

// TestPlanXml_RankingElementExists guards P8: <autonomy-policy> and
// <stop-conditions> both say "pick per <ranking>", but no <ranking> element
// existed (only a glossary prose entry) — a dangling reference. Step 3 must
// carry a real <ranking> element with the heuristic.
func TestPlanXml_RankingElementExists(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")
	body := planStepBody(t, content, `<step n="3" title="Analyze plan file`)

	assert.Contains(t, body, "<ranking>",
		"step 3 must contain a real <ranking> element")
	assert.Contains(t, body, "closest to user-facing surface or critical path",
		"the ranking element must carry the heuristic content")
	assert.Contains(t, body, "Break ties by lowest budget",
		"the ranking element must carry the tie-breaker")
}

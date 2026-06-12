package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanXml_DispatchDecisionGate guards the step 3d dispatch gate. plan.xml
// previously spawned one spec worker per pending task UNCONDITIONALLY — even a
// single-task plan paid a full worker window (re-reading the exact context the
// supervisor already held), idle/route cycles, pushback rounds, and a kill.
// For one module there is no parallelism to buy and one agent's context is
// enough, so the gate must self-spec it inline through the SAME quality gates
// (spec-validate + S5/S6/S9, capped self-revision) and FORCE the blind audit —
// with author == verifier the audit window is the only independent review.
func TestPlanXml_DispatchDecisionGate(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")

	// --- the gate exists between the 3b/3c gates and the spawn step ---
	step3c := strings.Index(content, `<step n="3c"`)
	step3d := strings.Index(content, `<step n="3d" title="Dispatch decision gate">`)
	step4 := strings.Index(content, `<step n="4"`)
	require.NotEqual(t, -1, step3c, "plan.xml must have a step 3c")
	require.NotEqual(t, -1, step3d, "plan.xml must have a step 3d Dispatch decision gate")
	require.NotEqual(t, -1, step4, "plan.xml must have a step 4")
	require.Less(t, step3c, step3d, "the dispatch gate must run after the spec cache check")
	require.Less(t, step3d, step4, "the dispatch gate must run before worker spawning")

	step3dEnd := strings.Index(content[step3d:], "</step>")
	require.NotEqual(t, -1, step3dEnd, "step 3d must be well-formed")
	gateBody := content[step3d : step3d+step3dEnd]

	// --- single pending task → inline self-spec, no spawn ---
	assert.Contains(t, gateBody, "exactly ONE task is pending",
		"the gate must branch on exactly one pending task")
	assert.Contains(t, gateBody, "SELF_SPEC",
		"the single-task branch must set the SELF_SPEC flag")
	assert.Contains(t, gateBody, "do NOT spawn a worker",
		"the single-task branch must forbid worker spawning")
	assert.Contains(t, gateBody, "spec-validate",
		"a self-written spec must pass the same spec-validate gate workers face")
	assert.Contains(t, gateBody, "S5/S6/S9",
		"a self-written spec must pass the same manual S5/S6/S9 checks workers face")
	assert.Contains(t, gateBody, "3 self-revision rounds",
		"self-revision must be capped, mirroring the worker pushback cap")
	assert.Contains(t, gateBody, "step 8b",
		"an accepted self-spec must jump to coverage verification, skipping steps 4-7")

	// --- 2+ pending tasks → normal parallel dispatch ---
	assert.Contains(t, gateBody, "2 or more tasks are pending",
		"the gate must keep the parallel-dispatch branch for multi-task plans")

	// --- the gate never re-scopes the decomposition ---
	assert.Contains(t, gateBody, "never how many",
		"the gate decides HOW specs get written, never the task count/boundaries")

	// --- SELF_SPEC forces the blind audit (author == verifier) ---
	step11a := strings.Index(content, `<step n="11a" title="Blind audit gate">`)
	require.NotEqual(t, -1, step11a, "plan.xml must have a step 11a Blind audit gate")
	step11aEnd := strings.Index(content[step11a:], "</step>")
	require.NotEqual(t, -1, step11aEnd, "step 11a must be well-formed")
	auditBody := content[step11a : step11a+step11aEnd]
	assert.Contains(t, auditBody, "OR SELF_SPEC is true",
		"step 11a must run when SELF_SPEC is true even if supervisor.unplanned_audit=false")

	// --- audit-fail replans on a self-specced plan dispatch a real worker ---
	step11b := strings.Index(content, `<step n="11b" title="Audit replan loop">`)
	require.NotEqual(t, -1, step11b, "plan.xml must have a step 11b Audit replan loop")
	step11bEnd := strings.Index(content[step11b:], "</step>")
	require.NotEqual(t, -1, step11bEnd, "step 11b must be well-formed")
	replanBody := content[step11b : step11b+step11bEnd]
	assert.Contains(t, replanBody, "never self-spec the same module twice",
		"after a failed audit the replan must dispatch a real worker, not self-spec again")

	// --- every failing severity profile must yield a remediable replan set ---
	// A score-fail built purely from SEV-3/SEV-4 accumulation (e.g. two SEV-3s
	// = 84) used to map to NOTHING — the loop re-audited identical artifacts,
	// burned all 3 passes, and parked the goal for a human. The tier fallback
	// (SEV-1/2 → SEV-3 → SEV-4) guarantees a non-empty replan set.
	assert.Contains(t, replanBody, "else SEV-3",
		"a severe-free score-fail must fall back to replanning SEV-3-cited specs")
	assert.Contains(t, replanBody, "else SEV-4 as last resort",
		"a SEV-4-only score-fail must still produce a replan set, never a sterile loop")
	assert.Contains(t, replanBody, "ALL findings that cite this spec",
		"a replanned spec must receive its full finding load so one round clears its deductions")

	// --- the spawn ban must not contradict the 11b replan dispatch ---
	// Both places that ban single-task spawning (the <llm> mandate and the final
	// critical-rules) must carve out the step 11b exception, or an obedient agent
	// deadlocks between "NEVER spawn" and 11b's "dispatch a real worker".
	assert.GreaterOrEqual(t, strings.Count(content, "SOLE EXCEPTION: step 11b"), 2,
		"every single-task spawn ban must carve out the step 11b audit-fail replan exception")
}

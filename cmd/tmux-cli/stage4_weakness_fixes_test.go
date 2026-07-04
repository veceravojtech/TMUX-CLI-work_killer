package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stage4Substep slices the body of one substep of stage-4-implementation.xml,
// from its `<substep n="..."` marker to the next marker (or end of file).
func stage4Substep(t *testing.T, content, from, to string) string {
	t.Helper()
	start := strings.Index(content, from)
	require.NotEqual(t, -1, start, "stage-4 must contain %q", from)
	end := len(content)
	if to != "" {
		end = strings.Index(content, to)
		require.NotEqual(t, -1, end, "stage-4 must contain %q", to)
		require.Less(t, start, end, "%q must precede %q", from, to)
	}
	return content[start:end]
}

// TestStage4Xml_DependsOnFromProduceConsumeNotRankChain guards weakness I1:
// substeps 4.2/4.3 used to chain EVERY goal depends_on its predecessor via a
// threaded PREV_GOAL_ID ("chained depends_on its predecessor so the daemon
// executes in rank order") — the exact ordinal/phase-position edge plan.xml
// step 3 forbids, forfeiting all daemon parallelism. depends_on must now come
// ONLY from genuine produce/consume dependency (plan.xml step 3 /
// EnforceFileOverlapDeps basis); PHASE_ORDER rank governs EMISSION order only;
// no-overlap units are SIBLINGS dispatched in parallel.
func TestStage4Xml_DependsOnFromProduceConsumeNotRankChain(t *testing.T) {
	content := readEmbeddedCommand(t, "feature/stage-4-implementation.xml")

	// The blanket rank-chain phrasing must be gone.
	assert.NotContains(t, content, "so the daemon executes in rank order",
		"stage-4 must not chain every goal to its rank predecessor")
	assert.NotContains(t, content, "depends_on: [PREV_GOAL_ID] if set",
		"stage-4 must not thread PREV_GOAL_ID across units as a blanket depends_on")

	sub42 := stage4Substep(t, content, `<substep n="4.2"`, `<substep n="4.3"`)
	assert.Contains(t, sub42, "EMISSION sequence ONLY",
		"4.2 rank ordering must govern emission order only, never a depends_on edge")
	assert.Contains(t, sub42, "SIBLINGS",
		"4.2 must state no-overlap units are siblings")

	sub43 := stage4Substep(t, content, `<substep n="4.3"`, `<substep n="4.3a"`)
	for _, marker := range []string{
		// Genuine-dependency basis, shared with plan.xml step 3 and the daemon.
		"produce/consume",
		"EnforceFileOverlapDeps",
		"internal/taskvisor/depinfer.go",
		// Parallel siblings for independent units.
		"dispatches them in PARALLEL",
		// PREV_GOAL_ID survives only as the unit's own tests-first predecessor.
		"OWN tests-first predecessor",
		// Emission order keeps the DAG acyclic; G-15 abort retained.
		"G-15",
	} {
		assert.Contains(t, sub43, marker,
			"4.3 must carry the produce/consume dependency-derivation marker %q", marker)
	}
}

// TestStage4Xml_RedPhaseGateSplitVerification guards weakness I2: the old
// "VERIFY EVERY GENERATED GATE" rule demanded a red-path baseline of the
// red-phase gate at emission time, but its subject (the failing test) is only
// created later by the tests-first goal's worker. Verification must be split:
// green-path + banner correctness at emission; the red-path proof (exit 0 on a
// genuine assertion failure) runs INSIDE goal A, whose validate re-proves it.
func TestStage4Xml_RedPhaseGateSplitVerification(t *testing.T) {
	content := readEmbeddedCommand(t, "feature/stage-4-implementation.xml")
	sub43 := stage4Substep(t, content, `<substep n="4.3"`, `<substep n="4.3a"`)

	// Red-path proof moved inside the tests-first goal A (acceptance).
	assert.Contains(t, sub43, "RUNS the generated red-phase gate and confirms it EXITS 0",
		"goal A's acceptance must make the worker prove the red path in-goal")
	assert.Contains(t, sub43, "RE-PROVES it at validation time",
		"goal A's validate must re-prove the red path")

	// Emission-time verification: green-path baseline + banner correctness.
	assert.Contains(t, sub43, "no meaningful red yet",
		"emission-time green-path baseline (no new test -> non-zero exit) must be specified")
	assert.Contains(t, sub43, "BANNER CORRECTNESS",
		"emission-time banner verification against the resolved runner's real output must be specified")
	assert.Contains(t, sub43, "created LATER by the tests-first goal's worker",
		"the rule must state why the red path cannot run at emission time")

	// Structured runner output preferred over brittle banner-grepping.
	assert.Contains(t, sub43, "JUnit XML",
		"machine-readable runner report must be the preferred discrimination form")
	assert.Contains(t, sub43, "brittle across runner versions and locales",
		"banner brittleness rationale must be stated")
}

// TestStage4Xml_InvestigationConfigOverflowConsolidation guards weakness I3:
// investigation_config caps at 4 entries, but the mandatory code-review
// investigator + auto-derived investigators + one convention-audit per MUST
// review-kind rule can overflow it. 4.3a must consolidate all MUST review
// rules into ONE convention-audit carrying the full rule-id list, never drop a
// rule, never exceed 4 entries.
func TestStage4Xml_InvestigationConfigOverflowConsolidation(t *testing.T) {
	content := readEmbeddedCommand(t, "feature/stage-4-implementation.xml")
	sub43a := stage4Substep(t, content, `<substep n="4.3a"`, `<substep n="4.4"`)

	assert.Contains(t, sub43a, "CONSOLIDATE ALL MUST review-kind rules into ONE convention-audit investigator",
		"4.3a must define the overflow consolidation strategy")
	assert.Contains(t, sub43a, "FULL list of rule ids",
		"the consolidated investigator must carry every merged rule id")
	assert.Contains(t, sub43a, "NEVER silently drop a rule and NEVER exceed 4 entries",
		"consolidation must neither drop rules nor exceed the cap")
	assert.Contains(t, sub43a, "log the consolidation",
		"consolidation must be logged")
}

// TestStage4Xml_DurableFeatureGoalsHandoff guards weakness I4: the 4.4 handoff
// to Stage 5 was a one-line in-conversation message that would not survive
// context compaction over an hours-long daemon run. 4.4 must ALSO write the
// authoritative handoff to feature-goals.md in the Stage-1 research-root.
func TestStage4Xml_DurableFeatureGoalsHandoff(t *testing.T) {
	content := readEmbeddedCommand(t, "feature/stage-4-implementation.xml")
	sub44 := stage4Substep(t, content, `<substep n="4.4"`, `<execution-rules>`)

	assert.Contains(t, sub44, "feature-goals.md",
		"4.4 must write the durable handoff file feature-goals.md")
	assert.Contains(t, sub44, "AUTHORITATIVE Stage-5 handoff",
		"feature-goals.md must be the authoritative Stage-5 handoff")
	assert.Contains(t, sub44, "SAME research-root the Stage-1 context dossier artifacts live in",
		"the handoff file must live in the Stage-1 dossier's research-root")
	for _, marker := range []string{"the feature description", "TESTS_MODE", "depends_on", "resolved validate commands"} {
		assert.Contains(t, sub44, marker,
			"feature-goals.md contents must include %q", marker)
	}
	assert.Contains(t, sub44, "in-conversation handoff block is absent",
		"Stage 5 / a resumed session must read the file when the conversation block is gone")
}

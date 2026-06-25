package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are prompt-contract guards over the embedded elaborate.xml template.
// elaborate.xml drives an LLM, not Go behavior, so the tests lock the F1
// invariant: the Tier-2 elaborator must RESOLVE the goal's phase to its
// task-plan-generate criteria-catalog shard and APPLY that catalog (plus the
// resolved convention pack) ADDITIVELY to the live-tree read when authoring
// validate / acceptance / goal.md. The phase→shard filename map is asserted by
// PRESENCE of prefixes/path strings only (per the spec), so a future shard
// rename is a table edit in the XML, not a test edit here.
//
// readEmbeddedCommand is defined in published_ports_test.go (package main) and
// is REUSED here — redefining it would be a duplicate-symbol compile error.

// TestElaborate_DeclaresPhaseCriteriaCatalogMap: the template carries a
// phase→catalog mapping referencing the task-plan-generate step-3 shards and the
// per-phase catalog id prefixes.
func TestElaborate_DeclaresPhaseCriteriaCatalogMap(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	assert.Contains(t, content, "task-plan-generate/step-3",
		"elaborate.xml must map phases to task-plan-generate step-3 catalog shards")
	for _, prefix := range []string{"PD-", "AU-", "EV-", "EH-", "SA-", "FG-"} {
		assert.Contains(t, content, prefix,
			"phase-catalog map must reference the %s catalog id prefix", prefix)
	}
}

// TestElaborate_HasPhaseConventionResolutionStep: a step n="3c" that resolves
// the goal's phase to its criteria catalog exists.
func TestElaborate_HasPhaseConventionResolutionStep(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	assert.Contains(t, content, `n="3c"`,
		"elaborate.xml must declare a step n=\"3c\" for phase→catalog resolution")
	assert.Contains(t, content, "criteria catalog",
		"step 3c must speak of resolving the phase's criteria catalog")
	step := sliceBetween(t, content, `n="3c"`, `n="4"`)
	assert.Contains(t, step, "phase",
		"step 3c must map the goal's phase to its catalog")
}

// TestElaborate_ReferencesInstalledShardsAsCatalog: the catalog source is the
// runtime-reachable installed shard path (proving shards-as-catalog).
func TestElaborate_ReferencesInstalledShardsAsCatalog(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	assert.Contains(t, content, ".claude/commands/tmux/task-plan-generate/",
		"elaborate.xml must reference the installed shard catalog path reachable at runtime")
}

// TestElaborate_DoesNotReferenceNonexistentSpecDoc: the nonexistent
// docs/task-plan-spec.md must never be cited as the catalog source.
func TestElaborate_DoesNotReferenceNonexistentSpecDoc(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	assert.NotContains(t, content, "docs/task-plan-spec.md",
		"elaborate.xml must not depend on the nonexistent docs/task-plan-spec.md")
}

// TestElaborate_PreservesBareCommandMandate: step 4's BARE-command and
// deliverable-pinning mandates survive the F1 edit (regression guard).
func TestElaborate_PreservesBareCommandMandate(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	step := sliceBetween(t, content, `n="4"`, `n="5"`)
	assert.Contains(t, step, "BARE",
		"step 4 must still mandate BARE validate commands")
	assert.Contains(t, step, "wrapcmd.classify()",
		"step 4 must still explain BARE routing via wrapcmd.classify()")
	assert.Contains(t, strings.ToUpper(step), "DELIVERABLE-PINNING",
		"step 4 must still carry the deliverable-pinning mandate")
}

// TestElaborate_PreservesCodeRuleInjectionByReference: [CODE-RULE-INJECTION]
// remains by-reference — it still cites task-plan-generate.xml's
// rule-to-goal-injection block and is not re-derived here.
func TestElaborate_PreservesCodeRuleInjectionByReference(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	assert.Contains(t, content, "[CODE-RULE-INJECTION]",
		"elaborate.xml must keep the [CODE-RULE-INJECTION] reference")
	assert.Contains(t, content, "rule-to-goal-injection",
		"[CODE-RULE-INJECTION] must remain by-reference to task-plan-generate.xml's rule-to-goal-injection")
}

// TestElaborate_ErrorReportingIsLastChildOfExecutionRules: within
// <execution-rules>, <error-reporting> must be the final child element (mirrors
// the global ReferenceErrorReporting walk's invariant locally).
func TestElaborate_ErrorReportingIsLastChildOfExecutionRules(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	block := sliceBetween(t, content, "<execution-rules>", "</execution-rules>")
	// Find the last OPENING element tag (a '<' not followed by '/'), so the
	// closing "</error-reporting>" tag doesn't shadow the assertion.
	last := -1
	for i := 0; i < len(block)-1; i++ {
		if block[i] == '<' && block[i+1] != '/' {
			last = i
		}
	}
	require.GreaterOrEqual(t, last, 0, "execution-rules block must contain a child element")
	assert.True(t, strings.HasPrefix(block[last:], "<error-reporting"),
		"<error-reporting> must be the LAST child of <execution-rules>, got %q", block[last:])
}

// TestElaborate_ConventionApplicationIsAdditiveNotPredictive: step 3c/4 state
// the catalog application is ADDITIVE to the live-tree read (never reintroduces
// prediction or collapses the real-tree authoring).
func TestElaborate_ConventionApplicationIsAdditiveNotPredictive(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	step := sliceBetween(t, content, `n="3c"`, `n="4"`)
	assert.Contains(t, strings.ToLower(step), "additive",
		"step 3c must state the catalog application is ADDITIVE")
	lower := strings.ToLower(step)
	assert.True(t, strings.Contains(lower, "live tree") || strings.Contains(lower, "live-tree") ||
		strings.Contains(lower, "real tree") || strings.Contains(lower, "real-tree"),
		"step 3c must tie the additive application to the live/real-tree read")
}

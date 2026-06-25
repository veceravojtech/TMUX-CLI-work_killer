package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are prompt-contract guards over the embedded elaborate.xml template
// for the F2 `validates:` elaboration branch. elaborate.xml drives an LLM, not
// Go behavior, so the tests lock the invariant: the Tier-2 elaborator must FORK
// validate[] authoring on whether the goal carries `validates:` (Goal.Validates
// non-empty ⇒ IsValidationGoal, goals.go:191-202,506). A validation goal authors
// a HEAVY cluster-wide validate stack over the real deliverables of every
// `depends_on` cluster member, while an ordinary impl goal keeps the EXISTING
// LIGHT deliverable-pinning presence validate over its own deliverable_area. The
// heavy arm must preserve the Go layer's terminal-to-itself / no-cascade
// guarantee (CascadeFailure short-circuit, goals.go:791-800): its authored
// validates must never block or cascade-fail the implementer it validates.
//
// readEmbeddedCommand is defined in published_ports_test.go and sliceBetween in
// task_plan_generate_template_test.go (both package main) — REUSED here;
// redefining either would be a duplicate-symbol compile error.

// TestElaborateXml_BranchesOnValidates: step 2's read list mentions `validates`
// (the branch discriminator) AND step 4 carries both arms — a heavy-cluster
// mandate keyed on `validates:` and the preserved light presence validate — so
// the fork provably exists.
func TestElaborateXml_BranchesOnValidates(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")

	step2 := sliceBetween(t, content, `n="2"`, `n="3"`)
	assert.Contains(t, step2, "validates",
		"step 2 must ALSO extract `validates` — the discriminator for step 4's fork")

	step4 := sliceBetween(t, content, `n="4"`, `n="5"`)
	assert.Contains(t, step4, "validates",
		"step 4 must fork on `validates:`")
	assert.Contains(t, strings.ToUpper(step4), "HEAVY",
		"step 4 must declare a HEAVY (cluster-gate) arm")
	assert.Contains(t, strings.ToUpper(step4), "LIGHT",
		"step 4 must declare a LIGHT (presence-validate) arm")
	assert.Contains(t, step4, `name="heavy"`,
		"step 4 must mark the heavy arm")
	assert.Contains(t, step4, `name="light"`,
		"step 4 must mark the light arm")
}

// TestElaborateXml_HeavyArmSpansCluster: the heavy arm authors over the
// cluster's `depends_on` members' real deliverables (the authoritative full
// cluster), not only the validation goal's own coarse `deliverable_area`.
func TestElaborateXml_HeavyArmSpansCluster(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	step4 := sliceBetween(t, content, `n="4"`, `n="5"`)
	heavy := sliceBetween(t, step4, `name="heavy"`, `name="light"`)

	assert.Contains(t, heavy, "depends_on",
		"heavy arm must enumerate the cluster from `depends_on` (the authoritative full cluster)")
	assert.Contains(t, strings.ToLower(heavy), "cluster",
		"heavy arm must author a cluster-wide validate stack")
	// Cluster enumeration uses depends_on, NOT validates: (which names only the
	// PRIMARY_IMPL). The heavy arm must say so explicitly to avoid under-scoping.
	assert.Contains(t, heavy, "PRIMARY_IMPL",
		"heavy arm must state `validates:` names only PRIMARY_IMPL, so depends_on is the cluster source")
}

// TestElaborateXml_HeavyArmTerminalNoCascade: the heavy arm carries the
// no-cascade / terminal-to-itself guard (its authored validates must not block
// the implementer it validates), mirroring goals.go CascadeFailure semantics.
func TestElaborateXml_HeavyArmTerminalNoCascade(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	step4 := sliceBetween(t, content, `n="4"`, `n="5"`)
	heavy := sliceBetween(t, step4, `name="heavy"`, `name="light"`)

	assert.Contains(t, strings.ToLower(heavy), "terminal",
		"heavy arm must state the validation goal is terminal-to-itself")
	assert.Contains(t, strings.ToLower(heavy), "cascade",
		"heavy arm must forbid cascade-failing the implementer")
	assert.Contains(t, strings.ToLower(heavy), "implementer",
		"heavy arm must name the implementer it must not block")
}

// TestElaborateXml_LightArmPreserved: the deliverable-pinning + BARE mandates
// survive for the impl-goal arm (regression guard — the branch must not weaken
// the existing light arm).
func TestElaborateXml_LightArmPreserved(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	step4 := sliceBetween(t, content, `n="4"`, `n="5"`)
	light := sliceBetween(t, step4, `name="light"`, `</branch>`)

	assert.Contains(t, light, "BARE",
		"light arm must still mandate BARE validate commands")
	assert.Contains(t, strings.ToUpper(light), "DELIVERABLE-PINNING",
		"light arm must still carry the deliverable-pinning mandate")
	assert.Contains(t, light, "deliverable_area",
		"light arm must still pin over the goal's own deliverable_area")
}

// TestElaborateXml_ErrorReportingStaysLast: the new fork execution-rule is added
// to <execution-rules> BEFORE <error-reporting>, which remains the last child
// (AGENTS.md invariant + TestEmbeddedCommands_ReferenceErrorReporting).
func TestElaborateXml_ErrorReportingStaysLast(t *testing.T) {
	content := readEmbeddedCommand(t, "elaborate.xml")
	block := sliceBetween(t, content, "<execution-rules>", "</execution-rules>")

	// The fork summary rule must exist within execution-rules...
	forkIdx := strings.Index(block, "validates")
	require.GreaterOrEqual(t, forkIdx, 0,
		"execution-rules must summarize the validates: fork")

	// ...and <error-reporting> must come AFTER it and be the LAST child element.
	errIdx := strings.Index(block, "<error-reporting")
	require.GreaterOrEqual(t, errIdx, 0, "execution-rules must reference <error-reporting>")
	assert.Greater(t, errIdx, forkIdx,
		"<error-reporting> must come AFTER the new fork rule")

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

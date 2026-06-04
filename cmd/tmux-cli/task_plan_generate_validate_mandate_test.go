package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskPlanGenerateXml_ValidateMandateInGoalEmissionStep guards the RC-A
// template-side fix. In the 69-goal test-project run EVERY goal landed with
// `validate: None` in goals.yaml — generation wrote validation rules only as
// PROSE into goal.md, never as structured fields. The daemon reads ONLY the
// structured fields, so the dispatch-time repair (EnsureInvestigationConfig)
// padded blind default investigators (RC-B: guaranteed validation errors in
// non-Go projects → budget-exhaustion cascade). task-plan-generate.xml MUST
// therefore carry a CRITICAL mandate inside the goal-emission step (the
// canonical substep 1.7, whose <scope-derivation> the execution-rules already
// reference for every other goal-create call): every goal-create/goal add MUST
// pass structured validate (>=1 project-runnable command), acceptance (>=1
// criterion), and scope when the footprint is known.
func TestTaskPlanGenerateXml_ValidateMandateInGoalEmissionStep(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")

	// --- Locate the goal-emission step by its stable anchor ---
	anchor := `<substep n="1.7" title="Call goal-create MCP">`
	start := strings.Index(content, anchor)
	require.NotEqual(t, -1, start,
		"task-plan-generate.xml must have the goal-emission substep 1.7 (Call goal-create MCP)")
	end := strings.Index(content[start:], "</substep>")
	require.NotEqual(t, -1, end, "substep 1.7 must be well-formed")
	body := content[start : start+end]

	// --- The mandate lives INSIDE the emission step, not merely anywhere ---
	assert.Contains(t, body, "--validate",
		"the emission step must mandate the --validate flag / validate param by name")
	assert.Contains(t, body, "--acceptance",
		"the emission step must mandate the --acceptance flag / acceptance param by name")
	assert.Contains(t, body, "PROJECT-RUNNABLE",
		"the emission step must require validate commands to be PROJECT-RUNNABLE "+
			"(resolved against the detected language/stack), not generic")
	assert.Contains(t, body, "NOT prose",
		"the emission step must state validate entries are commands, NOT prose")

	// --- Prose-insufficiency statement (the RC-A root cause) ---
	assert.Contains(t, body, "prose in goal.md are NOT sufficient",
		"the mandate must state prose-only Validation Rules in goal.md are NOT sufficient")
	assert.Contains(t, body, "daemon reads ONLY the structured fields",
		"the mandate must explain WHY prose fails: the daemon reads only structured fields")
	assert.Contains(t, body, "default investigators",
		"the mandate must name the consequence: the repair fallback injects wrong default investigators")

	// --- Scope/parallelism note (empty scope serializes) ---
	assert.Contains(t, body, "DisjointReadySet",
		"the mandate must name the DisjointReadySet gate that serializes scopeless goals")
	assert.Contains(t, body, "scope is the price of parallelism",
		"the mandate must state that scope is the price of parallelism")
}

// TestTaskPlanGenerateXml_ValidateMandateAppliesToEveryGoalCreate: substep 1.7
// is the canonical emission step, but the XML has a goal-create call site per
// phase (2.7, 3.14.4i, 3.15.4e, ...). The execution-rules section already
// extends substep 1.7's <scope-derivation> to EVERY call site; the
// validate/acceptance mandate must get the same treatment so no later phase
// can emit a structurally-empty goal.
func TestTaskPlanGenerateXml_ValidateMandateAppliesToEveryGoalCreate(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")

	rulesStart := strings.Index(content, "<execution-rules>")
	rulesEnd := strings.Index(content, "</execution-rules>")
	require.NotEqual(t, -1, rulesStart, "task-plan-generate.xml must have an execution-rules section")
	require.NotEqual(t, -1, rulesEnd, "execution-rules must be well-formed")
	rules := content[rulesStart:rulesEnd]

	assert.Contains(t, rules, "validate + acceptance on EVERY goal-create",
		"execution-rules must extend the structured validate/acceptance mandate to every goal-create call site")
	assert.Contains(t, rules, "project-runnable",
		"the execution-rule must require >=1 project-runnable validate command")
	assert.Contains(t, rules, "never prose-only",
		"the execution-rule must forbid prose-only validation rules")
}

// TestTaskPlanGenerateMd_ValidateMandate: the .md companion is the
// quick-reference the generation agent reads FIRST. Its pitfalls table covers
// goal-creation constraints (G-11 max_retries, GM-08 investigators, ...), so
// it must also surface the structured validate/acceptance/scope mandate —
// otherwise the agent's distilled view omits the one constraint whose absence
// produced the 69x `validate: None` cascade.
func TestTaskPlanGenerateMd_ValidateMandate(t *testing.T) {
	md := readEmbeddedCommand(t, "task-plan-generate.md")

	assert.Contains(t, md, "structured",
		"the companion must mention the structured-fields mandate")
	assert.Contains(t, md, "validate: None",
		"the companion must name the failure mode (validate: None) the mandate prevents")
	assert.Contains(t, md, "prose",
		"the companion must state prose-only validation rules are insufficient")
	assert.Contains(t, md, "scope is the price of parallelism",
		"the companion must carry the scope/parallelism note")
}

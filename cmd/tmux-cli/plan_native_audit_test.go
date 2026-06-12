package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanXml_NativeBlindAudit guards the serial native audit gate. The
// standalone /tmux:plan-audit window command was deleted: it cost a full tmux
// window + windows-message round-trip, its 10-minute timeout silently APPROVED
// plans when the auditor hung, and the caller wiped findings files on every
// loop-back so the auditor's cross-pass RESOLVED re-verification never fired.
// Step 11a now runs the audit as ONE serial Claude-native sub-agent — a fresh
// context with none of the planner's conversation, which is what keeps the
// audit blind — with the full rubric inline and findings preserved across
// passes.
func TestPlanXml_NativeBlindAudit(t *testing.T) {
	content := readEmbeddedCommand(t, "plan.xml")

	step11a := strings.Index(content, `<step n="11a" title="Blind audit gate">`)
	require.NotEqual(t, -1, step11a, "plan.xml must keep the step 11a Blind audit gate")
	step11aEnd := strings.Index(content[step11a:], "</step>")
	require.NotEqual(t, -1, step11aEnd, "step 11a must be well-formed")
	body := content[step11a : step11a+step11aEnd]

	// --- native serial sub-agent, not a tmux window ---
	assert.Contains(t, body, "Agent tool",
		"the audit must run via the native Agent tool")
	assert.Contains(t, body, "Do NOT create a tmux window",
		"no audit window may be created")
	assert.NotContains(t, body, "windows-create",
		"step 11a must not create windows")
	assert.NotContains(t, content, "/tmux:plan-audit",
		"the standalone plan-audit command is deleted — nothing may invoke it")
	assert.NotContains(t, body, "10 minutes",
		"no window timeout remains — the Agent tool returns synchronously")

	// --- the rubric moved inline and stays intact ---
	for _, s := range []string{"−25", "−15", "−8", "−3", ">= 90", "zero open SEV-1/SEV-2"} {
		assert.Contains(t, body, s, "scoring rubric must live inline in step 11a")
	}
	assert.Contains(t, body, "validate-executability",
		"8-dimension checklist must live inline in step 11a")
	assert.Contains(t, body, "scope-sanity",
		"8-dimension checklist must live inline in step 11a")
	assert.Contains(t, body, "never by weakening validate/acceptance",
		"the no-weakening guardrail must survive the move into plan.xml")

	// --- cross-pass ledger: cleanup is first-entry only, RESOLVED re-verified ---
	assert.Contains(t, body, "First entry only",
		"verdict cleanup must not run on 11b loop-backs")
	assert.Contains(t, body, "re-verify every RESOLVED claim",
		"pass 2+ must re-verify RESOLVED claims from prior findings files")

	// --- verdict files keep their daemon-visible names and locations ---
	assert.Contains(t, body, "docs/architecture/plan-approval.md",
		"standalone pass verdict must land where the daemon's RequirePlanApproval gate looks")
	assert.Contains(t, body, "plan-audit-{AUDIT_PASS}.md",
		"fail verdict must stay pass-numbered for the 11b replan loop")

	// --- the command pair is gone from the embedded tree ---
	templates := buildCommandTemplates()
	assert.NotContains(t, templates, "plan-audit.xml",
		"embedded plan-audit.xml must be deleted")
	assert.NotContains(t, templates, "plan-audit.md",
		"embedded plan-audit.md must be deleted")
}

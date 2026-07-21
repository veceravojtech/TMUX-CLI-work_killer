package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// supervisorStep returns the text of the `<step n="<id>" ...>` … `</step>` block
// in supervisor.xml. Scoping assertions to one step keeps them honest: a token
// that only exists somewhere else in the 79KB document must not satisfy a
// step-specific requirement.
func supervisorStep(t *testing.T, content, id string) string {
	t.Helper()
	open := `<step n="` + id + `"`
	start := strings.Index(content, open)
	require.NotEqual(t, -1, start, "supervisor.xml must declare step %s", id)
	end := strings.Index(content[start:], "</step>")
	require.NotEqual(t, -1, end, "step %s must be closed", id)
	return content[start : start+end]
}

// TestSupervisorXml_Step0_RemovesStaleFreshHandoff asserts the step-0 clean
// slate deletes a leftover handoff marker and logs it. A marker surviving to a
// fresh boot means the Stop hook never consumed it (§4 staleness rule); leaving
// it in place would let it ambush a later, unrelated Stop with a restart onto a
// stale plan.
func TestSupervisorXml_Step0_RemovesStaleFreshHandoff(t *testing.T) {
	step0 := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "0")

	assert.Contains(t, step0, ".tmux-cli/fresh-handoff",
		"step 0 must name the marker path byte-exactly")
	assert.Contains(t, step0, "Stale fresh-handoff marker removed",
		"step 0 must LOG the stale-marker removal, not silently unlink it")
	assert.Contains(t, step0, "one-shot",
		"step 0 must explain why a surviving marker means the hook never fired")
}

// TestSupervisorXml_Step0c_AdoptsHandoffCounters asserts the cap-integrity half
// of the contract (§6): a .md plan file whose frontmatter carries
// self_wave/cycle seeds those counters instead of 0, so /clear cannot launder
// the SELF_WAVE cap of 2 across a handoff chain.
func TestSupervisorXml_Step0c_AdoptsHandoffCounters(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	step0c := supervisorStep(t, content, "0c")

	for _, tok := range []string{
		"$ARGUMENTS",  // the plan file arrives as the argument
		".md",         // only a .md plan file carries frontmatter
		"frontmatter", // the counters live in a YAML frontmatter block
		"self_wave",
		"cycle",
		"SELF_WAVE",
	} {
		assert.Contains(t, step0c, tok,
			"step 0c counter adoption must reference %q", tok)
	}
	assert.Contains(t, step0c, "instead of 0",
		"step 0c must state the adopted counters REPLACE the default 0 start")
	assert.Contains(t, step0c, "cap",
		"step 0c must tie adoption to cap integrity (§6)")

	// Step 0b resolves paths only and must point at 0c for the counters, so the
	// two boot steps stay distinguishable.
	assert.Contains(t, supervisorStep(t, content, "0b"), "step 0c",
		"step 0b must cross-reference step 0c for counter adoption")
}

// TestSupervisorXml_Step9b_PlanFileBranch asserts the §5c plan-file branch:
// no pending tasks remain but the replan produced an executable next wave that
// exists only as a plan document.
func TestSupervisorXml_Step9b_PlanFileBranch(t *testing.T) {
	step9b := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b")

	// (1) the next-wave plan file, with frontmatter counters. <W> is the FILENAME
	// index and is deliberately NOT <N> (the self_wave value) — reusing one letter
	// for both conflated a counter with an identifier, and SELF_WAVE is not a safe
	// filename index (it does not increment for non-self-generated waves).
	assert.Contains(t, step9b, "next-wave-&lt;W&gt;.md",
		"9b must write the next wave to {research-root}/next-wave-<W>.md (XML-escaped)")
	assert.Contains(t, step9b, "{research-root}/next-wave-&lt;W&gt;.md",
		"the next-wave plan file must live under research-root")
	assert.Contains(t, step9b, "does NOT already exist",
		"the filename index must avoid clobbering an existing next-wave plan file")
	assert.Contains(t, step9b, "self_wave: &lt;post-increment SELF_WAVE&gt;",
		"the plan frontmatter must carry the post-increment self_wave")
	assert.Contains(t, step9b, "cycle: &lt;current cycle counter&gt;",
		"the plan frontmatter must carry the cycle counter")

	// (2) STANDALONE: the /tmux:supervisor:fresh marker procedure, then STOP.
	assert.Contains(t, step9b, "/tmux:supervisor:fresh",
		"the standalone branch must reference the fresh command by name")
	assert.Contains(t, step9b, ".tmux-cli/fresh-handoff",
		"the standalone branch must write the marker at the contract path")
	for _, field := range []string{
		"plan: ", "self_wave: ", "cycle: ", "requested_by: supervisor", "created: ",
	} {
		assert.Contains(t, step9b, field,
			"the marker must carry the §4 contract field %q", field)
	}
	assert.Contains(t, step9b, "rename",
		"the marker must be written atomically (temp + rename)")
	assert.Contains(t, step9b, "Do NOT windows-send /clear",
		"the standalone branch must forbid the self-send dance (the Stop hook sends)")

	// (3) GOAL_MODE keeps the existing windows-send dance.
	assert.Contains(t, step9b, "taskvisor-active",
		"9b must explain WHY goal mode keeps windows-send (the hook defers)")
	assert.Contains(t, step9b, "Keep the existing windows-send dance",
		"the GOAL_MODE branch must retain the windows-send restart")

	// Caps are enforced before anything is written — no /clear laundering.
	assert.Contains(t, step9b, "max_cycles",
		"9b's plan-file branch must enforce max_cycles")
	assert.Contains(t, step9b, "launder",
		"9b must state that a /clear may not launder a cap")

	// The pre-existing tasks-path continuation is untouched (§10.2 non-scope).
	assert.Contains(t, step9b, "Send /tmux:supervisor {tasks-path} to SUPERVISOR_WID via windows-send",
		"the existing tasks-path continuation must survive unchanged")
}

// TestSupervisorXml_Step9b_PlanBranchPrecedesNormalStop asserts evaluation
// order. The checks are read top-down; if the unqualified "no pending tasks
// remain" normal stop came first it would shadow the plan-file branch entirely,
// making the whole feature dead code.
func TestSupervisorXml_Step9b_PlanBranchPrecedesNormalStop(t *testing.T) {
	step9b := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b")

	planBranch := strings.Index(step9b, "PLAN DOCUMENT")
	require.NotEqual(t, -1, planBranch, "9b must carry the plan-document branch")
	normalStop := strings.Index(step9b, "Normal stop — all work is complete.")
	require.NotEqual(t, -1, normalStop, "9b must retain the normal-stop branch")

	assert.Less(t, planBranch, normalStop,
		"the plan-file branch must be evaluated BEFORE the normal stop, or it is unreachable")
	assert.Contains(t, step9b, "did NOT fire",
		"the normal-stop check must be qualified on the plan-file branch not firing")
}

// TestSupervisorXml_ExecutionRules_WindowsSendScoped asserts the execution-rules
// amendment: the plan-file self-restart is GOAL_MODE-only, while the standalone
// tasks-path continuation explicitly stays on windows-send (§10.2 fast-follow).
func TestSupervisorXml_ExecutionRules_WindowsSendScoped(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")
	start := strings.Index(content, "<execution-rules>")
	require.NotEqual(t, -1, start, "supervisor.xml must carry <execution-rules>")
	end := strings.Index(content[start:], "</execution-rules>")
	require.NotEqual(t, -1, end, "<execution-rules> must be closed")
	rules := content[start : start+end]

	assert.Contains(t, rules,
		"GOAL_MODE only; standalone cycle/wave restarts go through the fresh-handoff marker",
		"the windows-send rule must carry the §5c amendment verbatim")
	assert.Contains(t, rules, ".tmux-cli/fresh-handoff",
		"the amended rule must name the marker path byte-exactly")
	assert.Contains(t, rules, "TASKS-PATH continuation",
		"the amended rule must keep the tasks-path continuation distinguishable")
	assert.Contains(t, rules, "do not remove those sends",
		"the tasks-path continuation must be explicitly preserved (§10.2 non-scope)")
}

// TestSupervisorXml_WellFormed guards the surgical edits above: the amendments
// introduce YAML blocks and escaped placeholders into element text, and an
// unescaped '<' would break every consumer of the document.
func TestSupervisorXml_WellFormed(t *testing.T) {
	dec := xml.NewDecoder(strings.NewReader(readEmbeddedCommand(t, "supervisor.xml")))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "supervisor.xml must remain well-formed XML")
	}
}

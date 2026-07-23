package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cross-surface contract test for the fresh-handoff feature
// (docs/architecture/supervisor-fresh-design.md §4).
//
// The handoff spans THREE independently-authored surfaces that never call each
// other — they agree only by writing and grepping the same byte sequences:
//
//	WRITER  embedded/commands/tmux/supervisor/fresh.xml  (manual /tmux:supervisor:fresh)
//	WRITER  embedded/commands/tmux/supervisor.xml        (step 9b standalone handoff)
//	READER  embedded/tmux-supervisor-cycle.sh            (Stop hook, consumes the marker)
//
// Each slice has its own test asserting its own side of the contract. Those
// cannot catch DRIFT: if a writer renames `self_wave` to `selfWave`, both
// per-slice suites stay green and the feature silently stops working — the hook
// greps a field nobody writes any more, reads empty, and defaults to 0, which
// LAUNDERS THE SELF_WAVE CAP rather than failing loudly.
//
// This file is the single place the tokens are spelled. Every assertion below
// derives from the const block, so a rename must happen HERE (one edit, all
// three surfaces re-checked) or the suite goes red.
//
// Content assertions only: no tmux server, no filesystem writes.
const (
	// The marker file path — hard-coded in all three surfaces.
	contractMarkerPath = ".tmux-cli/fresh-handoff"

	// The command that arms a marker by hand.
	contractCommandName = "/tmux:supervisor:fresh"

	// The command the hook relaunches the window onto.
	contractRelaunchCommand = "/tmux:supervisor "

	// The confirmation line's fixed prefix. The arrow is a literal U+2192; an
	// HTML entity (&#8594;) reads differently to an LLM consuming the XML as
	// text, and the §8 e2e scenario greps the pipe-pane for this exact prefix.
	contractArmedLinePrefix = "FRESH HANDOFF armed → "
)

// contractMarkerFields are the §4 YAML keys. Both WRITERS must emit all five;
// the hook READER parses the first three (requested_by/created are diagnostic).
var contractMarkerFields = []string{"plan", "self_wave", "cycle", "requested_by", "created"}

// contractParsedFields are the keys the hook actually greps out of the marker.
var contractParsedFields = []string{"plan", "self_wave", "cycle"}

// xmlEsc mirrors the escaping the embedded XML surfaces use for angle brackets,
// so a token can be searched for in either an XML or a shell surface.
func xmlEsc(s string) string {
	return strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(s)
}

// TestFreshHandoffContract_MarkerPathIdenticalAcrossSurfaces asserts the one
// path every surface agrees on. A typo here is the single highest-impact drift:
// the writer arms a file the reader never looks at, so the handoff silently
// becomes a no-op and the supervisor just stops.
func TestFreshHandoffContract_MarkerPathIdenticalAcrossSurfaces(t *testing.T) {
	for name, content := range map[string]string{
		"supervisor/fresh.xml (writer)":     readEmbeddedCommand(t, freshXMLRel),
		"supervisor.xml step 9b (writer)":   readEmbeddedCommand(t, "supervisor.xml"),
		"tmux-supervisor-cycle.sh (reader)": hookSupervisorCycle,
	} {
		assert.Contains(t, content, contractMarkerPath,
			"%s must reference the marker at the §4 contract path %q", name, contractMarkerPath)
	}
}

// TestFreshHandoffContract_WritersEmitEveryField asserts both writers spell all
// five §4 fields. A writer that drops `self_wave` produces a marker the hook
// parses to 0 — the cap-laundering failure design goal 5 exists to prevent.
func TestFreshHandoffContract_WritersEmitEveryField(t *testing.T) {
	writers := map[string]string{
		"supervisor/fresh.xml": readEmbeddedCommand(t, freshXMLRel),
		"supervisor.xml":       supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b"),
	}

	for name, content := range writers {
		for _, field := range contractMarkerFields {
			assert.Contains(t, content, field+":",
				"%s must emit the §4 marker field %q as `%s:`", name, field, field)
		}
	}
}

// TestFreshHandoffContract_HookParsesWhatWritersEmit closes the loop: every
// field the hook greps must be a field the writers actually emit, spelled the
// same way. This is the assertion neither slice could make about itself.
func TestFreshHandoffContract_HookParsesWhatWritersEmit(t *testing.T) {
	branch := freshBranch(t)

	for _, field := range contractParsedFields {
		// The hook's own grep pattern, e.g. `^\s*self_wave:`.
		assert.Contains(t, branch, `'^\s*`+field+`:'`,
			"the hook's marker branch must grep the §4 field %q anchored at line start", field)
	}

	// And each parsed field is emitted by both writers (guards the other
	// direction: a field the hook reads that nobody writes).
	for name, content := range map[string]string{
		"supervisor/fresh.xml": readEmbeddedCommand(t, freshXMLRel),
		"supervisor.xml":       supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b"),
	} {
		for _, field := range contractParsedFields {
			assert.Contains(t, content, field+":",
				"%s must emit %q — the hook greps it", name, field)
		}
	}
}

// TestFreshHandoffContract_ArmedLineIdenticalAcrossWriters asserts both writers
// print the SAME confirmation prefix, with a literal arrow. The §8 e2e scenario
// asserts on this line, so two variants would mean two greps.
func TestFreshHandoffContract_ArmedLineIdenticalAcrossWriters(t *testing.T) {
	freshXML := readEmbeddedCommand(t, freshXMLRel)
	step9b := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b")

	for name, content := range map[string]string{
		"supervisor/fresh.xml": freshXML,
		"supervisor.xml 9b":    step9b,
	} {
		assert.Contains(t, content, contractArmedLinePrefix,
			"%s must print the armed line with the exact prefix %q (literal U+2192 arrow)",
			name, contractArmedLinePrefix)
		assert.NotContains(t, content, "FRESH HANDOFF armed &#8594;",
			"%s must use a literal → in the armed line, not the &#8594; entity — "+
				"the XML is consumed as raw text by the LLM that prints the line", name)
	}

	// Both spell the counters the same way inside the parenthesised suffix.
	for name, content := range map[string]string{
		"supervisor/fresh.xml": freshXML,
		"supervisor.xml 9b":    step9b,
	} {
		assert.Contains(t, content, "(self_wave=",
			"%s armed line must carry the (self_wave=…) suffix", name)
		assert.Contains(t, content, ", cycle=",
			"%s armed line must carry the , cycle=… suffix", name)
	}
}

// TestFreshHandoffContract_CommandNameConsistent asserts the installed command
// name is referenced identically wherever it is named.
func TestFreshHandoffContract_CommandNameConsistent(t *testing.T) {
	for name, content := range map[string]string{
		"supervisor/fresh.md":  readEmbeddedCommand(t, freshMdRel),
		"supervisor/fresh.xml": readEmbeddedCommand(t, freshXMLRel),
		"supervisor.xml 9b":    supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b"),
	} {
		assert.Contains(t, content, contractCommandName,
			"%s must name the command exactly %q", name, contractCommandName)
	}

	// The dir-namespaced install path is what PRODUCES that name.
	assert.Equal(t, "supervisor/fresh.md", freshMdRel,
		"the .md must install under supervisor/ to surface as /tmux:supervisor:fresh")
}

// TestFreshHandoffContract_HookRelaunchesOntoTheMarkerPlan asserts the hook's
// send uses the plan value it parsed, via the command the fresh instance's
// step 0c knows how to adopt counters from.
func TestFreshHandoffContract_HookRelaunchesOntoTheMarkerPlan(t *testing.T) {
	branch := freshBranch(t)

	assert.Contains(t, branch, `"/clear"`,
		"the hook must send /clear before the relaunch")
	assert.Contains(t, branch, contractRelaunchCommand+"${FRESH_PLAN}",
		"the hook must relaunch onto the marker's own plan value via %q", contractRelaunchCommand)

	clearIdx := strings.Index(branch, `"/clear"`)
	relaunchIdx := strings.Index(branch, contractRelaunchCommand+"${FRESH_PLAN}")
	require.NotEqual(t, -1, clearIdx)
	require.NotEqual(t, -1, relaunchIdx)
	assert.Less(t, clearIdx, relaunchIdx,
		"/clear must be sent BEFORE the relaunch — the ordering the whole feature exists to make deterministic")
}

// TestFreshHandoffContract_ConsumeBeforeSend re-asserts the one-shot lifecycle
// at the contract level: the marker is removed before ANY send, on the shared
// token rather than on execute-2's local anchor.
func TestFreshHandoffContract_ConsumeBeforeSend(t *testing.T) {
	branch := freshBranch(t)

	rmIdx := strings.Index(branch, `rm -f "$FRESH_MARKER"`)
	sendIdx := strings.Index(branch, "submit_to_pane")
	require.NotEqual(t, -1, rmIdx, "the hook must consume the marker with rm -f")
	require.NotEqual(t, -1, sendIdx, "the hook must send the restart keys (via submit_to_pane)")
	assert.Less(t, rmIdx, sendIdx,
		"the marker must be CONSUMED before anything is sent (§4 one-shot) — "+
			"otherwise a crashed send re-fires the restart on the next Stop")
}

// TestFreshHandoffContract_MaxCyclesEnforcedOnBothSides asserts the deliberate
// DOUBLE enforcement (design §6): the supervisor gate prevents writing an
// orphan marker, the hook gate is the §6 backstop for a marker armed by any
// other writer. Losing either side is a silent cap regression.
func TestFreshHandoffContract_MaxCyclesEnforcedOnBothSides(t *testing.T) {
	step9b := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "9b")
	branch := freshBranch(t)

	assert.Contains(t, step9b, "max_cycles",
		"9b's plan-file branch must gate on max_cycles BEFORE writing the marker")
	assert.Contains(t, branch, "FRESH_MAX_CYCLES",
		"the hook's marker branch must independently enforce max_cycles (§6 backstop)")
	assert.Contains(t, branch, `-ge "$FRESH_MAX_CYCLES"`,
		"the hook must use the same >= rule as the tasks.yaml branch")
}

// TestFreshHandoffContract_SelfWaveLinkageIsEndToEnd pins the ONE linkage the
// hook cannot enforce (execute-2's flagged risk). The hook parses `self_wave`
// but only LOGS it; cap integrity therefore depends entirely on this chain:
//
//	marker self_wave  →  plan-file frontmatter self_wave  →  step 0c adoption  →  9b cap
//
// If any link breaks, /clear silently resets SELF_WAVE to 0 and the 2-wave cap
// becomes unbounded — the exact failure design goal 5 names.
func TestFreshHandoffContract_SelfWaveLinkageIsEndToEnd(t *testing.T) {
	supervisorXML := readEmbeddedCommand(t, "supervisor.xml")
	step9b := supervisorStep(t, supervisorXML, "9b")
	step0c := supervisorStep(t, supervisorXML, "0c")

	// Link 1 — 9b writes the counter into the PLAN FILE frontmatter (not just
	// the marker). The frontmatter is the load-bearing half: the hook passes
	// only the plan PATH to the fresh instance, never the counters.
	assert.Contains(t, step9b, "self_wave: "+xmlEsc("<post-increment SELF_WAVE>"),
		"9b must write self_wave into the plan-file frontmatter — the marker's copy never reaches the fresh instance")

	// Link 2 — step 0c reads that frontmatter and ADOPTS it.
	assert.Contains(t, step0c, "frontmatter",
		"step 0c must read the plan file's frontmatter")
	assert.Contains(t, step0c, "self_wave",
		"step 0c must adopt self_wave")
	assert.Contains(t, step0c, "ADOPT",
		"step 0c must ADOPT the counters rather than merely reading them")

	// Link 3 — the glossary must NOT contradict adoption by declaring the
	// counter unconditionally zero per invocation. This is the drift that makes
	// every other link cosmetic: an LLM following the glossary zeroes SELF_WAVE
	// at boot and the cap launders on the first /clear.
	assert.NotContains(t, supervisorXML, "starts at 0 per invocation",
		"the SELF_WAVE glossary must not declare an unconditional per-invocation reset — "+
			"step 0c adopts a non-zero starting value from a handoff plan file")
	assert.Contains(t, supervisorXML, "per CHAIN",
		"the SELF_WAVE cap must be defined per CHAIN (across fresh-handoff /clears), not per invocation")

	// Link 4 — the cap is actually enforced against the adopted value in 9b.
	assert.Contains(t, step9b, "SELF_WAVE",
		"9b's plan-file branch must gate on SELF_WAVE")
	assert.Contains(t, step9b, "launder",
		"9b must state the cap-laundering prohibition explicitly")
}

// TestFreshHandoffContract_WindowIdentityNeverUsesDisplayMessage guards
// execute-1's MEASURED finding: `tmux display-message -p '#{window_name}'`
// returns the session's ACTIVE window, not the caller's. A guard built on it
// would let a marker be armed from any window whenever `supervisor` happened to
// be active, and refuse from `supervisor` when it wasn't — wrong in exactly the
// cases the guard exists for. Both surfaces must use the @window-uuid walk.
func TestFreshHandoffContract_WindowIdentityNeverUsesDisplayMessage(t *testing.T) {
	freshXML := readEmbeddedCommand(t, freshXMLRel)

	assert.Contains(t, freshXML, "$TMUX_WINDOW_UUID",
		"fresh.xml's window guard must resolve identity from $TMUX_WINDOW_UUID")
	assert.Contains(t, freshXML, "list-windows",
		"fresh.xml must walk tmux list-windows to map the uuid to a window name")
	assert.Contains(t, freshXML, "@window-uuid",
		"fresh.xml must match on the @window-uuid window option, as the hook does")
	assert.NotContains(t, freshXML, "display-message -p",
		"fresh.xml must NOT resolve its own window via display-message -p — "+
			"it reports the session's ACTIVE window, not the caller's (measured)")

	// The hook resolves identity the same way (its display-message calls are
	// user-facing countdown notices, never identity reads).
	assert.Contains(t, hookSupervisorCycle, "@window-uuid",
		"the hook must resolve window identity via @window-uuid")
	assert.NotContains(t, hookSupervisorCycle, "display-message -p",
		"the hook must not resolve window identity via display-message -p")
}

// TestFreshHandoffContract_GuardsAgreeAcrossSurfaces asserts the command
// refuses to arm in exactly the situations where the hook refuses to fire.
// A command that arms where the hook defers strands the marker until the next
// boot's step-0 cleanup — a stall the user gets no feedback about.
func TestFreshHandoffContract_GuardsAgreeAcrossSurfaces(t *testing.T) {
	freshXML := readEmbeddedCommand(t, freshXMLRel)

	for _, guard := range []string{"taskvisor-active", "recurring-active"} {
		assert.Contains(t, freshXML, guard,
			"fresh.xml must REFUSE to arm when %s exists", guard)
		assert.Contains(t, hookSupervisorCycle, guard,
			"the hook must DEFER when %s exists — the guards must agree", guard)
	}

	// Both restrict to the bare `supervisor` window.
	assert.Contains(t, freshXML, "supervisor",
		"fresh.xml must restrict arming to the supervisor window")
	assert.Contains(t, hookSupervisorCycle, `"$WINDOW_NAME" != "supervisor"`,
		"the hook must only act on the bare supervisor window")
}

// TestFreshHandoffContract_StaleMarkerCleanupExists asserts the §7 "hook never
// fired" row: a marker surviving to a fresh boot is removed and logged, so it
// can never ambush a later unrelated Stop.
func TestFreshHandoffContract_StaleMarkerCleanupExists(t *testing.T) {
	step0 := supervisorStep(t, readEmbeddedCommand(t, "supervisor.xml"), "0")

	assert.Contains(t, step0, contractMarkerPath,
		"step 0 must clean up a stale marker at the contract path")
	assert.Contains(t, strings.ToUpper(step0), "STALE",
		"step 0 must identify the removal as a STALE-marker cleanup")
	assert.Contains(t, strings.ToUpper(step0), "LOG",
		"step 0 must LOG the stale-marker removal — a silent delete hides a broken hook")
}

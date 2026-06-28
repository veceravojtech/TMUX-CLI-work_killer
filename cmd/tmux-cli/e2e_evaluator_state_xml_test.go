package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests pin execute-4's four appended blocks (STARTUP-ASSERT, RESUME,
// SUCCESS-CRITERIA, REPORT+STATE) into the labelled state/report slots of
// e2e-evaluator.xml. They are content assertions over the embedded conductor —
// same style as e2e_evaluator_xml_test.go (execute-2 skeleton, execute-3 loop).

// --- RESUME (step 1b slot, paired with the step-8 STATE write) ---------------

// TestResume_ContinuesAtCycle: state cycle=N,status=in-progress ⇒ loop starts at
// N; cycles 1..N-1 are NOT re-run (`cycle` = NEXT cycle to run).
func TestResume_ContinuesAtCycle(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "NEXT cycle to run",
		"RESUME must define `cycle` as the NEXT cycle to run, not the last completed")
	assert.Contains(t, content, "NOT re-run",
		"RESUME must state that already-finished cycles are NOT re-run on continue")
	assert.Contains(t, content, "in-progress",
		"RESUME continues only while status==in-progress")
}

// TestResume_MissingFileInitsCycle1: no state.json ⇒ init cycle=1, not an error.
func TestResume_MissingFileInitsCycle1(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "Missing file = cycle 1",
		"RESUME must treat a missing state file as cycle 1")
	assert.Contains(t, content, `"cycle": 1`,
		"RESUME must initialize cycle:1 on a fresh run")
	assert.Contains(t, content, "NOT an error",
		"a missing state file is a fresh run, NOT an error")
}

// TestResume_FreshFromScratchDefault: a no-arg / no-explicit-resume invocation
// must NOT continue a prior run — it CLEARS the scenario state + reports and
// starts fresh at cycle 1. Resume is OPT-IN via an explicit resume directive.
func TestResume_FreshFromScratchDefault(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "FRESH-FROM-SCRATCH IS THE DEFAULT",
		"RESUME must make fresh-from-scratch the default, not continue prior work")
	assert.Contains(t, content, "WIPED",
		"the default path must WIPE a found prior STATE_FILE, not resume it")
	assert.Contains(t, content, "e2e-report-cycle-*.md",
		"the fresh-default clear must delete prior per-cycle reports too")
	// Resume is opt-in via an explicit directive.
	assert.True(t,
		strings.Contains(content, "OPT-IN") && strings.Contains(content, "--resume"),
		"resuming an in-progress run must be opt-in via an explicit resume/--resume arg")
	assert.Contains(t, content, "STRICTLY defines resume",
		"continue only when $ARGUMENTS strictly defines resume")
}

// TestProvision_ReapsStaleTestSessions: PROVISION must reap leftover disposable
// /tmp test sessions from past runs by EXACT name (never pkill -f) before
// starting a fresh target.
func TestProvision_ReapsStaleTestSessions(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "REAP STALE TEST SESSIONS FROM PAST RUNS",
		"PROVISION must reap stale test sessions left by past runs")
	assert.Contains(t, content, "tmux-cli-tmp-",
		"the stale-session reap must scope to disposable /tmp targets by the tmux-cli-tmp- name prefix")
	assert.Contains(t, content, "tmux list-sessions",
		"the reap must enumerate sessions by exact name via tmux list-sessions")
	// Must reuse the never-pkill-f teardown discipline.
	assert.True(t,
		strings.Contains(content, "kill-session") && strings.Contains(content, "NEVER `pkill -f`"),
		"the reap must kill by exact session name and never pkill -f (self-SIGTERM footgun)")
}

// TestResume_Exhausted: cycle>max_cycles ⇒ status=exhausted, escalate, no loop.
func TestResume_Exhausted(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "max_cycles",
		"RESUME must guard cycle against max_cycles")
	assert.Contains(t, content, "exhausted",
		"RESUME must set status:exhausted past the cap")
	assert.Contains(t, content, "ESCALATE",
		"RESUME must escalate to the human when exhausted")
}

// --- STARTUP (step 1b slot) — no prerequisite gate ---------------------------

// TestStartup_NoPrerequisiteGate: the prerequisite/soft-pause gate was dropped
// (reporting is task-report-only). The conductor must NOT reintroduce a
// consumer-pipeline / auto-install-watcher prerequisite or a soft-pause gate.
func TestStartup_NoPrerequisiteGate(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "task-report-only",
		"reporting must be task-report-only — no external consumer prerequisite")
	assert.Contains(t, content, "NEVER blocks waiting for a fix",
		"the loop must never block waiting for a fix to land")
	// The removed gate's vocabulary must not creep back in.
	assert.NotContains(t, content, "SOFT-PAUSE",
		"the dropped soft-pause gate must not be reintroduced")
	assert.NotContains(t, content, "⏸ paused",
		"the dropped soft-pause line must not be reintroduced")
}

// --- SUCCESS-CRITERIA (step 10, consulted by JUDGE step 7) -------------------

// TestSuccess_AppUpRequired: all goals done but GET /login≠200 ⇒ FAIL, app_up:false.
func TestSuccess_AppUpRequired(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "app_up:false",
		"SUCCESS-CRITERIA false-pass guard must key on app_up:false")
	assert.Contains(t, content, "false pass",
		"SUCCESS-CRITERIA must call a green-daemon/dead-app a false pass")
	assert.Contains(t, content, "GET /login",
		"app-up probe must hit GET /login")
	// JUDGE may not PASS while the app is down.
	assert.True(t,
		strings.Contains(content, "may NOT return PASS") || strings.Contains(content, "never a pass"),
		"SUCCESS-CRITERIA must block PASS while app_up:false")
}

// TestSuccess_FullFlowPass: docs+goals+all-done+login200+unauth302+authed200 ⇒ passed.
func TestSuccess_FullFlowPass(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "goals.yaml",
		"full-flow PASS requires a generated goals.yaml")
	assert.Contains(t, content, `status: "passed"`,
		"a green cycle sets status:passed")
	assert.Contains(t, content, "302/401",
		"app-up probe must assert the unauthenticated dashboard redirect/deny")
	assert.Contains(t, content, "docs/architecture/*",
		"full-flow PASS requires discovery docs present")
}

// --- REPORT+STATE (steps 8–9) ------------------------------------------------

// TestReport_HasAllSections: per-cycle report carries all six fixed sections,
// including the p90 + mean-in-flight timing line.
func TestReport_HasAllSections(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	for _, section := range []string{
		"Driven Summary",
		"Failure Point",
		"Defect Signature",
		"Filed Task",
		"Timing Table",
		"Verdict",
	} {
		assert.Contains(t, content, section,
			"REPORT must declare the %q section in the fixed order", section)
	}
	assert.Contains(t, content, "p90",
		"Timing Table must record per-phase p90")
	assert.Contains(t, content, "in-flight",
		"Timing Table must record mean in-flight goals (achieved parallelism)")
	assert.Contains(t, content, "e2e-report-cycle-",
		"REPORT must write e2e-report-cycle-<n>.md")
}

// TestState_AtomicRewrite: state is rewritten atomically (temp + rename), so a
// concurrent re-invocation never observes a truncated file.
func TestState_AtomicRewrite(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, "temp + rename",
		"STATE write must be atomic temp+rename")
	assert.Contains(t, content, "last-writer-wins",
		"STATE atomic rewrite is last-writer-wins")
	assert.Contains(t, content, "NEVER truncate-in-place",
		"STATE must never truncate-in-place")
	// cycle is bumped only after REPORT, never off-by-one.
	assert.Contains(t, content, "NEXT cycle to run",
		"STATE must keep cycle = the NEXT cycle to run (no off-by-one)")
}

// TestState_GlossaryTerms: STATE_FILE / REPORT_FILE are declared in the glossary
// so the loop + resume steps reference them by name (execute-4 owns the slot).
func TestState_GlossaryTerms(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")

	assert.Contains(t, content, `name="STATE_FILE"`,
		"glossary must declare STATE_FILE")
	assert.Contains(t, content, `name="REPORT_FILE"`,
		"glossary must declare REPORT_FILE")
	assert.Contains(t, content, ".state.json",
		"STATE_FILE path is <scenario>.state.json")
}

// TestE2EEvaluatorXml_StateReportNoErrorReportingRegression: execute-4's appends
// must NOT add or remove the shared <error-reporting> reference (execute-2 owns
// the single block; TestEmbeddedCommands_ReferenceErrorReporting stays green).
func TestE2EEvaluatorXml_StateReportNoErrorReportingRegression(t *testing.T) {
	content := readEmbeddedCommand(t, "e2e-evaluator.xml")
	assert.Equal(t, 1, strings.Count(content, "<error-reporting>"),
		"the <error-reporting> reference must remain present exactly once")
}

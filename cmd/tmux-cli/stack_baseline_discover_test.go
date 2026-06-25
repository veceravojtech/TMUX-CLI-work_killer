package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiscoverXML_StackBaselineQuestionGatedDockerCompose verifies Step 7
// elicits a per-worktree baseline migrate command, gated on a docker run target
// with compose-hosted services, and offers a skip path.
func TestDiscoverXML_StackBaselineQuestionGatedDockerCompose(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	assert.Contains(t, content, `topic="stack-baseline"`,
		"Step 7 must carry a stack-baseline elicitation topic")

	// Isolate the stack-baseline question element so the gating condition and
	// skip affordance are asserted on that question, not the whole file.
	start := strings.Index(content, `topic="stack-baseline"`)
	require.NotEqual(t, -1, start)
	// Walk back to the opening <q of this element.
	qOpen := strings.LastIndex(content[:start], "<q ")
	require.NotEqual(t, -1, qOpen, "stack-baseline topic must sit on a <q> element")
	end := strings.Index(content[start:], "</q>")
	require.NotEqual(t, -1, end, "stack-baseline question must close")
	q := content[qOpen : start+end]

	assert.Contains(t, q, "docker",
		"stack-baseline question must be gated on the docker run target")
	assert.Contains(t, q, "compose",
		"stack-baseline question must be gated on compose-hosted services")
	low := strings.ToLower(q)
	assert.True(t, strings.Contains(low, "skip") || strings.Contains(low, "none"),
		"stack-baseline question must offer a skip/none path")
}

// TestDiscoverXML_StackBaselineInTestEnvSummary verifies the Step-7 pre-save
// summary surfaces the captured baseline command behind an is_docker conditional
// and only when a command was captured (skip emits nothing).
func TestDiscoverXML_StackBaselineInTestEnvSummary(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	start := strings.Index(content, "Here's the test environment configuration")
	require.NotEqual(t, -1, start, "Step-7 summary template must be present")
	end := strings.Index(content[start:], "Should I save this to")
	require.NotEqual(t, -1, end, "Step-7 summary template must end with the save prompt")
	summary := content[start : start+end]

	assert.Contains(t, summary, "Stack Baseline",
		"summary block must surface the captured Stack Baseline command")

	docker := strings.Index(summary, "{{#if is_docker}}")
	require.NotEqual(t, -1, docker, "summary must open an is_docker conditional")
	baseline := strings.Index(summary, "Stack Baseline")
	assert.Less(t, docker, baseline,
		"Stack Baseline summary must appear after the is_docker conditional opens")

	// The summary line is itself gated on a captured command so a skip emits
	// nothing (never an empty field).
	assert.Contains(t, summary, "{{#if stack_baseline_cmd}}",
		"Stack Baseline summary line must be gated on a captured stack_baseline_cmd")
}

// TestDiscoverXML_StackBaselineSavedBlockCanonicalForm verifies the saved-file
// action appends a `## Stack Baseline` block recording EXACTLY the canonical
// `**Stack Baseline:** <cmd>` line that resolveBaselineCmd parses, and that the
// block is omitted entirely on skip.
func TestDiscoverXML_StackBaselineSavedBlockCanonicalForm(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	// Locate the saved-file action that emits the Stack Baseline block.
	idx := strings.Index(content, "## Stack Baseline")
	require.NotEqual(t, -1, idx, "a `## Stack Baseline` saved-file block must be authored")

	// Find the enclosing <action ...> ... </action>.
	open := strings.LastIndex(content[:idx], "<action")
	require.NotEqual(t, -1, open, "the Stack Baseline block must be emitted from an <action>")
	close := strings.Index(content[idx:], "</action>")
	require.NotEqual(t, -1, close, "the Stack Baseline action must close")
	action := content[open : idx+close]

	// Gated on docker + compose so local-runtime projects never get the block.
	assert.Contains(t, action, "docker",
		"Stack Baseline block must be gated on the docker run target")
	assert.Contains(t, action, "compose",
		"Stack Baseline block must be gated on compose-hosted services")

	// Canonical, byte-parseable form: `**Stack Baseline:** {{stack_baseline_cmd}}`.
	assert.Contains(t, action, "**Stack Baseline:** {{stack_baseline_cmd}}",
		"saved block must record the canonical first-`:`-split form resolveBaselineCmd parses")

	// Omitted on skip — never an empty field.
	low := strings.ToLower(action)
	assert.True(t, strings.Contains(low, "omit") || strings.Contains(low, "skip"),
		"Stack Baseline action must state the block is omitted when the user skips")
}

// TestDiscoverXML_StackBaselineLinksConventionDoc verifies operators are pointed
// at T3's stack-baseline convention reference.
func TestDiscoverXML_StackBaselineLinksConventionDoc(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")
	assert.Contains(t, content, "docs/architecture/stack-baseline-convention.md",
		"the Stack Baseline flow must link operators to the convention doc")
}

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stage05Slice returns content[start:end) where start locates startMarker and
// end locates endMarker AFTER it; endMarker=="" slices to the end of content.
func stage05Slice(t *testing.T, content, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(content, startMarker)
	require.NotEqual(t, -1, start, "marker %q must exist", startMarker)
	if endMarker == "" {
		return content[start:]
	}
	end := strings.Index(content[start:], endMarker)
	require.NotEqual(t, -1, end, "marker %q must exist after %q", endMarker, startMarker)
	return content[start : start+end]
}

// TestFeatureStage0Xml_PersistsContextBlockDurably guards weakness V1: the
// Stage-0 context block was conversation-resident only, so compaction or a
// resumed session lost every resolved SIGNAL/TOPOLOGY value and later stages
// re-probed. Substep 0.4 must now write the composed block verbatim to
// feature-context.md in the research-root (resolved the same way stage-1
// substep 1.1 does), and stage-5's 5.1 must re-read that file when the
// in-conversation block is missing.
func TestFeatureStage0Xml_PersistsContextBlockDurably(t *testing.T) {
	stage0 := readEmbeddedCommand(t, "feature/stage-0-capability.xml")
	sub04 := stage05Slice(t, stage0, `<substep n="0.4"`, `</substep>`)

	assert.Contains(t, sub04, "feature-context.md",
		"stage-0 substep 0.4 must persist the context block to feature-context.md")
	assert.Contains(t, sub04, "stage-1-context.xml substep 1.1",
		"stage-0 substep 0.4 must resolve the research-root the same way stage-1 does")
	assert.Contains(t, sub04, "RE-READS this file",
		"stage-0 substep 0.4 must tell later stages to re-read the file instead of re-probing")

	stage5 := readEmbeddedCommand(t, "feature/stage-5-validation.xml")
	sub51 := stage05Slice(t, stage5, `<substep n="5.1"`, `<substep n="5.2"`)
	assert.Contains(t, sub51, "feature-context.md",
		"stage-5 substep 5.1 must fall back to feature-context.md when the Stage-0 block is absent")
}

// TestFeatureStage0Xml_ValidationResumeCheckInPreflight guards weakness V2:
// stage-0 preflight had no way to detect a resumed run whose implementation
// was already handed to the daemon, so re-running /tmux:feature re-probed and
// re-emitted duplicate goals instead of resuming validation. Substep 0.1 must
// carry a VALIDATION-RESUME check on feature-goals.md: same feature -> skip
// Stages 0-4 and jump to Stage 5; different feature -> fresh run.
func TestFeatureStage0Xml_ValidationResumeCheckInPreflight(t *testing.T) {
	stage0 := readEmbeddedCommand(t, "feature/stage-0-capability.xml")
	sub01 := stage05Slice(t, stage0, `<substep n="0.1"`, `<substep n="0.2"`)

	for _, marker := range []string{
		"VALIDATION-RESUME",
		"feature-goals.md",
		// The resume path skips the already-completed stages and lands on
		// validation directly.
		"skipping Stages 0-4, resuming at Stage 5",
		// A file naming a DIFFERENT feature must NOT trigger the resume.
		"DIFFERENT feature description",
		"FRESH run",
	} {
		assert.Contains(t, sub01, marker,
			"stage-0 substep 0.1 must retain VALIDATION-RESUME marker %q", marker)
	}
}

// TestFeatureStage5Xml_ResumableStopAcrossDaemonBoundary guards weakness V3
// (critical): stage-5 assumed it could synchronously collect verdicts, but the
// daemon executes goals over hours — with no wait/resume mechanic the stage
// either fabricated verdicts, validated unfinished goals, or idled forever.
// 5.1 must read feature-goals.md as the authoritative handoff when the
// in-conversation handoff is absent (and persist it when present); 5.3 must
// derive goal state from on-disk truth (goals.yaml status + signal.json /
// corrections) and, when any goal is still in-flight, print a status table and
// STOP in the named VALIDATION-PENDING resumable state instead of idling.
func TestFeatureStage5Xml_ResumableStopAcrossDaemonBoundary(t *testing.T) {
	stage5 := readEmbeddedCommand(t, "feature/stage-5-validation.xml")

	sub51 := stage05Slice(t, stage5, `<substep n="5.1"`, `<substep n="5.2"`)
	assert.Contains(t, sub51, "feature-goals.md",
		"stage-5 substep 5.1 must read/persist the feature-goals.md handoff file")
	assert.Contains(t, sub51, "AUTHORITATIVE",
		"stage-5 substep 5.1 must mark feature-goals.md authoritative when the in-conversation handoff is absent")

	sub53 := stage05Slice(t, stage5, `<substep n="5.3"`, `<execution-rules>`)
	for _, marker := range []string{
		// (b) on-disk state, never memory.
		"STATE FROM DISK, NEVER FROM MEMORY",
		".tmux-cli/goals.yaml",
		"signal.json",
		"corrections/cycle-N.md",
		// (d) no idle, no fabrication, no inline re-run; stop resumable.
		"Do NOT idle indefinitely",
		"STATUS TABLE",
		"VALIDATION-PENDING",
		"SAME feature description",
	} {
		assert.Contains(t, sub53, marker,
			"stage-5 substep 5.3 must retain daemon-boundary marker %q", marker)
	}

	// The execution-rules must carry the boundary rule so the mechanic
	// survives substep rewrites.
	rules := stage05Slice(t, stage5, `<execution-rules>`, `</execution-rules>`)
	assert.Contains(t, rules, "NEVER idle indefinitely across it",
		"stage-5 execution-rules must forbid idling across the async daemon boundary")
	assert.Contains(t, rules, "VALIDATION-PENDING",
		"stage-5 execution-rules must name the resumable stop state")
}

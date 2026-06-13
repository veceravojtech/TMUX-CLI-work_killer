package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedCommands_ReferenceErrorReporting asserts that every orchestration
// command wires in a by-name reference to the shared <error-reporting>
// procedure. The reference (an <error-reporting> element in each command's
// <execution-rules>) supplies the literal `error-reporting` token the validate
// greps require, so a genuine harness/infra/spec defect hit inside a command is
// auto-reported via task-report instead of being silently swallowed.
func TestEmbeddedCommands_ReferenceErrorReporting(t *testing.T) {
	for _, rel := range []string{
		"execute.xml",
		"supervisor.xml",
		"plan.xml",
		"investigate.xml",
		"investigate-worker.xml",
		"task-plan-generate.xml",
		"task-plan-discover.xml",
		"rules/add.xml",
	} {
		t.Run(rel, func(t *testing.T) {
			content := readEmbeddedCommand(t, rel)
			assert.Contains(t, content, "error-reporting",
				"%s must reference the shared error-reporting procedure", rel)
		})
	}
}

// TestTaskReportXml_OwnsErrorReportingBlock asserts task-report.xml is the
// single source of truth for the autonomous-reporting procedure: it carries
// exactly one <error-reporting> block authoring all six mandatory parts. The 8
// commands above only reference it by name; the body lives here once.
func TestTaskReportXml_OwnsErrorReportingBlock(t *testing.T) {
	content := readEmbeddedCommand(t, "task-report.xml")

	require.Contains(t, content, "<error-reporting>",
		"task-report.xml must author the shared <error-reporting> block")

	for _, marker := range []string{
		"TRIGGER",        // fire ONLY on genuine harness/infra/spec defects
		"DELEGATION",     // compose+submit per this file's Step 3 tables
		"MODE-AWARENESS", // GOAL_MODE defers goal-failure to the daemon
		"DEDUP",          // dedup key = category+title+goal_id
		"RECURSION",      // never auto-report a failure of task-report itself
		"api.enabled",    // GATE inherited from Step 0
	} {
		assert.Contains(t, content, marker,
			"task-report.xml <error-reporting> block must define the %q part", marker)
	}

	// The body is authored exactly once: only one opening <error-reporting> tag.
	assert.Equal(t, 1, strings.Count(content, "<error-reporting>"),
		"task-report.xml must carry exactly one <error-reporting> block (single source of truth)")
}

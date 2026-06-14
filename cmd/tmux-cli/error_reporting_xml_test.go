package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exemptFromErrorReporting lists the embedded command XMLs (relative to
// embedded/commands/tmux) that are deliberately NOT required to carry their own
// <error-reporting> reference. The 21 task-plan-generate/step-*.xml shards load
// into the SAME worker context as their parent task-plan-generate.xml (which
// DOES carry the reference) and therefore inherit it — duplicating the element
// per-shard would only invite drift. The list is explicit per-path (NOT a
// `step-` prefix match) on purpose: a new shard is NOT auto-exempted, so it
// fails TestEmbeddedCommands_ReferenceErrorReporting until its author either
// wires the reference or opts the new shard into this list. A staleness
// assertion keeps every entry honest (each must map to a real walked file).
var exemptFromErrorReporting = map[string]bool{
	"feature/stage-0-capability.xml":                              true,
	"task-plan-generate/step-1-gate0.xml":                         true,
	"task-plan-generate/step-2-scaffold.xml":                      true,
	"task-plan-generate/step-3.14-domain.xml":                     true,
	"task-plan-generate/step-3.15-application.xml":                true,
	"task-plan-generate/step-3.16-infrastructure.xml":             true,
	"task-plan-generate/step-3.16a-auth-bootstrap.xml":            true,
	"task-plan-generate/step-3.17-fixtures.xml":                   true,
	"task-plan-generate/step-3.17.0-controller-path-resolver.xml": true,
	"task-plan-generate/step-3.18-controller-actions.xml":         true,
	"task-plan-generate/step-3.19-auth-flows.xml":                 true,
	"task-plan-generate/step-3.19a-seed-admin.xml":                true,
	"task-plan-generate/step-3.20-event-listeners.xml":            true,
	"task-plan-generate/step-3.21-error-handling.xml":             true,
	"task-plan-generate/step-3.22-middleware.xml":                 true,
	"task-plan-generate/step-3.23-api-docs.xml":                   true,
	"task-plan-generate/step-3.24-messenger.xml":                  true,
	"task-plan-generate/step-3.25-health-check.xml":               true,
	"task-plan-generate/step-3.26-docker.xml":                     true,
	"task-plan-generate/step-3.27-cicd.xml":                       true,
	"task-plan-generate/step-3.28-dx.xml":                         true,
	"task-plan-generate/step-3.29-final-gates.xml":                true,
}

// TestEmbeddedCommands_ReferenceErrorReporting asserts that every orchestration
// command wires in a by-name reference to the shared <error-reporting>
// procedure. The reference (an <error-reporting> element in each command's
// <execution-rules>) supplies the literal `error-reporting` token the validate
// greps require, so a genuine harness/infra/spec defect hit inside a command is
// auto-reported via task-report instead of being silently swallowed.
//
// The check is DERIVED: it walks the embedded FS and enforces the token on
// every .xml minus the explicit exemptFromErrorReporting allow-list, so a
// newly-added command is enforced automatically (no hardcoded per-file list to
// forget to update).
func TestEmbeddedCommands_ReferenceErrorReporting(t *testing.T) {
	const root = "embedded/commands/tmux"
	seenExempt := map[string]bool{}
	checked := 0

	err := fs.WalkDir(embeddedCommands, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".xml") {
			return nil
		}
		rel := strings.TrimPrefix(path, root+"/")
		if exemptFromErrorReporting[rel] {
			seenExempt[rel] = true
			return nil
		}
		checked++
		assert.Contains(t, readEmbeddedCommand(t, rel), "error-reporting",
			"%s must reference the shared error-reporting procedure", rel)
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, checked, "walk must enforce the token on at least one command")

	// Staleness: every allow-list entry must map to a real walked file, else
	// the exemption is dead (file removed/renamed) and should be cleaned up.
	for rel := range exemptFromErrorReporting {
		assert.True(t, seenExempt[rel], "stale exemption: %s is in exemptFromErrorReporting but no such file was walked", rel)
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

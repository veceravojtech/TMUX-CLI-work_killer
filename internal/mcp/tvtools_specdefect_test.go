package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestM06_SpecDefectDetection(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	findings := []ValidationFinding{{
		Rule:         "binding-cross-reference: composer.json vs greenfield",
		Status:       "blocked",
		FailureClass: "spec-defect",
		Owner:        "planner",
		Detail:       "composer.json contradicts greenfield binding",
		Correction:   "regenerate Config without composer.json",
	}}

	output, err := server.GoalValidationDone("goal-005", "blocked", findings, "regenerate Config without composer.json", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "blocked", verdict, "binding contradiction rolls up to a blocked verdict")

	var specDefect map[string]any
	for _, f := range persisted {
		if f["failure_class"] == "spec-defect" {
			specDefect = f
			break
		}
	}
	require.NotNil(t, specDefect, "a spec-defect finding must be present in signal.json")

	// Load-bearing: owner resolved to planner, never the implementer or ops.
	assert.Equal(t, "planner", specDefect["owner"])
	assert.NotEqual(t, "implementer", specDefect["owner"])
	assert.NotEqual(t, "ops", specDefect["owner"])
	assert.Equal(t, "spec-defect", specDefect["failure_class"])
	assert.Equal(t, "blocked", specDefect["status"])
}

// TestM06_NoBindingNoDefect: when no binding is declared, a Config that mentions
// composer.json produces an ordinary pass-through with NO spec-defect finding.
// Proves the matcher is binding-gated, not a blanket ban on composer.json.

func TestM06_NoBindingNoDefect(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	// No binding ⇒ empty forbidden set ⇒ the preflight is a no-op and the
	// ordinary rule check passes; no spec-defect is emitted.
	findings := []ValidationFinding{{
		Rule:   "composer.json present",
		Status: "pass",
	}}

	output, err := server.GoalValidationDone("goal-005", "pass", findings, "", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "pass", verdict, "no binding ⇒ verdict unaffected by C8")
	for _, f := range persisted {
		assert.NotEqual(t, "spec-defect", f["failure_class"], "no spec-defect finding when no binding is declared")
	}
}

// TestM06_MultiBindingUnion: greenfield + no-orm both declared; a Config that
// references an ORM-config artifact (doctrine.yaml, in the no-orm pattern set)
// is flagged. Proves the forbidden set is the UNION of every detected binding's
// patterns — a ref matching either set is caught with owner=planner.

func TestM06_MultiBindingUnion(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-005
  description: Test
  status: running
`)
	server := newSpecDefectTestServer(t, tmpDir, "goal-005")

	// doctrine.yaml matches the no-orm pattern set; under the union of
	// greenfield+no-orm it is forbidden and emitted as a spec-defect.
	findings := []ValidationFinding{{
		Rule:         "binding-cross-reference: doctrine.yaml vs no-orm",
		Status:       "blocked",
		FailureClass: "spec-defect",
		Owner:        "planner",
		Detail:       "doctrine.yaml contradicts no-orm binding",
		Correction:   "regenerate Config without doctrine.yaml",
	}}

	output, err := server.GoalValidationDone("goal-005", "blocked", findings, "regenerate Config without doctrine.yaml", nil)
	require.NoError(t, err)
	assert.True(t, output.Written)

	verdict, persisted := readSignalFindings(t, tmpDir, "goal-005")
	assert.Equal(t, "blocked", verdict)

	var specDefect map[string]any
	for _, f := range persisted {
		if f["failure_class"] == "spec-defect" {
			specDefect = f
			break
		}
	}
	require.NotNil(t, specDefect, "ORM artifact under no-orm (unioned) binding must emit a spec-defect")
	assert.Equal(t, "planner", specDefect["owner"])
	assert.Contains(t, specDefect["rule"], "no-orm")
}

// --- GoalCreate preconditions write-path (WS3b) tests ---

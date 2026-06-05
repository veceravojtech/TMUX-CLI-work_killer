package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvestigateXml_ValidatorSelfIdFromMarker guards the P1 validator self-id:
// now that validator windows are always namespaced (validator-<ns>), investigate.xml
// must resolve VALIDATOR_WID by reading the per-goal validator-window marker
// VERBATIM — never by guessing the bare name "validator" or probing the active
// window. The companion investigate.md glossary must reflect the same.
func TestInvestigateXml_ValidatorSelfIdFromMarker(t *testing.T) {
	xml := readEmbeddedCommand(t, "investigate.xml")

	assert.Contains(t, xml, "validator-window",
		"investigate.xml must resolve VALIDATOR_WID from the GOAL_DIR/validator-window marker")
	assert.Contains(t, xml, "VERBATIM",
		"the marker must be read verbatim, not synthesized")
	// The VALIDATOR_WID glossary term must no longer claim the window is always
	// the bare name "validator".
	assert.NotContains(t, xml, `always "validator"`,
		"investigate.xml must drop the stale 'always \"validator\"' self-id")

	md := readEmbeddedCommand(t, "investigate.md")
	assert.Contains(t, md, "validator-window",
		"investigate.md glossary must point VALIDATOR_WID at the validator-window marker")
	assert.NotContains(t, md, `always "validator"`,
		"investigate.md must drop the stale bare-name self-id")
}

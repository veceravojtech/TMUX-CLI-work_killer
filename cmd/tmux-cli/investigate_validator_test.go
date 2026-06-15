package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvestigateXML_BrokenGateNotCodeDefect locks RC-2: the inline validation
// path must classify a broken / non-executable gate command (exit 127/126,
// "command not found", "not executable") as a broken-gate / validator-error owned
// by OPS — NEVER a code-defect charged to the implementer. This mirrors the
// daemon's runner-missing handling for validate.sh (dispatch.go exit 127/126),
// closing the gap where an inline gate that could not run was blamed on the code.
func TestInvestigateXML_BrokenGateNotCodeDefect(t *testing.T) {
	low := strings.ToLower(readEmbeddedCommand(t, "investigate.xml"))
	assert.Contains(t, low, "broken-gate",
		"inline path must define a broken-gate failure class")
	assert.Contains(t, low, "command not found",
		"inline path must recognize the command-not-found broken-gate signature")
	assert.True(t, strings.Contains(low, "127") && strings.Contains(low, "126"),
		"inline path must recognize the exit-127/126 broken-gate signature")
}

// TestInvestigateWorkerXML_BrokenGateClass locks RC-2 in the shared
// classification block: a broken/non-executable command is owned by ops, never
// code-defect.
func TestInvestigateWorkerXML_BrokenGateClass(t *testing.T) {
	low := strings.ToLower(readEmbeddedCommand(t, "investigate-worker.xml"))
	assert.Contains(t, low, "broken-gate",
		"classification block must define the broken-gate class")
	assert.Contains(t, low, "command not found",
		"classification block must recognize the command-not-found signature")
}

// TestInvestigateXML_ValidatorAgency locks Fork-2(c): when an inline gate is
// broken/non-executable OR the goal lacks a usable functional check, the
// validator must SYNTHESIZE and RUN its own validation (resolve the real command
// from the goal config / topology, or run a functional probe itself) rather than
// passing vacuously or blaming the code.
func TestInvestigateXML_ValidatorAgency(t *testing.T) {
	low := strings.ToLower(readEmbeddedCommand(t, "investigate.xml"))
	assert.Contains(t, low, "validator agency",
		"investigate.xml must carry the validator-agency provision")
	assert.True(t, strings.Contains(low, "run its own validation") || strings.Contains(low, "synthesize"),
		"validator must run its own validation when a gate is broken or missing")
}

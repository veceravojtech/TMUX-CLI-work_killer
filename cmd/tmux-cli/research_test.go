package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResearchModeCmd_JSONSpawnOnNoFlags asserts the CLI sentinel wiring: with
// no flags, --implied-lines defaults to -1 (Measurable=false), so the command
// fails safe to spawn and emits it as JSON, exiting 0.
func TestResearchModeCmd_JSONSpawnOnNoFlags(t *testing.T) {
	// Reset shared flag vars to their registered defaults (guard against
	// test-order pollution from other cases mutating the package globals).
	researchModeNamedFiles = nil
	researchModeNamedSymbols = nil
	researchModeConcreteEdit = false
	researchModeImpliedLines = -1
	researchModeImpliedFiles = 0
	researchModeCandidateLOC = 0
	researchModeJSON = false

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"research-mode", "--json"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	require.NoError(t, rootCmd.Execute())

	var got struct {
		Mode    string `json:"mode"`
		Precise bool   `json:"precise"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "spawn", got.Mode)
	assert.False(t, got.Precise)
	assert.NotEmpty(t, got.Reason)
}

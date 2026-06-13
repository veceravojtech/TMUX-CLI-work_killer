package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteXml_MandatoryPreDoneValidationGate(t *testing.T) {
	content := readEmbeddedCommand(t, "execute.xml")

	// The goal's discriminator grep must match this content.
	re := regexp.MustCompile(`(?i)before .*DONE.*Validation Rules|MUST run the goal's .*Validation Rules`)
	require.True(t, re.MatchString(content),
		"execute.xml must carry a pre-DONE Validation Rules instruction")

	// Slice the new gate step and prove it is non-optional.
	idx := strings.Index(content, `title="Mandatory pre-DONE validation gate"`)
	require.NotEqual(t, -1, idx, "execute.xml must contain the mandatory pre-DONE validation gate step")
	start := strings.LastIndex(content[:idx], "<step")
	require.NotEqual(t, -1, start)
	end := strings.Index(content[start:], "</step>")
	require.NotEqual(t, -1, end)
	body := content[start : start+end]

	assert.NotContains(t, body, `optional="true"`, "the pre-DONE gate must NOT be optional")
	assert.NotContains(t, strings.ToLower(body), "advisory", "the pre-DONE gate must NOT be advisory")
	assert.Contains(t, body, "Validation Rules")
	assert.Contains(t, body, "exit", "the gate must check command exit status")

	// The pre-existing optional advisory (step 5, line 82) must survive.
	assert.Contains(t, content, `optional="true"`,
		"the existing optional advisory self-check must remain")
}

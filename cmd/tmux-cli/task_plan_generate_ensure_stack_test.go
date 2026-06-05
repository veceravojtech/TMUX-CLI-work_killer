package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_EnsureStackConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "ENSURE-STACK-CONV",
		"template must declare an ENSURE-STACK-CONV rule")
	assert.Contains(t, content, `id="ENSURE-STACK-CONV"`,
		"ENSURE-STACK-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `condition="HAS_DATABASE"`,
		"ENSURE-STACK-CONV must be conditioned on HAS_DATABASE")
}

func TestTemplate_EnsureStackAcceptanceSC17(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "test -x bin/ensure-test-stack.sh",
		"scaffold section must include SC-17 acceptance criterion for ensure-test-stack.sh")
	assert.Contains(t, scaffold, "SC-17",
		"scaffold section must reference SC-17 identifier")
}

func TestTemplate_EnsureStackSeparateValidateLine(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "separate validate line",
		"ENSURE-STACK-CONV rule must state the separate validate line invariant")
	assert.NotContains(t, content, "&& bash bin/ensure-test-stack.sh",
		"no &&-joined ensure-stack invocation may appear anywhere in the template")
}

func TestTemplate_EnsureStackBeforePlaywright(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.18.4"`, `n="3.18.5"`)
	assert.Contains(t, block, "bash bin/ensure-test-stack.sh",
		"controller action validate block must include ensure-test-stack")
	ensureIdx := strings.Index(block, "bash bin/ensure-test-stack.sh")
	playwrightIdx := strings.Index(block, "npx playwright")
	require.GreaterOrEqual(t, ensureIdx, 0, "ensure-test-stack.sh must be present")
	require.GreaterOrEqual(t, playwrightIdx, 0, "npx playwright must be present")
	assert.Less(t, ensureIdx, playwrightIdx,
		"ensure-test-stack.sh must appear BEFORE npx playwright in the validate block")
}

func TestTemplate_EnsureStackInFinalE2E(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.29.3"`, `n="3.29.4"`)
	assert.Contains(t, block, "bash bin/ensure-test-stack.sh",
		"E2E regression final gate must include ensure-test-stack")
}

func TestTemplate_EnsureStackInAuthFlow(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.19.5"`, `n="3.19.6"`)
	assert.Contains(t, block, "bash bin/ensure-test-stack.sh",
		"auth flow validate block must include ensure-test-stack before Playwright")
}

func TestTemplate_EnsureStackCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "ENSURE-STACK-CONV",
		"companion doc must mention ENSURE-STACK-CONV")
}

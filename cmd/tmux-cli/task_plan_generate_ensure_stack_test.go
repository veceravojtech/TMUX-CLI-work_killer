package main

import (
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

// The ENSURE-STACK-CONV convention still ships in the bundle (resolved from the
// rules catalogue); its per-goal validate APPLICATION moved to dispatch-time
// /tmux:elaborate (two-tier §5/§6). The &&-join prohibition is a property of the
// convention and must hold wherever ensure-stack appears.
func TestTemplate_EnsureStackSeparateValidateLine(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, `id="ENSURE-STACK-CONV"`,
		"the ENSURE-STACK-CONV convention must still be declared in the resolved bundle")
	assert.NotContains(t, content, "&& bash bin/ensure-test-stack.sh",
		"no &&-joined ensure-stack invocation may appear anywhere in the template")
}

// Two-tier: the ensure-stack-before-playwright validate ORDER is authored at
// dispatch by /tmux:elaborate against the real runtime — the 3.18 action shard is
// a roadmap skeleton and authors no validate commands at Tier-1.
func TestTemplate_EnsureStackBeforePlaywright(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.18"`, `n="3.19"`)
	assert.Contains(t, block, `<param name="status">roadmap`,
		"the action shard emits a roadmap skeleton")
	assert.NotContains(t, block, "bash bin/ensure-test-stack.sh",
		"ensure-test-stack ordering is a Tier-2 validate authored at dispatch, not inline in the generator")
}

// Two-tier: the E2E regression final gate is a roadmap skeleton; its ensure-stack
// validate is authored at dispatch by /tmux:elaborate.
func TestTemplate_EnsureStackInFinalE2E(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.29.3"`, `n="3.29.4"`)
	assert.Contains(t, block, `<param name="status">roadmap`,
		"the E2E regression final gate emits a roadmap skeleton")
	assert.NotContains(t, block, "bash bin/ensure-test-stack.sh",
		"the final-gate ensure-stack validate is authored at dispatch by /tmux:elaborate")
}

// Two-tier: the auth-flow ensure-stack-before-Playwright validate moved to
// dispatch-time /tmux:elaborate; the 3.19 auth shard is a roadmap skeleton.
func TestTemplate_EnsureStackInAuthFlow(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.19"`, `n="3.19a"`)
	assert.Contains(t, block, `<param name="status">roadmap`,
		"the auth shard emits a roadmap skeleton")
	assert.NotContains(t, block, "bash bin/ensure-test-stack.sh",
		"the auth-flow ensure-stack validate is authored at dispatch by /tmux:elaborate")
}

func TestTemplate_EnsureStackCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "ENSURE-STACK-CONV",
		"companion doc must mention ENSURE-STACK-CONV")
}

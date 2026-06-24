package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_E2eEnvConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "E2E-ENV-CONV",
		"template must declare an E2E-ENV-CONV rule")
	assert.Contains(t, content, `id="E2E-ENV-CONV"`,
		"E2E-ENV-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `condition="HAS_DATABASE"`,
		"E2E-ENV-CONV must be conditioned on HAS_DATABASE")
}

func TestTemplate_E2eEnvConvIsCritical(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, `critical="true" id="E2E-ENV-CONV"`,
		"E2E-ENV-CONV must have critical=\"true\" attribute")
}

func TestTemplate_E2eEnvConvReferencesEnsureStack(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "bin/ensure-test-stack.sh seeds (--env=test)",
		"E2E-ENV-CONV rule must reference ensure-test-stack.sh seeding with --env=test")
}

// Two-tier (director redesign §5): the HC-04 health-check acceptance criterion is
// authored at dispatch by /tmux:elaborate — the 3.25 health-check shard is a
// roadmap skeleton. The HC-04 convention itself still ships in the resolved
// bundle (the E2E-ENV-CONV rule from the rules catalogue).
func TestTemplate_E2eEnvConvHealthCheckHC04(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "HC-04",
		"the HC-04 convention must still ship in the resolved bundle (rules catalogue)")
	healthCheck := sliceBetween(t, content, `n="3.25"`, `n="3.26"`)
	assert.Contains(t, healthCheck, `<param name="status">roadmap`,
		"the health-check shard emits a roadmap skeleton")
	assert.Contains(t, healthCheck, `<param name="phase">health_check`,
		"the health-check skeleton carries phase=health_check")
	// the concrete HC-04 env/database acceptance + probe are authored at dispatch by
	// /tmux:elaborate — the skeleton authors no acceptance/validate param at Tier-1.
	assert.NotContains(t, healthCheck, `<param name="acceptance">`,
		"the health-check skeleton must not author an acceptance param (Tier-2 / elaborate)")
}

func TestTemplate_E2eEnvConvScaffoldSC18(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-18",
		"scaffold section must contain SC-18 criterion")
	assert.Contains(t, scaffold, "APP_ENV=test",
		"SC-18 must reference APP_ENV=test pinning")
}

// Two-tier: the jq -e env/database health-probe is a concrete validate command
// authored at dispatch by /tmux:elaborate; the 3.25 shard is a roadmap skeleton.
func TestTemplate_E2eEnvConvHealthProbeUsesJq(t *testing.T) {
	content := readGenerateBundle(t)
	healthCheck := sliceBetween(t, content, `n="3.25"`, `n="3.26"`)
	assert.Contains(t, healthCheck, `<param name="status">roadmap`,
		"the health-check shard emits a roadmap skeleton")
	assert.NotContains(t, healthCheck, `jq -e`,
		"the jq -e health probe is a Tier-2 validate authored at dispatch by /tmux:elaborate")
}

func TestTemplate_E2eEnvConvGate0ReAssertion(t *testing.T) {
	content := readGenerateBundle(t)
	gate0 := sliceBetween(t, content, `n="1.4"`, `n="1.45"`)
	assert.Contains(t, gate0, "ENV-E01",
		"Gate-0 validate section must contain ENV-E01 re-assertion")
	assert.Contains(t, gate0, "APP_ENV=test",
		"ENV-E01 must assert APP_ENV=test")
}

func TestTemplate_E2eEnvConvCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "E2E-ENV-CONV",
		"companion doc must mention E2E-ENV-CONV")
}

func TestTemplate_E2eEnvConvG7ExtensionSeam(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "Side-effect env keys (G7)",
		"E2E-ENV-CONV rule must contain G7 extension seam sentence")
}

func TestTemplate_E2eEnvConvSeparateValidateLine(t *testing.T) {
	content := readGenerateBundle(t)
	assert.NotContains(t, content, "&amp;&amp; grep -q 'APP_ENV=test'",
		"APP_ENV=test grep must not be &&-joined (XML-escaped)")
	assert.NotContains(t, content, "&& grep -q 'APP_ENV=test'",
		"APP_ENV=test grep must not be &&-joined (literal)")
}

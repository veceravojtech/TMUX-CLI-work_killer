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

func TestTemplate_E2eEnvConvHealthCheckHC04(t *testing.T) {
	content := readGenerateBundle(t)
	healthCheck := sliceBetween(t, content, `n="3.25"`, `n="3.26"`)
	assert.Contains(t, healthCheck, "HC-04",
		"health-check section must contain HC-04 criterion")
	assert.Contains(t, healthCheck, "env",
		"HC-04 must reference env field")
	assert.Contains(t, healthCheck, "database",
		"HC-04 must reference database field")
}

func TestTemplate_E2eEnvConvScaffoldSC18(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-18",
		"scaffold section must contain SC-18 criterion")
	assert.Contains(t, scaffold, "APP_ENV=test",
		"SC-18 must reference APP_ENV=test pinning")
}

func TestTemplate_E2eEnvConvHealthProbeUsesJq(t *testing.T) {
	content := readGenerateBundle(t)
	healthCheck := sliceBetween(t, content, `n="3.25"`, `n="3.26"`)
	assert.Contains(t, healthCheck, `jq -e`,
		"health-check section must contain jq -e probe command for env/database assertion")
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

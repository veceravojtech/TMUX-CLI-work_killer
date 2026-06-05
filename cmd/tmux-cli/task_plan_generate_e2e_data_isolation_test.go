package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_E2eDataIsolationConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "E2E-DATA-ISOLATION-CONV",
		"template must declare an E2E-DATA-ISOLATION-CONV rule")
	assert.Contains(t, content, `id="E2E-DATA-ISOLATION-CONV"`,
		"E2E-DATA-ISOLATION-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `condition="HAS_DATABASE"`,
		"E2E-DATA-ISOLATION-CONV must be conditioned on HAS_DATABASE")
}

func TestTemplate_E2eDataIsolationConvIsCritical(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, `critical="true" id="E2E-DATA-ISOLATION-CONV"`,
		"E2E-DATA-ISOLATION-CONV must have critical=\"true\" attribute")
}

func TestTemplate_E2eDataIsolationConvReadOnly(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "READ-ONLY reference data",
		"E2E-DATA-ISOLATION-CONV rule must declare fixtures as READ-ONLY reference data")
}

func TestTemplate_E2eDataIsolationConvUniqueKeys(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "unique keys",
		"E2E-DATA-ISOLATION-CONV rule must mandate unique keys for spec-created data")
	assert.Contains(t, content, "timestamp or UUID",
		"E2E-DATA-ISOLATION-CONV rule must specify timestamp or UUID suffix strategy")
}

func TestTemplate_E2eDataIsolationConvFilteredAssertions(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "filter",
		"E2E-DATA-ISOLATION-CONV rule must mandate filtered assertions")
	assert.Contains(t, content, "never assert raw totals",
		"E2E-DATA-ISOLATION-CONV rule must warn against raw total assertions")
}

func TestTemplate_E2eDataIsolationConvForbidsMidSuiteReset(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "mid-suite is FORBIDDEN",
		"E2E-DATA-ISOLATION-CONV rule must forbid mid-suite fixture reload")
	assert.Contains(t, content, "purge-and-reload",
		"E2E-DATA-ISOLATION-CONV rule must explain purge-and-reload consequence")
}

func TestTemplate_E2eDataIsolationConvWarnsDama(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "dama/doctrine-test-bundle",
		"E2E-DATA-ISOLATION-CONV rule must reference dama/doctrine-test-bundle")
	assert.Contains(t, content, "NOT apply to E2E",
		"E2E-DATA-ISOLATION-CONV rule must warn dama does NOT apply to E2E")
}

func TestTemplate_E2eDataIsolationConvCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "E2E-DATA-ISOLATION-CONV",
		"companion doc must mention E2E-DATA-ISOLATION-CONV")
}

func TestTemplate_E2eDataIsolationTestStrategy(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/_base/test-strategy.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "## E2E Data Isolation",
		"_base/test-strategy.md must contain E2E Data Isolation section")
	assert.Contains(t, content, "read-only reference data",
		"_base/test-strategy.md E2E Data Isolation section must reference read-only fixtures")
}

func TestTemplate_E2eDataIsolationFixturesMd(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/php-symfony/fixtures.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "## E2E Data Isolation",
		"php-symfony/fixtures.md must contain E2E Data Isolation section")
	assert.Contains(t, content, "dama/doctrine-test-bundle",
		"php-symfony/fixtures.md E2E Data Isolation section must warn about dama/doctrine-test-bundle")
}

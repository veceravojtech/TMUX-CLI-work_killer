package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_E2eSidefxConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "E2E-SIDEFX-CONV",
		"template must declare an E2E-SIDEFX-CONV rule")
	assert.Contains(t, content, `id="E2E-SIDEFX-CONV"`,
		"E2E-SIDEFX-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `condition="HAS_DATABASE"`,
		"E2E-SIDEFX-CONV must be conditioned on HAS_DATABASE")
}

func TestTemplate_E2eSidefxExtendsEnvConv(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "extends E2E-ENV-CONV",
		"E2E-SIDEFX-CONV rule must reference that it extends E2E-ENV-CONV")
}

func TestTemplate_E2eSidefxMailerAssert(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "MAILER_DSN=null://null",
		"E2E-SIDEFX-CONV must mention MAILER_DSN=null://null")
	assert.Contains(t, content, "recipe default",
		"E2E-SIDEFX-CONV must mention that null transport is the recipe default")
}

func TestTemplate_E2eSidefxMessengerSync(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "sync://",
		"E2E-SIDEFX-CONV must mention sync:// transport")
	assert.Contains(t, content, "config/packages/test/messenger.yaml",
		"E2E-SIDEFX-CONV must mention config/packages/test/messenger.yaml")
}

func TestTemplate_E2eSidefxApplicationOutcomes(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "application-visible outcomes",
		"E2E-SIDEFX-CONV must contain the application-visible outcomes constraint")
}

func TestTemplate_E2eSidefxScaffoldSC23(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-23",
		"step-2 scaffold must contain SC-23 acceptance criterion")
	assert.Contains(t, scaffold, "MAILER_DSN=null://null",
		"SC-23 must reference MAILER_DSN=null://null grep assertion")
}

func TestTemplate_E2eSidefxScaffoldSC24(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-24",
		"step-2 scaffold must contain SC-24 acceptance criterion")
	assert.Contains(t, scaffold, "messenger.yaml",
		"SC-24 must reference messenger.yaml config file")
}

func TestTemplate_E2eSidefxNoMailcatcher(t *testing.T) {
	content := readGenerateBundle(t)
	assert.False(t, strings.Contains(strings.ToLower(content), "mailcatcher"),
		"readGenerateBundle must not contain 'mailcatcher' (rejected alternative)")
	assert.False(t, strings.Contains(strings.ToLower(content), "mailpit"),
		"readGenerateBundle must not contain 'mailpit' (rejected alternative)")
}

func TestTemplate_E2eSidefxCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "E2E-SIDEFX-CONV",
		"companion doc must mention E2E-SIDEFX-CONV")
}

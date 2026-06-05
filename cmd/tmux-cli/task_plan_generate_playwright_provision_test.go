package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_PlaywrightProvisionSC19Present(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-19",
		"scaffold section must contain SC-19 identifier")
	assert.Contains(t, scaffold, "npx playwright install --with-deps chromium",
		"scaffold section must contain npx playwright install --with-deps chromium")
}

func TestTemplate_PlaywrightProvisionConditioned(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, `id="SC-19" condition="Playwright available"`,
		"SC-19 must have condition=\"Playwright available\" attribute")
}

func TestTemplate_PlaywrightProvisionValidateCmd(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, `source="SC-19" condition="Playwright available"`,
		"validate command must reference SC-19 with Playwright available condition")
	assert.Contains(t, scaffold, `>npx playwright install --with-deps chromium</cmd>`,
		"validate command must contain npx playwright install --with-deps chromium")
}

func TestTemplate_PlaywrightProvisionChromiumOnly(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "--with-deps chromium",
		"scaffold must contain --with-deps chromium qualifier")
	for _, line := range strings.Split(scaffold, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<cmd") && strings.Contains(trimmed, "playwright install") {
			require.Contains(t, trimmed, "chromium",
				"every playwright install <cmd> in scaffold must specify chromium — bare install downloads all browsers")
		}
	}
}

func TestTemplate_PlaywrightProvisionCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "SC-19",
		"companion doc must mention SC-19")
}

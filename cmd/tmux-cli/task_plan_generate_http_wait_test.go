package main

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_HttpWaitConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "HTTP-WAIT-CONV",
		"template must declare an HTTP-WAIT-CONV rule")
	assert.Contains(t, content, `id="HTTP-WAIT-CONV"`,
		"HTTP-WAIT-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `critical="true" id="HTTP-WAIT-CONV"`,
		"HTTP-WAIT-CONV must be critical")
}

func TestTemplate_HttpWaitContainsWaitFlag(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "--wait",
		"template must contain the docker compose --wait flag")
}

func TestTemplate_HttpWaitContainsBoundedPoll(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "until curl",
		"template must contain the canonical bounded poll form with 'until curl'")
}

func TestTemplate_NoFixedSleepBeforeCurl(t *testing.T) {
	re := regexp.MustCompile(`sleep [0-9]+ *(&&|&amp;&amp;) *curl`)

	for _, pair := range []struct {
		name string
		fsys fs.FS
	}{
		{"embeddedCommands", embeddedCommands},
		{"embeddedTemplates", embeddedTemplates},
	} {
		err := fs.WalkDir(pair.fsys, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, readErr := fs.ReadFile(pair.fsys, path)
			require.NoError(t, readErr, "reading %s/%s", pair.name, path)
			assert.NotRegexp(t, re, string(data),
				"fixed sleep before curl found in %s/%s — use docker compose --wait or bounded poll instead", pair.name, path)
			return nil
		})
		require.NoError(t, err, "walking %s", pair.name)
	}
}

func TestTemplate_HttpWaitCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "HTTP-WAIT-CONV",
		"companion doc must mention HTTP-WAIT-CONV")
}

func TestTemplate_HttpWaitEnvironmentGateBase(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/_base/environment-gate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "until curl",
		"_base environment-gate.md must document the canonical bounded poll form")
}

func TestTemplate_HttpWaitEnvironmentGateSymfony(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/php-symfony/environment-gate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "until",
		"php-symfony environment-gate.md must document a readiness polling pattern")
	assert.True(t,
		assert.ObjectsAreEqual(true, contains(content, "pg_isready")) ||
			assert.ObjectsAreEqual(true, contains(content, "curl")),
		"php-symfony environment-gate.md must document pg_isready or curl readiness polling")
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

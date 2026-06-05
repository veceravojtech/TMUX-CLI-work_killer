package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedTemplates_IncludesBaseTier guards against Go's embed directive
// silently dropping the `_base` tier. `//go:embed` excludes directories whose
// names begin with `_` unless the `all:` prefix is used, so without `all:` the
// `_base/*` templates are absent from the binary and every edit to them is inert.
func TestEmbeddedTemplates_IncludesBaseTier(t *testing.T) {
	content, err := fs.ReadFile(embeddedTemplates, "embedded/templates/_base/agents.md")
	require.NoError(t, err, "_base tier must be embedded (use //go:embed all:embedded/templates)")
	assert.NotEmpty(t, content, "_base/agents.md should have content")
}

func TestEmbeddedTemplates_PhpSymfonyFixturesContainsEnsureStack(t *testing.T) {
	content, err := fs.ReadFile(embeddedTemplates, "embedded/templates/php-symfony/fixtures.md")
	require.NoError(t, err, "php-symfony fixtures.md must be embedded")
	assert.Contains(t, string(content), "ensure-test-stack",
		"php-symfony fixtures.md must contain ensure-test-stack section")
}

// TestEmbeddedTemplates_BaseAndOverlayBothPresent walks the embedded FS and
// asserts both the `_base` (base) and `php-symfony` (overlay) tiers ship.
func TestEmbeddedTemplates_BaseAndOverlayBothPresent(t *testing.T) {
	var baseFound, overlayFound bool
	err := fs.WalkDir(embeddedTemplates, "embedded/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, "embedded/templates/_base/") {
			baseFound = true
		}
		if strings.HasPrefix(path, "embedded/templates/php-symfony/") {
			overlayFound = true
		}
		return nil
	})
	require.NoError(t, err)
	assert.True(t, baseFound, "expected an embedded/templates/_base/ entry (base tier embedded)")
	assert.True(t, overlayFound, "expected an embedded/templates/php-symfony/ entry (overlay tier embedded)")
}

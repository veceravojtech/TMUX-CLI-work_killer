package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhpSymfonyDiscoveryTriad locks the php-symfony aggregate-implementation
// triad capture into BOTH embedded templates. The discovery template must ASK
// the pattern (defaulting to the aggregate triad for the monorepo pack) and the
// domain-model output template must DESCRIBE each aggregate as a triad, so
// downstream generation emits triad specs (root + <X>Data DAO + readonly <X>DTO
// + <X>EventRecord events implementing <Aggregate>EventInterface + shared <X>Id
// under shared/src/Domain/<Module>) rather than classic aggregates.
//
// Tokens are matched by substring (the validate gate greps the raw files), so
// they must appear VERBATIM in each template. Mirrors the embed_templates_test.go
// golden-test style (fs.ReadFile through embeddedTemplates → require.NoError →
// assert.Contains).
func TestPhpSymfonyDiscoveryTriad(t *testing.T) {
	triadTokens := []string{
		"EventRecord",        // <X>EventRecord event snapshots
		"EventInterface",     // <Aggregate>EventInterface
		"Data",               // <X>Data DAO
		"DTO",                // readonly <X>DTO
		"shared/src/Domain/", // shared aggregate Id placement
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"discovery", "embedded/templates/php-symfony/discovery.md"},
		{"domain-model", "embedded/templates/php-symfony/domain-model.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := fs.ReadFile(embeddedTemplates, tc.path)
			require.NoError(t, err, "%s must be embedded", tc.path)
			body := string(content)
			for _, tok := range triadTokens {
				assert.Contains(t, body, tok,
					"%s must contain triad token %q so generation emits triad aggregates", tc.path, tok)
			}
		})
	}
}

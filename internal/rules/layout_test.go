package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLayout_IsGreenfieldSkeleton(t *testing.T) {
	assert.Equal(t, Layout{SourceRoots: []string{"src"}, InfraLayer: "Infrastructure"}, DefaultLayout())
}

func TestExpandGlob_NoTokenPassesThrough(t *testing.T) {
	// A glob with neither token is returned byte-unchanged under any Layout.
	got := ExpandGlob("templates/**/*.twig", Layout{SourceRoots: []string{"contexts/*/src"}, InfraLayer: "Bundle"})
	assert.Equal(t, []string{"templates/**/*.twig"}, got)
}

func TestExpandGlob_SrcMultiRoot(t *testing.T) {
	layout := Layout{SourceRoots: []string{"contexts/*/src", "projects/*/src"}, InfraLayer: "Bundle"}
	got := ExpandGlob("{src}/Domain/**/*.php", layout)
	assert.Equal(t, []string{
		"contexts/*/src/Domain/**/*.php",
		"projects/*/src/Domain/**/*.php",
	}, got)
}

func TestExpandGlob_InfraToken(t *testing.T) {
	layout := Layout{SourceRoots: []string{"contexts/*/src"}, InfraLayer: "Bundle"}
	got := ExpandGlob("{src}/{infra}/**/*.php", layout)
	assert.Equal(t, []string{"contexts/*/src/Bundle/**/*.php"}, got)
}

func TestExpandGlob_GreenfieldDefault(t *testing.T) {
	got := ExpandGlob("{src}/{infra}/**/*.php", DefaultLayout())
	assert.Equal(t, []string{"src/Infrastructure/**/*.php"}, got)
}

func TestExpandGlob_DedupesCollapsedRoots(t *testing.T) {
	// Two roots that produce the same glob (token absent after substitution)
	// must not emit a duplicate.
	layout := Layout{SourceRoots: []string{"contexts/*/src", "projects/*/src"}, InfraLayer: "Bundle"}
	got := ExpandGlob("templates/**/*.twig", layout)
	assert.Equal(t, []string{"templates/**/*.twig"}, got)
}

func TestResolveLayout_GreenfieldDefaultWithWarning(t *testing.T) {
	dir := t.TempDir()
	layout, warnings := ResolveLayout(dir)
	assert.Equal(t, DefaultLayout(), layout)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[len(warnings)-1], "discovery should ASK")
}

func TestResolveLayout_FromLayersDoc(t *testing.T) {
	dir := t.TempDir()
	docDir := filepath.Join(dir, "docs", "architecture")
	require.NoError(t, os.MkdirAll(docDir, 0o755))
	doc := `# Project Layout

## Layers

- Source roots: contexts/*/src, projects/*/src, packages/*/src
- Infrastructure layer: Bundle

## Other
unrelated
`
	require.NoError(t, os.WriteFile(filepath.Join(docDir, "layout.md"), []byte(doc), 0o644))

	layout, warnings := ResolveLayout(dir)
	assert.Equal(t, Layout{
		SourceRoots: []string{"contexts/*/src", "projects/*/src", "packages/*/src"},
		InfraLayer:  "Bundle",
	}, layout)
	assert.Empty(t, warnings, "a parsed layers doc must not emit the ASK warning")
}

func TestResolveLayout_FromComposerPSR4(t *testing.T) {
	dir := t.TempDir()
	composer := `{"autoload":{"psr-4":{"App\\":"src/"}}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "composer.json"), []byte(composer), 0o644))

	layout, warnings := ResolveLayout(dir)
	assert.Equal(t, []string{"src"}, layout.SourceRoots)
	assert.Equal(t, "Infrastructure", layout.InfraLayer)
	assert.Empty(t, warnings, "composer-resolved roots must not emit the ASK warning")
}

func TestExpandAppliesTo_BackwardCompatLiteral(t *testing.T) {
	// A rule whose applies_to carries no token is unchanged by expansion under
	// any Layout (byte-for-byte backward compatibility).
	in := []CodeRule{{ID: "LIT", AppliesTo: []string{"src/**/*.php"}}}
	for _, layout := range []Layout{
		DefaultLayout(),
		{SourceRoots: []string{"contexts/*/src", "projects/*/src"}, InfraLayer: "Bundle"},
	} {
		out := ExpandAppliesTo(in, layout)
		require.Len(t, out, 1)
		assert.Equal(t, []string{"src/**/*.php"}, out[0].AppliesTo)
	}
	// Caller slice must not be mutated.
	assert.Equal(t, []string{"src/**/*.php"}, in[0].AppliesTo)
}

// TestLayout_MonorepoMatchesPersistenceRules is THE regression test for the
// portability defect: code living outside top-level src/ with an infra layer
// named "Bundle" must produce a NON-EMPTY matched footprint once the tokenized
// rules are expanded against the discovery-resolved Layout. It exercises both
// gates — Match (plan injection) and Check (brownfield diff) — over the same
// monorepo tree.
func TestLayout_MonorepoMatchesPersistenceRules(t *testing.T) {
	files := []string{
		"contexts/orders/src/Bundle/Domain/DoctrineOrderRepository.php",
		"contexts/orders/src/Domain/Order.php",
		"projects/shop/src/Order/OrderRepository.php",
	}
	layout := Layout{
		SourceRoots: []string{"contexts/*/src", "projects/*/src", "packages/*/src"},
		InfraLayer:  "Bundle",
	}

	// PERS-shaped: the Bundle-globbed persistence rule + a Repository rule.
	// ARCH-shaped: a Domain-layer rule.
	tokenized := []CodeRule{
		{
			ID:           "PHP-PERS-001",
			Severity:     "must",
			ValidateKind: "review",
			Phase:        "domain",
			Acceptance:   []string{"GIVEN domain persistence THEN it uses mapped Doctrine entities"},
			AppliesTo:    []string{"{src}/{infra}/**/*.php", "{src}/**/*Repository.php"},
		},
		{
			ID:           "PHP-ARCH-003",
			Severity:     "should",
			ValidateKind: "review",
			Phase:        "domain",
			AppliesTo:    []string{"{src}/Domain/**/*.php"},
		},
	}

	expanded := ExpandAppliesTo(tokenized, layout)

	// The {infra} token resolved to the real "Bundle" directory name.
	assert.Contains(t, expanded[0].AppliesTo, "contexts/*/src/Bundle/**/*.php")

	// --- Match gate (plan injection) ---
	matchRes, _ := Match(expanded, files, "")
	require.NotEmpty(t, matchRes.Rules, "tokenized rules must match the monorepo footprint")
	var matchedIDs []string
	for _, r := range matchRes.Rules {
		matchedIDs = append(matchedIDs, r.ID)
	}
	assert.Contains(t, matchedIDs, "PHP-PERS-001",
		"the Bundle-globbed persistence rule must match the Bundle Repository file")

	// --- Check gate (brownfield diff) ---
	checkRes := Check(expanded, files, "", t.TempDir())
	require.NotEmpty(t, checkRes.Rules, "tokenized rules must produce a non-empty diff footprint")
	var bundleMatched bool
	for _, r := range checkRes.Rules {
		if r.ID == "PHP-PERS-001" {
			for _, m := range r.Matched {
				if m == "contexts/orders/src/Bundle/Domain/DoctrineOrderRepository.php" {
					bundleMatched = true
				}
			}
		}
	}
	assert.True(t, bundleMatched, "the Bundle file must appear in PHP-PERS-001's matched footprint")
}

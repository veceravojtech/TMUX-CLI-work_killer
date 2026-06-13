package main

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// conventionIDs are the planner conventions extracted from
// task-plan-generate.xml's <conventions> block. The golden contract: every id
// lives in exactly ONE catalogue file and never re-inlines into the spine.
var conventionIDs = []string{
	"CMD-CONV", "HTTP-CONV", "NODE-TOOL-CONV", "ENSURE-STACK-CONV",
	"HTTP-WAIT-CONV", "E2E-ARTIFACT-CONV", "DOCKER-RUNTIME-FRONTLOAD",
	"E2E-ENV-CONV", "E2E-SIDEFX-CONV", "E2E-DATA-ISOLATION-CONV",
	"E2E-AUTH-STATE-CONV",
}

func readEmbeddedRules(t *testing.T) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := fs.WalkDir(embeddedRules, "embedded/rules", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := embeddedRules.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files[strings.TrimPrefix(p, "embedded/rules/")] = string(data)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, files, "embedded rules catalogue must not be empty")
	return files
}

func TestRulesCatalogue_NoRuleLost(t *testing.T) {
	files := readEmbeddedRules(t)

	for _, id := range conventionIDs {
		needle := fmt.Sprintf(`id="%s"`, id)
		count := 0
		for name, content := range files {
			if strings.HasSuffix(name, ".md") && strings.Contains(content, needle) {
				count++
			}
		}
		assert.Equal(t, 1, count, "convention %s must live in exactly one catalogue file", id)
	}

	for _, element := range []string{"<scope-derivation>", "<validate-acceptance-mandate>"} {
		count := 0
		for _, content := range files {
			count += strings.Count(content, element)
		}
		assert.Equal(t, 1, count, "element %s must appear exactly once in the catalogue", element)
	}
}

func TestRulesCatalogue_SpineDoesNotReinline(t *testing.T) {
	spine, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err)
	for _, id := range conventionIDs {
		assert.NotContains(t, string(spine), fmt.Sprintf(`id="%s"`, id),
			"convention %s must not be re-inlined in the spine — it lives in embedded/rules/", id)
	}
	assert.Contains(t, string(spine), "tmux-cli rules resolve",
		"spine <conventions> block must instruct loading via rules resolve")
}

type manifestPack struct {
	ID          string         `yaml:"id"`
	When        map[string]any `yaml:"when"`
	Conventions []string       `yaml:"conventions"`
	CodeRules   []string       `yaml:"code_rules"`
}

func TestRulesCatalogue_ManifestIntegrity(t *testing.T) {
	files := readEmbeddedRules(t)

	var manifest struct {
		Version int            `yaml:"version"`
		Packs   []manifestPack `yaml:"packs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(files["manifest.yaml"]), &manifest))
	require.NotEmpty(t, manifest.Packs)

	referenced := map[string]bool{"manifest.yaml": true, "SCHEMA.md": true}
	for _, p := range manifest.Packs {
		for _, f := range append(append([]string{}, p.Conventions...), p.CodeRules...) {
			rel := path.Join(p.ID, f)
			assert.Contains(t, files, rel, "manifest references missing file %s", rel)
			assert.False(t, referenced[rel], "file %s referenced more than once", rel)
			referenced[rel] = true
		}
	}
	for name := range files {
		assert.True(t, referenced[name], "orphan file %s not referenced by manifest", name)
	}
}

// TestRulesCatalogue_Falsifiability ports the catalogue selftest to the shared
// rules.LintRuleSets contract: a validate that can never go red manufactures
// false confidence (the vacuous-gate lesson), so every embedded rule's checking
// contract is enforced at build time. The contract now lives in
// internal/rules/lint.go and is shared verbatim with `tmux-cli rules lint`;
// this test parses the embedded catalogue (bare-list YAML per file, skipping the
// manifest) and asserts the shared checker finds zero violations — so the guard
// stays exactly as strong and any embedded breach still fails here.
func TestRulesCatalogue_Falsifiability(t *testing.T) {
	files := readEmbeddedRules(t)

	var sets []rules.RuleSet
	for name, content := range files {
		if !strings.HasSuffix(name, ".yaml") || name == "manifest.yaml" {
			continue
		}
		var ruleSet []rules.CodeRule
		require.NoError(t, yaml.Unmarshal([]byte(content), &ruleSet), "%s must parse", name)
		require.NotEmpty(t, ruleSet, "%s must contain rules", name)
		sets = append(sets, rules.RuleSet{Source: name, Rules: ruleSet})
	}
	require.NotEmpty(t, sets, "catalogue must contain at least one code rule")

	findings := rules.LintRuleSets(sets)
	assert.Empty(t, findings,
		"embedded catalogue must satisfy the falsifiability contract: %v", findings)
}

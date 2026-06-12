package main

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

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

// codeRule mirrors the schema in embedded/rules/SCHEMA.md.
type codeRule struct {
	ID           string   `yaml:"id"`
	Category     string   `yaml:"category"`
	Scope        string   `yaml:"scope"`
	Severity     string   `yaml:"severity"`
	Title        string   `yaml:"title"`
	Rule         string   `yaml:"rule"`
	Why          string   `yaml:"why"`
	AppliesTo    []string `yaml:"applies_to"`
	Acceptance   []string `yaml:"acceptance"`
	Validate     []string `yaml:"validate"`
	ValidateKind string   `yaml:"validate_kind"`
	Phase        string   `yaml:"phase"`
	Signal       string   `yaml:"signal"`
	Examples     struct {
		Bad  string `yaml:"bad"`
		Good string `yaml:"good"`
	} `yaml:"examples"`
}

// weakOnly matches validate lines that may not be a rule's SOLE machine check
// (a green build the rule doesn't earn).
var weakOnly = regexp.MustCompile(`^\s*(vendor/bin/phpstan|make\s+stan|eslint)\b`)

// TestRulesCatalogue_Falsifiability ports the catalogue selftest: a validate
// that can never go red manufactures false confidence (the vacuous-gate
// lesson), so every rule's checking contract is enforced at build time.
func TestRulesCatalogue_Falsifiability(t *testing.T) {
	files := readEmbeddedRules(t)
	seen := map[string]string{}

	for name, content := range files {
		if !strings.HasSuffix(name, ".yaml") || name == "manifest.yaml" {
			continue
		}
		var ruleSet []codeRule
		require.NoError(t, yaml.Unmarshal([]byte(content), &ruleSet), "%s must parse", name)
		require.NotEmpty(t, ruleSet, "%s must contain rules", name)

		for _, r := range ruleSet {
			label := name + "/" + r.ID
			assert.NotEmpty(t, r.ID, "%s: rule missing id", name)
			if prev, dup := seen[r.ID]; dup {
				// The php-symfony pack stacks ON TOP of php — ids must stay
				// globally unique so goal references are unambiguous.
				t.Errorf("%s: id %s already defined in %s", name, r.ID, prev)
			}
			seen[r.ID] = name

			assert.NotEmpty(t, r.Category, "%s: missing category", label)
			assert.Contains(t, []string{"generic", "project"}, r.Scope, "%s: bad scope", label)
			assert.Contains(t, []string{"must", "should"}, r.Severity, "%s: bad severity", label)
			assert.NotEmpty(t, r.Title, "%s: missing title", label)
			assert.NotEmpty(t, r.Rule, "%s: missing rule", label)
			assert.NotEmpty(t, r.Why, "%s: missing why", label)
			assert.NotEmpty(t, r.AppliesTo, "%s: missing applies_to", label)
			assert.NotEmpty(t, r.Acceptance, "%s: missing acceptance", label)
			assert.NotEmpty(t, r.Validate, "%s: missing validate", label)
			assert.NotEmpty(t, r.Phase, "%s: missing phase", label)
			require.Contains(t, []string{"automated", "review", "mixed"}, r.ValidateKind,
				"%s: validate_kind must be automated|review|mixed", label)

			switch r.ValidateKind {
			case "review":
				for _, v := range r.Validate {
					assert.True(t, strings.HasPrefix(strings.TrimSpace(v), "review:"),
						"%s: review rule validate line must start with 'review:' (got %q)", label, v)
				}
			case "automated":
				require.NotEmpty(t, r.Signal, "%s: automated rule must carry signal", label)
				require.NotEmpty(t, r.Examples.Bad, "%s: automated rule must carry examples.bad", label)
				require.NotEmpty(t, r.Examples.Good, "%s: automated rule must carry examples.good", label)
			case "mixed":
				hasReview := false
				for _, v := range r.Validate {
					if strings.HasPrefix(strings.TrimSpace(v), "review:") {
						hasReview = true
					}
				}
				hasSignal := r.Signal != ""
				assert.True(t, hasReview || hasSignal,
					"%s: mixed rule needs review: lines or a signal", label)
			}

			if r.ValidateKind != "review" {
				allWeak := true
				for _, v := range r.Validate {
					if !weakOnly.MatchString(v) {
						allWeak = false
					}
				}
				assert.False(t, allWeak,
					"%s: machine-checked rule must not borrow a bare stan/eslint run as its only check", label)
			}

			if r.Signal != "" {
				re, err := regexp.Compile(r.Signal)
				require.NoError(t, err, "%s: signal must compile", label)
				require.NotEmpty(t, r.Examples.Bad, "%s: signal requires examples.bad", label)
				require.NotEmpty(t, r.Examples.Good, "%s: signal requires examples.good", label)
				assert.True(t, re.MatchString(r.Examples.Bad),
					"%s: signal must match its own bad example — the check cannot detect the violation it describes", label)
				assert.False(t, re.MatchString(r.Examples.Good),
					"%s: signal must NOT match the good example — it fires on compliant code", label)
			}
		}
	}
	require.NotEmpty(t, seen, "catalogue must contain at least one code rule")
}

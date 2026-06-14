package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shouldRule is a minimal severity=should CodeRule whose applies_to is the
// supplied globs — keeps the match-path tests terse.
func shouldRule(id string, applies ...string) CodeRule {
	return CodeRule{ID: id, Severity: "should", Phase: "application", AppliesTo: applies}
}

func TestRulesMatch_GlobDoubleStarMatchesNestedAndFlat(t *testing.T) {
	// `**` spans zero or more path segments (incl. `/`): src/**/*.php must
	// match both a deeply nested file AND a flat one directly under src/.
	assert.True(t, MatchGlob("src/**/*.php", "src/A/B/c.php"))
	assert.True(t, MatchGlob("src/**/*.php", "src/c.php"))
}

func TestRulesMatch_SingleStarDoesNotCrossSlash(t *testing.T) {
	assert.False(t, MatchGlob("src/*.php", "src/a/b.php"))
	assert.True(t, MatchGlob("src/*.php", "src/b.php"))
}

func TestRulesMatch_InvalidGlobSkippedWithWarning(t *testing.T) {
	// A malformed glob (unterminated char class) must not panic; the rule is
	// skipped and a warning surfaces.
	rules := []CodeRule{shouldRule("X", "src/[unterminated")}
	result, warns := Match(rules, []string{"src/a.php"}, "")
	assert.Empty(t, result.Rules)
	require.NotEmpty(t, warns)
	assert.Contains(t, warns[0], "X")
}

func TestRulesMatch_PhaseFilterIncludesMatching(t *testing.T) {
	rules := []CodeRule{
		{ID: "A", Severity: "should", Phase: "application", AppliesTo: []string{"src/**/*.php"}},
		{ID: "B", Severity: "should", Phase: "domain", AppliesTo: []string{"src/**/*.php"}},
	}
	result, _ := Match(rules, []string{"src/X.php"}, "application")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, "A", result.Rules[0].ID)
}

func TestRulesMatch_PhaseFilterSilentWhenNoRuleHasPhase(t *testing.T) {
	// Documents the allowedPhases mismatch risk: a --phase value no rule
	// carries yields an empty result with NO warning (silent), exit 0.
	rules := []CodeRule{shouldRule("A", "src/**/*.php")}
	result, warns := Match(rules, []string{"src/X.php"}, "no-such-phase")
	assert.Empty(t, result.Rules)
	assert.Empty(t, warns)
}

func TestRulesMatch_MustRuleEmitsCRAcceptanceLine(t *testing.T) {
	r := CodeRule{
		ID: "PHP-ARCH-002", Severity: "must", Phase: "application",
		AppliesTo:  []string{"src/**/*.php"},
		Acceptance: []string{"GIVEN a handler THEN it returns a DTO"},
	}
	result, _ := Match([]CodeRule{r}, []string{"src/Foo.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, "CR-PHP-ARCH-002: GIVEN a handler THEN it returns a DTO", result.Rules[0].AcceptanceLine)
}

func TestRulesMatch_ShouldRuleHasEmptyAcceptanceLine(t *testing.T) {
	r := CodeRule{
		ID: "X", Severity: "should", Phase: "application",
		AppliesTo:  []string{"src/**/*.php"},
		Acceptance: []string{"some acceptance text"},
	}
	result, _ := Match([]CodeRule{r}, []string{"src/Foo.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, "", result.Rules[0].AcceptanceLine)
}

func TestRulesMatch_SignalRuleRendersFailClosedValidateCmd(t *testing.T) {
	r := CodeRule{
		ID: "X", Severity: "should", Phase: "application",
		AppliesTo: []string{"src/**/*.php"},
		Signal:    `new \w+\(`,
	}
	result, _ := Match([]CodeRule{r}, []string{"src/A/BarHandler.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, `sh -c '! grep -rE "new \w+\(" src/A/BarHandler.php'`, result.Rules[0].ValidateCmd)
}

func TestRulesMatch_NoSignalNoValidateCmd(t *testing.T) {
	r := shouldRule("X", "src/**/*.php") // no signal
	result, _ := Match([]CodeRule{r}, []string{"src/Foo.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, "", result.Rules[0].ValidateCmd)
}

func TestRulesMatch_RunnableBaselineKeepsExit0or1(t *testing.T) {
	// A valid regex is runnable whether it matches examples.bad (grep exit 0)
	// or not (grep exit 1) — both keep the rule.
	hit := CodeRule{ID: "HIT", Severity: "should", Phase: "p", AppliesTo: []string{"*.php"}, Signal: "forbidden"}
	hit.Examples.Bad = "a forbidden call lives here"
	miss := CodeRule{ID: "MISS", Severity: "should", Phase: "p", AppliesTo: []string{"*.php"}, Signal: "forbidden"}
	miss.Examples.Bad = "totally compliant code"
	result, warns := Match([]CodeRule{hit, miss}, []string{"a.php"}, "")
	require.Len(t, result.Rules, 2)
	assert.Empty(t, warns)
}

func TestRulesMatch_RunnableBaselineDropsBadRegexExit2(t *testing.T) {
	// An uncompilable signal is the plan-time "exit 2 = bad pattern" case → drop.
	r := CodeRule{ID: "BAD", Severity: "should", Phase: "p", AppliesTo: []string{"*.php"}, Signal: "["}
	r.Examples.Bad = "anything"
	result, warns := Match([]CodeRule{r}, []string{"a.php"}, "")
	assert.Empty(t, result.Rules)
	require.NotEmpty(t, warns)
	assert.Contains(t, warns[0], "BAD")
	assert.Contains(t, warns[0], "not runnable")
}

func TestRulesMatch_JSONPayloadSchemaStable(t *testing.T) {
	// Pins the sibling-module contract: marshaled payload keys are EXACTLY
	// these seven, in any order.
	r := CodeRule{
		ID: "X", Severity: "must", ValidateKind: "automated", Phase: "application",
		AppliesTo: []string{"*.php"}, Acceptance: []string{"acc"}, Signal: "foo",
	}
	result, _ := Match([]CodeRule{r}, []string{"a.php"}, "")
	require.Len(t, result.Rules, 1)

	data, err := json.Marshal(result.Rules[0])
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"acceptance_line", "id", "paths", "phase", "severity", "validate_cmd", "validate_kind",
	}, keys)
}

func TestRulesMatch_NoFileMatchesEmptyRules(t *testing.T) {
	r := shouldRule("X", "src/*.go")
	result, warns := Match([]CodeRule{r}, []string{"src/a.php"}, "")
	assert.Empty(t, result.Rules)
	assert.Empty(t, warns)

	data, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{"rules":[],"warnings":[]}`, string(data))
}

func TestRulesMatch_NoRulesTreeReturnsEmpty(t *testing.T) {
	root := t.TempDir() // no .tmux-cli/rules at all
	loaded, err := LoadCodeRules(root, nil)
	require.NoError(t, err)
	assert.Empty(t, loaded)

	result, warns := Match(loaded, []string{"a.php"}, "")
	assert.Empty(t, result.Rules)
	assert.Empty(t, warns)
}

func TestRulesMatch_LoadCodeRulesPopulatesPathsFromSource(t *testing.T) {
	// LoadCodeRules parses each pack's bare top-level []CodeRule sequence and
	// stamps each rule with its source code-rules file path, which Match
	// surfaces verbatim as Paths.
	root := t.TempDir()
	dir := filepath.Join(root, RulesDir, "php")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	yamlBody := `- id: PHP-X-001
  category: architecture
  scope: generic
  severity: must
  title: t
  rule: r
  why: w
  applies_to: ["src/**/*.php"]
  acceptance: ["GIVEN x THEN y"]
  validate: ["review: check"]
  validate_kind: review
  phase: application
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "code-rules.yaml"), []byte(yamlBody), 0o644))

	resolved := []ResolvedFile{{Pack: "php", Kind: KindCodeRules, Path: ".tmux-cli/rules/php/code-rules.yaml"}}
	loaded, err := LoadCodeRules(root, resolved)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "PHP-X-001", loaded[0].ID)

	result, _ := Match(loaded, []string{"src/A/Foo.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, []string{".tmux-cli/rules/php/code-rules.yaml"}, result.Rules[0].Paths)
	assert.Equal(t, "CR-PHP-X-001: GIVEN x THEN y", result.Rules[0].AcceptanceLine)
}

func TestLoadCodeRules_BareSequencePack(t *testing.T) {
	// Every shipped pack is authored as a BARE top-level []CodeRule sequence
	// (no `rules:` wrapper) — the same shape resolve and the golden catalogue
	// test use. LoadCodeRules must decode that shape with no unmarshal error and
	// stamp each rule's source path.
	root := t.TempDir()
	dir := filepath.Join(root, RulesDir, "php")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	yamlBody := `- id: PHP-TYPE-001
  category: types
  scope: generic
  severity: must
  title: t
  rule: r
  why: w
  applies_to: ["src/**/*.php"]
  acceptance: ["GIVEN x THEN y"]
  validate: ["review: check"]
  validate_kind: review
  phase: application
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "code-rules.yaml"), []byte(yamlBody), 0o644))

	resolved := []ResolvedFile{{Pack: "php", Kind: KindCodeRules, Path: ".tmux-cli/rules/php/code-rules.yaml"}}
	loaded, err := LoadCodeRules(root, resolved)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "PHP-TYPE-001", loaded[0].ID)
	assert.Equal(t, ".tmux-cli/rules/php/code-rules.yaml", loaded[0].sourcePath)

	result, _ := Match(loaded, []string{"src/A/Foo.php"}, "")
	require.Len(t, result.Rules, 1)
	assert.Equal(t, []string{".tmux-cli/rules/php/code-rules.yaml"}, result.Rules[0].Paths)
}

func TestRulesMatch_LoadCodeRulesIgnoresConventions(t *testing.T) {
	// Only Kind==code-rules files are parsed; convention entries are skipped.
	root := t.TempDir()
	dir := filepath.Join(root, RulesDir, "php")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "code-rules.yaml"),
		[]byte("- id: ONLY\n  severity: should\n  phase: application\n  applies_to: [\"*.php\"]\n"), 0o644))
	resolved := []ResolvedFile{
		{Pack: "php", Kind: KindConvention, Path: ".tmux-cli/rules/php/conventions.md"},
		{Pack: "php", Kind: KindCodeRules, Path: ".tmux-cli/rules/php/code-rules.yaml"},
	}
	loaded, err := LoadCodeRules(root, resolved)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "ONLY", loaded[0].ID)
}

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validAutomatedRule returns a fully falsifiable automated rule: its signal
// compiles, matches examples.bad, and does NOT match examples.good. Tests break
// exactly one field to assert the corresponding finding.
func validAutomatedRule() CodeRule {
	r := CodeRule{
		ID:           "AUTO-1",
		Category:     "test",
		Scope:        "generic",
		Severity:     "must",
		Title:        "title",
		Rule:         "do the thing",
		Why:          "because it matters",
		AppliesTo:    []string{"**/*.go"},
		Acceptance:   []string{"Given x When y Then z"},
		Validate:     []string{"go vet ./..."},
		ValidateKind: "automated",
		Phase:        "implement",
		Signal:       "forbidden",
	}
	r.Examples.Bad = "this is forbidden"
	r.Examples.Good = "this is allowed"
	return r
}

// validReviewRule returns a clean review rule: every validate line is prefixed
// `review:` and it carries no signal.
func validReviewRule() CodeRule {
	return CodeRule{
		ID:           "REV-1",
		Category:     "test",
		Scope:        "generic",
		Severity:     "should",
		Title:        "title",
		Rule:         "a human checks the thing",
		Why:          "because it matters",
		AppliesTo:    []string{"**/*.go"},
		Acceptance:   []string{"Given x When y Then z"},
		Validate:     []string{"review: a human verifies X"},
		ValidateKind: "review",
		Phase:        "review",
	}
}

func findingMessages(fs []LintFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Source+" "+f.RuleID+": "+f.Message)
	}
	return out
}

func assertHasFinding(t *testing.T, findings []LintFinding, substr string) {
	t.Helper()
	for _, f := range findings {
		if strings.Contains(f.Message, substr) {
			return
		}
	}
	t.Errorf("expected a finding whose message contains %q, got: %v", substr, findingMessages(findings))
}

func TestRulesLint_CleanCatalogue(t *testing.T) {
	sets := []RuleSet{{Source: "a.yaml", Rules: []CodeRule{validAutomatedRule(), validReviewRule()}}}
	assert.Empty(t, LintRuleSets(sets), "fully falsifiable rules must produce no findings")
}

func TestRulesLint_DuplicateID(t *testing.T) {
	dup := validReviewRule()
	dup.ID = "AUTO-1" // collides with validAutomatedRule's id across sets
	sets := []RuleSet{
		{Source: "first.yaml", Rules: []CodeRule{validAutomatedRule()}},
		{Source: "second.yaml", Rules: []CodeRule{dup}},
	}
	findings := LintRuleSets(sets)
	require.NotEmpty(t, findings)
	found := false
	for _, f := range findings {
		if f.Source == "second.yaml" && strings.Contains(f.Message, "already defined in") &&
			strings.Contains(f.Message, "first.yaml") {
			found = true
		}
	}
	assert.True(t, found, "duplicate-id finding must cite both sources, got: %v", findingMessages(findings))
}

func TestRulesLint_MissingRequiredField(t *testing.T) {
	r := validAutomatedRule()
	r.Why = ""
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "why")
}

func TestRulesLint_BadScopeSeverityKind(t *testing.T) {
	r := validAutomatedRule()
	r.Scope = "x"
	r.Severity = "x"
	r.ValidateKind = "x"
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "scope")
	assertHasFinding(t, findings, "severity")
	assertHasFinding(t, findings, "validate_kind")
}

func TestRulesLint_AutomatedNeedsSignalAndExamples(t *testing.T) {
	r := validAutomatedRule()
	r.Signal = ""
	r.Examples.Bad = ""
	r.Examples.Good = ""
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	require.NotEmpty(t, findings)
	assertHasFinding(t, findings, "signal")
}

func TestRulesLint_SignalMustMatchBad(t *testing.T) {
	r := validAutomatedRule()
	r.Signal = "neverappears" // matches neither bad nor good
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "signal must match its own bad example")
}

func TestRulesLint_SignalMustNotMatchGood(t *testing.T) {
	r := validAutomatedRule()
	r.Signal = "this" // matches both "this is forbidden" and "this is allowed"
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "signal must NOT match the good example")
}

func TestRulesLint_SignalMustCompile(t *testing.T) {
	r := validAutomatedRule()
	r.Signal = "(unclosed" // invalid regexp
	var findings []LintFinding
	require.NotPanics(t, func() {
		findings = LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	})
	assertHasFinding(t, findings, "signal must compile")
}

func TestRulesLint_ReviewLinePrefix(t *testing.T) {
	r := validReviewRule()
	r.Validate = []string{"review: ok line", "make stan"} // second line not prefixed
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "review:")
}

func TestRulesLint_WeakOnly(t *testing.T) {
	r := validAutomatedRule()
	r.Validate = []string{"vendor/bin/phpstan"} // borrows a bare stan run as its only check
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "must not borrow")
}

func TestRulesLint_MixedNeedsReviewOrSignal(t *testing.T) {
	r := validAutomatedRule()
	r.ValidateKind = "mixed"
	r.Signal = ""
	r.Examples.Bad = ""
	r.Examples.Good = ""
	r.Validate = []string{"go test ./..."} // neither a review: line nor a signal
	findings := LintRuleSets([]RuleSet{{Source: "a.yaml", Rules: []CodeRule{r}}})
	assertHasFinding(t, findings, "mixed rule needs")
}

func TestRulesLint_LoadLocalMissingDir(t *testing.T) {
	dir := t.TempDir() // no .tmux-cli/rules/local/code-rules
	sets, findings := LoadLocalRuleSets(dir, false)
	assert.Empty(t, sets, "absent local tree yields zero sets")
	assert.Empty(t, findings, "absent local tree is legitimately clean")
}

func TestRulesLint_LoadLocalParsesBareList(t *testing.T) {
	dir := t.TempDir()
	crDir := filepath.Join(dir, RulesDir, "local", "code-rules")
	require.NoError(t, os.MkdirAll(crDir, 0o755))

	good := `- id: LOC-1
  category: local
  scope: project
  severity: should
  title: local rule
  rule: do the local thing
  why: because the project requires it
  applies_to:
    - "**/*.go"
  acceptance:
    - "Given x When y Then z"
  validate:
    - "go test ./..."
  validate_kind: automated
  phase: implement
  signal: "forbidden"
  examples:
    bad: "this is forbidden"
    good: "this is allowed"
`
	require.NoError(t, os.WriteFile(filepath.Join(crDir, "x.yaml"), []byte(good), 0o644))
	// A mapping where a bare list is expected → unmarshal error → parse finding.
	require.NoError(t, os.WriteFile(filepath.Join(crDir, "bad.yaml"), []byte("not: a list\n"), 0o644))

	sets, findings := LoadLocalRuleSets(dir, false)

	require.Len(t, sets, 1, "only the well-formed bare list parses into a set")
	assert.Equal(t, ".tmux-cli/rules/local/code-rules/x.yaml", sets[0].Source)
	require.Len(t, sets[0].Rules, 1)
	assert.Equal(t, "LOC-1", sets[0].Rules[0].ID)

	parseFinding := false
	for _, f := range findings {
		if strings.Contains(f.Source, "bad.yaml") && strings.Contains(f.Message, "parse error") {
			parseFinding = true
		}
	}
	assert.True(t, parseFinding, "malformed file must yield a parse-error finding, got: %v", findingMessages(findings))
}

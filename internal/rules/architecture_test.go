package rules

// architecture_test.go pins the architecture signal's asymmetric matching (an
// unknown signal admits only `architecture: ddd`, with a warning — the
// generator's default topology; "basic" must be declared) plus its Detect
// sources: the explicit Architecture: line, the on-disk DDD markers, and the
// unknown fallback. It also pins the SCHEMA.md local-override contract:
// LoadCodeRules replaces an embedded rule redefined by local/, and lint treats
// exactly that pairing (and only that pairing) as legitimate.

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mustParseRules(t *testing.T, body string) []CodeRule {
	t.Helper()
	var rs []CodeRule
	if err := yaml.Unmarshal([]byte(body), &rs); err != nil {
		t.Fatal(err)
	}
	return rs
}

func archCondition(v string) *Condition { return &Condition{Architecture: v} }

func TestMatchesArchitecture_KnownValues(t *testing.T) {
	for _, tc := range []struct {
		cond, sig string
		want      bool
	}{
		{"ddd", "ddd", true},
		{"basic", "basic", true},
		{"ddd", "basic", false},
		{"basic", "ddd", false},
	} {
		ok, warn := matches(archCondition(tc.cond), Signals{Architecture: tc.sig, NAuthFlows: -1})
		if ok != tc.want || warn != "" {
			t.Fatalf("cond=%s sig=%s: got ok=%v warn=%q, want ok=%v warn empty", tc.cond, tc.sig, ok, warn, tc.want)
		}
	}
}

func TestMatchesArchitecture_UnknownAdmitsOnlyDDDWithWarning(t *testing.T) {
	ok, warn := matches(archCondition("ddd"), Signals{NAuthFlows: -1})
	if !ok || !strings.Contains(warn, "architecture unknown") {
		t.Fatalf("unknown signal must match ddd conservatively with a warning; got ok=%v warn=%q", ok, warn)
	}
	ok, warn = matches(archCondition("basic"), Signals{NAuthFlows: -1})
	if ok || warn != "" {
		t.Fatalf("unknown signal must NOT match basic; got ok=%v warn=%q", ok, warn)
	}
}

func TestDetectArchitecture_ExplicitLineAuthoritative(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, filepath.Join("docs", "architecture", "test-environment.md"),
		"# Test Environment\n**Run Target:** local\n**Architecture:** basic (deliberate flat app)\n")
	if sig := Detect(root); sig.Architecture != "basic" {
		t.Fatalf("explicit Architecture line must win, got %q", sig.Architecture)
	}
}

func TestDetectArchitecture_DDDMarkers(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, filepath.Join("docs", "architecture", "bounded-contexts.md"),
		"## Bounded Context Inventory\n### Orders\n")
	if sig := Detect(root); sig.Architecture != "ddd" {
		t.Fatalf("bounded-contexts.md must derive ddd, got %q", sig.Architecture)
	}

	root2 := t.TempDir()
	writeProjectFile(t, root2, filepath.Join("contexts", "orders", "composer.json"), "{}")
	if sig := Detect(root2); sig.Architecture != "ddd" {
		t.Fatalf("contexts/*/composer.json must derive ddd, got %q", sig.Architecture)
	}
}

func TestDetectArchitecture_UnknownByDefault(t *testing.T) {
	if sig := Detect(t.TempDir()); sig.Architecture != "" {
		t.Fatalf("bare project must stay unknown, got %q", sig.Architecture)
	}
}

func TestDetectArchitecture_UnrecognizedValueIgnored(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, filepath.Join("docs", "architecture", "test-environment.md"),
		"**Architecture:** hexagonal\n")
	if sig := Detect(root); sig.Architecture != "" {
		t.Fatalf("unrecognized architecture value must stay unknown, got %q", sig.Architecture)
	}
}

// TestCheck_DashLeadingSignalRuns pins the `-e` pattern guard: a signal whose
// regex begins with a dash (`->persist…`, PHP-CTRL-003's shape) must run as a
// PATTERN, not be parsed as a grep option and silently dropped as exit-2
// non-runnable — the failure mode the stress fixture exposed.
func TestCheck_DashLeadingSignalRuns(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "src/OrderController.php", "<?php $this->entityManager->persist($order);\n")

	rules := []CodeRule{{
		ID: "DASH-1", Severity: "must", ValidateKind: "mixed", Phase: "application",
		AppliesTo: []string{"src/**/*.php"},
		Signal:    `->persist\s*\(|->flush\s*\(`,
		Examples: struct {
			Bad  string `yaml:"bad"`
			Good string `yaml:"good"`
		}{Bad: `$em->persist($x);`, Good: `$service->create($cmd);`},
	}}

	res := Check(rules, []string{"src/OrderController.php"}, "", root)
	if len(res.Warnings) != 0 {
		t.Fatalf("dash-leading signal must be runnable, got warnings: %v", res.Warnings)
	}
	if len(res.Rules) != 1 || !res.Rules[0].Violated {
		t.Fatalf("dash-leading signal must fire on the planted violation: %+v", res.Rules)
	}
}

const overrideBase = `- id: X-1
  category: c
  scope: generic
  severity: must
  title: embedded
  rule: r
  why: w
  applies_to: ["src/**/*.php"]
  acceptance: ["GIVEN x THEN y"]
  validate: ["review: x"]
  validate_kind: review
  phase: domain
`

const overrideLocal = `- id: X-1
  category: c
  scope: project
  severity: should
  title: local override
  rule: r2
  why: w2
  applies_to: ["src/**/*.php"]
  acceptance: ["GIVEN x THEN z"]
  validate: ["review: z"]
  validate_kind: review
  phase: domain
`

func TestLoadCodeRules_LocalOverrideWins(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, filepath.Join(RulesDir, "php", "code-rules.yaml"), overrideBase)
	writeProjectFile(t, root, filepath.Join(RulesDir, "local", "code-rules", "override.yaml"), overrideLocal)

	resolved := []ResolvedFile{
		{Pack: "php", Kind: KindCodeRules, Path: RulesDir + "/php/code-rules.yaml"},
		{Pack: "local", Kind: KindCodeRules, Path: RulesDir + "/local/code-rules/override.yaml"},
	}
	rules, err := LoadCodeRules(root, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("duplicate id must collapse to ONE rule (local wins), got %d", len(rules))
	}
	r := rules[0]
	if r.Title != "local override" || r.Severity != "should" || !strings.Contains(r.sourcePath, "local/code-rules") {
		t.Fatalf("the LOCAL definition must win: got title=%q severity=%q source=%q", r.Title, r.Severity, r.sourcePath)
	}
}

func TestLintRuleSets_LocalOverrideNotADuplicate(t *testing.T) {
	embedded := RuleSet{Source: "php/code-rules.yaml", Rules: mustParseRules(t, overrideBase)}
	local := RuleSet{Source: ".tmux-cli/rules/local/code-rules/override.yaml", Rules: mustParseRules(t, overrideLocal)}

	if findings := LintRuleSets([]RuleSet{embedded, local}); len(findings) != 0 {
		t.Fatalf("embedded id redefined by local/ is an override, not a duplicate: %v", findings)
	}
	// Order-independent: lint may load local sets before embedded ones.
	if findings := LintRuleSets([]RuleSet{local, embedded}); len(findings) != 0 {
		t.Fatalf("override exemption must not depend on set order: %v", findings)
	}
}

func TestLintRuleSets_DuplicateWithinLocalStillFlagged(t *testing.T) {
	a := RuleSet{Source: ".tmux-cli/rules/local/code-rules/a.yaml", Rules: mustParseRules(t, overrideLocal)}
	b := RuleSet{Source: ".tmux-cli/rules/local/code-rules/b.yaml", Rules: mustParseRules(t, overrideLocal)}
	if findings := LintRuleSets([]RuleSet{a, b}); len(findings) == 0 {
		t.Fatal("two LOCAL definitions of one id are ambiguous and must be flagged")
	}
}

func TestLintRuleSets_DuplicateWithinEmbeddedStillFlagged(t *testing.T) {
	a := RuleSet{Source: "php/code-rules.yaml", Rules: mustParseRules(t, overrideBase)}
	b := RuleSet{Source: "php-symfony/architecture-ddd.yaml", Rules: mustParseRules(t, overrideBase)}
	if findings := LintRuleSets([]RuleSet{a, b}); len(findings) == 0 {
		t.Fatal("two EMBEDDED definitions of one id are ambiguous and must be flagged")
	}
}

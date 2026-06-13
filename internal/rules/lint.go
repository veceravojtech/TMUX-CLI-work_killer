package rules

// lint.go owns the code-rules FALSIFIABILITY contract: the single source of
// truth shared by the embedded-catalogue selftest
// (cmd/tmux-cli/rules_catalogue_test.go) and the `tmux-cli rules lint` CLI
// subcommand. The contract is a PURE function over parsed []CodeRule — a
// validate that can never go red manufactures false confidence (the
// vacuous-gate lesson), so every rule's checking contract is enforced here.
//
// Parsing stays with each caller (the selftest reads embed.FS; the CLI reads
// disk) because the embedded catalogue and on-disk local files are both bare
// `[]CodeRule` lists reached via different mechanisms. The checker COLLECTS
// every violation as a finding (rather than failing fatally) so the caller sees
// all breaches at once and the gate is reported, not just the first failure.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LintFinding is one falsifiability-contract violation. Source is the rule set's
// label (project-relative path for disk loads; the caller's chosen label for
// the embedded selftest); RuleID is the offending rule's id (empty for
// file-level parse errors or a rule missing its id); Message is the breach.
type LintFinding struct {
	Source  string `json:"source"`
	RuleID  string `json:"rule_id"`
	Message string `json:"message"`
}

// RuleSet is the bare `[]CodeRule` parsed from one source file, tagged with the
// label used in findings.
type RuleSet struct {
	Source string
	Rules  []CodeRule
}

// weakOnly matches validate lines that may not be a rule's SOLE machine check
// (a green build the rule doesn't earn). Moved verbatim from the catalogue
// selftest so the embedded guard and the CLI share one definition.
var weakOnly = regexp.MustCompile(`^\s*(vendor/bin/phpstan|make\s+stan|eslint)\b`)

// LintRuleSets runs the full per-rule falsifiability contract over every rule in
// every set and returns one finding per violation (empty slice = clean). It is
// PURE: no I/O, no *testing.T. The contract:
//
//   - schema completeness: id, category, scope∈{generic,project},
//     severity∈{must,should}, title, rule, why, applies_to, acceptance,
//     validate, phase, validate_kind∈{automated,review,mixed};
//   - GLOBAL id-uniqueness across all sets (a `seen` map; a goal reference must
//     be unambiguous);
//   - validate_kind dispatch: review → every validate line prefixed `review:`;
//     automated → non-empty signal + examples.bad + examples.good; mixed → a
//     `review:` line OR a signal;
//   - machine-checked (≠review) rules: not ALL validate lines are weakOnly;
//   - signal (when non-empty): compiles, matches examples.bad, does NOT match
//     examples.good. A non-compiling signal records a finding and SKIPS the
//     match sub-checks (you cannot run a regex that did not compile) — no panic.
func LintRuleSets(sets []RuleSet) []LintFinding {
	var findings []LintFinding
	seen := map[string]string{} // rule id -> source it was first defined in

	for _, set := range sets {
		for _, r := range set.Rules {
			add := func(msg string) {
				findings = append(findings, LintFinding{Source: set.Source, RuleID: r.ID, Message: msg})
			}

			// Schema completeness + global id-uniqueness.
			switch {
			case r.ID == "":
				add("missing id")
			default:
				if prev, dup := seen[r.ID]; dup {
					add(fmt.Sprintf("id %s already defined in %s", r.ID, prev))
				} else {
					seen[r.ID] = set.Source
				}
			}

			if r.Category == "" {
				add("missing category")
			}
			if r.Scope != "generic" && r.Scope != "project" {
				add(fmt.Sprintf("scope must be generic|project (got %q)", r.Scope))
			}
			if r.Severity != "must" && r.Severity != "should" {
				add(fmt.Sprintf("severity must be must|should (got %q)", r.Severity))
			}
			if r.Title == "" {
				add("missing title")
			}
			if r.Rule == "" {
				add("missing rule")
			}
			if r.Why == "" {
				add("missing why")
			}
			if len(r.AppliesTo) == 0 {
				add("missing applies_to")
			}
			if len(r.Acceptance) == 0 {
				add("missing acceptance")
			}
			if len(r.Validate) == 0 {
				add("missing validate")
			}
			if r.Phase == "" {
				add("missing phase")
			}
			if r.ValidateKind != "automated" && r.ValidateKind != "review" && r.ValidateKind != "mixed" {
				add(fmt.Sprintf("validate_kind must be automated|review|mixed (got %q)", r.ValidateKind))
			}

			// validate_kind dispatch.
			switch r.ValidateKind {
			case "review":
				for _, v := range r.Validate {
					if !strings.HasPrefix(strings.TrimSpace(v), "review:") {
						add(fmt.Sprintf("review rule validate line must start with 'review:' (got %q)", v))
					}
				}
			case "automated":
				if r.Signal == "" {
					add("automated rule must carry a signal")
				}
				if r.Examples.Bad == "" {
					add("automated rule must carry examples.bad")
				}
				if r.Examples.Good == "" {
					add("automated rule must carry examples.good")
				}
			case "mixed":
				hasReview := false
				for _, v := range r.Validate {
					if strings.HasPrefix(strings.TrimSpace(v), "review:") {
						hasReview = true
						break
					}
				}
				if !hasReview && r.Signal == "" {
					add("mixed rule needs a review: validate line or a signal")
				}
			}

			// Machine-checked rules must not borrow a bare stan/eslint run as
			// their ONLY check (an empty validate is flagged above, not here).
			if r.ValidateKind != "review" && len(r.Validate) > 0 {
				allWeak := true
				for _, v := range r.Validate {
					if !weakOnly.MatchString(v) {
						allWeak = false
						break
					}
				}
				if allWeak {
					add("machine-checked rule must not borrow a bare stan/eslint run as its only check")
				}
			}

			// Signal falsifiability: compiles, fires on its own bad example,
			// stays silent on the good one.
			if r.Signal != "" {
				re, err := regexp.Compile(r.Signal)
				if err != nil {
					add(fmt.Sprintf("signal must compile: %v", err))
					continue // cannot run a regex that did not compile — skip match sub-checks
				}
				if r.Examples.Bad == "" {
					add("signal requires examples.bad")
				} else if !re.MatchString(r.Examples.Bad) {
					add("signal must match its own bad example — the check cannot detect the violation it describes")
				}
				if r.Examples.Good == "" {
					add("signal requires examples.good")
				} else if re.MatchString(r.Examples.Good) {
					add("signal must NOT match the good example — it fires on compliant code")
				}
			}
		}
	}
	return findings
}

// LoadLocalRuleSets reads project-local code-rules as bare `[]CodeRule` RuleSets
// for linting. It always reads `.tmux-cli/rules/local/code-rules/*.yaml`; with
// includeEmbedded it ALSO reads materialized pack rules (`<pack>/*.yaml` under
// `.tmux-cli/rules/`, excluding manifest.yaml and the local/ subtree). Missing
// directories yield zero sets and no findings (an absent local tree is
// legitimately clean). A read/YAML error on a file yields a parse-error
// LintFinding and that file is skipped; sibling files keep loading. Source
// labels are project-relative slash paths.
//
// NOTE: parsing is the bare-list schema — it MUST NOT route through
// LoadCodeRules, which expects a `{rules: [...]}` wrapper.
func LoadLocalRuleSets(projectRoot string, includeEmbedded bool) ([]RuleSet, []LintFinding) {
	var sets []RuleSet
	var findings []LintFinding

	localDir := filepath.Join(projectRoot, RulesDir, "local", "code-rules")
	ls, lf := loadBareRuleDir(projectRoot, localDir)
	sets = append(sets, ls...)
	findings = append(findings, lf...)

	if includeEmbedded {
		es, ef := loadEmbeddedPackRuleSets(projectRoot)
		sets = append(sets, es...)
		findings = append(findings, ef...)
	}

	return sets, findings
}

// loadBareRuleDir parses every `*.yaml` (non-recursive, non-dotfile) in dir as a
// bare `[]CodeRule`. A missing dir is clean (zero sets, no findings).
func loadBareRuleDir(projectRoot, dir string) ([]RuleSet, []LintFinding) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // missing/unreadable dir → treated as absent (clean)
	}
	var sets []RuleSet
	var findings []LintFinding
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		set, fnd := loadBareRuleFile(projectRoot, filepath.Join(dir, e.Name()))
		if set != nil {
			sets = append(sets, *set)
		}
		findings = append(findings, fnd...)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Source < sets[j].Source })
	return sets, findings
}

// loadEmbeddedPackRuleSets walks the materialized rules tree and parses each
// pack `*.yaml` as a bare list, excluding manifest.yaml and the local/ subtree
// (handled separately). A missing tree yields nothing (only local is linted).
func loadEmbeddedPackRuleSets(projectRoot string) ([]RuleSet, []LintFinding) {
	root := filepath.Join(projectRoot, RulesDir)
	localPrefix := filepath.Join(root, "local") + string(os.PathSeparator)

	var sets []RuleSet
	var findings []LintFinding

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // missing tree / walk error → only local
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		if filepath.Base(p) == "manifest.yaml" || strings.HasPrefix(p, localPrefix) {
			return nil
		}
		set, fnd := loadBareRuleFile(projectRoot, p)
		if set != nil {
			sets = append(sets, *set)
		}
		findings = append(findings, fnd...)
		return nil
	})

	sort.Slice(sets, func(i, j int) bool { return sets[i].Source < sets[j].Source })
	return sets, findings
}

// loadBareRuleFile reads one bare-list YAML file into a RuleSet. A read/parse
// error yields a parse-error finding and a nil set (caller skips the file).
func loadBareRuleFile(projectRoot, absPath string) (*RuleSet, []LintFinding) {
	label := sourceLabel(projectRoot, absPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, []LintFinding{{Source: label, Message: fmt.Sprintf("parse error: %v", err)}}
	}
	var rs []CodeRule
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, []LintFinding{{Source: label, Message: fmt.Sprintf("parse error: %v", err)}}
	}
	return &RuleSet{Source: label, Rules: rs}, nil
}

// sourceLabel renders a finding-friendly project-relative slash path.
func sourceLabel(projectRoot, absPath string) string {
	if rel, err := filepath.Rel(projectRoot, absPath); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

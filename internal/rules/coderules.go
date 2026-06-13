package rules

// coderules.go owns the code-rules half of the resolver: loading the
// per-project catalogue, glob-matching rules to changed files, and rendering
// the deterministic per-rule injection payloads the sibling planner module
// copies verbatim (design §6.4 determinism boundary — Go owns ALL glob,
// severity, and signal routing; nothing downstream re-matches or re-renders).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// CodeRule is the exported mirror of the catalogue rule schema
// (embedded/rules/SCHEMA.md; field set pinned by the codeRule literal in
// cmd/tmux-cli/rules_catalogue_test.go). sourcePath is internal provenance set
// by LoadCodeRules — it is not a YAML field.
type CodeRule struct {
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

	sourcePath string // resolved code-rules file this rule was loaded from
}

// RulePayload is one fully-rendered rule injection. Every field is pre-computed
// here so the consuming planner copies it verbatim — it never re-globs,
// re-derives severity, or re-renders a command.
type RulePayload struct {
	ID             string   `json:"id"`
	Severity       string   `json:"severity"`
	ValidateKind   string   `json:"validate_kind"`
	Phase          string   `json:"phase"`
	Paths          []string `json:"paths"`
	AcceptanceLine string   `json:"acceptance_line"`
	ValidateCmd    string   `json:"validate_cmd"`
}

// MatchResult is the full `rules match` output: the matched payloads plus any
// human-readable warnings (conservative inclusions, dropped rules, bad globs).
type MatchResult struct {
	Rules    []RulePayload `json:"rules"`
	Warnings []string      `json:"warnings"`
}

// LoadCodeRules reads every Kind==code-rules file from resolved (in resolve
// order, so local/ overrides stay last), unmarshals each `{rules: [...]}`
// wrapper, and concatenates the rules, stamping each with its source path.
func LoadCodeRules(projectRoot string, resolved []ResolvedFile) ([]CodeRule, error) {
	var out []CodeRule
	for _, f := range resolved {
		if f.Kind != KindCodeRules {
			continue
		}
		data, err := os.ReadFile(filepath.Join(projectRoot, f.Path))
		if err != nil {
			return nil, fmt.Errorf("code-rules file unreadable: %s: %w", f.Path, err)
		}
		var wrapper struct {
			Rules []CodeRule `yaml:"rules"`
		}
		if err := yaml.Unmarshal(data, &wrapper); err != nil {
			return nil, fmt.Errorf("code-rules file invalid: %s: %w", f.Path, err)
		}
		for i := range wrapper.Rules {
			wrapper.Rules[i].sourcePath = f.Path
		}
		out = append(out, wrapper.Rules...)
	}
	return out, nil
}

// compileGlob translates an anchored glob into a regexp. Translation:
//   - `**` (absorbing a following `/`) → `(?:.*/)?` so `**/` spans zero or
//     more path segments; a bare `**` → `.*`.
//   - `*` → `[^/]*` (never crosses a path separator).
//   - `?` → `[^/]`.
//   - `[` / `]` pass through as a regex char class — an unbalanced class makes
//     compilation fail, which is exactly how a malformed glob is detected.
//   - every other byte is regex-escaped (literal).
//
// No doublestar dependency: filepath.Match cannot express `**`, so we roll the
// translator and pin its behavior with tests.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i += 2
				if i < len(pattern) && pattern[i] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
			i++
		case '?':
			b.WriteString("[^/]")
			i++
		case '[', ']':
			b.WriteByte(c)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// MatchGlob reports whether path matches the glob pattern. A malformed pattern
// matches nothing (callers that need to warn use compileGlob directly).
func MatchGlob(pattern, path string) bool {
	re, err := compileGlob(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

// Match routes rules to files and renders the injection payloads. For each
// rule: skip on phase mismatch; match files against applies_to globs (a
// malformed glob is dropped with a warning); skip rules nothing matched. A
// `must` rule gets a `CR-<id>: <first acceptance>` line; a rule carrying a
// signal is runnable-gated then rendered as the fail-closed negated grep over
// its matched footprint, or dropped with a warning when the signal is not
// runnable. The second return is the same warnings slice for callers that want
// it directly.
func Match(rules []CodeRule, files []string, phase string) (MatchResult, []string) {
	result := MatchResult{Rules: []RulePayload{}, Warnings: []string{}}

	for _, r := range rules {
		if phase != "" && r.Phase != phase {
			continue
		}

		// Compile each applies_to glob once; warn (once) on a bad pattern.
		var compiled []*regexp.Regexp
		for _, pat := range r.AppliesTo {
			re, err := compileGlob(pat)
			if err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("rule %s: invalid glob %q skipped", r.ID, pat))
				continue
			}
			compiled = append(compiled, re)
		}

		var matched []string
		for _, f := range files {
			for _, re := range compiled {
				if re.MatchString(f) {
					matched = append(matched, f)
					break
				}
			}
		}
		if len(matched) == 0 {
			continue
		}

		if r.Severity == "must" && len(r.Acceptance) == 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("rule %s: must rule has empty acceptance — acceptance_line blank", r.ID))
		}

		validateCmd := ""
		if r.Signal != "" {
			if !runnable(r.Signal, r.Examples.Bad) {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("dropped %s: signal not runnable (exit 2)", r.ID))
				continue
			}
			validateCmd = RenderValidateCmd(r.Signal, matched)
		}

		paths := []string{}
		if r.sourcePath != "" {
			paths = append(paths, r.sourcePath)
		}

		result.Rules = append(result.Rules, RulePayload{
			ID:             r.ID,
			Severity:       r.Severity,
			ValidateKind:   r.ValidateKind,
			Phase:          r.Phase,
			Paths:          paths,
			AcceptanceLine: RenderAcceptanceLine(r),
			ValidateCmd:    validateCmd,
		})
	}

	return result, result.Warnings
}

// RenderAcceptanceLine returns `CR-<id>: <first acceptance>` for a `must` rule
// with at least one acceptance entry, else "" (the key is always present;
// `should` rules and empty-acceptance rules carry no line).
func RenderAcceptanceLine(r CodeRule) string {
	if r.Severity != "must" || len(r.Acceptance) == 0 {
		return ""
	}
	return "CR-" + r.ID + ": " + r.Acceptance[0]
}

// RenderValidateCmd renders the fail-closed validate command: a NEGATED
// recursive grep over the matched footprint. grep matching a forbidden pattern
// exits 0 → `!` flips it to a non-zero (failed) check, so a violation fails the
// gate. The runnable-ness baseline must NEVER be run against this negated form
// (its `!` would mask a grep exit-2 as success).
func RenderValidateCmd(signal string, files []string) string {
	return `sh -c '! grep -rE "` + signal + `" ` + strings.Join(files, " ") + `'`
}

// runnable is the plan-time gate separating "bad regex" (drop) from "file not
// written yet" (greenfield, blessed-green at daemon time). It runs the BARE
// grep against the rule's own examples.bad — a guaranteed-present input — so
// exit 2 can only mean a malformed pattern. The signal must compile under Go's
// regexp; when examples.bad is present the bare `grep -E` over it must exit 0
// (match) or 1 (no match); exit 2 (or a compile failure) → not runnable.
func runnable(signal, examplesBad string) bool {
	if _, err := regexp.Compile(signal); err != nil {
		return false
	}
	if examplesBad == "" {
		return true // compile success is the whole gate when there is no example
	}
	cmd := exec.Command("grep", "-E", signal)
	cmd.Stdin = strings.NewReader(examplesBad)
	err := cmd.Run()
	if err == nil {
		return true // exit 0: matched
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode() == 1 // 1 = no match (runnable); 2 = bad pattern (drop)
	}
	// grep unavailable / could not start: fall back to the compile-only gate.
	return true
}

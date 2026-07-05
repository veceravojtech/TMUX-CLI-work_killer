package rules

// check.go is the brownfield sibling of Match (coderules.go): given a set of
// changed files, it reports which resolved code-rules APPLY and — for the
// automated half — which are VIOLATED (the rule's anti-pattern is present in
// the diff). This is the house-catalogue enforcement-command analog (design §2.9): a diff
// gate, not a fail-closed plan injection. It reuses Match's routing primitives
// (compileGlob, runnable) verbatim — Go still owns ALL glob/severity/signal
// routing (§6.4 determinism boundary). The core is git-free: the CLI derives
// the changed-file list and passes it in, so this is testable with t.TempDir().
//
// Violation semantics are the INVERSE of Match's fail-closed RenderValidateCmd:
// here grep exit 0 (the bad pattern is FOUND) means violated:true. We never
// negate the signal — §2.9 says "run automated signals against those files;
// emit applicable/violated".

import (
	"fmt"
	"os/exec"
	"regexp"
)

// CheckRulePayload is one applicable rule's diff-gate verdict. It mirrors
// MatchResult's payload shape (id/severity/validate_kind/phase/paths) and adds
// the check-specific fields: the matched changed files, the bare signal, the
// violated verdict, and whether an agent still needs to review (any rule whose
// validate_kind is not "automated").
type CheckRulePayload struct {
	ID           string   `json:"id"`
	Severity     string   `json:"severity"`
	ValidateKind string   `json:"validate_kind"`
	Phase        string   `json:"phase"`
	Paths        []string `json:"paths"`
	Matched      []string `json:"matched"`
	Signal       string   `json:"signal"`
	Violated     bool     `json:"violated"`
	AgentReview  bool     `json:"agent_review"`
}

// CheckResult is the full `rules check` output: the applicable per-rule
// verdicts plus human-readable warnings (dropped non-runnable signals, bad
// globs, grep path errors). Slices are always non-nil so --json is
// schema-stable for agent consumers (mirror MatchResult).
type CheckResult struct {
	Rules    []CheckRulePayload `json:"rules"`
	Warnings []string           `json:"warnings"`
}

// Check runs each rule's automated signal against the changed files and reports
// applicable/violated per rule. For each rule: skip on phase mismatch; compile
// applies_to globs (a bad glob is dropped with a warning); collect the changed
// files it matches; omit the rule entirely if none. When the rule carries a
// signal it is runnable-gated (not runnable → drop with warning, never inject a
// dead check, §6.1) then run with signalMatches over the matched footprint
// (grep exit 0 → violated:true). A signal-less rule is never violated — its
// verdict is the agent's (AgentReview). dir is the working directory grep runs
// in, so files may be repo-relative.
func Check(codeRules []CodeRule, files []string, phase, dir string) CheckResult {
	result := CheckResult{Rules: []CheckRulePayload{}, Warnings: []string{}}

	for _, r := range codeRules {
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

		violated := false
		if r.Signal != "" {
			// Drop a signal that cannot run (bad regex / exit 2) — never inject
			// a dead check. The gate takes the BARE signal + examples.bad; do
			// NOT pass it a negated form.
			if !runnable(r.Signal, r.Examples.Bad) {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("dropped %s: signal not runnable (exit 2)", r.ID))
				continue
			}
			v, err := signalMatches(r.Signal, matched, dir)
			if err != nil {
				// grep exit >=2 (path error): never let it masquerade as
				// violated or clean — drop with a warning.
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("dropped %s: signal grep error: %v", r.ID, err))
				continue
			}
			violated = v
		}

		paths := []string{}
		if r.sourcePath != "" {
			paths = append(paths, r.sourcePath)
		}

		result.Rules = append(result.Rules, CheckRulePayload{
			ID:           r.ID,
			Severity:     r.Severity,
			ValidateKind: r.ValidateKind,
			Phase:        r.Phase,
			Paths:        paths,
			Matched:      matched,
			Signal:       r.Signal,
			Violated:     violated,
			AgentReview:  r.ValidateKind != "automated",
		})
	}

	return result
}

// signalMatches runs the bare signal as a recursive extended grep over the
// matched changed files. grep exit codes: 0 = match (violated), 1 = no match
// (clean), >=2 = error (returned so the caller can warn-and-drop — a path error
// must never be reported as a clean or dirty verdict). -rE mirrors
// RenderValidateCmd's form: harmless on file paths, recurses if a path is a
// directory.
func signalMatches(signal string, files []string, dir string) (bool, error) {
	args := append([]string{"-rE", "-e", signal}, files...)
	cmd := exec.Command("grep", args...)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return true, nil // exit 0: the anti-pattern is present
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() == 1 {
			return false, nil // exit 1: no match → clean
		}
		return false, fmt.Errorf("grep exit %d", ee.ExitCode())
	}
	return false, err // grep could not start
}

package tasks

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type SpecGap struct {
	ID      string
	Message string
}

type SpecStats struct {
	TestCases          int
	AcceptanceCriteria int
	CodeMapEntries     int
}

type SpecValidationResult struct {
	Valid bool
	Gaps  []SpecGap
	Stats SpecStats
}

var (
	sectionRe    = regexp.MustCompile(`(?m)^## (.+)`)
	subSectionRe = regexp.MustCompile(`(?m)^### (.+)`)
	codeRefRe    = regexp.MustCompile(`\S+\.\w+:\d+(-\d+)?`)
	givenRe      = regexp.MustCompile(`(?i)\bgiven\b.*\bwhen\b.*\bthen\b`)
	checkboxRe   = regexp.MustCompile(`(?m)^- \[[ x]\] `)
	// testCaseRe counts named test cases in a Test Plan. The token (TestXxx /
	// test_xxx / testXxx / TC-N) must lead a `-`/`|` bullet, but a markdown
	// emphasis wrapper (**bold** / *italic* / _underscore_) and/or one backtick
	// between the bullet and the token is tolerated — a readable `- **TC-1 ...`
	// bullet counts, not just a bare `- TC-1`. Rejections are unaffected: the
	// wrapper is optional and the line still fails unless a real token follows
	// (mirrors tdbRe's existing `**` tolerance).
	testCaseRe   = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]*|\|[ \t]*)(?:[*_]{1,2}[ \t]*)?` + "`?" + `(?:Test\w+|test_\w+|test[A-Z]\w*|TC-\d+)`)
	tdbRe        = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?(?:(?:\*\*)?[\w /\-]+:(?:\*\*)?\s*)?(?:\*\*)?\s*(?:TBD|TODO|PLACEHOLDER|(?i:to be determined))(?:\*\*)?\s*\.?\s*$`)

	// S9 gate-objectivity. subjectiveGateRe matches vague adjectives that can
	// never describe a command-decidable gate. coverageClauseRe matches the
	// dangerous "coverage as a pass condition" forms ("with ... coverage",
	// "missing ... coverage", "test coverage", "coverage of", "covers <thing>")
	// while deliberately NOT matching meta-references like "not a coverage
	// judgment call" or "uncovered". A numeric coverage threshold ("coverage >=
	// 80%") is objective and is left to the caller's anchor (the % / digit).
	subjectiveGateRe = regexp.MustCompile(`(?i)\b(appropriate(ly)?|properly|sufficient(ly)?|adequate(ly)?|reasonabl[ey]|demonstrabl[ey]|as needed|where applicable)\b`)
	coverageClauseRe = regexp.MustCompile(`(?i)(with\s+[\w/ ,.-]*\bcoverage\b|missing[\w/ ,.-]*\bcoverage\b|\btests?\s+coverage\b|\bcoverage\s+of\b|\bcovers?\s+(creation|mutation|invariant|event|the|all|each|every))`)

	// S10 Code Rules satisfaction. A `## Code Rules` section lists matched rules:
	// `- CR-<id>: <satisfaction>` is a `must` rule whose satisfaction line states
	// how the spec honors it; `- <id>: ...` (no CR- prefix) is a `should` rule and
	// is NOT subject to the emptiness check. crLineRe matches only the CR- form,
	// capturing the id (group 1) and the trailing satisfaction text (group 2).
	crLineRe = regexp.MustCompile(`(?m)^\s*-\s*CR-([\w.-]+):[ \t]*(.*)$`)
)

func ValidateSpecFile(path string) (*SpecValidationResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read spec file: %w", err)
	}

	content := string(data)
	sections := parseSections(content)

	result := &SpecValidationResult{Valid: true}

	checkIntent(sections, result)
	checkCodeMap(sections, result)
	checkImplementationPlan(sections, result)
	checkTestPlan(sections, result)
	checkAcceptanceCriteria(sections, result)
	checkBoundaries(sections, result)
	checkRFD(content, result)
	checkGateObjectivity(sections, result)
	checkCodeRules(sections, result)

	result.Valid = len(result.Gaps) == 0
	return result, nil
}

func parseSections(content string) map[string]string {
	sections := make(map[string]string)
	locs := sectionRe.FindAllStringIndex(content, -1)
	names := sectionRe.FindAllStringSubmatch(content, -1)

	for i, loc := range locs {
		name := strings.TrimSpace(names[i][1])
		var body string
		if i+1 < len(locs) {
			body = content[loc[1]:locs[i+1][0]]
		} else {
			body = content[loc[1]:]
		}
		sections[name] = body
	}
	return sections
}

func checkIntent(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Intent"]
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S0",
			Message: "Intent section missing — must have **Problem:** and **Approach:**",
		})
		return
	}
	if !strings.Contains(body, "**Problem:**") || !strings.Contains(body, "**Approach:**") {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S0",
			Message: "Intent section must contain **Problem:** and **Approach:** fields",
		})
	}
}

func checkCodeMap(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Code Map"]
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S1",
			Message: "Code Map section missing — must cite specific files with line numbers",
		})
		return
	}
	entries := codeRefRe.FindAllString(body, -1)
	result.Stats.CodeMapEntries = len(entries)
	if len(entries) == 0 {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S1",
			Message: "Code Map has no file:line references — cite specific files, functions, and line numbers",
		})
	}
}

func checkImplementationPlan(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Implementation Plan"]
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S2",
			Message: "Implementation Plan section missing",
		})
		return
	}
	subs := subSectionRe.FindAllStringSubmatch(body, -1)
	var hasFiles bool
	for _, sub := range subs {
		name := strings.TrimSpace(sub[1])
		if strings.Contains(name, "Files to Create") || strings.Contains(name, "Files to Modify") {
			hasFiles = true
		}
	}
	if !hasFiles {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S2",
			Message: "Implementation Plan lacks '### Files to Create/Modify' subsection",
		})
	}
}

func checkTestPlan(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Test Plan"]
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S3",
			Message: "Test Plan section missing",
		})
		return
	}
	cases := testCaseRe.FindAllString(body, -1)
	result.Stats.TestCases = len(cases)
	if len(cases) == 0 {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S3",
			Message: "Test Plan has no specific test cases — lead each bullet with a TestXxx / test_xxx / TC-N token then the expected behavior (bold/backtick wrappers are fine: `- **TC-1** …`)",
		})
	}
}

func checkAcceptanceCriteria(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Acceptance Criteria"]
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S4",
			Message: "Acceptance Criteria section missing",
		})
		return
	}
	checkboxes := checkboxRe.FindAllString(body, -1)
	result.Stats.AcceptanceCriteria = len(checkboxes)
	if len(checkboxes) == 0 {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S4",
			Message: "Acceptance Criteria has no checkbox entries",
		})
		return
	}
	lines := strings.Split(body, "\n")
	var givenCount int
	for _, line := range lines {
		if checkboxRe.MatchString(line) && givenRe.MatchString(line) {
			givenCount++
		}
	}
	if givenCount == 0 {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S4",
			Message: "Acceptance Criteria don't use Given/When/Then format",
		})
	}
}

func checkBoundaries(sections map[string]string, result *SpecValidationResult) {
	body, ok := findSection(sections, "Boundaries")
	if !ok {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S7",
			Message: "Boundaries & Constraints section missing — must define Always/Ask First/Never tiers",
		})
		return
	}
	if !strings.Contains(body, "**Never:**") && !strings.Contains(body, "Never:") {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S7",
			Message: "Boundaries section has no 'Never:' entries — scope edges are undefined",
		})
	}
}

func checkRFD(content string, result *SpecValidationResult) {
	if tdbRe.MatchString(content) {
		result.Gaps = append(result.Gaps, SpecGap{
			ID:      "S8",
			Message: "Spec contains TBD/TODO/PLACEHOLDER text — all sections must be complete",
		})
	}
}

// checkGateObjectivity flags subjective gates (S9). A gate that hinges on a
// coverage judgment or a vague adjective ("demonstrably", "properly", "missing
// test coverage", "with invariant coverage") cannot be decided from a command's
// exit status or output, so the dispatch→validate→correct loop cannot converge
// on it: the validator flip-flops and the implementer receives prose it cannot
// action. Gates must be objective — an exit code, a numeric threshold, or a
// presence-grep (GM-09b). This scans only the machine-checkable gate sections
// (Validation Rules, Investigation Config), so it is a no-op on worker tech-specs
// that have neither.
func checkGateObjectivity(sections map[string]string, result *SpecValidationResult) {
	var offenders []string
	for _, name := range []string{"Validation Rules", "Investigation Config"} {
		body, ok := sections[name]
		if !ok {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if subjectiveGateRe.MatchString(trimmed) || coverageClauseRe.MatchString(trimmed) {
				offenders = append(offenders, trimmed)
			}
		}
	}
	if len(offenders) == 0 {
		return
	}
	snippet := []rune(offenders[0])
	if len(snippet) > 80 {
		snippet = append(snippet[:80:80], '…')
	}
	result.Gaps = append(result.Gaps, SpecGap{
		ID: "S9",
		Message: fmt.Sprintf(
			"Validation Rules / Investigation Config contain %d subjective gate(s) with no objective anchor — e.g. %q. A gate must be decidable from exit status, a numeric threshold, or a presence-grep (GM-09b); decompose coverage-style criteria into greppable checks.",
			len(offenders), string(snippet),
		),
	})
}

// checkCodeRules flags an unsatisfied `must` rule (S10). A spec's `## Code Rules`
// section lists the rules matched for the goal; each `- CR-<id>:` line MUST carry
// a non-empty satisfaction statement saying how the spec honors that `must` rule.
// A blank satisfaction line means the rule is declared but not addressed, so the
// implementer has no objective contract to meet. This is a policy-free structural
// check: it never parses rule YAML and never special-cases Valid (the existing
// `Valid = len(Gaps)==0` aggregation flips it for free). Absent section → no gap;
// `should` rules (plain `- <id>:`, no CR- prefix) are excluded by crLineRe.
func checkCodeRules(sections map[string]string, result *SpecValidationResult) {
	body, ok := sections["Code Rules"]
	if !ok {
		return
	}
	var offenders []string
	for _, m := range crLineRe.FindAllStringSubmatch(body, -1) {
		if strings.TrimSpace(m[2]) == "" {
			offenders = append(offenders, "CR-"+m[1])
		}
	}
	if len(offenders) == 0 {
		return
	}
	result.Gaps = append(result.Gaps, SpecGap{
		ID: "S10",
		Message: fmt.Sprintf(
			"Code Rules section declares %s with no satisfaction line — every `- CR-<id>:` must state how the spec honors that must-rule.",
			strings.Join(offenders, ", "),
		),
	})
}

func findSection(sections map[string]string, prefix string) (string, bool) {
	for name, body := range sections {
		if strings.HasPrefix(name, prefix) {
			return body, true
		}
	}
	return "", false
}

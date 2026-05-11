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
	codeRefRe    = regexp.MustCompile("`[^`]+:\\d+`")
	givenRe      = regexp.MustCompile(`(?i)\bgiven\b.*\bwhen\b.*\bthen\b`)
	checkboxRe   = regexp.MustCompile(`(?m)^- \[[ x]\] `)
	testCaseRe   = regexp.MustCompile(`(?m)^- Test\w+|^- test_\w+`)
	tdbRe        = regexp.MustCompile(`(?i)\bTBD\b|\bto be determined\b|\bTODO\b|\bPLACEHOLDER\b`)
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
			Message: "Test Plan has no specific test cases — list TestXxx entries with expected behavior",
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

func findSection(sections map[string]string, prefix string) (string, bool) {
	for name, body := range sections {
		if strings.HasPrefix(name, prefix) {
			return body, true
		}
	}
	return "", false
}

package tasks

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestBlind3TodoAll(t *testing.T) {
	matches, _ := filepath.Glob(".tmux-cli/research/*/execute-*.md")
	if len(matches) == 0 {
		matches, _ = filepath.Glob("../../.tmux-cli/research/*/execute-*.md")
	}
	reTODO := regexp.MustCompile(`(?i)\bTODO\b`)
	tfRe := regexp.MustCompile(`(?m)^## (Intent|Implementation Plan|Acceptance Criteria)`)

	var todoAll, todoTF int
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		c := string(data)
		n := len(reTODO.FindAllString(c, -1))
		todoAll += n
		if tfRe.MatchString(c) {
			todoTF += n
		}
	}
	t.Logf("TODO across ALL execute files: %d", todoAll)
	t.Logf("TODO across template-format files: %d", todoTF)

	// also: how does tdbRe \bPLACEHOLDER\b (uppercase-word) compare to substring placeholder?
	rePHword := regexp.MustCompile(`(?i)\bplaceholder\b`)
	rePHsub := regexp.MustCompile(`(?i)placeholder`)
	var phWordTF, phSubTF int
	for _, f := range matches {
		data, _ := os.ReadFile(f)
		c := string(data)
		if !tfRe.MatchString(c) {
			continue
		}
		phWordTF += len(rePHword.FindAllString(c, -1))
		phSubTF += len(rePHsub.FindAllString(c, -1))
	}
	t.Logf("placeholder \\bword\\b TF: %d ; substring TF: %d", phWordTF, phSubTF)

	// Of the 28 S8-flagging template-format files, what is the FIRST/dominant trigger token?
	// Count, per S8-flagging file, which token classes are present.
	reMust := regexp.MustCompile(`\{\{[^}]*[Pp]laceholder[^}]*\}\}`)
	rePH := regexp.MustCompile(`(?i)\bplaceholder\b`)
	reTD := regexp.MustCompile(`(?i)\bTODO\b`)
	reTB := regexp.MustCompile(`(?i)\bTBD\b`)
	reTBT := regexp.MustCompile(`(?i)\bto be determined\b`)
	var filesPH, filesMust, filesTODO, filesTBD, filesTBT, filesPHonly int
	var mustacheTotal int
	for _, f := range matches {
		data, _ := os.ReadFile(f)
		c := string(data)
		if !tfRe.MatchString(c) {
			continue
		}
		if !tdbRe.MatchString(c) {
			continue
		}
		hasPH := rePH.MatchString(c)
		hasMust := reMust.MatchString(c)
		hasTODO := reTD.MatchString(c)
		hasTBD := reTB.MatchString(c)
		hasTBT := reTBT.MatchString(c)
		mustacheTotal += len(reMust.FindAllString(c, -1))
		if hasPH {
			filesPH++
		}
		if hasMust {
			filesMust++
		}
		if hasTODO {
			filesTODO++
		}
		if hasTBD {
			filesTBD++
		}
		if hasTBT {
			filesTBT++
		}
		if hasPH && !hasTODO && !hasTBD && !hasTBT {
			filesPHonly++
		}
	}
	t.Logf("Among 28 S8-flagging TF files -- files containing:")
	t.Logf("  placeholder-word: %d ; mustache {{..placeholder..}}: %d ; TODO: %d ; TBD: %d ; to-be-determined: %d",
		filesPH, filesMust, filesTODO, filesTBD, filesTBT)
	t.Logf("  files where placeholder is the ONLY trigger: %d", filesPHonly)
	t.Logf("  total mustache {{..placeholder..}} occurrences (TF): %d", mustacheTotal)

	// Value-position scan over template-format: does ANY line genuinely == field: TBD/TODO/PLACEHOLDER?
	reVal := regexp.MustCompile(`(?im)^\s*([-*]\s*)?(\*\*)?[\w \-/]*:\s*(\*\*)?\s*(TBD|TODO|PLACEHOLDER)\s*$`)
	var valHits int
	for _, f := range matches {
		data, _ := os.ReadFile(f)
		c := string(data)
		if !tfRe.MatchString(c) {
			continue
		}
		for _, m := range reVal.FindAllString(c, -1) {
			t.Logf("  VALUE-POS hit in %s: %q", filepath.Base(f), m)
			valHits++
		}
	}
	t.Logf("value-position field:TOKEN hits (TF): %d", valHits)
}

package transcript

import "regexp"

// ansiRe matches one terminal escape unit: a CSI sequence (captured so the
// final byte can steer the replacement), an OSC sequence (BEL or ST
// terminated), or a stray ESC/BEL byte. Mirrors the e2e pipe-pane prior art
// (cmd/tmux-cli/e2e.go ansiEscapeRe), split so CSI finals are inspectable.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x1b\x07]`)

// positioningFinals are CSI final bytes that MOVE the cursor horizontally or
// absolutely (CUF C, CHA G, CUP H/f, HPA `, VPA d). The pane renderer uses
// them instead of literal spaces, so stripping them to nothing would glue
// adjacent words together; per prior art they degrade to a single space
// ("column-escape → space tolerance").
const positioningFinals = "CGHf`d"

// Strip removes ANSI CSI/OSC/SGR escape sequences (and stray ESC/BEL/NUL
// bytes) from a captured pane chunk, degrading cursor-positioning escapes to a
// single space. The result is the plain-UTF-8 text the contract stores as the
// segment's raw pre-redaction "text".
func Strip(s string) string {
	out := ansiRe.ReplaceAllStringFunc(s, func(m string) string {
		final := m[len(m)-1]
		if len(m) > 2 && m[1] == '[' && indexByte(positioningFinals, final) {
			return " "
		}
		return ""
	})
	// pipe-pane streams can carry NULs (prior art: e2e normalizeLog); they are
	// not text.
	return removeNUL(out)
}

func indexByte(set string, b byte) bool {
	for i := 0; i < len(set); i++ {
		if set[i] == b {
			return true
		}
	}
	return false
}

func removeNUL(s string) string {
	if !containsNUL(s) {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0 {
			b = append(b, s[i])
		}
	}
	return string(b)
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

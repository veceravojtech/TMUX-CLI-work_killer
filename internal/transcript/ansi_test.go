package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStrip_RemovesSGRColorEscapes(t *testing.T) {
	assert.Equal(t, "FRESH HANDOFF armed",
		Strip("\x1b[1;32mFRESH HANDOFF armed\x1b[0m"))
}

func TestStrip_ColumnEscapesDegradeToSpace(t *testing.T) {
	// A column-move (CHA) sits where the renderer meant a gap: stripping it to
	// nothing would glue the words together ([[e2e-pipe-pane-ansi-matching]]).
	assert.Equal(t, "FRESH HANDOFF", Strip("FRESH\x1b[12GHANDOFF"))
	// CUP (H) and HPA (`) behave the same.
	assert.Equal(t, "a b", Strip("a\x1b[1;5Hb"))
	assert.Equal(t, "a b", Strip("a\x1b[7`b"))
}

func TestStrip_RemovesOSCTitleSequences(t *testing.T) {
	assert.Equal(t, "prompt", Strip("\x1b]0;window title\x07prompt"))
	assert.Equal(t, "prompt", Strip("\x1b]2;title\x1b\\prompt"))
}

func TestStrip_RemovesStrayEscBelAndNul(t *testing.T) {
	assert.Equal(t, "ab", Strip("a\x1b\x07\x00b"))
}

func TestStrip_PlainTextUntouched(t *testing.T) {
	in := "  go test ./... -short -race  # indented, spaced"
	assert.Equal(t, in, Strip(in))
}

func TestStrip_PrivateModeAndEraseSequences(t *testing.T) {
	// DEC private mode (?25l cursor hide) and EL (K erase) carry no text and no
	// positioning meaning worth a space.
	assert.Equal(t, "ab", Strip("a\x1b[?25l\x1b[Kb"))
}

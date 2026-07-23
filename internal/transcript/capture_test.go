package transcript

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureStream_StripsAndAppendsLines(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "execute-1", fixedClock(base, time.Millisecond))

	CaptureStream(strings.NewReader("\x1b[32mgreen line\x1b[0m\r\nplain line\n"), w)
	require.NoError(t, w.Close())

	segs := readWindowSegments(t, root, "execute-1")
	require.Len(t, segs, 2)
	assert.Equal(t, "green line", segs[0].Text)
	assert.Equal(t, "plain line", segs[1].Text)
	assert.Equal(t, int64(1), segs[0].Seq)
	assert.Equal(t, int64(2), segs[1].Seq)
}

func TestCaptureStream_SkipsEscapeOnlyRedrawLines(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "supervisor", fixedClock(base, time.Millisecond))

	CaptureStream(strings.NewReader("\x1b[2J\x1b[H\r\n\r\nreal content\n\x1b[?25l\n"), w)
	require.NoError(t, w.Close())

	segs := readWindowSegments(t, root, "supervisor")
	require.Len(t, segs, 1, "escape-only/blank redraw lines carry no transcript content")
	assert.Equal(t, "real content", segs[0].Text)
}

func TestCaptureStream_FlushesFinalUnterminatedLine(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "execute-1", fixedClock(base, time.Millisecond))

	CaptureStream(strings.NewReader("no trailing newline"), w)
	require.NoError(t, w.Close())

	segs := readWindowSegments(t, root, "execute-1")
	require.Len(t, segs, 1)
	assert.Equal(t, "no trailing newline", segs[0].Text)
}

func TestCaptureStream_KeepsDrainingOnWriteErrors(t *testing.T) {
	// A writer on an unwritable root errors every Append; the stream must still
	// drain to EOF (a tee upstream dies on SIGPIPE if we stop reading).
	w := NewWriter(Options{Root: "/proc/definitely/not/writable", SessionID: "s", Window: "supervisor"})
	assert.NotPanics(t, func() {
		CaptureStream(strings.NewReader(strings.Repeat("line\n", 1000)), w)
	})
}

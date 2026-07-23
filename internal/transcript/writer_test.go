package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock returns a clock func advancing by step on each call, seeded at base.
func fixedClock(base time.Time, step time.Duration) func() time.Time {
	cur := base
	return func() time.Time {
		t := cur
		cur = cur.Add(step)
		return t
	}
}

func testWriter(root, window string, clock func() time.Time) *Writer {
	return NewWriter(Options{
		Root:      root,
		SessionID: "sess-1",
		Window:    window,
		PID:       424242,
		Clock:     clock,
	})
}

func readWindowSegments(t *testing.T, root, window string) []Segment {
	t.Helper()
	dir := filepath.Join(root, window)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []Segment
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "seg-") || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<21)
		for sc.Scan() {
			if len(strings.TrimSpace(sc.Text())) == 0 {
				continue
			}
			var seg Segment
			require.NoError(t, json.Unmarshal(sc.Bytes(), &seg), "segment %s line %q", e.Name(), sc.Text())
			out = append(out, seg)
		}
		f.Close()
	}
	return out
}

func TestWriter_Append_WritesContractFieldsInOrder(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "execute-1", fixedClock(base, time.Second))
	require.NoError(t, w.Append("hello world"))
	require.NoError(t, w.Close())

	// Path pattern: transcripts/<window>/seg-<yyyymmddThhmmss>-<pid>.ndjson.
	path := filepath.Join(root, "execute-1", "seg-20260723T100000-424242.ndjson")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "segment file must land at the contract path")

	// Byte-exact field ORDER is contract-pinned (the ship worker consumes it).
	line := strings.TrimSpace(string(data))
	assert.Equal(t,
		`{"ts":"2026-07-23T10:00:00Z","session_id":"sess-1","window":"execute-1","kind":"worker","seq":1,"text":"hello world"}`,
		line)
}

func TestWriter_Seq_MonotonicPerSegmentFromOne(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "supervisor", fixedClock(base, time.Millisecond))
	for i := 0; i < 5; i++ {
		require.NoError(t, w.Append("line"))
	}
	require.NoError(t, w.Close())

	segs := readWindowSegments(t, root, "supervisor")
	require.Len(t, segs, 5)
	for i, s := range segs {
		assert.Equal(t, int64(i+1), s.Seq, "seq starts at 1 and is monotonic per file")
		assert.Equal(t, "supervisor", s.Kind)
	}
}

func TestWriter_Rotation_ByAge_ResetsSeq(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	// Each Append advances the clock 3 minutes: the 3rd write crosses 5 min.
	w := testWriter(root, "taskvisor", fixedClock(base, 3*time.Minute))
	require.NoError(t, w.Append("a"))
	require.NoError(t, w.Append("b"))
	first := w.currentSegmentName()
	require.NoError(t, w.Append("c"))
	second := w.currentSegmentName()
	require.NoError(t, w.Close())

	assert.NotEqual(t, first, second, "age threshold must rotate to a new segment")
	segs := readWindowSegments(t, root, "taskvisor")
	require.Len(t, segs, 3)
	assert.Equal(t, int64(1), segs[2].Seq, "seq must reset to 1 in the fresh segment")
}

func TestWriter_Rotation_BySize(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "execute-1", fixedClock(base, time.Second))
	big := strings.Repeat("x", 512*1024)
	require.NoError(t, w.Append(big))
	require.NoError(t, w.Append(big)) // crosses 1 MiB
	first := w.currentSegmentName()
	require.NoError(t, w.Append("after"))
	second := w.currentSegmentName()
	require.NoError(t, w.Close())
	assert.NotEqual(t, first, second, "1 MiB threshold must rotate to a new segment")
}

func TestWriter_CapEviction_RemovesOldestAcrossWindows(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	// Older window fills first; a later window's writes must evict the OLDER
	// window's segments (tree-wide cap, not per-window).
	old := testWriter(root, "execute-1", fixedClock(base, 6*time.Minute))
	old.capBytesOverride = 4096
	require.NoError(t, old.Append(strings.Repeat("a", 1500)))
	require.NoError(t, old.Append(strings.Repeat("b", 1500))) // rotated by age
	require.NoError(t, old.Close())

	later := testWriter(root, "supervisor", fixedClock(base.Add(time.Hour), time.Second))
	later.capBytesOverride = 4096
	require.NoError(t, later.Append(strings.Repeat("c", 1500)))
	require.NoError(t, later.Append(strings.Repeat("d", 1500)))
	require.NoError(t, later.Close())

	oldEntries, err := os.ReadDir(filepath.Join(root, "execute-1"))
	require.NoError(t, err)
	assert.Less(t, len(oldEntries), 2, "oldest window's segments must be evicted first")
	laterEntries, err := os.ReadDir(filepath.Join(root, "supervisor"))
	require.NoError(t, err)
	assert.NotEmpty(t, laterEntries, "the active window's segments survive")
}

func TestWriter_NeverFailsCaller_UnwritableRoot(t *testing.T) {
	w := NewWriter(Options{Root: "/proc/definitely/not/writable", SessionID: "s", Window: "supervisor"})
	assert.Error(t, w.Append("x"), "error surfaces for diagnostics")
	assert.NotPanics(t, func() { _ = w.Append("x") })
	assert.NoError(t, w.Close())
}

func TestWriter_TornTail_ReaderSkipsPartialLastLine(t *testing.T) {
	// The writer appends whole lines; a crash can leave a torn tail. The
	// contract makes the READER tolerant — prove a torn tail doesn't poison
	// parsing of the preceding whole lines.
	root := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := testWriter(root, "execute-1", fixedClock(base, time.Millisecond))
	require.NoError(t, w.Append("whole line"))
	require.NoError(t, w.Close())

	path := filepath.Join(root, "execute-1", w.currentSegmentName())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"ts":"2026-07-23T10:00:01Z","session_id":"sess-1","window":"exec`) // torn, no newline
	require.NoError(t, err)
	require.NoError(t, f.Close())

	sc := bufio.NewScanner(strings.NewReader(readFileString(t, path)))
	var whole int
	for sc.Scan() {
		var seg Segment
		if json.Unmarshal(sc.Bytes(), &seg) == nil {
			whole++
		}
	}
	assert.Equal(t, 1, whole, "exactly the whole line parses; the torn tail is skipped")
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

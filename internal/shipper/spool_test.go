package shipper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpoolDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/proj", ".tmux-cli", "logs", "spool"), SpoolDir("/proj"))
}

func TestListSegments_MissingDirIsEmpty(t *testing.T) {
	segs, err := ListSegments(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err)
	assert.Empty(t, segs)
}

func TestListSegments_SortedAndFiltered(t *testing.T) {
	dir := t.TempDir()
	// Out-of-order creation; only events-*.jsonl count, sorted ascending.
	for _, n := range []string{
		"events-20260723T090000-42.jsonl",
		"events-20260723T080000-42.jsonl",
		"cursor.json",
		"notes.txt",
		"events-20260723T100000-7.jsonl",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("{}\n"), 0o644))
	}
	segs, err := ListSegments(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"events-20260723T080000-42.jsonl",
		"events-20260723T090000-42.jsonl",
		"events-20260723T100000-7.jsonl",
	}, segs)
}

func TestCursor_RoundTripAtomic(t *testing.T) {
	dir := t.TempDir()
	// Missing cursor → zero value, no error.
	c, err := LoadCursor(dir)
	require.NoError(t, err)
	assert.Equal(t, Cursor{}, c)

	require.NoError(t, SaveCursor(dir, Cursor{Segment: "events-x.jsonl", Offset: 128}))
	got, err := LoadCursor(dir)
	require.NoError(t, err)
	assert.Equal(t, Cursor{Segment: "events-x.jsonl", Offset: 128}, got)

	// No stray temp files survive the atomic write.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only cursor.json should remain (temp renamed, not left behind)")
	assert.Equal(t, "cursor.json", entries[0].Name())
}

func TestCursor_CorruptDegradesToZero(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cursor.json"), []byte("{not json"), 0o644))
	c, err := LoadCursor(dir)
	require.NoError(t, err)
	assert.Equal(t, Cursor{}, c, "a corrupt cursor restarts from the beginning, never errors")
}

func TestScanSegment_CompleteLinesAndConsumed(t *testing.T) {
	data := []byte(`{"a":1}` + "\n" + `{"b":2}` + "\n")
	lines, consumed := scanSegment(data)
	require.Len(t, lines, 2)
	assert.Equal(t, int64(len(data)), consumed)
	assert.True(t, lines[0].ship)
	assert.JSONEq(t, `{"a":1}`, string(lines[0].json))
	assert.True(t, lines[1].ship)
}

func TestScanSegment_TornTailHeldBack(t *testing.T) {
	// A complete line followed by a partial (no terminating newline).
	data := []byte(`{"a":1}` + "\n" + `{"b":`)
	lines, consumed := scanSegment(data)
	require.Len(t, lines, 1, "the partial trailing line must be held back")
	assert.Equal(t, int64(len(`{"a":1}`+"\n")), consumed, "consumed stops at the last newline")
}

func TestScanSegment_NonJSONCompleteLineSkippedButConsumed(t *testing.T) {
	// A complete but corrupt line between two valid ones: skipped from shipping,
	// yet still consumed so the cursor advances past it (no wedge).
	data := []byte(`{"a":1}` + "\n" + "GARBAGE\n" + `{"b":2}` + "\n")
	lines, consumed := scanSegment(data)
	require.Len(t, lines, 3)
	assert.True(t, lines[0].ship)
	assert.False(t, lines[1].ship, "corrupt complete line is not shipped")
	assert.True(t, lines[2].ship)
	assert.Equal(t, int64(len(data)), consumed, "the corrupt line is still consumed")
}

func TestScanSegment_EmptyLinesSkipped(t *testing.T) {
	data := []byte("\n" + `{"a":1}` + "\n" + "\n")
	lines, consumed := scanSegment(data)
	require.Len(t, lines, 3)
	assert.False(t, lines[0].ship)
	assert.True(t, lines[1].ship)
	assert.False(t, lines[2].ship)
	assert.Equal(t, int64(len(data)), consumed)
}

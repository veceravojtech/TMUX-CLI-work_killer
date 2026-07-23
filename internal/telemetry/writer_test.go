package telemetry

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

func testIdentity() Identity {
	return Identity{SessionID: "sess-1", Project: "cli", Fingerprint: "deadbeef"}
}

func readSegments(t *testing.T, dir string) []Event {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "events-") && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	var out []Event
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		require.NoError(t, err)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<21)
		for sc.Scan() {
			line := sc.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			var ev Event
			require.NoError(t, json.Unmarshal(line, &ev), "segment %s line %q", n, string(line))
			out = append(out, ev)
		}
		f.Close()
	}
	return out
}

func TestWriter_Emit_WritesJSONLWithContractFields(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 123456789, time.UTC)
	w := NewWriter(Options{
		Dir:      dir,
		Identity: testIdentity(),
		Enabled:  true,
		PID:      4242,
		Clock:    fixedClock(base, 0),
	})
	require.NoError(t, w.Emit("session.start", "supervisor", map[string]any{"hostname": "box", "binary_version": "0.1.0"}))

	// Raw first line: assert byte-exact field ORDER matches the frozen contract.
	entries, _ := os.ReadDir(dir)
	require.Len(t, entries, 1)
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	line := strings.TrimRight(string(raw), "\n")
	assert.True(t, strings.HasSuffix(string(raw), "\n"), "each event line must end with newline")
	// Field order: ts, event, session_id, project, fingerprint, window, seq, payload
	idx := func(k string) int { return strings.Index(line, "\""+k+"\":") }
	order := []string{"ts", "event", "session_id", "project", "fingerprint", "window", "seq", "payload"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, idx(order[i-1]), idx(order[i]), "%s must precede %s", order[i-1], order[i])
	}

	evs := readSegments(t, dir)
	require.Len(t, evs, 1)
	ev := evs[0]
	assert.Equal(t, "session.start", ev.Event)
	assert.Equal(t, "sess-1", ev.SessionID)
	assert.Equal(t, "cli", ev.Project)
	assert.Equal(t, "deadbeef", ev.Fingerprint)
	assert.Equal(t, "supervisor", ev.Window)
	assert.Equal(t, int64(1), ev.Seq)
	assert.Equal(t, "box", ev.Payload["hostname"])
	// RFC3339Nano UTC
	parsed, perr := time.Parse(time.RFC3339Nano, ev.Ts)
	require.NoError(t, perr)
	assert.Equal(t, base.UTC(), parsed.UTC())
}

func TestWriter_Seq_MonotonicPerSegment(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 1, Clock: fixedClock(base, 0)})
	for i := 0; i < 5; i++ {
		require.NoError(t, w.Emit("goal.status", "", map[string]any{"n": i}))
	}
	evs := readSegments(t, dir)
	require.Len(t, evs, 5)
	for i, ev := range evs {
		assert.Equal(t, int64(i+1), ev.Seq)
	}
}

func TestWriter_Disabled_IsNoOp(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: false, PID: 1})
	require.NoError(t, w.Emit("session.start", "", nil))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "disabled writer must not create any spool files")
}

func TestWriter_NilPayload_MarshalsEmptyObject(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 1})
	require.NoError(t, w.Emit("session.end", "", nil))
	raw, _ := os.ReadFile(filepath.Join(dir, firstSeg(t, dir)))
	assert.Contains(t, string(raw), `"payload":{}`, "nil payload must serialize as an empty object, never null")
}

func TestWriter_PayloadGuard_TruncatesOverLongStrings(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 1})
	long := strings.Repeat("x", 500)
	require.NoError(t, w.Emit("hook.fired", "", map[string]any{"detail": long, "id": "short"}))
	evs := readSegments(t, dir)
	require.Len(t, evs, 1)
	assert.Len(t, evs[0].Payload["detail"], PayloadMaxStringLen, "over-long payload strings must be truncated at the writer boundary")
	assert.Equal(t, "short", evs[0].Payload["id"])
}

func TestWriter_Rotation_ByAge(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	// step >= 5min so each emit lands in a new segment (age rotation), and each
	// tick advances the wall-second so the segment name differs.
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 7, Clock: fixedClock(base, SegmentMaxAge+time.Second)})
	require.NoError(t, w.Emit("a", "", nil))
	require.NoError(t, w.Emit("b", "", nil))
	require.NoError(t, w.Emit("c", "", nil))
	entries, _ := os.ReadDir(dir)
	assert.GreaterOrEqual(t, len(entries), 2, "age-based rotation must produce multiple segments")
	// seq resets to 1 in each new segment
	evs := readSegments(t, dir)
	require.Len(t, evs, 3)
	for _, ev := range evs {
		assert.Equal(t, int64(1), ev.Seq, "seq restarts at 1 per rotated segment")
	}
}

func TestWriter_CapEviction_RemovesOldestSegments(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed several old oversized segments that together exceed the cap.
	blob := strings.Repeat("y", 1024)
	for i := 0; i < 3; i++ {
		name := "events-2026072300000" + string(rune('1'+i)) + "-9.jsonl"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(blob), 0o644))
	}
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 9, Clock: fixedClock(base, 0)})
	w.capBytesOverride = 2048 // force eviction well below default 200MiB
	require.NoError(t, w.Emit("goal.status", "", nil))
	entries, _ := os.ReadDir(dir)
	var total int64
	for _, e := range entries {
		info, _ := e.Info()
		total += info.Size()
	}
	assert.LessOrEqual(t, total, int64(2048), "cap eviction must bring the spool at/under the cap")
	// The current segment must survive eviction.
	assert.FileExists(t, filepath.Join(dir, w.currentSegmentName()))
}

func TestWriter_NeverFailsCaller_UnwritableDir(t *testing.T) {
	// Point the writer at a path whose parent is a FILE — MkdirAll fails.
	tmp := t.TempDir()
	notADir := filepath.Join(tmp, "blocker")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o644))
	w := NewWriter(Options{Dir: filepath.Join(notADir, "spool"), Identity: testIdentity(), Enabled: true, PID: 1})
	// Emit returns an error internally but must not panic; package-level Emit swallows it.
	assert.NotPanics(t, func() { _ = w.Emit("session.start", "", nil) })
}

func firstSeg(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "events-") {
			return e.Name()
		}
	}
	t.Fatalf("no segment file in %s", dir)
	return ""
}

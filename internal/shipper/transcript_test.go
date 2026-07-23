package shipper

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transcriptPost is one recorded transcript ingest POST.
type transcriptPost struct {
	path    string
	window  string
	batchID string
	body    string
}

// transcriptRecorder is a fake transcript ingest that records every POST.
type transcriptRecorder struct {
	mu      sync.Mutex
	posts   []transcriptPost
	fail401 bool // when true the FIRST transcript POST 401s to exercise refresh
	n       int
}

func newTranscriptIngest(t *testing.T, rec *transcriptRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(200)
			io.WriteString(w, `{"access_token":"tok2","refresh_token":"ref2","expires_in":3600}`)
			return
		}
		rec.mu.Lock()
		rec.n++
		n := rec.n
		rec.mu.Unlock()
		if rec.fail401 && n == 1 {
			w.WriteHeader(401)
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		body, _ := io.ReadAll(zr)
		rec.mu.Lock()
		rec.posts = append(rec.posts, transcriptPost{
			path:    r.URL.Path,
			window:  r.Header.Get("X-Window"),
			batchID: r.Header.Get("X-Batch-Id"),
			body:    string(body),
		})
		rec.mu.Unlock()
		w.WriteHeader(200)
		io.WriteString(w, `{"accepted":1,"duplicate":false}`)
	}))
}

// tline builds one contract-shaped transcript NDJSON line (with trailing \n).
func tline(window string, seq int64, text string) string {
	return fmt.Sprintf(`{"ts":"2026-07-23T08:00:00.000000001Z","session_id":"sess-1","window":%q,"kind":"worker","seq":%d,"text":%s}`,
		window, seq, mustJSON(text)) + "\n"
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// shippedLine re-marshals the expected post-redaction line the shipper emits.
func shippedLine(window string, seq int64, text string) string {
	return fmt.Sprintf(`{"ts":"2026-07-23T08:00:00.000000001Z","session_id":"sess-1","window":%q,"kind":"worker","seq":%d,"text":%s}`,
		window, seq, mustJSON(text))
}

func writeTranscriptSegment(t *testing.T, root, window, name, content string) string {
	t.Helper()
	dir := filepath.Join(TranscriptsDir(root), window)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func newTranscriptShipper(t *testing.T, srvURL, root string, hook redact.Hook) *TranscriptShipper {
	store := seedStore(t, srvURL, "tok1", "ref1")
	return NewTranscriptShipper(root, "sess-1", NewClient(srvURL, "fp", store), hook, nil)
}

func TestTranscriptShipOnce_NoDirIsNoop(t *testing.T) {
	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, t.TempDir(), redact.Hook{})
	require.NoError(t, ts.ShipOnce(context.Background()))
	assert.Empty(t, rec.posts)
}

func TestTranscriptShipOnce_NotArmedIsNoop(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "hello"))
	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	store := seedStore(t, srv.URL, "tok1", "ref1")
	ts := NewTranscriptShipper(root, "sess-1", NewClient(srv.URL, "fp", store), redact.Hook{},
		func() bool { return false })
	require.NoError(t, ts.ShipOnce(context.Background()))
	assert.Empty(t, rec.posts, "a disarmed shipper must ship nothing")
}

func TestTranscriptShipOnce_ShipsRedactedPerWindow(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "login password=hunter2 ok"))
	writeTranscriptSegment(t, root, "execute-1", "seg-20260723T080000-2.ndjson",
		tline("execute-1", 1, "curl -H 'Authorization: Bearer abc.def.ghi'"))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 2)

	byWindow := map[string]transcriptPost{}
	for _, p := range rec.posts {
		byWindow[p.window] = p
		assert.Equal(t, "/api/v1/telemetry/sessions/sess-1/transcripts", p.path)
		assert.NotEmpty(t, p.batchID)
	}
	require.Contains(t, byWindow, "supervisor")
	require.Contains(t, byWindow, "execute-1")
	assert.Equal(t,
		shippedLine("supervisor", 1, "login password=«REDACTED:kv» ok")+"\n",
		byWindow["supervisor"].body)
	assert.Equal(t,
		shippedLine("execute-1", 1, "curl -H 'Authorization: Bearer «REDACTED:bearer»'")+"\n",
		byWindow["execute-1"].body)

	// Per-window cursors advanced; open segments kept.
	for _, w := range []string{"supervisor", "execute-1"} {
		cur, err := LoadCursor(filepath.Join(TranscriptsDir(root), w))
		require.NoError(t, err)
		assert.NotEmpty(t, cur.Segment)
		assert.Greater(t, cur.Offset, int64(0))
	}

	// Second pass with no new data ships nothing more.
	require.NoError(t, ts.ShipOnce(context.Background()))
	assert.Len(t, rec.posts, 2)
}

func TestTranscriptShipOnce_DeletesRotatedSegmentKeepsOpen(t *testing.T) {
	root := t.TempDir()
	older := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "one"))
	newer := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T090000-1.ndjson",
		tline("supervisor", 2, "two"))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 2)
	assert.NoFileExists(t, older, "fully-shipped rotated segment must be deleted")
	assert.FileExists(t, newer, "open (newest) segment must be kept")

	cur, _ := LoadCursor(filepath.Join(TranscriptsDir(root), "supervisor"))
	assert.Equal(t, "seg-20260723T090000-1.ndjson", cur.Segment)
}

func TestTranscriptShipOnce_TornTailHeldBack(t *testing.T) {
	root := t.TempDir()
	full := tline("supervisor", 1, "complete")
	torn := `{"ts":"2026-07-23T08:00:01Z","session_id":"sess-1","window":"supervisor","kind":"worker","seq":2,"text":"tor`
	p := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson", full+torn)

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 1)
	assert.Equal(t, shippedLine("supervisor", 1, "complete")+"\n", rec.posts[0].body,
		"the torn tail must NOT ship")

	// The writer completes the torn line; the next pass ships ONLY it.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("n\"}\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 2)
	assert.Contains(t, rec.posts[1].body, `"text":"torn"`)
}

func TestTranscriptShipOnce_NonJSONLineSkipped(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "good")+"GARBAGE\n"+tline("supervisor", 2, "also good"))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 1)
	assert.Equal(t,
		shippedLine("supervisor", 1, "good")+"\n"+shippedLine("supervisor", 2, "also good")+"\n",
		rec.posts[0].body)
}

func TestTranscriptShipOnce_HookRewritesLines(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "mentions codename-orion here"))
	hookPath := filepath.Join(t.TempDir(), "redact.sh")
	require.NoError(t, os.WriteFile(hookPath,
		[]byte("#!/bin/sh\nsed 's/codename-orion/[project]/g'\n"), 0o755))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{Path: hookPath})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 1)
	assert.Equal(t, shippedLine("supervisor", 1, "mentions [project] here")+"\n", rec.posts[0].body)
}

func TestTranscriptShipOnce_FailingHookHoldsSegmentLocal(t *testing.T) {
	root := t.TempDir()
	seg := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "sensitive"))
	hookPath := filepath.Join(t.TempDir(), "redact.sh")
	require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{Path: hookPath})

	err := ts.ShipOnce(context.Background())
	require.Error(t, err, "a present-and-failing hook is fail-closed")
	assert.Empty(t, rec.posts, "nothing ships when the hook fails")
	assert.FileExists(t, seg, "the held segment stays local")
	cur, _ := LoadCursor(filepath.Join(TranscriptsDir(root), "supervisor"))
	assert.Equal(t, Cursor{}, cur, "cursor must not advance past an unredacted segment")
}

func TestTranscriptShipOnce_FailingHookInOneWindowDoesNotBlockOthers(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "aaa-broken", "seg-20260723T080000-1.ndjson",
		tline("aaa-broken", 1, "held"))
	writeTranscriptSegment(t, root, "zzz-healthy", "seg-20260723T080000-1.ndjson",
		tline("zzz-healthy", 1, "ships"))
	// Hook fails only for the broken window's content.
	hookPath := filepath.Join(t.TempDir(), "redact.sh")
	require.NoError(t, os.WriteFile(hookPath,
		[]byte("#!/bin/sh\nin=$(cat)\ncase \"$in\" in *held*) exit 1;; esac\nprintf '%s\\n' \"$in\"\n"), 0o755))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{Path: hookPath})

	err := ts.ShipOnce(context.Background())
	require.Error(t, err, "the held window's failure must surface for backoff")
	require.Len(t, rec.posts, 1, "the healthy window still ships")
	assert.Equal(t, "zzz-healthy", rec.posts[0].window)
}

func TestTranscriptShipOnce_HookOutputMustStayNDJSON(t *testing.T) {
	root := t.TempDir()
	seg := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "text"))
	// A hook that mangles the stream into non-JSON is a redaction failure.
	hookPath := filepath.Join(t.TempDir(), "redact.sh")
	require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\necho NOT-JSON\n"), 0o755))

	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{Path: hookPath})

	require.Error(t, ts.ShipOnce(context.Background()))
	assert.Empty(t, rec.posts)
	assert.FileExists(t, seg)
}

func TestTranscriptShipOnce_401RefreshesAndShips(t *testing.T) {
	root := t.TempDir()
	writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "after refresh"))

	rec := &transcriptRecorder{fail401: true}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.NoError(t, ts.ShipOnce(context.Background()))
	require.Len(t, rec.posts, 1)
	assert.Contains(t, rec.posts[0].body, "after refresh")
}

func TestTranscriptShipOnce_TransportErrorLeavesCursor(t *testing.T) {
	root := t.TempDir()
	seg := writeTranscriptSegment(t, root, "supervisor", "seg-20260723T080000-1.ndjson",
		tline("supervisor", 1, "unacked"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, root, redact.Hook{})

	require.Error(t, ts.ShipOnce(context.Background()))
	cur, _ := LoadCursor(filepath.Join(TranscriptsDir(root), "supervisor"))
	assert.Equal(t, int64(0), cur.Offset)
	assert.FileExists(t, seg, "unacked segment is never deleted")
}

// --- arming gate ---

func writeSettings(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(body), 0o644))
}

func loggedInStore(t *testing.T) *auth.Store {
	return seedStore(t, "https://api.example.com", "tok", "ref")
}

func loggedOutStore(t *testing.T) *auth.Store {
	t.Helper()
	return auth.NewStoreAt(filepath.Join(t.TempDir(), "auth.json"))
}

func TestTranscriptsArmed(t *testing.T) {
	cases := []struct {
		name     string
		settings string // empty = no setting.yaml at all
		loggedIn bool
		want     bool
	}{
		{"opted in and logged in", "telemetry:\n    enabled: true\n    transcripts: true\n", true, true},
		{"transcripts default false", "telemetry:\n    enabled: true\n", true, false},
		{"explicit transcripts false", "telemetry:\n    enabled: true\n    transcripts: false\n", true, false},
		{"kill switch overrides opt-in", "telemetry:\n    enabled: false\n    transcripts: true\n", true, false},
		{"enabled defaults true when absent", "telemetry:\n    transcripts: true\n", true, true},
		{"no settings file", "", true, false},
		{"logged out never arms", "telemetry:\n    enabled: true\n    transcripts: true\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.settings != "" {
				writeSettings(t, root, tc.settings)
			}
			store := loggedInStore(t)
			if !tc.loggedIn {
				store = loggedOutStore(t)
			}
			assert.Equal(t, tc.want, TranscriptsArmed(root, store))
		})
	}
}

// --- run loop ---

func TestTranscriptRun_StopsOnContextCancel(t *testing.T) {
	rec := &transcriptRecorder{}
	srv := newTranscriptIngest(t, rec)
	defer srv.Close()
	ts := newTranscriptShipper(t, srv.URL, t.TempDir(), redact.Hook{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx, RunOptions{PollInterval: 10 * time.Millisecond, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, ReauthBackoff: time.Millisecond})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

// Guard: transcript segment listing must ignore foreign files (cursor.json,
// tmp files) and only pick up seg-*.ndjson.
func TestListTranscriptSegments_FiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"seg-20260723T090000-1.ndjson", "seg-20260723T080000-1.ndjson", "cursor.json", "junk.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644))
	}
	segs, err := ListTranscriptSegments(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"seg-20260723T080000-1.ndjson", "seg-20260723T090000-1.ndjson"}, segs)
	// Missing dir → empty, no error.
	segs, err = ListTranscriptSegments(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.Empty(t, segs)
}

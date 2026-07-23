package shipper

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ingestRecorder is a fake events ingest that records every decompressed batch
// body and the X-Batch-Id, acking each with a fixed accepted count.
type ingestRecorder struct {
	mu       sync.Mutex
	bodies   []string
	batchIDs []string
	fail401  bool // when true, the FIRST events POST 401s to exercise refresh
	posts    int
}

func newIngest(t *testing.T, rec *ingestRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(200)
			io.WriteString(w, `{"access_token":"tok2","refresh_token":"ref2","expires_in":3600}`)
			return
		}
		rec.mu.Lock()
		rec.posts++
		n := rec.posts
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
		rec.bodies = append(rec.bodies, string(body))
		rec.batchIDs = append(rec.batchIDs, r.Header.Get("X-Batch-Id"))
		rec.mu.Unlock()
		w.WriteHeader(200)
		io.WriteString(w, `{"accepted":1,"duplicate":false}`)
	}))
}

func writeSegment(t *testing.T, spool, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(spool, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(spool, name), []byte(content), 0o644))
}

func newShipper(t *testing.T, srvURL, projectRoot string) *Shipper {
	store := seedStore(t, srvURL, "tok1", "ref1")
	return New(projectRoot, "sess-1", NewClient(srvURL, "fp", store))
}

func TestShipOnce_NoSpoolIsNoop(t *testing.T) {
	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, t.TempDir())
	require.NoError(t, s.ShipOnce(context.Background()))
	assert.Empty(t, rec.bodies)
}

func TestShipOnce_ShipsSingleOpenSegmentAndAdvancesCursor(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	content := `{"seq":1}` + "\n" + `{"seq":2}` + "\n"
	writeSegment(t, spool, seg, content)

	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))

	require.Len(t, rec.bodies, 1)
	assert.Equal(t, "{\"seq\":1}\n{\"seq\":2}\n", rec.bodies[0])

	// Cursor advanced to end of the (still-open) newest segment; segment kept.
	cur, err := LoadCursor(spool)
	require.NoError(t, err)
	assert.Equal(t, Cursor{Segment: seg, Offset: int64(len(content))}, cur)
	assert.FileExists(t, filepath.Join(spool, seg))

	// A second pass with no new data ships nothing.
	require.NoError(t, s.ShipOnce(context.Background()))
	assert.Len(t, rec.bodies, 1, "no new lines → no new batch")
}

func TestShipOnce_ResumesFromCursorOffset(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	first := `{"seq":1}` + "\n"
	writeSegment(t, spool, seg, first)

	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))
	require.Len(t, rec.bodies, 1)

	// Writer appends more lines to the same open segment.
	f, err := os.OpenFile(filepath.Join(spool, seg), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"seq":2}` + "\n" + `{"seq":3}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, s.ShipOnce(context.Background()))
	require.Len(t, rec.bodies, 2)
	assert.Equal(t, "{\"seq\":2}\n{\"seq\":3}\n", rec.bodies[1], "second batch must contain ONLY the appended lines")
}

func TestShipOnce_TornTailHeldBackUntilCompleted(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	// One complete line + a torn (unterminated) tail.
	writeSegment(t, spool, seg, `{"seq":1}`+"\n"+`{"seq":2`)

	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))
	require.Len(t, rec.bodies, 1)
	assert.Equal(t, "{\"seq\":1}\n", rec.bodies[0], "the torn tail must NOT be shipped")

	// The writer completes the torn line.
	f, _ := os.OpenFile(filepath.Join(spool, seg), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`}` + "\n")
	f.Close()

	require.NoError(t, s.ShipOnce(context.Background()))
	require.Len(t, rec.bodies, 2)
	assert.Equal(t, "{\"seq\":2}\n", rec.bodies[1], "the now-complete line ships on the next pass")
}

func TestShipOnce_DeletesRotatedSegmentAndKeepsOpenOne(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	older := "events-20260723T080000-1.jsonl"
	newer := "events-20260723T090000-1.jsonl"
	writeSegment(t, spool, older, `{"seq":1}`+"\n")
	writeSegment(t, spool, newer, `{"seq":2}`+"\n")

	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))

	require.Len(t, rec.bodies, 2)
	assert.Equal(t, "{\"seq\":1}\n", rec.bodies[0])
	assert.Equal(t, "{\"seq\":2}\n", rec.bodies[1])

	assert.NoFileExists(t, filepath.Join(spool, older), "a fully-shipped rotated segment must be deleted")
	assert.FileExists(t, filepath.Join(spool, newer), "the open (newest) segment must be kept")

	cur, _ := LoadCursor(spool)
	assert.Equal(t, newer, cur.Segment)
}

func TestShipOnce_SkipsCorruptLineButShipsRest(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	writeSegment(t, spool, seg, `{"seq":1}`+"\n"+"GARBAGE\n"+`{"seq":2}`+"\n")

	rec := &ingestRecorder{}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))
	require.Len(t, rec.bodies, 1)
	assert.Equal(t, "{\"seq\":1}\n{\"seq\":2}\n", rec.bodies[0], "corrupt line dropped; valid lines shipped")
}

func TestShipOnce_401MidShipRefreshesAndDoesNotLoseData(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	writeSegment(t, spool, seg, `{"seq":1}`+"\n")

	rec := &ingestRecorder{fail401: true}
	srv := newIngest(t, rec)
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	require.NoError(t, s.ShipOnce(context.Background()))
	// One recorded body (the refresh-retry succeeded); no data lost.
	require.Len(t, rec.bodies, 1)
	assert.Equal(t, "{\"seq\":1}\n", rec.bodies[0])
}

func TestShipOnce_TransportErrorLeavesCursorForResume(t *testing.T) {
	root := t.TempDir()
	spool := SpoolDir(root)
	seg := "events-20260723T080000-1.jsonl"
	writeSegment(t, spool, seg, `{"seq":1}`+"\n")

	// A server that always 500s → PostEvents errors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	s := newShipper(t, srv.URL, root)

	err := s.ShipOnce(context.Background())
	require.Error(t, err, "a persistent transport error must surface so the caller backs off")

	// Cursor NOT advanced past the unacked line — it will resume next pass.
	cur, _ := LoadCursor(spool)
	assert.Equal(t, int64(0), cur.Offset)
	assert.FileExists(t, filepath.Join(spool, seg), "unacked segment is never deleted")
}

func TestGzipLines_Deterministic(t *testing.T) {
	a, _ := gzipLines([][]byte{[]byte(`{"x":1}`)})
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("{\"x\":1}\n"))
	zw.Close()
	assert.Equal(t, buf.Bytes(), a)
}

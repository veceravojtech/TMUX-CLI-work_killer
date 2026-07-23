package shipper

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedStore writes a non-stale login to a fresh store so currentToken returns the
// access token without a pre-emptive refresh.
func seedStore(t *testing.T, baseURL, access, refresh string) *auth.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	store := auth.NewStoreAt(path)
	require.NoError(t, store.Save(&auth.Auth{
		APIURL:       baseURL,
		Account:      "acct",
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    time.Now().Add(1 * time.Hour), // not stale
		Scopes:       []string{"telemetry:write"},
	}))
	return store
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(out)
}

func TestPostEvents_HeadersBodyAndAck(t *testing.T) {
	var (
		gotBody     []byte
		gotHeaders  http.Header
		gotBatchIDs []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/telemetry/sessions/sess-1/events", r.URL.Path)
		gotHeaders = r.Header.Clone()
		gotBatchIDs = append(gotBatchIDs, r.Header.Get("X-Batch-Id"))
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		io.WriteString(w, `{"accepted":2,"duplicate":false}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp-abc", seedStore(t, srv.URL, "tok1", "ref1"))
	c.newBatchID = func() string { return "batch-fixed" }

	gz, err := gzipLines([][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)})
	require.NoError(t, err)
	ack, err := c.PostEvents(context.Background(), "sess-1", gz)
	require.NoError(t, err)

	assert.Equal(t, 2, ack.Accepted)
	assert.False(t, ack.Duplicate)
	assert.Equal(t, "gzip", gotHeaders.Get("Content-Encoding"))
	assert.Equal(t, "application/x-ndjson", gotHeaders.Get("Content-Type"))
	assert.Equal(t, "batch-fixed", gotHeaders.Get("X-Batch-Id"))
	assert.Equal(t, "Bearer tok1", gotHeaders.Get("Authorization"))
	assert.Equal(t, "fp-abc", gotHeaders.Get("X-Fingerprint"))
	assert.Equal(t, "{\"a\":1}\n{\"b\":2}\n", gunzip(t, gotBody))
}

func TestPostEvents_401RefreshesAndRetriesSameBatchID(t *testing.T) {
	var mu sync.Mutex
	batchIDs := []string{}
	authz := []string{}
	refreshCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			var body map[string]string
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			assert.Equal(t, "ref1", body["refresh_token"])
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			w.WriteHeader(200)
			io.WriteString(w, `{"access_token":"tok2","refresh_token":"ref2","expires_in":3600,"scopes":["telemetry:write"],"account":"acct"}`)
		case "/api/v1/telemetry/sessions/sess-1/events":
			mu.Lock()
			batchIDs = append(batchIDs, r.Header.Get("X-Batch-Id"))
			authz = append(authz, r.Header.Get("Authorization"))
			n := len(authz)
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(401) // stale token
				return
			}
			w.WriteHeader(200)
			io.WriteString(w, `{"accepted":1,"duplicate":false}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	store := seedStore(t, srv.URL, "tok1", "ref1")
	c := NewClient(srv.URL, "fp", store)
	c.newBatchID = func() string { return "batch-1" }

	gz, err := gzipLines([][]byte{[]byte(`{"e":"x"}`)})
	require.NoError(t, err)
	ack, err := c.PostEvents(context.Background(), "sess-1", gz)
	require.NoError(t, err)
	assert.Equal(t, 1, ack.Accepted)

	assert.Equal(t, 1, refreshCalls, "exactly one single-flight refresh")
	require.Len(t, batchIDs, 2, "one 401 + one retry")
	assert.Equal(t, "batch-1", batchIDs[0])
	assert.Equal(t, "batch-1", batchIDs[1], "the retry MUST reuse the same X-Batch-Id (idempotency)")
	assert.Equal(t, "Bearer tok1", authz[0])
	assert.Equal(t, "Bearer tok2", authz[1], "retry carries the rotated token")

	// The rotated pair was persisted to the store.
	stored, _ := store.Load()
	require.NotNil(t, stored)
	assert.Equal(t, "tok2", stored.AccessToken)
	assert.Equal(t, "ref2", stored.RefreshToken)
}

func TestPostEvents_SecondUnauthorizedIsLoginRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(200)
			io.WriteString(w, `{"access_token":"tok2","refresh_token":"ref2","expires_in":3600}`)
			return
		}
		w.WriteHeader(401) // always unauthorized, even after refresh
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	gz, _ := gzipLines([][]byte{[]byte(`{"e":"x"}`)})
	_, err := c.PostEvents(context.Background(), "sess-1", gz)
	assert.ErrorIs(t, err, ErrLoginRequired)
}

func TestPostEvents_InvalidGrantClearsStoreAndLoginRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	store := seedStore(t, srv.URL, "tok1", "ref1")
	c := NewClient(srv.URL, "fp", store)
	gz, _ := gzipLines([][]byte{[]byte(`{"e":"x"}`)})
	_, err := c.PostEvents(context.Background(), "sess-1", gz)
	assert.ErrorIs(t, err, ErrLoginRequired)

	stored, _ := store.Load()
	assert.Nil(t, stored, "invalid_grant must clear the store (auth.RefreshStore deletes it)")
}

func TestRegisterManifest_ByteExactBodyAndPath(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/telemetry/sessions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	m := Manifest{
		SessionID:     "sess-1",
		Project:       "cli",
		Fingerprint:   "deadbeef",
		Hostname:      "host-1",
		StartedAt:     "2026-07-23T00:00:00Z",
		BinaryVersion: "0.1.0",
	}
	require.NoError(t, c.RegisterManifest(context.Background(), m))

	assert.Equal(t, "Bearer tok1", gotAuth)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &decoded))
	assert.Equal(t, "sess-1", decoded["session_id"])
	assert.Equal(t, "cli", decoded["project"])
	assert.Equal(t, "deadbeef", decoded["fingerprint"])
	assert.Equal(t, "host-1", decoded["hostname"])
	assert.Equal(t, "2026-07-23T00:00:00Z", decoded["started_at"])
	assert.Equal(t, "0.1.0", decoded["binary_version"])
}

func TestGzipLines_RoundTrip(t *testing.T) {
	gz, err := gzipLines([][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)})
	require.NoError(t, err)
	assert.Equal(t, "{\"a\":1}\n{\"b\":2}\n", gunzip(t, gz))
	assert.Less(t, len(gz), 200)
}

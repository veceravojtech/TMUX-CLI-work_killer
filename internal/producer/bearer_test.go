package producer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/auth"
)

// loggedInStore writes an auth.json under an XDG-overridden temp config dir and
// returns a Store over it, exercising the same store-path resolution production
// uses. expiresAt controls staleness: a past/near value forces a proactive refresh.
func loggedInStore(t *testing.T, srvURL, access, refresh string, expiresAt time.Time) *auth.Store {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := auth.NewStore()
	require.NoError(t, err)
	require.NoError(t, store.Save(&auth.Auth{
		APIURL:       srvURL,
		Account:      "me@example.com",
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		Scopes:       []string{"tasks:write"},
	}))
	return store
}

// loggedOutStore returns a Store over a fresh XDG-overridden temp dir with no
// auth.json — Load yields (nil,nil), so the client falls back to signature mode.
func loggedOutStore(t *testing.T) *auth.Store {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := auth.NewStore()
	require.NoError(t, err)
	return store
}

// writeToken writes a §4/§3 token success body (the shape both refresh and the
// device poll return).
func writeToken(w http.ResponseWriter, access, refresh string, expiresIn int) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    expiresIn,
		"scopes":        []string{"tasks:write"},
		"account":       "me@example.com",
	})
}

func writeInvalidGrant(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
}

const farFuture = time.Hour

// TestBearer_HappyPath: a valid, non-stale token → the batch carries
// Authorization: Bearer <token> with NO signature headers and no refresh call.
func TestBearer_HappyPath(t *testing.T) {
	_, priv := testKeypair(t)
	var refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			t.Errorf("refresh must not be called for a fresh token")
			writeInvalidGrant(w)
		case "/api/v1/tasks":
			assert.Equal(t, "Bearer acc1", r.Header.Get("Authorization"))
			assert.Empty(t, r.Header.Get("X-Signature"), "Bearer mode must send no signature")
			assert.Empty(t, r.Header.Get("X-Timestamp"), "Bearer mode must send no timestamp")
			assert.NotEmpty(t, r.Header.Get("X-Fingerprint"), "fingerprint stays as attribution metadata")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"t1","status":"queued"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(farFuture))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "t1", resp.ID.String())
	assert.Equal(t, int32(0), atomic.LoadInt32(&refreshCalls))
}

// TestBearer_401RefreshRetrySuccess: a 401 triggers exactly one transparent
// rotation-aware refresh and one retry with the rotated token → success.
func TestBearer_401RefreshRetrySuccess(t *testing.T) {
	_, priv := testKeypair(t)
	var taskCalls, refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "ref1", body["refresh_token"], "must present the stored refresh token")
			writeToken(w, "acc2", "ref2", 3600)
		case "/api/v1/tasks":
			n := atomic.AddInt32(&taskCalls, 1)
			if n == 1 {
				assert.Equal(t, "Bearer acc1", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			assert.Equal(t, "Bearer acc2", r.Header.Get("Authorization"), "retry must use the rotated token")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"t2","status":"queued"}`)
		}
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(farFuture))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "t2", resp.ID.String())
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls), "exactly one refresh")
	assert.Equal(t, int32(2), atomic.LoadInt32(&taskCalls), "one original + one retry")

	// The rotated pair is persisted for the next batch.
	saved, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "acc2", saved.AccessToken)
	assert.Equal(t, "ref2", saved.RefreshToken)
}

// TestBearer_SecondUnauthorizedIsLoginRequired: refresh succeeds but the retry is
// STILL 401 → a typed ErrLoginRequired naming `tmux-cli login`, after exactly one
// refresh + one retry.
func TestBearer_SecondUnauthorizedIsLoginRequired(t *testing.T) {
	_, priv := testKeypair(t)
	var taskCalls, refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			writeToken(w, "acc2", "ref2", 3600)
		case "/api/v1/tasks":
			atomic.AddInt32(&taskCalls, 1)
			w.WriteHeader(http.StatusUnauthorized) // never accepts
		}
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(farFuture))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLoginRequired)
	assert.Contains(t, err.Error(), "tmux-cli login", "the error must name the remediation verbatim")
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls), "one refresh only")
	assert.Equal(t, int32(2), atomic.LoadInt32(&taskCalls), "one original + one retry, no more")
}

// TestBearer_InvalidGrantClearsStore: a stale token forces a proactive refresh
// that returns invalid_grant → the store is cleared (auth package) and a typed
// re-login error is returned before any task request is made.
func TestBearer_InvalidGrantClearsStore(t *testing.T) {
	_, priv := testKeypair(t)
	var taskCalls, refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			writeInvalidGrant(w)
		case "/api/v1/tasks":
			atomic.AddInt32(&taskCalls, 1)
		}
	}))
	defer srv.Close()

	// expires within the 120s stale window → EnsureFresh refreshes pre-emptively.
	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(30*time.Second))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLoginRequired)
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls))
	assert.Equal(t, int32(0), atomic.LoadInt32(&taskCalls), "no task request once the refresh is rejected")

	// auth package dropped the store on invalid_grant.
	_, statErr := os.Stat(store.Path())
	assert.True(t, os.IsNotExist(statErr), "auth.json must be cleared on invalid_grant")
	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Nil(t, loaded, "store reads as logged out after invalid_grant")
}

// TestBearer_LoggedOutIsByteIdenticalSigning: with no auth.json the request is the
// EXACT pre-Bearer signed shape — signature verifies over <ts><body>, and there is
// no Authorization header.
func TestBearer_LoggedOutIsByteIdenticalSigning(t *testing.T) {
	pub, priv := testKeypair(t)
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/tasks", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "logged out must NOT send a Bearer header")
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		verifySig(t, pub, r, body) // asserts X-Signature/X-Timestamp/X-Fingerprint + verifies
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"t3","status":"queued"}`)
	}))
	defer srv.Close()

	store := loggedOutStore(t)
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "t3", resp.ID.String())
	assert.NotEmpty(t, gotBody)
}

// TestBearer_SingleFlightProactiveRefresh: N parallel batches all see a stale
// token, but only ONE refresh endpoint call is made (single-flight under -race).
func TestBearer_SingleFlightProactiveRefresh(t *testing.T) {
	_, priv := testKeypair(t)
	var refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			writeToken(w, "acc2", "ref2", 3600)
		case "/api/v1/tasks":
			assert.Equal(t, "Bearer acc2", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"t","status":"queued"}`)
		}
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(30*time.Second))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.SubmitTask(context.Background(), sampleRequest())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "batch %d", i)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls),
		"parallel stale batches must trigger exactly one proactive refresh")
}

// TestBearer_SingleFlight401Refresh: N parallel batches each get a 401 on the same
// token; the on-401 refresh is single-flight — exactly one rotation, and the
// waiters reuse the rotated token rather than rotating the single-use refresh again.
func TestBearer_SingleFlight401Refresh(t *testing.T) {
	_, priv := testKeypair(t)
	var refreshCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/refresh":
			n := atomic.AddInt32(&refreshCalls, 1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			// A second rotation would present the already-rotated (invalid) token;
			// the single-flight guard must prevent it. Fail loudly if it happens.
			if body["refresh_token"] != "ref1" {
				writeInvalidGrant(w)
				return
			}
			if n > 1 {
				t.Errorf("refresh called %d times — single-flight violated", n)
			}
			writeToken(w, "acc2", "ref2", 3600)
		case "/api/v1/tasks":
			switch r.Header.Get("Authorization") {
			case "Bearer acc2":
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"id":"t","status":"queued"}`)
			default: // stale acc1
				w.WriteHeader(http.StatusUnauthorized)
			}
		}
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(farFuture))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.SubmitTask(context.Background(), sampleRequest())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "batch %d", i)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls),
		"parallel 401s must rotate the refresh token exactly once")
}

// TestBearer_UploadArtifactUsesBearer: the multipart upload path (doSignedRaw) also
// authenticates with Bearer when logged in — confirming Bearer is centralized at the
// transport, not bolted onto SubmitTask alone. A logged-out upload keeps its
// digest-signature (X-Content-SHA256) path unchanged.
func TestBearer_UploadArtifactUsesBearer(t *testing.T) {
	_, priv := testKeypair(t)
	file := filepath.Join(t.TempDir(), "log.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer acc1", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("X-Signature"), "Bearer upload sends no signature")
		assert.NotEmpty(t, r.Header.Get("X-Content-SHA256"), "digest header still advertised")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, fmt.Sprintf(`{"id":1,"filename":%q,"sha256":"x","size":5}`, "log.txt"))
	}))
	defer srv.Close()

	store := loggedInStore(t, srv.URL, "acc1", "ref1", time.Now().Add(farFuture))
	c := newClient(srv.URL, priv, srv.Client()).withAuth(store, auth.NewClient(srv.URL))

	art, err := c.UploadArtifact(context.Background(), "7", file, "log")
	require.NoError(t, err)
	require.NotNil(t, art)
	assert.Equal(t, "1", art.ID.String())
}

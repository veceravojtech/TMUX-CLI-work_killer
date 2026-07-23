package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow is a stable clock for deterministic expiry/staleness assertions.
var fixedNow = time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)

// newTestClient returns a Client pointed at baseURL whose inter-poll wait records
// the requested durations and returns immediately (no real sleeps), and whose
// clock is fixed.
func newTestClient(baseURL string, waits *[]time.Duration) *Client {
	c := NewClient(baseURL)
	c.now = func() time.Time { return fixedNow }
	c.wait = func(_ context.Context, d time.Duration) error {
		if waits != nil {
			*waits = append(*waits, d)
		}
		return nil
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestStartDeviceCode_ReturnsContractShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/auth/device/code", r.URL.Path)
		var meta ClientMeta
		require.NoError(t, json.NewDecoder(r.Body).Decode(&meta))
		assert.Equal(t, "tmux-cli", meta.Client)
		writeJSON(w, 200, DeviceCode{
			DeviceCode:      "dev-code-0123456789012345678901234567890123456789",
			UserCode:        "WDJB-MJHT",
			VerificationURI: "https://tmux.vojta.ai/device",
			ExpiresIn:       900,
			Interval:        5,
		})
	}))
	defer srv.Close()

	dc, err := newTestClient(srv.URL, nil).StartDeviceCode(context.Background(), ClientMeta{Client: "tmux-cli"})
	require.NoError(t, err)
	assert.Equal(t, "WDJB-MJHT", dc.UserCode)
	assert.Equal(t, 5, dc.Interval)
}

func TestPoll_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, Token{
			AccessToken:  "jwt-abc",
			RefreshToken: "refresh-1",
			ExpiresIn:    3600,
			Scopes:       []string{"tasks:write", "artifacts:write", "telemetry:write"},
			Account:      "user@example.test",
		})
	}))
	defer srv.Close()

	tok, err := newTestClient(srv.URL, nil).Poll(context.Background(), "dev-code", 5)
	require.NoError(t, err)
	assert.Equal(t, "user@example.test", tok.Account)
	assert.Len(t, tok.Scopes, 3)
}

func TestPoll_PendingSlowDownApproved(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			writeJSON(w, 400, errBody{Error: "authorization_pending"})
		case 2:
			writeJSON(w, 400, errBody{Error: "slow_down"})
		default:
			writeJSON(w, 200, Token{AccessToken: "jwt", RefreshToken: "r", ExpiresIn: 3600, Account: "u@e.test"})
		}
	}))
	defer srv.Close()

	var waits []time.Duration
	tok, err := newTestClient(srv.URL, &waits).Poll(context.Background(), "dev-code", 5)
	require.NoError(t, err)
	assert.Equal(t, "u@e.test", tok.Account)
	require.Len(t, waits, 2, "one wait after pending, one after slow_down")
	assert.Equal(t, 5*time.Second, waits[0], "pending keeps the base interval")
	assert.Equal(t, 10*time.Second, waits[1], "slow_down increases the interval by 5s")
}

func TestPoll_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, errBody{Error: "access_denied"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL, nil).Poll(context.Background(), "dev-code", 5)
	assert.ErrorIs(t, err, ErrAccessDenied)
}

func TestPoll_ExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, errBody{Error: "expired_token"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL, nil).Poll(context.Background(), "dev-code", 5)
	assert.ErrorIs(t, err, ErrExpiredToken)
}

func TestPoll_CtxCancelMidPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, errBody{Error: "authorization_pending"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(srv.URL)
	c.now = func() time.Time { return fixedNow }
	// Simulate Ctrl-C arriving during the inter-poll wait: cancel, then report
	// the cancellation the way realWait would.
	c.wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	_, err := c.Poll(ctx, "dev-code", 5)
	assert.ErrorIs(t, err, context.Canceled, "ctx cancel must abort the poll cleanly")
}

func TestRefresh_RotationInvalidatesOldToken(t *testing.T) {
	current := "refresh-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if body.RefreshToken != current {
			writeJSON(w, 401, errBody{Error: "invalid_grant"})
			return
		}
		current = "refresh-2" // single-use rotation
		writeJSON(w, 200, Token{AccessToken: "jwt-2", RefreshToken: "refresh-2", ExpiresIn: 3600, Account: "u@e.test"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, nil)
	tok, err := c.Refresh(context.Background(), "refresh-1")
	require.NoError(t, err)
	assert.Equal(t, "refresh-2", tok.RefreshToken)

	// The old refresh token is now invalid.
	_, err = c.Refresh(context.Background(), "refresh-1")
	assert.ErrorIs(t, err, ErrReauthRequired)
}

func TestRefreshStore_PersistsRotatedPair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, Token{AccessToken: "jwt-2", RefreshToken: "refresh-2", ExpiresIn: 3600,
			Scopes: []string{"tasks:write"}, Account: "u@e.test"})
	}))
	defer srv.Close()

	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	require.NoError(t, s.Save(&Auth{APIURL: srv.URL, Account: "u@e.test", AccessToken: "jwt-1",
		RefreshToken: "refresh-1", ExpiresAt: fixedNow.Add(time.Minute)}))

	c := newTestClient(srv.URL, nil)
	na, err := RefreshStore(context.Background(), c, s, mustLoad(t, s))
	require.NoError(t, err)
	assert.Equal(t, "refresh-2", na.RefreshToken)

	stored := mustLoad(t, s)
	assert.Equal(t, "refresh-2", stored.RefreshToken, "rotated refresh token must be persisted")
	assert.Equal(t, "jwt-2", stored.AccessToken)
	assert.True(t, fixedNow.Add(3600*time.Second).Equal(stored.ExpiresAt), "expiry re-stamped from now+expires_in")
}

func TestRefreshStore_InvalidGrantDeletesStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, errBody{Error: "invalid_grant"})
	}))
	defer srv.Close()

	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	require.NoError(t, s.Save(sampleAuth()))

	_, err := RefreshStore(context.Background(), newTestClient(srv.URL, nil), s, mustLoad(t, s))
	assert.ErrorIs(t, err, ErrReauthRequired)

	got, loadErr := s.Load()
	require.NoError(t, loadErr)
	assert.Nil(t, got, "invalid_grant must drop auth.json")
}

func TestEnsureFresh_FreshTokenNoNetwork(t *testing.T) {
	// A server that fails the test if hit — a fresh token must not refresh.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call for a fresh token: %s", r.URL.Path)
	}))
	defer srv.Close()

	a := &Auth{APIURL: srv.URL, AccessToken: "jwt", RefreshToken: "r", ExpiresAt: fixedNow.Add(time.Hour)}
	got, err := EnsureFresh(context.Background(), newTestClient(srv.URL, nil), NewStoreAt(filepath.Join(t.TempDir(), "auth.json")), a)
	require.NoError(t, err)
	assert.Same(t, a, got)
}

func TestEnsureFresh_StaleTokenRefreshes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, Token{AccessToken: "jwt-2", RefreshToken: "refresh-2", ExpiresIn: 3600, Account: "u@e.test"})
	}))
	defer srv.Close()

	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	a := &Auth{APIURL: srv.URL, AccessToken: "jwt-1", RefreshToken: "refresh-1", ExpiresAt: fixedNow.Add(30 * time.Second)}
	require.NoError(t, s.Save(a))

	got, err := EnsureFresh(context.Background(), newTestClient(srv.URL, nil), s, a)
	require.NoError(t, err)
	assert.Equal(t, "jwt-2", got.AccessToken, "stale token within 120s must refresh")
}

func TestWhoami_ReturnsAccountAndDeviceLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer jwt-abc", r.Header.Get("Authorization"))
		writeJSON(w, 200, Whoami{
			Account:     "user@example.test",
			Scopes:      []string{"tasks:write", "telemetry:write"},
			DeviceLabel: "laptop (abcdef012345)",
			CreatedAt:   "2026-07-22T16:00:00Z",
		})
	}))
	defer srv.Close()

	wi, err := newTestClient(srv.URL, nil).Whoami(context.Background(), "jwt-abc")
	require.NoError(t, err)
	assert.Equal(t, "user@example.test", wi.Account)
	assert.Equal(t, "laptop (abcdef012345)", wi.DeviceLabel)
}

func TestWhoami_UnauthorizedIsReauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL, nil).Whoami(context.Background(), "bad")
	assert.ErrorIs(t, err, ErrReauthRequired)
}

func mustLoad(t *testing.T, s *Store) *Auth {
	t.Helper()
	a, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, a)
	return a
}

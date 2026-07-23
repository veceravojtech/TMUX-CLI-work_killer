package producer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/console/tmux-cli/internal/auth"
)

// ErrLoginRequired signals the caller must run `tmux-cli login`: a Bearer request
// got a 401 that a single transparent refresh did not clear, or the refresh token
// itself was rejected (invalid_grant → the store was cleared). It is a typed
// sentinel so the MCP task-report tool can surface the exact remediation verbatim
// (errors.Is-checkable), rather than a bare "status 401".
var ErrLoginRequired = errors.New("producer: authentication required — run: tmux-cli login")

// bearerAuth is the account-auth side of the producer client: it owns the
// user-global auth store and the device-flow HTTP client, and serializes token
// refresh so parallel uploads never double-rotate the single-use refresh token
// (contract §4 rotation). It consumes internal/auth as the SOLE token authority —
// auth.EnsureFresh / auth.RefreshStore own all staleness and rotation logic; this
// type never re-derives them.
type bearerAuth struct {
	client *auth.Client
	store  *auth.Store
	// mu single-flights both the proactive (EnsureFresh) and the on-401
	// (RefreshStore) refresh: at most one refresh endpoint call is in flight, and
	// a waiter that wakes to find the store already rotated reuses the new token
	// instead of rotating again.
	mu sync.Mutex
}

// currentToken resolves the Bearer access token for a request-batch, refreshing
// pre-emptively (contract: expires_at − now < 120s) under the single-flight lock.
// It returns ("", nil) when logged out — the caller falls back to Ed25519 signing
// (byte-identical to the pre-Bearer client). A refresh failure (e.g. invalid_grant
// → the store is cleared by auth.RefreshStore) returns ("", auth.ErrReauthRequired),
// which the send path maps to ErrLoginRequired.
func (b *bearerAuth) currentToken(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, err := b.store.Load()
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", nil // logged out → signature mode
	}
	fresh, err := auth.EnsureFresh(ctx, b.client, b.store, a)
	if err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}

// refreshAfter401 forces one rotation-aware refresh after a Bearer 401,
// single-flight. staleToken is the access token that got the 401: if another
// goroutine already rotated to a different token while this call waited on the
// lock, it returns that newer token WITHOUT a second refresh (a second rotation of
// the now-invalidated refresh token would itself fail). A cleared store (concurrent
// logout / invalid_grant) yields auth.ErrReauthRequired.
func (b *bearerAuth) refreshAfter401(ctx context.Context, staleToken string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, err := b.store.Load()
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", auth.ErrReauthRequired
	}
	if a.AccessToken != staleToken {
		// Another goroutine already refreshed under the lock; reuse its token.
		return a.AccessToken, nil
	}
	na, err := auth.RefreshStore(ctx, b.client, b.store, a)
	if err != nil {
		return "", err
	}
	return na.AccessToken, nil
}

// authorizeAndDo stamps auth onto req and sends it. In Bearer mode it sets
// Authorization: Bearer <token> and, on a 401, performs exactly ONE transparent
// refresh + ONE retry; a second 401 returns ErrLoginRequired. In signature mode
// (logged out, or no auth configured) it signs over signBody and sets
// X-Signature/X-Timestamp — byte-identical to the pre-Bearer client — and sends
// once with no retry. X-Fingerprint is set in BOTH modes: it stays as attribution
// metadata (design §3), never identity. rebuild recreates the request (and its
// one-shot body reader) for the single Bearer retry.
func (c *Client) authorizeAndDo(ctx context.Context, req *http.Request, rebuild func() (*http.Request, error), signBody []byte) (*http.Response, error) {
	req.Header.Set("X-Fingerprint", c.fingerprint)

	// Mode selection per request-batch (design §3): a valid/refreshable token in
	// the store → Bearer; logged out → legacy signing.
	var token string
	if c.auth != nil {
		t, err := c.auth.currentToken(ctx)
		if err != nil {
			return nil, loginErr(err)
		}
		token = t
	}

	if token == "" {
		// Signature mode — the exact bytes and headers of the pre-Bearer client.
		ts := time.Now().Unix()
		req.Header.Set("X-Signature", c.sign(ts, signBody))
		req.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
		return c.httpClient.Do(req)
	}

	// Bearer mode.
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	// 401 → one transparent refresh + one retry.
	drain(resp)
	newTok, rerr := c.auth.refreshAfter401(ctx, token)
	if rerr != nil {
		return nil, loginErr(rerr)
	}
	req2, berr := rebuild()
	if berr != nil {
		return nil, berr
	}
	req2.Header.Set("X-Fingerprint", c.fingerprint)
	req2.Header.Set("Authorization", "Bearer "+newTok)
	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode == http.StatusUnauthorized {
		drain(resp2)
		return nil, ErrLoginRequired
	}
	return resp2, nil
}

// loginErr maps auth's re-auth sentinel to the producer-level ErrLoginRequired so
// callers (and the MCP task-report tool) get one typed error naming `tmux-cli
// login`. Any other error (transport, I/O) passes through unchanged.
func loginErr(err error) error {
	if errors.Is(err, auth.ErrReauthRequired) {
		return ErrLoginRequired
	}
	return err
}

// drain reads and closes a response body so its connection can be reused before
// the request is retried or discarded.
func drain(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

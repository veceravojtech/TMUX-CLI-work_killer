package shipper

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/google/uuid"
)

// MaxCompressedBatchBytes caps a single events batch's COMPRESSED size at 5 MiB
// (contract §Ingest "gzip JSONL batch ≤ 5 MiB compressed").
const MaxCompressedBatchBytes = 5 << 20

// maxRespBytes bounds how much of an ingest response body is read — plenty for
// the small JSON ack shapes here while bounding a hostile/huge body.
const maxRespBytes = 1 << 20

// ErrLoginRequired signals the shipper must pause until the user re-authenticates:
// a Bearer request got a 401 that a single transparent refresh did not clear, or
// the refresh token itself was rejected (invalid_grant → the store was cleared).
var ErrLoginRequired = errors.New("shipper: authentication required — run: tmux-cli login")

// authRefresher is the account-auth side of the shipper client: it owns the
// user-global auth store and the device-flow HTTP client and serializes token
// refresh so a 401 mid-ship never double-rotates the single-use refresh token
// (contract §4 rotation). It mirrors producer.bearerAuth — internal/auth
// (EnsureFresh / RefreshStore) is the SOLE token authority; this type never
// re-derives staleness or rotation.
type authRefresher struct {
	client *auth.Client
	store  *auth.Store
	mu     sync.Mutex
}

// currentToken resolves the Bearer access token, refreshing pre-emptively when
// the stored token is within the staleness window, under the single-flight lock.
// It returns ("", nil) when logged out (the shipper only runs past gating when
// logged in, but a concurrent logout is tolerated). A refresh failure maps to
// auth.ErrReauthRequired, surfaced upward as ErrLoginRequired.
func (a *authRefresher) currentToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, err := a.store.Load()
	if err != nil {
		return "", err
	}
	if stored == nil {
		return "", nil
	}
	fresh, err := auth.EnsureFresh(ctx, a.client, a.store, stored)
	if err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}

// refreshAfter401 forces one rotation-aware refresh after a Bearer 401,
// single-flight. staleToken is the access token that got the 401: a waiter that
// wakes to find another goroutine already rotated reuses the newer token WITHOUT
// a second refresh (a second rotation of the now-invalidated refresh token would
// itself fail). A cleared store (concurrent logout / invalid_grant) yields
// auth.ErrReauthRequired.
func (a *authRefresher) refreshAfter401(ctx context.Context, staleToken string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, err := a.store.Load()
	if err != nil {
		return "", err
	}
	if stored == nil {
		return "", auth.ErrReauthRequired
	}
	if stored.AccessToken != staleToken {
		return stored.AccessToken, nil
	}
	rotated, err := auth.RefreshStore(ctx, a.client, a.store, stored)
	if err != nil {
		return "", err
	}
	return rotated.AccessToken, nil
}

// Client POSTs telemetry manifests and gzip event batches to the backend ingest,
// authenticating with a Bearer token (transparent single-flight refresh on 401).
type Client struct {
	baseURL     string
	httpClient  *http.Client
	fingerprint string
	auth        *authRefresher
	// newBatchID mints a per-batch idempotency key; a field so tests get
	// deterministic X-Batch-Id values.
	newBatchID func() string
}

// NewClient builds a shipper Client against baseURL, using store as the auth
// source and fingerprint as attribution metadata. baseURL's trailing slash is
// trimmed. The auth.Client is constructed against the SAME baseURL so refresh
// hits the account endpoints on the configured backend.
func NewClient(baseURL, fingerprint string, store *auth.Store) *Client {
	trimmed := strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL:     trimmed,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		fingerprint: fingerprint,
		auth: &authRefresher{
			client: auth.NewClient(trimmed),
			store:  store,
		},
		newBatchID: func() string { return uuid.NewString() },
	}
}

// Manifest is the POST /api/v1/telemetry/sessions body. JSON keys are byte-exact
// with the contract (session_id, project, fingerprint, hostname, started_at
// RFC3339, binary_version).
type Manifest struct {
	SessionID     string `json:"session_id"`
	Project       string `json:"project"`
	Fingerprint   string `json:"fingerprint"`
	Hostname      string `json:"hostname"`
	StartedAt     string `json:"started_at"`
	BinaryVersion string `json:"binary_version"`
}

// RegisterManifest performs the idempotent session-manifest upsert (contract
// POST /api/v1/telemetry/sessions → 200 {"ok":true}). A 401 triggers exactly one
// transparent refresh + retry; a second 401 returns ErrLoginRequired.
func (c *Client) RegisterManifest(ctx context.Context, m Manifest) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost,
			c.baseURL+"/api/v1/telemetry/sessions", bytes.NewReader(body))
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shipper: manifest register returned status %d", resp.StatusCode)
	}
	return nil
}

// EventsAck is the POST .../events response (contract → 200
// {"accepted":<n>,"duplicate":bool} or {"rejected":<n>} on a partially-malformed
// batch — rejected lines never fail the batch).
type EventsAck struct {
	Accepted  int  `json:"accepted"`
	Duplicate bool `json:"duplicate"`
	Rejected  int  `json:"rejected"`
}

// PostEvents ships one gzip-compressed NDJSON batch to
// /api/v1/telemetry/sessions/{session_id}/events with the contract headers. The
// X-Batch-Id idempotency key is minted once and REUSED across the 401-refresh
// retry so a replay is deduped server-side rather than double-counted. A second
// 401 returns ErrLoginRequired.
func (c *Client) PostEvents(ctx context.Context, sessionID string, gzBody []byte) (EventsAck, error) {
	batchID := c.newBatchID()
	url := c.baseURL + "/api/v1/telemetry/sessions/" + sessionID + "/events"
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(gzBody))
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Content-Type", "application/x-ndjson")
		req.Header.Set("X-Batch-Id", batchID)
		return req, nil
	})
	if err != nil {
		return EventsAck{}, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return EventsAck{}, fmt.Errorf("shipper: events post returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return EventsAck{}, err
	}
	var ack EventsAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return EventsAck{}, err
	}
	return ack, nil
}

// doWithRefresh stamps auth onto a freshly-built request and sends it, performing
// exactly ONE transparent refresh + retry on a 401. build receives the resolved
// token (already set as the Bearer header by the caller-agnostic wrapper) and
// must return a NEW request each call so the retry gets a fresh one-shot body
// reader. X-Fingerprint is always set (attribution metadata, design §3).
func (c *Client) doWithRefresh(ctx context.Context, build func(token string) (*http.Request, error)) (*http.Response, error) {
	token, err := c.auth.currentToken(ctx)
	if err != nil {
		return nil, loginErr(err)
	}
	req, err := build(token)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Fingerprint", c.fingerprint)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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
	req2, berr := build(newTok)
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

// gzipLines compresses the NDJSON body formed by joining lines with '\n' (each
// line a complete JSON object) plus a trailing newline. It returns the compressed
// bytes; callers size batches so the result stays ≤ MaxCompressedBatchBytes.
func gzipLines(lines [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for _, ln := range lines {
		if _, err := gz.Write(ln); err != nil {
			return nil, err
		}
		if _, err := gz.Write([]byte{'\n'}); err != nil {
			return nil, err
		}
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// loginErr maps auth's re-auth sentinel to the shipper-level ErrLoginRequired so
// the ship loop can pause on it; any other error passes through unchanged.
func loginErr(err error) error {
	if errors.Is(err, auth.ErrReauthRequired) {
		return ErrLoginRequired
	}
	return err
}

// drain reads and closes a response body so its connection can be reused.
func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

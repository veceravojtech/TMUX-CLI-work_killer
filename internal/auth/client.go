package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxBodyBytes caps how much of a response body is read — plenty for the small
// JSON shapes here while bounding a hostile/huge body.
const maxBodyBytes = 1 << 20

// Client is the HTTP transport for the device-code flow, refresh, and whoami. It
// holds no secrets and no store state — the store-aware orchestration lives in
// EnsureFresh / RefreshStore (session.go).
type Client struct {
	baseURL    string
	httpClient *http.Client
	// wait sleeps d, aborting early if ctx is cancelled. Field (not a bare
	// time.Sleep) so Poll's inter-poll delay is injectable in tests without real
	// multi-second waits, and so Ctrl-C aborts a poll cleanly.
	wait func(ctx context.Context, d time.Duration) error
	// now is the clock, injectable for deterministic staleness/expiry tests.
	now func() time.Time
}

// NewClient builds a Client against baseURL (trailing slash trimmed). Per-request
// timeout is generous so a slow approval poll is bounded by ctx, not the client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		wait:       realWait,
		now:        time.Now,
	}
}

// realWait is the production inter-poll delay: a ctx-cancellable timer.
func realWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errBody is the RFC-8628 error envelope ({"error":"..."}).
type errBody struct {
	Error string `json:"error"`
}

func parseError(data []byte) string {
	var e errBody
	_ = json.Unmarshal(data, &e)
	return e.Error
}

// do issues one request and returns (status, body, transportErr). A non-2xx
// status is NOT an error here — callers branch on status/error-code themselves.
func (c *Client) do(ctx context.Context, method, path string, body any, bearer string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// StartDeviceCode registers this machine and returns the device/user codes (§1).
func (c *Client) StartDeviceCode(ctx context.Context, meta ClientMeta) (*DeviceCode, error) {
	status, data, err := c.do(ctx, http.MethodPost, "/api/v1/auth/device/code", meta, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("auth: device code request returned status %d: %s", status, snippet(data))
	}
	var dc DeviceCode
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// Poll polls the device token endpoint until a terminal outcome (§3): success
// returns the Token; authorization_pending keeps polling at interval; slow_down
// increases interval by 5s; access_denied → ErrAccessDenied; expired_token →
// ErrExpiredToken. Ctx cancellation aborts cleanly (returns ctx.Err()), leaving
// no partial state. interval is seconds; a non-positive value defaults to 5.
func (c *Client) Poll(ctx context.Context, deviceCode string, interval int) (*Token, error) {
	if interval <= 0 {
		interval = 5
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, data, err := c.do(ctx, http.MethodPost, "/api/v1/auth/device/token",
			map[string]string{"device_code": deviceCode}, "")
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			var t Token
			if err := json.Unmarshal(data, &t); err != nil {
				return nil, err
			}
			return &t, nil
		}
		if status != http.StatusBadRequest {
			return nil, fmt.Errorf("auth: device token poll returned status %d: %s", status, snippet(data))
		}
		switch code := parseError(data); code {
		case "authorization_pending":
			// keep interval, poll again after the wait
		case "slow_down":
			interval += 5
		case "access_denied":
			return nil, ErrAccessDenied
		case "expired_token":
			return nil, ErrExpiredToken
		default:
			return nil, fmt.Errorf("auth: unexpected device token error %q", code)
		}
		if err := c.wait(ctx, time.Duration(interval)*time.Second); err != nil {
			return nil, err
		}
	}
}

// Refresh exchanges a refresh token for a rotated Token (§4). It is pure
// transport: on 401 invalid_grant it returns ErrReauthRequired but does NOT touch
// the store — RefreshStore layers the store side effects on top.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	status, data, err := c.do(ctx, http.MethodPost, "/api/v1/auth/token/refresh",
		map[string]string{"refresh_token": refreshToken}, "")
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		var t Token
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return &t, nil
	}
	if status == http.StatusUnauthorized && parseError(data) == "invalid_grant" {
		return nil, ErrReauthRequired
	}
	return nil, fmt.Errorf("auth: token refresh returned status %d: %s", status, snippet(data))
}

// Whoami fetches the account/scopes/device-label for a Bearer access token (§5).
// A 401 maps to ErrReauthRequired.
func (c *Client) Whoami(ctx context.Context, accessToken string) (*Whoami, error) {
	status, data, err := c.do(ctx, http.MethodGet, "/api/v1/auth/whoami", nil, accessToken)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		var w Whoami
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return &w, nil
	}
	if status == http.StatusUnauthorized {
		return nil, ErrReauthRequired
	}
	return nil, fmt.Errorf("auth: whoami returned status %d: %s", status, snippet(data))
}

// snippet returns a short, single-line view of an error body for diagnostics —
// never used on success bodies, so no secret material is logged.
func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

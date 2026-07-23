package shipper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ReviewSchemaVersion is the report schema this client understands (contract §1).
// A report carrying a HIGHER version is still returned parsed — consumers render
// summary + Raw instead of the structured sections (forward-compat, never crash).
const ReviewSchemaVersion = 1

// ErrUnknownSession maps the review trigger's 404 {"error":"unknown session"} —
// a review over a nonexistent session is meaningless (contract §2, no stub
// auto-register on this endpoint).
var ErrUnknownSession = errors.New("shipper: unknown session")

// ErrNoReview maps the review fetch's 404 {"error":"no review"} — nothing has
// been generated yet; callers treat it as "run the trigger".
var ErrNoReview = errors.New("shipper: no review generated yet")

// Review is the §1 review-report JSON shape, byte-exact with the P4 contract.
// Raw preserves the verbatim response body for --json output and for rendering
// a report whose schema_version is newer than ReviewSchemaVersion.
type Review struct {
	SchemaVersion int                `json:"schema_version"`
	SessionID     string             `json:"session_id"`
	Project       string             `json:"project"`
	GeneratedAt   string             `json:"generated_at"`
	Summary       ReviewSummary      `json:"summary"`
	Agents        []ReviewAgent      `json:"agents"`
	Phases        []ReviewPhase      `json:"phases"`
	Anomalies     []ReviewAnomaly    `json:"anomalies"`
	Suggestions   []ReviewSuggestion `json:"suggestions"`

	Raw json.RawMessage `json:"-"`
}

// ReviewSummary is the always-present summary object (contract §1).
type ReviewSummary struct {
	StartedAt          string      `json:"started_at"`
	EndedAt            string      `json:"ended_at"`
	DurationSec        float64     `json:"duration_sec"`
	Windows            int         `json:"windows"`
	EventsTotal        int         `json:"events_total"`
	TranscriptSegments int         `json:"transcript_segments"`
	Goals              ReviewGoals `json:"goals"`
	Retries            int         `json:"retries"`
	Bounces            int         `json:"bounces"`
	Escalations        int         `json:"escalations"`
}

// ReviewGoals is the summary.goals counter triple.
type ReviewGoals struct {
	Total  int `json:"total"`
	Done   int `json:"done"`
	Failed int `json:"failed"`
}

// ReviewAgent is one per-window agent row; kind ∈
// {supervisor,worker,daemon,other} re-derived server-side from the window name.
type ReviewAgent struct {
	Window             string `json:"window"`
	Kind               string `json:"kind"`
	Events             int    `json:"events"`
	FirstTS            string `json:"first_ts"`
	LastTS             string `json:"last_ts"`
	TranscriptSegments int    `json:"transcript_segments"`
	Summary            string `json:"summary"`
}

// ReviewPhase is one taskvisor-phase timing row. The fleet baselines are
// pointers because the contract pins `null` when the cross-session sample is
// below the min-sample threshold.
type ReviewPhase struct {
	Phase       string   `json:"phase"`
	Count       int      `json:"count"`
	P50Sec      float64  `json:"p50_sec"`
	P90Sec      float64  `json:"p90_sec"`
	FleetP50Sec *float64 `json:"fleet_p50_sec"`
	FleetP90Sec *float64 `json:"fleet_p90_sec"`
	OverCeiling bool     `json:"over_ceiling"`
}

// ReviewAnomaly is one detected anomaly (severity ∈ {info,warn,error}).
type ReviewAnomaly struct {
	Type             string `json:"type"`
	Severity         string `json:"severity"`
	Window           string `json:"window"`
	Detail           string `json:"detail"`
	EvidenceEventIDs []int  `json:"evidence_event_ids"`
}

// ReviewSuggestion is one actionable suggestion (priority ∈ {low,medium,high}).
type ReviewSuggestion struct {
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Detail   string `json:"detail"`
}

// SessionSummary is one row of GET /api/v1/telemetry/sessions (contract §2),
// byte-exact JSON keys.
type SessionSummary struct {
	SessionID          string `json:"session_id"`
	Project            string `json:"project"`
	Fingerprint        string `json:"fingerprint"`
	Hostname           string `json:"hostname"`
	StartedAt          string `json:"started_at"`
	Windows            int    `json:"windows"`
	Events             int    `json:"events"`
	TranscriptSegments int    `json:"transcript_segments"`
	HasReview          bool   `json:"has_review"`
	Stub               bool   `json:"stub"`
}

// ListSessions fetches this device's sessions (scoped server-side by the request
// X-Fingerprint), optionally filtered by project, newest started_at first. Same
// auth discipline as PostEvents: doWithRefresh, one transparent 401 refresh.
func (c *Client) ListSessions(ctx context.Context, project string) ([]SessionSummary, error) {
	endpoint := c.baseURL + "/api/v1/telemetry/sessions"
	if project != "" {
		endpoint += "?project=" + url.QueryEscape(project)
	}
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	})
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shipper: sessions list returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Sessions []SessionSummary `json:"sessions"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Sessions == nil {
		return []SessionSummary{}, nil
	}
	return wrapper.Sessions, nil
}

// PostReview triggers review generation for a session (contract §2 POST
// .../review). force=false returns the cached report when one exists;
// force=true always regenerates. Generation is synchronous — the response body
// IS the report. A 404 maps to ErrUnknownSession.
func (c *Client) PostReview(ctx context.Context, sessionID string, force bool) (Review, error) {
	body, err := json.Marshal(map[string]bool{"force": force})
	if err != nil {
		return Review{}, err
	}
	endpoint := c.baseURL + "/api/v1/telemetry/sessions/" + sessionID + "/review"
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return Review{}, err
	}
	defer drain(resp)
	return decodeReviewResponse(resp, ErrUnknownSession)
}

// GetReview fetches the stored report without triggering generation (contract §2
// GET .../review). A 404 maps to ErrNoReview ("run the trigger").
func (c *Client) GetReview(ctx context.Context, sessionID string) (Review, error) {
	endpoint := c.baseURL + "/api/v1/telemetry/sessions/" + sessionID + "/review"
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	})
	if err != nil {
		return Review{}, err
	}
	defer drain(resp)
	return decodeReviewResponse(resp, ErrNoReview)
}

// decodeReviewResponse maps the shared review-response statuses: 200 parses the
// §1 report (keeping the verbatim body in Raw), 404 returns the endpoint's
// sentinel, anything else is a status error.
func decodeReviewResponse(resp *http.Response, notFound error) (Review, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		// parsed below
	case http.StatusNotFound:
		return Review{}, notFound
	default:
		return Review{}, fmt.Errorf("shipper: review request returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return Review{}, err
	}
	var r Review
	if err := json.Unmarshal(data, &r); err != nil {
		return Review{}, err
	}
	r.Raw = data
	return r, nil
}

package shipper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleReportJSON is the contract §1 example verbatim (p4-review-contract.md) —
// tests parse it to prove the Go structs are byte-exact with the pin.
const sampleReportJSON = `{
  "schema_version": 1,
  "session_id": "sess-1",
  "project": "cli",
  "generated_at": "2026-07-23T12:00:00Z",
  "summary": {
    "started_at": "2026-07-23T11:00:00Z",
    "ended_at": "2026-07-23T11:30:30Z",
    "duration_sec": 1830.0,
    "windows": 5,
    "events_total": 123,
    "transcript_segments": 45,
    "goals": { "total": 3, "done": 2, "failed": 1 },
    "retries": 4,
    "bounces": 1,
    "escalations": 0
  },
  "agents": [
    {
      "window": "supervisor",
      "kind": "supervisor",
      "events": 40,
      "first_ts": "2026-07-23T11:00:01Z",
      "last_ts": "2026-07-23T11:30:10Z",
      "transcript_segments": 12,
      "summary": "spawned 3 workers, 1 pushback, all accepted"
    }
  ],
  "phases": [
    {
      "phase": "supervising",
      "count": 3,
      "p50_sec": 120.0,
      "p90_sec": 200.0,
      "fleet_p50_sec": 110.0,
      "fleet_p90_sec": 180.0,
      "over_ceiling": false
    }
  ],
  "anomalies": [
    {
      "type": "missing_successor",
      "severity": "warn",
      "window": "supervisor",
      "detail": "marker armed → no consume event within session",
      "evidence_event_ids": [17, 18]
    }
  ],
  "suggestions": [
    { "title": "Fresh-handoff marker stranded", "priority": "high",
      "detail": "arm→consume gap on execute-2; check Stop-hook delivery" }
  ]
}`

func TestListSessions_QueryHeadersAndParse(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotFP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotFP = r.Header.Get("X-Fingerprint")
		w.WriteHeader(200)
		io.WriteString(w, `{"sessions":[
			{"session_id":"sess-2","project":"cli","fingerprint":"ab12","hostname":"box",
			 "started_at":"2026-07-23T12:00:00Z","windows":2,"events":10,"transcript_segments":3,
			 "has_review":false,"stub":false},
			{"session_id":"sess-1","project":"cli","fingerprint":"ab12","hostname":"box",
			 "started_at":"2026-07-23T11:00:00Z","windows":5,"events":123,"transcript_segments":45,
			 "has_review":true,"stub":false}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp-abc", seedStore(t, srv.URL, "tok1", "ref1"))
	sessions, err := c.ListSessions(context.Background(), "cli")
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/telemetry/sessions", gotPath)
	assert.Equal(t, "project=cli", gotQuery)
	assert.Equal(t, "Bearer tok1", gotAuth)
	assert.Equal(t, "fp-abc", gotFP)
	require.Len(t, sessions, 2)
	assert.Equal(t, "sess-2", sessions[0].SessionID)
	assert.Equal(t, "cli", sessions[0].Project)
	assert.Equal(t, "2026-07-23T12:00:00Z", sessions[0].StartedAt)
	assert.Equal(t, 2, sessions[0].Windows)
	assert.Equal(t, 10, sessions[0].Events)
	assert.Equal(t, 3, sessions[0].TranscriptSegments)
	assert.False(t, sessions[0].HasReview)
	assert.True(t, sessions[1].HasReview)
	assert.Equal(t, "box", sessions[1].Hostname)
	assert.Equal(t, "ab12", sessions[1].Fingerprint)
	assert.False(t, sessions[1].Stub)
}

func TestListSessions_NoProjectOmitsQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		io.WriteString(w, `{"sessions":[]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	sessions, err := c.ListSessions(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, gotQuery)
	assert.NotNil(t, sessions, "empty list is [], never nil")
	assert.Len(t, sessions, 0)
}

func TestPostReview_BodyHeadersAndParse(t *testing.T) {
	var gotBody []byte
	var gotCT, gotFP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/telemetry/sessions/sess-1/review", r.URL.Path)
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		gotFP = r.Header.Get("X-Fingerprint")
		w.WriteHeader(200)
		io.WriteString(w, sampleReportJSON)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp-abc", seedStore(t, srv.URL, "tok1", "ref1"))
	r, err := c.PostReview(context.Background(), "sess-1", true)
	require.NoError(t, err)

	assert.JSONEq(t, `{"force":true}`, string(gotBody))
	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, "fp-abc", gotFP)

	assert.Equal(t, 1, r.SchemaVersion)
	assert.Equal(t, "sess-1", r.SessionID)
	assert.Equal(t, "cli", r.Project)
	assert.Equal(t, "2026-07-23T12:00:00Z", r.GeneratedAt)
	assert.Equal(t, "2026-07-23T11:00:00Z", r.Summary.StartedAt)
	assert.Equal(t, "2026-07-23T11:30:30Z", r.Summary.EndedAt)
	assert.Equal(t, 1830.0, r.Summary.DurationSec)
	assert.Equal(t, 5, r.Summary.Windows)
	assert.Equal(t, 123, r.Summary.EventsTotal)
	assert.Equal(t, 45, r.Summary.TranscriptSegments)
	assert.Equal(t, 3, r.Summary.Goals.Total)
	assert.Equal(t, 2, r.Summary.Goals.Done)
	assert.Equal(t, 1, r.Summary.Goals.Failed)
	assert.Equal(t, 4, r.Summary.Retries)
	assert.Equal(t, 1, r.Summary.Bounces)
	assert.Equal(t, 0, r.Summary.Escalations)

	require.Len(t, r.Agents, 1)
	assert.Equal(t, "supervisor", r.Agents[0].Window)
	assert.Equal(t, "supervisor", r.Agents[0].Kind)
	assert.Equal(t, 40, r.Agents[0].Events)
	assert.Equal(t, "2026-07-23T11:00:01Z", r.Agents[0].FirstTS)
	assert.Equal(t, "2026-07-23T11:30:10Z", r.Agents[0].LastTS)
	assert.Equal(t, 12, r.Agents[0].TranscriptSegments)
	assert.Equal(t, "spawned 3 workers, 1 pushback, all accepted", r.Agents[0].Summary)

	require.Len(t, r.Phases, 1)
	assert.Equal(t, "supervising", r.Phases[0].Phase)
	assert.Equal(t, 3, r.Phases[0].Count)
	assert.Equal(t, 120.0, r.Phases[0].P50Sec)
	assert.Equal(t, 200.0, r.Phases[0].P90Sec)
	require.NotNil(t, r.Phases[0].FleetP50Sec)
	assert.Equal(t, 110.0, *r.Phases[0].FleetP50Sec)
	require.NotNil(t, r.Phases[0].FleetP90Sec)
	assert.Equal(t, 180.0, *r.Phases[0].FleetP90Sec)
	assert.False(t, r.Phases[0].OverCeiling)

	require.Len(t, r.Anomalies, 1)
	assert.Equal(t, "missing_successor", r.Anomalies[0].Type)
	assert.Equal(t, "warn", r.Anomalies[0].Severity)
	assert.Equal(t, "supervisor", r.Anomalies[0].Window)
	assert.Equal(t, []int{17, 18}, r.Anomalies[0].EvidenceEventIDs)

	require.Len(t, r.Suggestions, 1)
	assert.Equal(t, "Fresh-handoff marker stranded", r.Suggestions[0].Title)
	assert.Equal(t, "high", r.Suggestions[0].Priority)

	// Raw carries the verbatim response body for --json / forward-compat render.
	assert.JSONEq(t, sampleReportJSON, string(r.Raw))
}

func TestPostReview_NullFleetBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"schema_version":1,"session_id":"s","project":"p",
			"generated_at":"2026-07-23T12:00:00Z",
			"summary":{"started_at":"","ended_at":"","duration_sec":0,"windows":0,
			"events_total":0,"transcript_segments":0,"goals":{"total":0,"done":0,"failed":0},
			"retries":0,"bounces":0,"escalations":0},
			"agents":[],
			"phases":[{"phase":"validating","count":1,"p50_sec":10.0,"p90_sec":11.0,
			"fleet_p50_sec":null,"fleet_p90_sec":null,"over_ceiling":false}],
			"anomalies":[],"suggestions":[]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	r, err := c.PostReview(context.Background(), "s", false)
	require.NoError(t, err)
	require.Len(t, r.Phases, 1)
	assert.Nil(t, r.Phases[0].FleetP50Sec)
	assert.Nil(t, r.Phases[0].FleetP90Sec)
}

func TestPostReview_ForceFalseBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		io.WriteString(w, sampleReportJSON)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	_, err := c.PostReview(context.Background(), "sess-1", false)
	require.NoError(t, err)
	assert.JSONEq(t, `{"force":false}`, string(gotBody))
}

func TestPostReview_404IsErrUnknownSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":"unknown session"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	_, err := c.PostReview(context.Background(), "nope", false)
	assert.ErrorIs(t, err, ErrUnknownSession)
}

func TestPostReview_401RefreshesAndRetries(t *testing.T) {
	var mu sync.Mutex
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
		case "/api/v1/telemetry/sessions/sess-1/review":
			mu.Lock()
			authz = append(authz, r.Header.Get("Authorization"))
			n := len(authz)
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(401)
				return
			}
			// The retry must carry a fresh body reader.
			data, _ := io.ReadAll(r.Body)
			assert.JSONEq(t, `{"force":true}`, string(data))
			w.WriteHeader(200)
			io.WriteString(w, sampleReportJSON)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	r, err := c.PostReview(context.Background(), "sess-1", true)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", r.SessionID)
	assert.Equal(t, 1, refreshCalls)
	require.Len(t, authz, 2)
	assert.Equal(t, "Bearer tok1", authz[0])
	assert.Equal(t, "Bearer tok2", authz[1])
}

func TestPostReview_SecondUnauthorizedIsLoginRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(200)
			io.WriteString(w, `{"access_token":"tok2","refresh_token":"ref2","expires_in":3600}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	_, err := c.PostReview(context.Background(), "sess-1", false)
	assert.ErrorIs(t, err, ErrLoginRequired)
}

func TestGetReview_FetchStored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/telemetry/sessions/sess-1/review", r.URL.Path)
		w.WriteHeader(200)
		io.WriteString(w, sampleReportJSON)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	r, err := c.GetReview(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", r.SessionID)
}

func TestGetReview_404IsErrNoReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":"no review"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp", seedStore(t, srv.URL, "tok1", "ref1"))
	_, err := c.GetReview(context.Background(), "sess-1")
	assert.ErrorIs(t, err, ErrNoReview)
}

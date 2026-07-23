package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/shipper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewSampleReport mirrors the contract §1 example (p4-review-contract.md) —
// the render tests consume exactly what the generate worker produces.
const reviewSampleReport = `{
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
    { "window": "supervisor", "kind": "supervisor", "events": 40,
      "first_ts": "2026-07-23T11:00:01Z", "last_ts": "2026-07-23T11:30:10Z",
      "transcript_segments": 12, "summary": "spawned 3 workers, 1 pushback, all accepted" }
  ],
  "phases": [
    { "phase": "supervising", "count": 3, "p50_sec": 120.0, "p90_sec": 200.0,
      "fleet_p50_sec": 110.0, "fleet_p90_sec": 180.0, "over_ceiling": false }
  ],
  "anomalies": [
    { "type": "missing_successor", "severity": "warn", "window": "supervisor",
      "detail": "marker armed → no consume event within session", "evidence_event_ids": [17, 18] }
  ],
  "suggestions": [
    { "title": "Fresh-handoff marker stranded", "priority": "high",
      "detail": "arm→consume gap on execute-2; check Stop-hook delivery" }
  ]
}`

const reviewSessionsList = `{"sessions":[
  {"session_id":"sess-2","project":"cli","fingerprint":"fp","hostname":"box",
   "started_at":"2026-07-23T12:00:00Z","windows":2,"events":10,"transcript_segments":3,
   "has_review":false,"stub":false},
  {"session_id":"sess-1","project":"cli","fingerprint":"fp","hostname":"box",
   "started_at":"2026-07-23T11:00:00Z","windows":5,"events":123,"transcript_segments":45,
   "has_review":true,"stub":false}
]}`

// seedReviewStore writes a fresh, non-stale login so the client's Bearer path
// works without a pre-emptive refresh (mirror of shipper's client_test seedStore).
func seedReviewStore(t *testing.T, baseURL string) *auth.Store {
	t.Helper()
	store := auth.NewStoreAt(filepath.Join(t.TempDir(), "auth.json"))
	require.NoError(t, store.Save(&auth.Auth{
		APIURL:       baseURL,
		Account:      "acct",
		AccessToken:  "tok1",
		RefreshToken: "ref1",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		Scopes:       []string{"telemetry:write"},
	}))
	return store
}

func newReviewTestClient(t *testing.T, srv *httptest.Server) *shipper.Client {
	t.Helper()
	return shipper.NewClient(srv.URL, "fp", seedReviewStore(t, srv.URL))
}

func TestSessionList_TableRender(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/telemetry/sessions", r.URL.Path)
		assert.Equal(t, "cli", r.URL.Query().Get("project"))
		w.WriteHeader(200)
		io.WriteString(w, reviewSessionsList)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionList(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", false)
	assert.Equal(t, 0, code)

	got := out.String()
	// Header + one line per session.
	assert.Contains(t, got, "SESSION_ID")
	assert.Contains(t, got, "PROJECT")
	assert.Contains(t, got, "STARTED")
	assert.Contains(t, got, "WINDOWS")
	assert.Contains(t, got, "EVENTS")
	assert.Contains(t, got, "REVIEW")
	assert.Contains(t, got, "sess-2")
	assert.Contains(t, got, "sess-1")
	assert.Contains(t, got, "2026-07-23T12:00:00Z")
	assert.Contains(t, got, "yes") // has_review on sess-1
	lines := strings.Split(strings.TrimSpace(got), "\n")
	assert.Len(t, lines, 3, "header + 2 rows")
}

func TestSessionList_EmptyPrintsNoSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"sessions":[]}`)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionList(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", false)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "no sessions")
}

func TestSessionList_JSONPrintsRawShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, reviewSessionsList)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionList(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", true)
	assert.Equal(t, 0, code)

	var decoded struct {
		Sessions []map[string]any `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Len(t, decoded.Sessions, 2)
	assert.Equal(t, "sess-2", decoded.Sessions[0]["session_id"])
	assert.Equal(t, true, decoded.Sessions[1]["has_review"])
}

func TestSessionList_LoginRequiredExit2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionList(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", false)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "tmux-cli login")
}

// reviewRoundTripServer serves list + review endpoints, recording review bodies.
func reviewRoundTripServer(t *testing.T, listJSON, reportJSON string) (*httptest.Server, *[][]byte, *[]string) {
	t.Helper()
	bodies := &[][]byte{}
	paths := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/telemetry/sessions" && r.Method == http.MethodGet:
			w.WriteHeader(200)
			io.WriteString(w, listJSON)
		case strings.HasSuffix(r.URL.Path, "/review") && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, b)
			*paths = append(*paths, r.URL.Path)
			w.WriteHeader(200)
			io.WriteString(w, reportJSON)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"unknown session"}`)
		}
	}))
	return srv, bodies, paths
}

func TestSessionReview_ResolvesNewestAndRenders(t *testing.T) {
	srv, bodies, paths := reviewRoundTripServer(t, reviewSessionsList, reviewSampleReport)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "", false, false)
	assert.Equal(t, 0, code)

	// Newest started_at wins (sess-2), cached trigger (force:false).
	require.Len(t, *paths, 1)
	assert.Equal(t, "/api/v1/telemetry/sessions/sess-2/review", (*paths)[0])
	require.Len(t, *bodies, 1)
	assert.JSONEq(t, `{"force":false}`, string((*bodies)[0]))

	got := out.String()
	// All five human sections render.
	assert.Contains(t, got, "SUMMARY")
	assert.Contains(t, got, "AGENTS")
	assert.Contains(t, got, "PHASES")
	assert.Contains(t, got, "ANOMALIES")
	assert.Contains(t, got, "SUGGESTIONS")
	// Section content spot-checks.
	assert.Contains(t, got, "sess-1")
	assert.Contains(t, got, "supervisor")
	assert.Contains(t, got, "supervising")
	assert.Contains(t, got, "missing_successor")
	assert.Contains(t, got, "Fresh-handoff marker stranded")
	assert.Contains(t, got, "[warn]")
	assert.Contains(t, got, "[high]")
}

func TestSessionReview_RefreshSendsForceTrue(t *testing.T) {
	srv, bodies, _ := reviewRoundTripServer(t, reviewSessionsList, reviewSampleReport)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "", true, false)
	assert.Equal(t, 0, code)
	require.Len(t, *bodies, 1)
	assert.JSONEq(t, `{"force":true}`, string((*bodies)[0]))
}

func TestSessionReview_ExplicitIDSkipsList(t *testing.T) {
	listCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/telemetry/sessions" {
			listCalled = true
			w.WriteHeader(200)
			io.WriteString(w, `{"sessions":[]}`)
			return
		}
		assert.Equal(t, "/api/v1/telemetry/sessions/sess-x/review", r.URL.Path)
		w.WriteHeader(200)
		io.WriteString(w, reviewSampleReport)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "sess-x", false, false)
	assert.Equal(t, 0, code)
	assert.False(t, listCalled, "an explicit SESSION_ID must not trigger a list call")
}

func TestSessionReview_NoSessionsExit1(t *testing.T) {
	srv, _, _ := reviewRoundTripServer(t, `{"sessions":[]}`, reviewSampleReport)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "", false, false)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "no session found — run a session first, or pass a session id")
}

func TestSessionReview_Unknown404Exit1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":"unknown session"}`)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "ghost", false, false)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "unknown session")
}

func TestSessionReview_LoginRequiredExit2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/refresh" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "sess-1", false, false)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "tmux-cli login")
}

func TestSessionReview_BackendErrorExit3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "sess-1", false, false)
	assert.Equal(t, 3, code)
}

func TestSessionReview_JSONPrintsRawUnmodified(t *testing.T) {
	srv, _, _ := reviewRoundTripServer(t, reviewSessionsList, reviewSampleReport)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "sess-1", false, true)
	assert.Equal(t, 0, code)
	assert.Equal(t, reviewSampleReport+"\n", out.String(), "--json must print the raw report bytes unmodified")
}

func TestSessionReview_UnknownHigherSchemaVersionRendersSummaryPlusRaw(t *testing.T) {
	// A v2 report with fields this CLI does not know: render summary + raw, never crash.
	v2 := strings.Replace(reviewSampleReport, `"schema_version": 1`, `"schema_version": 2`, 1)
	srv, _, _ := reviewRoundTripServer(t, reviewSessionsList, v2)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runTelemetrySessionReview(context.Background(), &out, &errOut, newReviewTestClient(t, srv), "cli", "sess-1", false, false)
	assert.Equal(t, 0, code)

	got := out.String()
	assert.Contains(t, got, "SUMMARY")
	assert.Contains(t, got, "schema_version 2")
	assert.Contains(t, got, `"schema_version": 2`, "raw JSON must be included for a newer schema")
	assert.NotContains(t, got, "ANOMALIES", "structured sections beyond summary are not rendered for an unknown schema")
}

func TestNewestSessionID_PicksMaxStartedAt(t *testing.T) {
	sessions := []shipper.SessionSummary{
		{SessionID: "a", StartedAt: "2026-07-23T10:00:00Z"},
		{SessionID: "c", StartedAt: "2026-07-23T12:00:00Z"},
		{SessionID: "b", StartedAt: "2026-07-23T11:00:00Z"},
	}
	assert.Equal(t, "c", newestSessionID(sessions))
	assert.Equal(t, "", newestSessionID(nil))
}

func TestDeriveProject_ConfigOverrideElseBasename(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, filepath.Base(dir), deriveProject(dir), "no setting.yaml → basename")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte("project: lane-x\n"), 0o644))
	assert.Equal(t, "lane-x", deriveProject(dir))
}

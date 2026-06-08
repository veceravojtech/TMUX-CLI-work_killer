package mcp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validReportInput returns a TaskReportInput with all six required fields set to
// valid, in-enum, content-bearing values plus an optional payload. Individual
// tests mutate one field to exercise a single rejection path.
func validReportInput() TaskReportInput {
	return TaskReportInput{
		Category:           "execute",
		Severity:           "critical",
		Title:              "build is red",
		Description:        "go build fails on internal/mcp",
		ProposedFix:        "add the missing import in tools_report.go",
		ExpectedGreenState: "go build ./... exits 0",
		Payload:            map[string]any{"goal": "goal-003"},
	}
}

// withReportHook swaps the package-level newProducerClient seam for the duration
// of a test and restores it via t.Cleanup.
func withReportHook(t *testing.T, hook func(producer.Config) *producer.Client) {
	t.Helper()
	prev := newProducerClient
	newProducerClient = hook
	t.Cleanup(func() { newProducerClient = prev })
}

// failIfCalled is a newProducerClient hook that fails the test if the tool ever
// reaches client construction — used by the pre-submit rejection tests to prove
// SubmitTask never runs.
func failIfCalled(t *testing.T) func(producer.Config) *producer.Client {
	t.Helper()
	return func(producer.Config) *producer.Client {
		t.Fatalf("newProducerClient must NOT be called when input is rejected before submit")
		return nil
	}
}

func newReportServer(t *testing.T) *Server {
	t.Helper()
	return &Server{workingDir: t.TempDir()}
}

func TestTaskReport_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TaskReportInput)
		want   []string
	}{
		{"category", func(in *TaskReportInput) { in.Category = "" }, []string{"category"}},
		{"severity", func(in *TaskReportInput) { in.Severity = "  " }, []string{"severity"}},
		{"title", func(in *TaskReportInput) { in.Title = "" }, []string{"title"}},
		{"description", func(in *TaskReportInput) { in.Description = "" }, []string{"description"}},
		{"proposed_fix", func(in *TaskReportInput) { in.ProposedFix = "" }, []string{"proposed_fix"}},
		{"expected_green_state", func(in *TaskReportInput) { in.ExpectedGreenState = "" }, []string{"expected_green_state"}},
		{"multiple", func(in *TaskReportInput) { in.Title = ""; in.Description = "   " }, []string{"title", "description"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			in := validReportInput()
			tc.mutate(&in)
			out, err := newReportServer(t).TaskReport(context.Background(), in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidInput), "must wrap ErrInvalidInput")
			for _, name := range tc.want {
				assert.Contains(t, err.Error(), name, "error must name the missing field")
			}
		})
	}
}

func TestTaskReport_StubProposedFixRejected(t *testing.T) {
	for _, stub := range []string{"TBD", "none", "  Fix it "} {
		t.Run(stub, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			in := validReportInput()
			in.ProposedFix = stub
			out, err := newReportServer(t).TaskReport(context.Background(), in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidInput))
			assert.Contains(t, err.Error(), "proposed_fix")
		})
	}
}

func TestTaskReport_InvalidCategory(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	in := validReportInput()
	in.Category = "frontend"
	out, err := newReportServer(t).TaskReport(context.Background(), in)
	assert.Nil(t, out)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
	for _, allowed := range []string{"plan", "supervisor", "validator", "execute", "general"} {
		assert.Contains(t, err.Error(), allowed, "error must list the allowed categories")
	}
}

func TestTaskReport_InvalidSeverity(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	in := validReportInput()
	in.Severity = "high"
	out, err := newReportServer(t).TaskReport(context.Background(), in)
	assert.Nil(t, out)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
	for _, allowed := range []string{"critical", "warning", "info"} {
		assert.Contains(t, err.Error(), allowed, "error must list the allowed severities")
	}
}

func TestTaskReport_ReportingDisabled(t *testing.T) {
	withReportHook(t, func(producer.Config) *producer.Client { return nil })
	out, err := newReportServer(t).TaskReport(context.Background(), validReportInput())
	assert.Nil(t, out)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
	assert.Contains(t, strings.ToLower(err.Error()), "disabled")
}

func TestTaskReport_LoadConfigError(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	// A malformed setting.yaml makes producer.LoadConfig return an error; the
	// tool must propagate it and never construct a client or submit.
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte("apiUrl: [not: valid"), 0o644))

	s := &Server{workingDir: root}
	out, err := s.TaskReport(context.Background(), validReportInput())
	assert.Nil(t, out)
	require.Error(t, err)
}

func TestTaskReport_HappyPath_Httptest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tasks", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = body

		ts := r.Header.Get("X-Timestamp")
		sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
		require.NoError(t, err)
		assert.True(t, ed25519.Verify(pub, []byte(ts+string(body)), sig),
			"signature must verify over <ts><body>")

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"task-1","status":"queued","extra":"ignored"}`)
	}))
	defer srv.Close()

	withReportHook(t, func(producer.Config) *producer.Client {
		return producer.NewClientForTest(srv.URL, priv, srv.Client())
	})

	out, err := newReportServer(t).TaskReport(context.Background(), validReportInput())
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "task-1", out.ID)
	assert.Equal(t, "queued", out.Status)

	// The optional payload must be forwarded on the wire, and system_info must be
	// collected server-side (present in the request body though absent from the
	// input struct).
	var sent map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	payload, ok := sent["payload"].(map[string]any)
	require.True(t, ok, "payload must be forwarded")
	assert.Equal(t, "goal-003", payload["goal"])
	_, hasSysInfo := sent["system_info"]
	assert.True(t, hasSysInfo, "system_info must be collected server-side")
}

// TestValidEnums_SingleSource guards the single source of truth: the producer
// enum sets must hold exactly the documented keys so the daemon's coercion and
// the task-report tool's validation never drift.
func TestValidEnums_SingleSource(t *testing.T) {
	assert.Len(t, producer.ValidCategories, 5)
	for _, c := range []string{"plan", "supervisor", "validator", "execute", "general"} {
		assert.True(t, producer.ValidCategories[c], "category %q must be valid", c)
	}
	assert.Len(t, producer.ValidSeverities, 3)
	for _, s := range []string{"critical", "warning", "info"} {
		assert.True(t, producer.ValidSeverities[s], "severity %q must be valid", s)
	}
}

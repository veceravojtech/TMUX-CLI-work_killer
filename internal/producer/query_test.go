package producer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifySig reconstructs the signed message (ts + raw body) and asserts the
// detached Ed25519 signature header verifies — the same check the backend runs.
func verifySig(t *testing.T, pub ed25519.PublicKey, r *http.Request, body []byte) {
	t.Helper()
	ts := r.Header.Get("X-Timestamp")
	assert.NotEmpty(t, ts)
	assert.NotEmpty(t, r.Header.Get("X-Fingerprint"))
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
	require.NoError(t, err)
	assert.True(t, ed25519.Verify(pub, []byte(ts+string(body)), sig),
		"signature must verify over <ts><body>")
}

func TestListTasks_BuildsQueryAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)

	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/tasks", r.URL.Path)
		// GET signs over an empty body.
		body, _ := io.ReadAll(r.Body)
		assert.Empty(t, body)
		verifySig(t, pub, r, nil)
		gotQuery = r.URL.Query()

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"tasks":[{"id":42,"fingerprint":"fp","instanceId":1,"instanceName":"worker-01",
				"category":"execute","severity":"critical","status":"claimed","title":"t",
				"description":"d","proposedFix":"f","expectedGreenState":"g",
				"claimedBy":"fp","claimedAt":"2026-06-08T11:05:00+00:00",
				"createdAt":"2026-06-08T11:00:00+00:00","updatedAt":"2026-06-08T11:05:00+00:00"}],
			"total":1,"limit":50,"offset":0}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	out, err := c.ListTasks(context.Background(), ListTasksParams{
		Fingerprint: "fp", ClaimedBy: "cb", Status: "claimed",
		Category: "execute", Severity: "critical", Since: "2026-06-01T00:00:00Z",
		Limit: 10, Offset: 20,
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "fp", gotQuery.Get("fingerprint"))
	assert.Equal(t, "cb", gotQuery.Get("claimedBy"))
	assert.Equal(t, "claimed", gotQuery.Get("status"))
	assert.Equal(t, "execute", gotQuery.Get("category"))
	assert.Equal(t, "critical", gotQuery.Get("severity"))
	assert.Equal(t, "2026-06-01T00:00:00Z", gotQuery.Get("since"))
	assert.Equal(t, "10", gotQuery.Get("limit"))
	assert.Equal(t, "20", gotQuery.Get("offset"))

	assert.Equal(t, 1, out.Total)
	require.Len(t, out.Tasks, 1)
	assert.Equal(t, "42", out.Tasks[0].ID.String())
	assert.Equal(t, "claimed", out.Tasks[0].Status)
	assert.Equal(t, "worker-01", out.Tasks[0].InstanceName)
}

func TestListTasks_OmitsEmptyFilters(t *testing.T) {
	_, priv := testKeypair(t)
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"tasks":[],"total":0,"limit":50,"offset":0}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.ListTasks(context.Background(), ListTasksParams{Status: "new"})
	require.NoError(t, err)
	assert.Equal(t, "status=new", raw)
}

func TestGetTask_DecodesDetailWithEvents(t *testing.T) {
	pub, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/tasks/42", r.URL.Path)
		verifySig(t, pub, r, nil)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"status":"claimed","title":"t",
			"events":[
				{"id":1,"action":"created","actor":"fp","oldValue":null,"newValue":null,"createdAt":"x"},
				{"id":2,"action":"claimed","actor":"fp","oldValue":null,"newValue":{"status":"claimed"},"createdAt":"y"}
			]}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	out, err := c.GetTask(context.Background(), "42")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "42", out.ID.String())
	require.Len(t, out.Events, 2)
	assert.Equal(t, "created", out.Events[0].Action)
	assert.Equal(t, "claimed", out.Events[1].NewValue["status"])
}

func TestGetTask_NotFound(t *testing.T) {
	_, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not_found"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	out, err := c.GetTask(context.Background(), "999")
	assert.Nil(t, out)
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestClaimTask_Success(t *testing.T) {
	pub, priv := testKeypair(t)
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tasks/claim", r.URL.Path)
		gotQuery = r.URL.Query()
		verifySig(t, pub, r, nil)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":7,"status":"claimed","category":"execute"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.ClaimTask(context.Background(), ClaimParams{Category: "execute", Severity: "critical"})
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "7", task.ID.String())
	assert.Equal(t, "claimed", task.Status)
	assert.Equal(t, "execute", gotQuery.Get("category"))
	assert.Equal(t, "critical", gotQuery.Get("severity"))
}

func TestClaimTask_ScopesToClientLane(t *testing.T) {
	_, priv := testKeypair(t)
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	c.project = "fp-cli:/proj/cli"
	_, err := c.ClaimTask(context.Background(), ClaimParams{})
	require.NoError(t, err)
	assert.Equal(t, "fp-cli:/proj/cli", gotQuery.Get("project"),
		"claim defaults to the client's own lane")
}

func TestClaimTask_ExplicitProjectOverridesClientLane(t *testing.T) {
	_, priv := testKeypair(t)
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	c.project = "fp-cli:/proj/cli"
	_, err := c.ClaimTask(context.Background(), ClaimParams{Project: "override:/lane"})
	require.NoError(t, err)
	assert.Equal(t, "override:/lane", gotQuery.Get("project"),
		"an explicit ClaimParams.Project overrides the client lane")
}

func TestListTasks_ScopesToClientLane(t *testing.T) {
	_, priv := testKeypair(t)
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"tasks":[],"total":0,"limit":50,"offset":0}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	c.project = "fp-web:/proj/web"
	_, err := c.ListTasks(context.Background(), ListTasksParams{Status: "new"})
	require.NoError(t, err)
	assert.Equal(t, "fp-web:/proj/web", gotQuery.Get("project"),
		"list defaults to the client's own lane")
}

func TestClaimTask_NoContent(t *testing.T) {
	_, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.ClaimTask(context.Background(), ClaimParams{})
	assert.NoError(t, err)
	assert.Nil(t, task, "204 means nothing claimable -> nil task, no error")
}

func TestUpdateTaskStatus_SendsBodyAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/v1/tasks/42/status", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		verifySig(t, pub, r, body)
		require.NoError(t, json.Unmarshal(body, &sent))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"status":"resolved"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.UpdateTaskStatus(context.Background(), "42", UpdateStatusParams{
		Status:     "resolved",
		Resolution: map[string]any{"summary": "done"},
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "resolved", task.Status)
	assert.Equal(t, "resolved", sent["status"])
	res, ok := sent["resolution"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "done", res["summary"])
}

func TestUpdateTaskStatus_OmitsNilResolution(t *testing.T) {
	_, priv := testKeypair(t)
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &sent))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"status":"in_progress"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.UpdateTaskStatus(context.Background(), "42", UpdateStatusParams{Status: "in_progress"})
	require.NoError(t, err)
	_, hasRes := sent["resolution"]
	assert.False(t, hasRes, "resolution must be omitted when nil")
	assert.Equal(t, "in_progress", sent["status"])
}

func TestUpdateTaskStatus_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		code int
		want error
	}{
		{"forbidden", http.StatusForbidden, ErrForbidden},
		{"not_found", http.StatusNotFound, ErrTaskNotFound},
		{"invalid_transition", http.StatusUnprocessableEntity, ErrInvalidTransition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, priv := testKeypair(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			c := newClient(srv.URL, priv, srv.Client())
			task, err := c.UpdateTaskStatus(context.Background(), "42", UpdateStatusParams{Status: "resolved"})
			assert.Nil(t, task)
			assert.True(t, errors.Is(err, tc.want), "want %v, got %v", tc.want, err)
		})
	}
}

func TestQueryMethods_NilReceiverNoOp(t *testing.T) {
	var c *Client
	list, err := c.ListTasks(context.Background(), ListTasksParams{})
	assert.NoError(t, err)
	assert.Nil(t, list)

	task, err := c.ClaimTask(context.Background(), ClaimParams{})
	assert.NoError(t, err)
	assert.Nil(t, task)
}

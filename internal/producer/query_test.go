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

func TestListProjectBindings_BuildsQueryAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/project-bindings", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		assert.Empty(t, body)
		verifySig(t, pub, r, nil)
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"bindings":[
			{"projectName":"web","path":"/p/web","repository":"git@x:web.git","branch":"master","fingerprint":"fp","hostname":"box"}],
			"total":1}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	out, err := c.ListProjectBindings(context.Background(), ListBindingsParams{Hostname: "box"})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "box", gotQuery.Get("hostname"))
	require.Len(t, out.Bindings, 1)
	assert.Equal(t, "web", out.Bindings[0].ProjectName)
	assert.Equal(t, "/p/web", out.Bindings[0].Path)
	assert.Equal(t, "git@x:web.git", out.Bindings[0].Repository)
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

// editCurrentTaskJSON is the detail body the GET inside EditTask reads to seed the
// full-replacement merge. It carries a non-empty payload and one prerequisite so
// the "preserved across a partial edit" assertions are meaningful.
const editCurrentTaskJSON = `{"id":42,"status":"new","category":"general","severity":"info",` +
	`"title":"Old title","description":"Old description","proposedFix":"Old fix",` +
	`"expectedGreenState":"Old green","payload":{"existing":"kept"},"dependsOn":[7],"events":[]}`

// editTestServer mounts a server that answers EditTask's leading GET with
// editCurrentTaskJSON and captures the PATCH that follows. patchBody is the raw
// merged body; patchPath is its escaped path. The PATCH responds with reply.
func editTestServer(t *testing.T, pub ed25519.PublicKey, reply string, patchBody *[]byte, patchPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, editCurrentTaskJSON)
			return
		}
		assert.Equal(t, http.MethodPatch, r.Method)
		body, _ := io.ReadAll(r.Body)
		verifySig(t, pub, r, body)
		if patchBody != nil {
			*patchBody = body
		}
		if patchPath != nil {
			*patchPath = r.URL.EscapedPath()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, reply)
	}))
}

func TestEditTask_MergesOntoCurrentContentAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)
	var raw []byte
	srv := editTestServer(t, pub, `{"id":42,"status":"new","description":"new"}`, &raw, nil)
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.EditTask(context.Background(), "42", EditTaskParams{
		Description: "new",
		Severity:    "warning",
		Payload:     map[string]any{"k": "v"},
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "new", task.Description)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(raw, &sent))
	// Changed fields take the new value.
	assert.Equal(t, "new", sent["description"])
	assert.Equal(t, "warning", sent["severity"])
	assert.Equal(t, "v", sent["payload"].(map[string]any)["k"])
	// Unchanged fields are sent at their CURRENT value (full replacement).
	assert.Equal(t, "Old title", sent["title"])
	assert.Equal(t, "general", sent["category"])
	assert.Equal(t, "Old fix", sent["proposedFix"])
	assert.Equal(t, "Old green", sent["expectedGreenState"])
}

// TestEditTask_PreservesUnchangedFields is the core lossless guarantee: a
// title-only edit must re-send every other field — including the existing payload
// and dependsOn — at its current value, so the full-replacement backend does not
// reset them.
func TestEditTask_PreservesUnchangedFields(t *testing.T) {
	pub, priv := testKeypair(t)
	var raw []byte
	srv := editTestServer(t, pub, `{"id":42,"status":"new","title":"t"}`, &raw, nil)
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.EditTask(context.Background(), "42", EditTaskParams{Title: "t"})
	require.NoError(t, err)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(raw, &sent))
	assert.Equal(t, "t", sent["title"])
	assert.Equal(t, "Old description", sent["description"])
	assert.Equal(t, "Old fix", sent["proposedFix"])
	assert.Equal(t, "Old green", sent["expectedGreenState"])
	assert.Equal(t, "general", sent["category"])
	assert.Equal(t, "info", sent["severity"])
	// The pre-existing payload and prerequisite survive a partial edit.
	assert.Equal(t, "kept", sent["payload"].(map[string]any)["existing"])
	assert.Equal(t, []any{"7"}, sent["dependsOn"])
}

func TestEditTask_PathEscapesID(t *testing.T) {
	pub, priv := testKeypair(t)
	var gotPath string
	srv := editTestServer(t, pub, `{"id":"a b","status":"new"}`, nil, &gotPath)
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.EditTask(context.Background(), "a b", EditTaskParams{Title: "t"})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/tasks/a%20b", gotPath)
}

// TestEditTask_ErrorMapping covers failures on the PATCH: the GET succeeds (a
// claimed/terminal task is still readable) and the edit itself is rejected.
func TestEditTask_ErrorMapping(t *testing.T) {
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
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, editCurrentTaskJSON)
					return
				}
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			c := newClient(srv.URL, priv, srv.Client())
			task, err := c.EditTask(context.Background(), "42", EditTaskParams{Title: "t"})
			assert.Nil(t, task)
			assert.True(t, errors.Is(err, tc.want), "want %v, got %v", tc.want, err)
		})
	}
}

// TestEditTask_NotFoundOnGet: editing an unknown task fails on the leading GET,
// surfacing ErrTaskNotFound before any PATCH is attempted.
func TestEditTask_NotFoundOnGet(t *testing.T) {
	_, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "no PATCH must be sent when the task is missing")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.EditTask(context.Background(), "42", EditTaskParams{Title: "t"})
	assert.Nil(t, task)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "want ErrTaskNotFound, got %v", err)
}

func TestEditTask_NilReceiver(t *testing.T) {
	var c *Client
	task, err := c.EditTask(context.Background(), "42", EditTaskParams{Title: "t"})
	require.NoError(t, err)
	assert.Nil(t, task)
}

func TestDeny_SendsReasonAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tasks/42/deny", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		verifySig(t, pub, r, body)
		require.NoError(t, json.Unmarshal(body, &sent))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"status":"denied"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.Deny(context.Background(), "42", "dup")
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "denied", task.Status)
	assert.Equal(t, "dup", sent["reason"])
}

func TestDeny_NotFound(t *testing.T) {
	_, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.Deny(context.Background(), "42", "dup")
	assert.Nil(t, task)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "want ErrTaskNotFound, got %v", err)
}

func TestForceResolve_SendsReasonAndDecodes(t *testing.T) {
	pub, priv := testKeypair(t)
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tasks/42/resolve", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		verifySig(t, pub, r, body)
		require.NoError(t, json.Unmarshal(body, &sent))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"status":"resolved"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.ForceResolve(context.Background(), "42", "manual fix")
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "resolved", task.Status)
	assert.Equal(t, "manual fix", sent["reason"])
}

func TestForceResolve_NotFound(t *testing.T) {
	_, priv := testKeypair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	task, err := c.ForceResolve(context.Background(), "42", "manual fix")
	assert.Nil(t, task)
	assert.True(t, errors.Is(err, ErrTaskNotFound), "want ErrTaskNotFound, got %v", err)
}

func TestQueryMethods_NilReceiverNoOp(t *testing.T) {
	var c *Client
	list, err := c.ListTasks(context.Background(), ListTasksParams{})
	assert.NoError(t, err)
	assert.Nil(t, list)

	task, err := c.ClaimTask(context.Background(), ClaimParams{})
	assert.NoError(t, err)
	assert.Nil(t, task)

	denied, err := c.Deny(context.Background(), "42", "dup")
	assert.NoError(t, err)
	assert.Nil(t, denied)

	resolved, err := c.ForceResolve(context.Background(), "42", "manual fix")
	assert.NoError(t, err)
	assert.Nil(t, resolved)
}

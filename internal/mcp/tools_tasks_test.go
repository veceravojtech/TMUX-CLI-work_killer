package mcp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withTaskServer stands up an httptest backend that returns respBody for any
// request, hooks newProducerClient to a signed test client pointed at it, and
// returns a Server. The handler records the last request path+query+method.
func withTaskServer(t *testing.T, status int, respBody string) (*Server, *http.Request) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	last := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*last = *r
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)

	withReportHook(t, func(producer.Config) *producer.Client {
		return producer.NewClientForTest(srv.URL, priv, srv.Client())
	})
	return newReportServer(t), last
}

// capturedReq records the PATCH the read-modify-write EditTask issues after its
// leading GET, so a test can assert the merged body / path the backend receives.
type capturedReq struct {
	method string
	path   string
	body   []byte
}

// editDetailJSON is the current content EditTask's leading GET reads before
// merging a partial edit into the full-replacement PATCH body.
const editDetailJSON = `{"id":42,"status":"new","category":"general","severity":"info",` +
	`"title":"Old title","description":"Old description","proposedFix":"Old fix",` +
	`"expectedGreenState":"Old green","payload":{"existing":"kept"},"dependsOn":[],"events":[]}`

// withTaskEditServer stands up a server for EditTask's read-modify-write flow: the
// leading GET returns editDetailJSON; the following PATCH returns patchStatus +
// patchBody and is captured into the returned *capturedReq.
func withTaskEditServer(t *testing.T, patchStatus int, patchBody string) (*Server, *capturedReq) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, editDetailJSON)
			return
		}
		cap.body, _ = io.ReadAll(r.Body)
		cap.method = r.Method
		cap.path = r.URL.Path
		w.WriteHeader(patchStatus)
		_, _ = io.WriteString(w, patchBody)
	}))
	t.Cleanup(srv.Close)

	withReportHook(t, func(producer.Config) *producer.Client {
		return producer.NewClientForTest(srv.URL, priv, srv.Client())
	})
	return newReportServer(t), cap
}

// --- task-list ---------------------------------------------------------------

func TestTaskList_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{
		"tasks":[{"id":42,"status":"claimed","title":"t","category":"execute"}],
		"total":1,"limit":50,"offset":0}`)

	out, err := s.TaskList(context.Background(), TaskListInput{
		Fingerprint: "fp", Status: "claimed", Category: "execute", Limit: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, 1, out.Total)
	require.Len(t, out.Tasks, 1)
	assert.Equal(t, "42", out.Tasks[0].ID)
	assert.Equal(t, "claimed", out.Tasks[0].Status)

	assert.Equal(t, http.MethodGet, last.Method)
	assert.Equal(t, "/api/v1/tasks", last.URL.Path)
	assert.Equal(t, "fp", last.URL.Query().Get("fingerprint"))
	assert.Equal(t, "10", last.URL.Query().Get("limit"))
}

func TestTaskList_ProjectScoped(t *testing.T) {
	// project=cli must reach the wire and scope the result to the cli lane: the
	// project-aware backend returns ONLY cli-lane rows when query project==cli,
	// proving no other-lane row surfaces.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var gotProject string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = r.URL.Query().Get("project")
		w.WriteHeader(http.StatusOK)
		if gotProject == "cli" {
			_, _ = io.WriteString(w, `{"tasks":[
				{"id":1,"status":"new","title":"a","project":"cli"},
				{"id":2,"status":"new","title":"b","project":"cli"}],
				"total":2,"limit":50,"offset":0}`)
			return
		}
		// mixed lanes when not scoped — the test must never see this page.
		_, _ = io.WriteString(w, `{"tasks":[
			{"id":1,"status":"new","title":"a","project":"cli"},
			{"id":3,"status":"new","title":"c","project":"web"}],
			"total":2,"limit":50,"offset":0}`)
	}))
	t.Cleanup(srv.Close)
	withReportHook(t, func(producer.Config) *producer.Client {
		return producer.NewClientForTest(srv.URL, priv, srv.Client())
	})
	s := newReportServer(t)

	out, err := s.TaskList(context.Background(), TaskListInput{Project: "cli"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "cli", gotProject, "project=cli must reach the wire")
	require.Len(t, out.Tasks, 2)
	for _, task := range out.Tasks {
		assert.Equal(t, "cli", task.Project, "only cli-lane rows must surface")
	}
}

func TestTaskList_OmittedProjectSendsNoParam(t *testing.T) {
	// Omitting Project must not force a project param at the MCP layer — prior
	// behavior is preserved (NewClientForTest leaves c.project empty, so the
	// producer's lane default emits nothing).
	s, last := withTaskServer(t, http.StatusOK, `{"tasks":[],"total":0,"limit":50,"offset":0}`)
	_, err := s.TaskList(context.Background(), TaskListInput{})
	require.NoError(t, err)
	assert.Equal(t, "", last.URL.Query().Get("project"), "no project param when project omitted")
}

func TestTaskList_SummaryTruncatesAndOmitsBodies(t *testing.T) {
	// A list row must be token-bounded: the (potentially huge) body fields are
	// not returned in full — description is capped to a preview, and
	// proposed_fix/expected_green_state are not part of the summary at all.
	bigDesc := strings.Repeat("x", 5000)
	s, _ := withTaskServer(t, http.StatusOK, `{
		"tasks":[{"id":1,"status":"new","title":"t","description":"`+bigDesc+`",
			"proposedFix":"`+bigDesc+`","expectedGreenState":"`+bigDesc+`"}],
		"total":1,"limit":50,"offset":0}`)

	out, err := s.TaskList(context.Background(), TaskListInput{})
	require.NoError(t, err)
	require.Len(t, out.Tasks, 1)
	assert.Less(t, len(out.Tasks[0].DescriptionPreview), 5000, "description must be capped to a preview")
	assert.True(t, strings.HasSuffix(out.Tasks[0].DescriptionPreview, "…"), "truncated preview must be marked")

	// Marshalling the whole page must stay small regardless of body size.
	blob, err := json.Marshal(out)
	require.NoError(t, err)
	assert.Less(t, len(blob), 2000, "a single-row page must be token-bounded")
}

func TestTaskList_InvalidFiltersRejected(t *testing.T) {
	cases := []struct {
		name string
		in   TaskListInput
		want string
	}{
		{"status", TaskListInput{Status: "bogus"}, "status"},
		{"category", TaskListInput{Category: "bogus"}, "category"},
		{"severity", TaskListInput{Severity: "bogus"}, "severity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t)) // must reject before building a client
			s := newReportServer(t)
			out, err := s.TaskList(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// --- task-get ----------------------------------------------------------------

func TestTaskGet_MissingIDRejected(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskGet(context.Background(), TaskGetInput{ID: "  "})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
}

func TestTaskGet_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"claimed","title":"t",
		"events":[{"id":1,"action":"created","actor":"fp","createdAt":"x"}]}`)

	out, err := s.TaskGet(context.Background(), TaskGetInput{ID: "42"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "42", out.Task.ID)
	require.Len(t, out.Events, 1)
	assert.Equal(t, "created", out.Events[0].Action)
	assert.Equal(t, "/api/v1/tasks/42", last.URL.Path)
}

func TestTaskGet_NotFound(t *testing.T) {
	s, _ := withTaskServer(t, http.StatusNotFound, `{"error":"not_found"}`)
	out, err := s.TaskGet(context.Background(), TaskGetInput{ID: "999"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, producer.ErrTaskNotFound)
}

// --- task-claim --------------------------------------------------------------

func TestTaskClaim_Claimed(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":7,"status":"claimed","category":"execute"}`)
	out, err := s.TaskClaim(context.Background(), TaskClaimInput{Category: "execute"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.True(t, out.Claimed)
	require.NotNil(t, out.Task)
	assert.Equal(t, "7", out.Task.ID)
	assert.Equal(t, "/api/v1/tasks/claim", last.URL.Path)
	assert.Equal(t, "execute", last.URL.Query().Get("category"))
}

func TestTaskClaim_NothingClaimable(t *testing.T) {
	s, _ := withTaskServer(t, http.StatusNoContent, ``)
	out, err := s.TaskClaim(context.Background(), TaskClaimInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.False(t, out.Claimed)
	assert.Nil(t, out.Task)
}

func TestTaskClaim_InvalidFilterRejected(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskClaim(context.Background(), TaskClaimInput{Severity: "nope"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "severity")
}

// --- task-update-status ------------------------------------------------------

func TestTaskUpdateStatus_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"resolved"}`)
	out, err := s.TaskUpdateStatus(context.Background(), TaskUpdateStatusInput{
		ID: "42", Status: "resolved", Resolution: map[string]any{"summary": "done"},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "resolved", out.Task.Status)
	assert.Equal(t, http.MethodPatch, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/status", last.URL.Path)
}

func TestTaskUpdateStatus_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   TaskUpdateStatusInput
		want string
	}{
		{"missing id", TaskUpdateStatusInput{Status: "resolved"}, "id"},
		{"missing status", TaskUpdateStatusInput{ID: "42"}, "status"},
		{"non-worker status", TaskUpdateStatusInput{ID: "42", Status: "claimed"}, "status"},
		{"bogus status", TaskUpdateStatusInput{ID: "42", Status: "frobnicate"}, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			s := newReportServer(t)
			out, err := s.TaskUpdateStatus(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTaskUpdateStatus_InvalidTransitionSurfaced(t *testing.T) {
	s, _ := withTaskServer(t, http.StatusUnprocessableEntity, `{"error":"invalid_transition"}`)
	out, err := s.TaskUpdateStatus(context.Background(), TaskUpdateStatusInput{ID: "42", Status: "resolved"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, producer.ErrInvalidTransition)
}

// --- task-edit ---------------------------------------------------------------

func TestTaskEdit_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"claimed","description":"new"}`)
	out, err := s.TaskEdit(context.Background(), TaskEditInput{ID: "42", Description: "new"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "new", out.Task.Description)
	assert.Equal(t, http.MethodPatch, last.Method)
	assert.Equal(t, "/api/v1/tasks/42", last.URL.Path)
}

// TestTaskEdit_MergesProvidedOntoCurrent: a partial edit through the MCP tool is
// merged onto the task's current content and PATCHed as a complete body — the
// provided fields take the new value, every other field (including the existing
// payload) is re-sent at its current value so the full-replacement backend keeps it.
func TestTaskEdit_MergesProvidedOntoCurrent(t *testing.T) {
	s, cap := withTaskEditServer(t, http.StatusOK, `{"id":42,"status":"new","title":"t","severity":"warning"}`)

	out, err := s.TaskEdit(context.Background(), TaskEditInput{ID: "42", Title: "t", Severity: "warning"})
	require.NoError(t, err)
	require.NotNil(t, out)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(cap.body, &sent))
	// Provided fields take the new value.
	assert.Equal(t, "t", sent["title"])
	assert.Equal(t, "warning", sent["severity"])
	// Unchanged fields are re-sent at their current value (lossless full replacement).
	assert.Equal(t, "Old description", sent["description"])
	assert.Equal(t, "Old fix", sent["proposedFix"])
	assert.Equal(t, "Old green", sent["expectedGreenState"])
	assert.Equal(t, "general", sent["category"])
	assert.Equal(t, "kept", sent["payload"].(map[string]any)["existing"])
}

func TestTaskEdit_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   TaskEditInput
		want string
	}{
		{"missing id", TaskEditInput{Description: "x"}, "id"},
		{"blank id", TaskEditInput{ID: "  ", Description: "x"}, "id"},
		{"no editable field", TaskEditInput{ID: "42"}, "no editable fields"},
		{"invalid severity", TaskEditInput{ID: "42", Severity: "huge"}, "severity"},
		{"invalid category", TaskEditInput{ID: "42", Category: "bogus"}, "category"},
		{"contentless proposed_fix", TaskEditInput{ID: "42", ProposedFix: "TBD"}, "stub"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			s := newReportServer(t)
			out, err := s.TaskEdit(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTaskEdit_PayloadCountsAsProvided(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"claimed"}`)
	out, err := s.TaskEdit(context.Background(), TaskEditInput{ID: "42", Payload: map[string]any{}})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "/api/v1/tasks/42", last.URL.Path)
}

func TestTaskEdit_TerminalStateRejected(t *testing.T) {
	// The GET succeeds (a terminal task is still readable); the PATCH is rejected.
	s, _ := withTaskEditServer(t, http.StatusUnprocessableEntity, `{"error":"not_editable"}`)
	out, err := s.TaskEdit(context.Background(), TaskEditInput{ID: "42", Description: "new"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, producer.ErrInvalidTransition)
}

func TestTaskEdit_ForbiddenAndNotFound(t *testing.T) {
	cases := []struct {
		name string
		code int
		want error
	}{
		{"forbidden", http.StatusForbidden, producer.ErrForbidden},
		{"not_found", http.StatusNotFound, producer.ErrTaskNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// GET succeeds; the edit failure is surfaced from the PATCH.
			s, _ := withTaskEditServer(t, tc.code, `{}`)
			out, err := s.TaskEdit(context.Background(), TaskEditInput{ID: "42", Description: "new"})
			assert.Nil(t, out)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// --- task-deny ---------------------------------------------------------------

func TestTaskDeny_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"denied"}`)
	out, err := s.TaskDeny(context.Background(), TaskDenyInput{ID: "42", Reason: "dup"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "denied", out.Task.Status)
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/deny", last.URL.Path)
}

func TestTaskDeny_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   TaskDenyInput
		want string
	}{
		{"missing id", TaskDenyInput{Reason: "dup"}, "id"},
		{"blank id", TaskDenyInput{ID: "  ", Reason: "dup"}, "id"},
		{"missing reason", TaskDenyInput{ID: "42"}, "reason"},
		{"blank reason", TaskDenyInput{ID: "42", Reason: "  "}, "reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			s := newReportServer(t)
			out, err := s.TaskDeny(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// --- task-resolve (force resolve) --------------------------------------------

func TestTaskResolve_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"resolved"}`)
	out, err := s.TaskResolve(context.Background(), TaskResolveInput{ID: "42", Reason: "manual"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "resolved", out.Task.Status)
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/resolve", last.URL.Path)
}

func TestTaskResolve_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   TaskResolveInput
		want string
	}{
		{"missing id", TaskResolveInput{Reason: "manual"}, "id"},
		{"blank id", TaskResolveInput{ID: "  ", Reason: "manual"}, "id"},
		{"missing reason", TaskResolveInput{ID: "42"}, "reason"},
		{"blank reason", TaskResolveInput{ID: "42", Reason: "  "}, "reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			s := newReportServer(t)
			out, err := s.TaskResolve(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTaskDenyResolve_DisabledClient(t *testing.T) {
	withReportHook(t, func(producer.Config) *producer.Client { return nil })
	s := newReportServer(t)

	_, err := s.TaskDeny(context.Background(), TaskDenyInput{ID: "42", Reason: "dup"})
	assert.ErrorContains(t, err, "disabled")

	_, err = s.TaskResolve(context.Background(), TaskResolveInput{ID: "42", Reason: "manual"})
	assert.ErrorContains(t, err, "disabled")
}

// --- task-set-status (consolidated admin transition) -------------------------

func TestTaskSetStatus_Archive_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"archived"}`)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "archived", Reason: "dupe"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "archived", out.Task.Status)
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/archive", last.URL.Path)
}

func TestTaskSetStatus_Denied_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"denied"}`)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "denied", Reason: "invalid"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "denied", out.Task.Status)
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/deny", last.URL.Path)
}

func TestTaskSetStatus_Resolved_HappyPath(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"resolved"}`)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "resolved", Reason: "done oob"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "resolved", out.Task.Status)
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/42/resolve", last.URL.Path)
}

// TestTaskSetStatus_UnclaimedAllThree drives a single unclaimed-task id through
// all three admin targets in one table test — proving no claim step is required.
func TestTaskSetStatus_UnclaimedAllThree(t *testing.T) {
	cases := []struct {
		status   string
		wantPath string
	}{
		{"archived", "/api/v1/tasks/42/archive"},
		{"denied", "/api/v1/tasks/42/deny"},
		{"resolved", "/api/v1/tasks/42/resolve"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			s, last := withTaskServer(t, http.StatusOK, `{"id":42,"status":"`+tc.status+`"}`)
			out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: tc.status, Reason: "oob"})
			require.NoError(t, err)
			require.NotNil(t, out)
			assert.Equal(t, tc.status, out.Task.Status)
			assert.Equal(t, http.MethodPost, last.Method)
			assert.Equal(t, tc.wantPath, last.URL.Path)
		})
	}
}

func TestTaskSetStatus_InvalidStatus(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "frobnicate", Reason: "x"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), `invalid status "frobnicate"`)
	assert.Contains(t, err.Error(), "archived, denied, resolved")
}

func TestTaskSetStatus_WorkerStatusRejected(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "in_progress", Reason: "x"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), `invalid status "in_progress"`)
}

func TestTaskSetStatus_MissingId(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "  ", Status: "archived", Reason: "x"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
}

func TestTaskSetStatus_BlankReason(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "archived", Reason: "  "})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason")
}

func TestTaskSetStatus_BlankStatus(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "  ", Reason: "x"})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
}

func TestTaskSetStatus_DisabledClient(t *testing.T) {
	withReportHook(t, func(producer.Config) *producer.Client { return nil })
	s := newReportServer(t)
	_, err := s.TaskSetStatus(context.Background(), TaskSetStatusInput{ID: "42", Status: "archived", Reason: "x"})
	assert.ErrorContains(t, err, "disabled")
}

// --- disabled reporting ------------------------------------------------------

func TestTaskQuery_DisabledClient(t *testing.T) {
	withReportHook(t, func(producer.Config) *producer.Client { return nil })
	s := newReportServer(t)

	_, err := s.TaskList(context.Background(), TaskListInput{})
	assert.ErrorContains(t, err, "disabled")

	_, err = s.TaskClaim(context.Background(), TaskClaimInput{})
	assert.ErrorContains(t, err, "disabled")
}

// --- buildTaskMessage CODE_RULES block (goal-028, phase 2c) -------------------

func TestBuildTaskMessage_CodeRulesOmittedWhenEmpty(t *testing.T) {
	msg := buildTaskMessage("supervisor", "execute-1", "t", "ctx.md", "scope", "", ".tmux-cli/research/x", "", "")
	assert.NotContains(t, msg, "CODE_RULES")
}

func TestBuildTaskMessage_CodeRulesEmittedWhenPresent(t *testing.T) {
	codeRules := "CR-no-god-objects: keep handler thin\n- secrets-in-env: apply"
	msg := buildTaskMessage("supervisor", "execute-1", "t", "ctx.md", "scope", "", ".tmux-cli/research/x", "", codeRules)
	assert.Contains(t, msg, "CODE_RULES:")
	assert.Contains(t, msg, "CR-no-god-objects: keep handler thin")
}

func TestBuildTaskMessage_CodeRulesBlockPlacement(t *testing.T) {
	codeRules := "CR-no-god-objects: keep handler thin"
	msg := buildTaskMessage("supervisor", "execute-1", "t", "ctx.md", "scope", "", ".tmux-cli/research/x", "", codeRules)
	assert.Less(t, strings.Index(msg, "CONTEXT:"), strings.Index(msg, "CODE_RULES:"))
	assert.Less(t, strings.Index(msg, "CODE_RULES:"), strings.Index(msg, "DELIVERABLE"))
}

// --- task-link ---------------------------------------------------------------

// TestTaskLink_MissingFields proves both id and prerequisite_id are required and
// validated BEFORE any client is built (the producer seam is never reached).
func TestTaskLink_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		in   TaskLinkInput
		want string
	}{
		{"missing id", TaskLinkInput{PrerequisiteID: "12"}, "id"},
		{"blank id", TaskLinkInput{ID: "  ", PrerequisiteID: "12"}, "id"},
		{"missing prerequisite", TaskLinkInput{ID: "20"}, "prerequisite_id"},
		{"blank prerequisite", TaskLinkInput{ID: "20", PrerequisiteID: " "}, "prerequisite_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t)) // must reject before building a client
			s := newReportServer(t)
			out, err := s.TaskLink(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestTaskLink_HappyPath_Httptest drives the tool through the producer seam and
// asserts it POSTs the edge and surfaces the updated task view.
func TestTaskLink_HappyPath_Httptest(t *testing.T) {
	s, last := withTaskServer(t, http.StatusOK, `{"id":20,"status":"new","dependsOn":[12],"ready":false}`)
	out, err := s.TaskLink(context.Background(), TaskLinkInput{ID: "20", PrerequisiteID: "12"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "20", out.Task.ID)
	assert.False(t, out.Task.Ready)
	require.Len(t, out.Task.DependsOn, 1)
	assert.Equal(t, "12", out.Task.DependsOn[0])
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/v1/tasks/20/dependencies", last.URL.Path)
}

// TestTaskGet_SurfacesDependsOnAndReady decodes a backend reply carrying
// dependsOn + ready into the agent-facing view (ready=false ⇒ blocked).
func TestTaskGet_SurfacesDependsOnAndReady(t *testing.T) {
	s, _ := withTaskServer(t, http.StatusOK,
		`{"id":20,"status":"new","dependsOn":[12,13],"ready":false,"events":[]}`)
	out, err := s.TaskGet(context.Background(), TaskGetInput{ID: "20"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.False(t, out.Task.Ready)
	assert.Equal(t, []string{"12", "13"}, out.Task.DependsOn)
}

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

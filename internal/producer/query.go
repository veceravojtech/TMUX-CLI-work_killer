package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Read-path sentinel errors so callers (the MCP query tools) can map an HTTP
// status to a precise, user-facing message instead of a bare "status N". They
// mirror the documented API responses: 404 not_found, 403 forbidden, 422
// invalid_transition on the status-advance endpoint.
var (
	ErrTaskNotFound      = errors.New("producer: task not found")
	ErrForbidden         = errors.New("producer: caller is not the claimer of this task")
	ErrInvalidTransition = errors.New("producer: invalid status transition")
)

// Task is the serialized task shape the backend returns from the list, detail,
// claim, and status-update endpoints (see GET /api/v1/tasks). IDs use FlexibleID
// because the backend emits them as JSON numbers; nullable string fields decode
// to "" from JSON null.
type Task struct {
	ID                 FlexibleID `json:"id"`
	Fingerprint        string     `json:"fingerprint"`
	InstanceID         FlexibleID `json:"instanceId"`
	InstanceName       string     `json:"instanceName"`
	Category           string     `json:"category"`
	Severity           string     `json:"severity"`
	Status             string     `json:"status"`
	Priority           int        `json:"priority"`
	Project            string     `json:"project"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	ProposedFix        string     `json:"proposedFix"`
	ExpectedGreenState string     `json:"expectedGreenState"`
	ClaimedBy          string     `json:"claimedBy"`
	ClaimedAt          string     `json:"claimedAt"`
	CreatedAt          string     `json:"createdAt"`
	UpdatedAt          string     `json:"updatedAt"`
	// DependsOn is the task's prerequisite ids; the backend emits them as JSON
	// numbers, so FlexibleID is used (mirroring ID/InstanceID). Ready is the
	// backend-computed gate: false means the task is blocked on an unresolved
	// prerequisite. Both decode from absent/null to their zero value (empty
	// slice / false), which reads as "no deps, blocked-by-nothing" for a
	// pre-deploy backend that omits them.
	DependsOn []FlexibleID `json:"dependsOn,omitempty"`
	Ready     bool         `json:"ready"`
}

// TaskList is one page of GET /api/v1/tasks: the filtered slice plus the full
// filtered total and the echoed pagination.
type TaskList struct {
	Tasks  []Task `json:"tasks"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// TaskEvent is one entry in a task's history (GET /api/v1/tasks/{id}). OldValue
// and NewValue are free-form objects or null.
type TaskEvent struct {
	ID        FlexibleID     `json:"id"`
	Action    string         `json:"action"`
	Actor     string         `json:"actor"`
	OldValue  map[string]any `json:"oldValue,omitempty"`
	NewValue  map[string]any `json:"newValue,omitempty"`
	CreatedAt string         `json:"createdAt"`
}

// TaskDetail is a single task plus its event history. The embedded Task carries
// all the list-item fields; encoding/json promotes them during decode.
type TaskDetail struct {
	Task
	Events []TaskEvent `json:"events"`
}

// ListTasksParams are the optional, AND-combined filters for GET /api/v1/tasks.
// Empty string fields and non-positive Limit/Offset are omitted so the backend
// applies its defaults (limit 50, offset 0).
type ListTasksParams struct {
	Fingerprint string
	ClaimedBy   string
	Status      string
	Category    string
	Severity    string
	Project     string
	Since       string
	Limit       int
	Offset      int
}

func (p ListTasksParams) query() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("fingerprint", p.Fingerprint)
	set("claimedBy", p.ClaimedBy)
	set("status", p.Status)
	set("category", p.Category)
	set("severity", p.Severity)
	set("project", p.Project)
	set("since", p.Since)
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	return q
}

// ClaimParams optionally narrows the pool POST /api/v1/tasks/claim draws from.
type ClaimParams struct {
	Category string
	Severity string
	Project  string
}

func (p ClaimParams) query() url.Values {
	q := url.Values{}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Severity != "" {
		q.Set("severity", p.Severity)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	return q
}

// UpdateStatusParams is the PATCH /api/v1/tasks/{id}/status body. Resolution is
// optional and only sent (recorded by the backend) when non-nil.
type UpdateStatusParams struct {
	Status     string         `json:"status"`
	Resolution map[string]any `json:"resolution,omitempty"`
}

// EditTaskParams is the PATCH /api/v1/tasks/{id} body — the editable content of
// a filed task. Every field is optional and only sent (and recorded by the
// backend) when non-empty/non-nil, so a caller patches just the fields it wants
// to change. Tags are camelCase to match the backend task DTO.
type EditTaskParams struct {
	Title              string         `json:"title,omitempty"`
	Description        string         `json:"description,omitempty"`
	ProposedFix        string         `json:"proposedFix,omitempty"`
	ExpectedGreenState string         `json:"expectedGreenState,omitempty"`
	Severity           string         `json:"severity,omitempty"`
	Category           string         `json:"category,omitempty"`
	Payload            map[string]any `json:"payload,omitempty"`
}

// ListTasks fetches a filtered page of tasks. A nil receiver is a no-op
// returning (nil, nil), matching SubmitTask, so callers never have to nil-check.
func (c *Client) ListTasks(ctx context.Context, p ListTasksParams) (*TaskList, error) {
	if c == nil {
		return nil, nil
	}
	if p.Project == "" {
		p.Project = c.project // default to this worker's lane; callers may override
	}
	resp, err := c.doSigned(ctx, http.MethodGet, "/api/v1/tasks", p.query(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task list", nil); err != nil {
		return nil, err
	}
	var out TaskList
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTask fetches one task plus its event history. A missing id returns
// ErrTaskNotFound. A nil receiver is a no-op returning (nil, nil).
func (c *Client) GetTask(ctx context.Context, id string) (*TaskDetail, error) {
	if c == nil {
		return nil, nil
	}
	resp, err := c.doSigned(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task detail", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}
	var out TaskDetail
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ClaimTask atomically claims the next-highest-priority new task, optionally
// narrowed by p. A 204 (nothing claimable) returns (nil, nil) — distinguish it
// from an error, not from a claimed task. A nil receiver is a no-op.
func (c *Client) ClaimTask(ctx context.Context, p ClaimParams) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	if p.Project == "" {
		p.Project = c.project // claim only within this worker's lane unless overridden
	}
	resp, err := c.doSigned(ctx, http.MethodPost, "/api/v1/tasks/claim", p.query(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, resp.Body)
		return nil, nil
	}
	if err := expect2xx(resp, "task claim", nil); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateTaskStatus advances a claimed task. It maps the documented failures to
// sentinels: 403 -> ErrForbidden, 404 -> ErrTaskNotFound, 422 ->
// ErrInvalidTransition. A nil receiver is a no-op.
func (c *Client) UpdateTaskStatus(ctx context.Context, id string, p UpdateStatusParams) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id)+"/status", nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task status update", map[int]error{
		http.StatusForbidden:           ErrForbidden,
		http.StatusNotFound:            ErrTaskNotFound,
		http.StatusUnprocessableEntity: ErrInvalidTransition,
	}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EditTask amends a filed task's content via PATCH /api/v1/tasks/{id}. It maps
// the documented failures to sentinels: 403 -> ErrForbidden, 404 ->
// ErrTaskNotFound, 422 -> ErrInvalidTransition (terminal-state edit rejected by
// the backend). A nil receiver is a no-op.
func (c *Client) EditTask(ctx context.Context, id string, p EditTaskParams) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(id), nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task edit", map[int]error{
		http.StatusForbidden:           ErrForbidden,
		http.StatusNotFound:            ErrTaskNotFound,
		http.StatusUnprocessableEntity: ErrInvalidTransition,
	}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReasonParams is the JSON body for the deny and force-resolve endpoints — a
// single human-readable reason recorded by the backend with the status change.
type ReasonParams struct {
	Reason string `json:"reason"`
}

// Deny denies a task via POST /api/v1/tasks/{id}/deny with a {"reason":...}
// body. It maps 404 -> ErrTaskNotFound and leaves other non-2xx to the generic
// expect2xx error. A nil receiver is a no-op returning (nil, nil).
func (c *Client) Deny(ctx context.Context, id, reason string) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(ReasonParams{Reason: reason})
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/deny", nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task deny", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForceResolve administratively resolves a task via POST
// /api/v1/tasks/{id}/resolve with a {"reason":...} body — distinct from the
// claim-gated status advance to "resolved". It maps 404 -> ErrTaskNotFound and
// leaves other non-2xx to the generic expect2xx error. A nil receiver is a
// no-op returning (nil, nil).
func (c *Client) ForceResolve(ctx context.Context, id, reason string) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(ReasonParams{Reason: reason})
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/resolve", nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task force-resolve", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Archive administratively archives a task via POST
// /api/v1/tasks/{id}/archive with a {"reason":...} body — retires a task
// out-of-band (e.g. an erroneous or duplicate task) without claiming it. It maps
// 404 -> ErrTaskNotFound and leaves other non-2xx to the generic expect2xx
// error. A nil receiver is a no-op returning (nil, nil).
func (c *Client) Archive(ctx context.Context, id, reason string) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(ReasonParams{Reason: reason})
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/archive", nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task archive", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LinkParams is the JSON body for the add-dependency endpoint — a single
// prerequisite task id the target task is made to depend on.
type LinkParams struct {
	PrerequisiteID string `json:"prerequisiteId"`
}

// Link adds one prerequisite edge to a task via POST
// /api/v1/tasks/{id}/dependencies with a {"prerequisiteId":...} body — making
// the task depend on prerequisiteID. It maps 404 -> ErrTaskNotFound (unknown
// task) and 422 -> ErrInvalidTransition (cycle / self-dependency rejected by
// the backend); other non-2xx fall to the generic expect2xx error. The backend
// owns the dependency model, cycle detection, and ready computation; this only
// shapes the request and decodes the updated task. A nil receiver is a no-op
// returning (nil, nil).
func (c *Client) Link(ctx context.Context, id, prerequisiteID string) (*Task, error) {
	if c == nil {
		return nil, nil
	}
	body, err := json.Marshal(LinkParams{PrerequisiteID: prerequisiteID})
	if err != nil {
		return nil, err
	}
	resp, err := c.doSigned(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/dependencies", nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "task link", map[int]error{
		http.StatusNotFound:            ErrTaskNotFound,
		http.StatusUnprocessableEntity: ErrInvalidTransition,
	}); err != nil {
		return nil, err
	}
	var out Task
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doSigned builds, signs, and sends an authenticated request. The Ed25519
// signature covers fmt.Sprintf("%d", ts)+string(body) — an empty body for GET —
// exactly as the backend verifier reconstructs it. body may be nil (no body
// sent and no Content-Type set). It is a thin wrapper over doSignedRaw that
// pins the JSON content type, so every existing JSON caller is byte-identical.
func (c *Client) doSigned(ctx context.Context, method, path string, query url.Values, body []byte) (*http.Response, error) {
	return c.doSignedRaw(ctx, method, path, query, body, "application/json")
}

// doSignedRaw is the content-type-aware core of doSigned: it builds, signs, and
// sends an authenticated request, setting the caller-supplied contentType on the
// body (when body != nil). The Ed25519 signature covers the EXACT body bytes
// (fmt.Sprintf("%d", ts)+string(body)) so the caller MUST pass the same bytes it
// wants on the wire. It exists so a multipart upload can pass its
// boundary-carrying w.FormDataContentType() — which doSigned's hardcoded
// application/json cannot express — while keeping the JSON path unchanged. body
// may be nil (no body sent and no Content-Type set, e.g. a GET).
func (c *Client) doSignedRaw(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: failed to build request:", err)
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	ts := time.Now().Unix()
	req.Header.Set("X-Signature", c.sign(ts, body))
	req.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Fingerprint", c.fingerprint)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: request failed:", err)
		return nil, err
	}
	return resp, nil
}

// expect2xx returns nil for a 2xx response. For a non-2xx it drains the body and
// returns a mapped sentinel (if statusErrors has the code) or a generic error.
func expect2xx(resp *http.Response, what string, statusErrors map[int]error) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	io.Copy(io.Discard, resp.Body)
	if statusErrors != nil {
		if err, ok := statusErrors[resp.StatusCode]; ok {
			return err
		}
	}
	err := fmt.Errorf("producer: %s returned status %d", what, resp.StatusCode)
	fmt.Fprintln(os.Stderr, err)
	return err
}

// decode reads and JSON-decodes a 2xx response body into v.
func decode(resp *http.Response, v any) error {
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		fmt.Fprintln(os.Stderr, "producer: failed to decode response:", err)
		return err
	}
	return nil
}

// ProjectBindingDTO is one row of the project-lane registry: a project NAME and
// one of its concrete addresses (the machine + absolute path + repo). Fetched so
// an agent can pick the right lane for a cross-project task report.
type ProjectBindingDTO struct {
	ProjectName string `json:"projectName"`
	Path        string `json:"path"`
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
}

// BindingList is the decoded GET /api/v1/project-bindings reply.
type BindingList struct {
	Bindings []ProjectBindingDTO `json:"bindings"`
	Total    int                 `json:"total"`
}

// ListBindingsParams optionally narrows the registry fetch.
type ListBindingsParams struct {
	Hostname    string
	Fingerprint string
}

func (p ListBindingsParams) query() url.Values {
	q := url.Values{}
	if p.Hostname != "" {
		q.Set("hostname", p.Hostname)
	}
	if p.Fingerprint != "" {
		q.Set("fingerprint", p.Fingerprint)
	}
	return q
}

// ListProjectBindings fetches the project-lane registry (all enabled bindings,
// optionally narrowed by p). A nil receiver is a no-op returning (nil, nil).
func (c *Client) ListProjectBindings(ctx context.Context, p ListBindingsParams) (*BindingList, error) {
	if c == nil {
		return nil, nil
	}
	resp, err := c.doSigned(ctx, http.MethodGet, "/api/v1/project-bindings", p.query(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "project bindings", nil); err != nil {
		return nil, err
	}
	var out BindingList
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

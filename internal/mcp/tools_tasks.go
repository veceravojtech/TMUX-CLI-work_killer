package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/taskvisor"
)

// This file adds the task *query* tools (task-list, task-get, task-claim,
// task-update-status) that complement the write-only task-report tool. They let
// an agent track tasks it reported, claim assigned work, and advance a claimed
// task's lifecycle. All four build a producer client the same way task-report
// does (LoadConfig + newProducerClient seam) and share taskClient below.

// TaskView is the agent-facing projection of producer.Task. IDs are rendered as
// strings (the backend emits numbers); empty fields are omitted.
type TaskView struct {
	ID                 string   `json:"id" jsonschema:"Backend task id"`
	Fingerprint        string   `json:"fingerprint,omitempty" jsonschema:"Fingerprint of the machine that reported the task"`
	InstanceID         string   `json:"instance_id,omitempty"`
	InstanceName       string   `json:"instance_name,omitempty"`
	Category           string   `json:"category,omitempty"`
	Severity           string   `json:"severity,omitempty"`
	Status             string   `json:"status" jsonschema:"Lifecycle status (new, claimed, in_progress, resolved, failed, denied, archived)"`
	Priority           int      `json:"priority" jsonschema:"Manual priority (higher = sooner); use as the goal priority when converting this task to a goal"`
	Project            string   `json:"project,omitempty" jsonschema:"Project lane this task belongs to"`
	Title              string   `json:"title,omitempty"`
	Description        string   `json:"description,omitempty"`
	ProposedFix        string   `json:"proposed_fix,omitempty"`
	ExpectedGreenState string   `json:"expected_green_state,omitempty"`
	Payload            any      `json:"payload,omitempty" jsonschema:"Structured payload (goal_id, cycle, log excerpts, ...); surfaced on detail/edit responses, omitted from the list"`
	ClaimedBy          string   `json:"claimed_by,omitempty" jsonschema:"Fingerprint of the machine that claimed the task"`
	ClaimedAt          string   `json:"claimed_at,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	DependsOn          []string `json:"depends_on,omitempty" jsonschema:"Prerequisite task ids this task depends on"`
	Ready              bool     `json:"ready" jsonschema:"Backend-computed readiness; false means the task is blocked on an unresolved prerequisite"`
}

// descriptionPreviewLen caps the description excerpt returned in a list row so a
// page stays token-bounded no matter how large the underlying task bodies are
// (descriptions/fixes are TEXT and can be hundreds of KB). The full text is
// always available via task-get.
const descriptionPreviewLen = 280

// preview returns s capped to descriptionPreviewLen runes, marking truncation
// with an ellipsis. Rune-based so it never splits a multibyte character.
func preview(s string) string {
	r := []rune(s)
	if len(r) <= descriptionPreviewLen {
		return s
	}
	return string(r[:descriptionPreviewLen]) + "…"
}

// TaskSummary is the token-bounded list-row projection of a task: identity,
// status, and a capped description preview — but NOT the full body fields
// (description/proposed_fix/expected_green_state), which are returned in full
// only by task-get. This keeps a list/search page small regardless of how large
// individual task bodies are.
type TaskSummary struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	Category           string `json:"category,omitempty"`
	Severity           string `json:"severity,omitempty"`
	Priority           int    `json:"priority"`
	Project            string `json:"project,omitempty"`
	Title              string `json:"title,omitempty"`
	Fingerprint        string `json:"fingerprint,omitempty" jsonschema:"Fingerprint of the machine that reported the task"`
	ClaimedBy          string `json:"claimed_by,omitempty" jsonschema:"Fingerprint of the machine that claimed the task"`
	InstanceName       string `json:"instance_name,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
	DescriptionPreview string `json:"description_preview,omitempty" jsonschema:"Capped excerpt of the description; fetch the full task with task-get"`
}

func toTaskSummary(t producer.Task) TaskSummary {
	return TaskSummary{
		ID:                 t.ID.String(),
		Status:             t.Status,
		Category:           t.Category,
		Severity:           t.Severity,
		Priority:           t.Priority,
		Project:            t.Project,
		Title:              t.Title,
		Fingerprint:        t.Fingerprint,
		ClaimedBy:          t.ClaimedBy,
		InstanceName:       t.InstanceName,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		DescriptionPreview: preview(t.Description),
	}
}

func toTaskView(t producer.Task) TaskView {
	var dependsOn []string
	if len(t.DependsOn) > 0 {
		dependsOn = make([]string, 0, len(t.DependsOn))
		for _, d := range t.DependsOn {
			dependsOn = append(dependsOn, d.String())
		}
	}
	return TaskView{
		ID:                 t.ID.String(),
		Fingerprint:        t.Fingerprint,
		InstanceID:         t.InstanceID.String(),
		InstanceName:       t.InstanceName,
		Category:           t.Category,
		Severity:           t.Severity,
		Status:             t.Status,
		Priority:           t.Priority,
		Project:            t.Project,
		Title:              t.Title,
		Description:        t.Description,
		ProposedFix:        t.ProposedFix,
		ExpectedGreenState: t.ExpectedGreenState,
		Payload:            t.Payload,
		ClaimedBy:          t.ClaimedBy,
		ClaimedAt:          t.ClaimedAt,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		DependsOn:          dependsOn,
		Ready:              t.Ready,
	}
}

// taskClient builds the producer client shared by the query tools. A nil client
// means reporting is disabled in .tmux-cli/setting.yaml — surfaced as an error so
// the agent gets a clear, actionable message instead of a silent no-op.
func (s *Server) taskClient() (*producer.Client, error) {
	cfg, err := producer.LoadConfig(s.workingDir)
	if err != nil {
		return nil, err
	}
	client := newProducerClient(cfg)
	if client == nil {
		return nil, fmt.Errorf("%w: task reporting is disabled (enable api in .tmux-cli/setting.yaml)", ErrInvalidInput)
	}
	return client, nil
}

// rejectIfNotIn rejects value when it is non-empty and not a member of set,
// naming the field and listing the allowed values. An empty value is accepted
// (the caller decides whether the field is required separately).
func rejectIfNotIn(field, value string, set map[string]bool) error {
	if value == "" {
		return nil
	}
	if !set[value] {
		return fmt.Errorf("%w: invalid %s %q; allowed: %s", ErrInvalidInput, field, value, sortedKeys(set))
	}
	return nil
}

// ----------------------------------------------------------------------------
// task-list
// ----------------------------------------------------------------------------

// TaskListInput defines the input schema for the task-list tool. All filters are
// optional and AND-combined.
type TaskListInput struct {
	Fingerprint string `json:"fingerprint,omitempty" jsonschema:"Tasks reported by this machine fingerprint"`
	ClaimedBy   string `json:"claimed_by,omitempty" jsonschema:"Tasks claimed by (assigned to) this machine fingerprint"`
	Status      string `json:"status,omitempty" jsonschema:"Filter by status: new, claimed, in_progress, resolved, failed, denied, archived"`
	Category    string `json:"category,omitempty" jsonschema:"Filter by category: plan, supervisor, validator, execute, general"`
	Severity    string `json:"severity,omitempty" jsonschema:"Filter by severity: critical, warning, info"`
	Project     string `json:"project,omitempty" jsonschema:"Filter by project (lane) name, e.g. cli or web; omit for the default lane"`
	Since       string `json:"since,omitempty" jsonschema:"ISO-8601 datetime; only tasks created at or after this time"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size, clamped to 1..200 (default 50)"`
	Offset      int    `json:"offset,omitempty" jsonschema:"Rows to skip (default 0)"`
}

// TaskListOutput is one page of results plus the full filtered total. Rows are
// TaskSummary (bounded); use task-get for a row's full body and event history.
type TaskListOutput struct {
	Tasks  []TaskSummary `json:"tasks"`
	Total  int           `json:"total" jsonschema:"Total tasks matching the filters, ignoring pagination"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

// TaskList queries the backend for a filtered, paginated page of tasks.
func (s *Server) TaskList(ctx context.Context, in TaskListInput) (*TaskListOutput, error) {
	if err := rejectIfNotIn("status", in.Status, producer.ValidStatuses); err != nil {
		return nil, err
	}
	if err := rejectIfNotIn("category", in.Category, producer.ValidCategories); err != nil {
		return nil, err
	}
	if err := rejectIfNotIn("severity", in.Severity, producer.ValidSeverities); err != nil {
		return nil, err
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	list, err := client.ListTasks(ctx, producer.ListTasksParams{
		Fingerprint: in.Fingerprint,
		ClaimedBy:   in.ClaimedBy,
		Status:      in.Status,
		Category:    in.Category,
		Severity:    in.Severity,
		Project:     in.Project,
		Since:       in.Since,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, err
	}

	out := &TaskListOutput{Total: list.Total, Limit: list.Limit, Offset: list.Offset}
	out.Tasks = make([]TaskSummary, 0, len(list.Tasks))
	for _, t := range list.Tasks {
		out.Tasks = append(out.Tasks, toTaskSummary(t))
	}
	return out, nil
}

// TaskListHandler is the MCP tool handler for task-list.
func (s *Server) TaskListHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskListInput) (
	*sdkmcp.CallToolResult, TaskListOutput, error,
) {
	output, err := s.TaskList(ctx, input)
	if err != nil {
		return nil, TaskListOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-get
// ----------------------------------------------------------------------------

// TaskGetInput defines the input schema for the task-get tool.
type TaskGetInput struct {
	ID string `json:"id" jsonschema:"Backend task id to fetch; required"`
}

// TaskEventView is an agent-facing projection of one task history event.
type TaskEventView struct {
	ID        string         `json:"id"`
	Action    string         `json:"action"`
	Actor     string         `json:"actor,omitempty"`
	OldValue  map[string]any `json:"old_value,omitempty"`
	NewValue  map[string]any `json:"new_value,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
}

// TaskGetOutput is a single task plus its event history.
type TaskGetOutput struct {
	Task   TaskView        `json:"task"`
	Events []TaskEventView `json:"events"`
}

// TaskGet fetches one task and its event history.
func (s *Server) TaskGet(ctx context.Context, in TaskGetInput) (*TaskGetOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	detail, err := client.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &TaskGetOutput{Task: toTaskView(detail.Task)}
	out.Events = make([]TaskEventView, 0, len(detail.Events))
	for _, e := range detail.Events {
		out.Events = append(out.Events, TaskEventView{
			ID:        e.ID.String(),
			Action:    e.Action,
			Actor:     e.Actor,
			OldValue:  e.OldValue,
			NewValue:  e.NewValue,
			CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}

// TaskGetHandler is the MCP tool handler for task-get.
func (s *Server) TaskGetHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskGetInput) (
	*sdkmcp.CallToolResult, TaskGetOutput, error,
) {
	output, err := s.TaskGet(ctx, input)
	if err != nil {
		return nil, TaskGetOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-claim
// ----------------------------------------------------------------------------

// TaskClaimInput defines the input schema for the task-claim tool. Both filters
// are optional and narrow the pool of claimable tasks.
type TaskClaimInput struct {
	Category string `json:"category,omitempty" jsonschema:"Only claim tasks of this category: plan, supervisor, validator, execute, general"`
	Severity string `json:"severity,omitempty" jsonschema:"Only claim tasks of this severity: critical, warning, info"`
}

// TaskClaimOutput reports whether a task was claimed and, if so, the task. When
// nothing was claimable Claimed is false and Task is nil — that is not an error.
type TaskClaimOutput struct {
	Claimed bool      `json:"claimed" jsonschema:"True if a task was claimed; false when nothing matched"`
	Task    *TaskView `json:"task,omitempty"`
}

// TaskClaim atomically claims the next highest-priority new task.
func (s *Server) TaskClaim(ctx context.Context, in TaskClaimInput) (*TaskClaimOutput, error) {
	if err := rejectIfNotIn("category", in.Category, producer.ValidCategories); err != nil {
		return nil, err
	}
	if err := rejectIfNotIn("severity", in.Severity, producer.ValidSeverities); err != nil {
		return nil, err
	}

	// Git-freshness preflight (goal-005): refuse to claim a backend task onto a
	// diverged checkout. Best-effort — a settings-load failure skips the gate (a
	// claim is never blocked on a config-read error), and a bare working dir with
	// no upstream SKIPs inside PreflightGitFreshness, keeping existing claim tests
	// green. Runs after filter validation, before the producer client is built.
	if settings, lerr := setup.LoadSettings(s.workingDir); lerr == nil && settings.Taskvisor.GitFreshnessEnabled() {
		if _, ferr := taskvisor.PreflightGitFreshness(ctx, nil, s.workingDir, filepath.Base(s.workingDir)); ferr != nil {
			return nil, ferr
		}
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	task, err := client.ClaimTask(ctx, producer.ClaimParams{Category: in.Category, Severity: in.Severity})
	if err != nil {
		return nil, err
	}
	if task == nil {
		return &TaskClaimOutput{Claimed: false}, nil
	}
	view := toTaskView(*task)
	return &TaskClaimOutput{Claimed: true, Task: &view}, nil
}

// TaskClaimHandler is the MCP tool handler for task-claim.
func (s *Server) TaskClaimHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskClaimInput) (
	*sdkmcp.CallToolResult, TaskClaimOutput, error,
) {
	output, err := s.TaskClaim(ctx, input)
	if err != nil {
		return nil, TaskClaimOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-update-status
// ----------------------------------------------------------------------------

// TaskUpdateStatusInput defines the input schema for the task-update-status tool.
type TaskUpdateStatusInput struct {
	ID         string         `json:"id" jsonschema:"Backend task id to advance; required"`
	Status     string         `json:"status" jsonschema:"Target status; required; one of in_progress, resolved, failed"`
	Resolution map[string]any `json:"resolution,omitempty" jsonschema:"Optional structured resolution recorded when transitioning to resolved"`
}

// TaskUpdateStatusOutput is the updated task.
type TaskUpdateStatusOutput struct {
	Task TaskView `json:"task"`
}

// TaskUpdateStatus advances a claimed task's status. Only the worker that claimed
// the task may advance it (the backend enforces this and returns ErrForbidden).
func (s *Server) TaskUpdateStatus(ctx context.Context, in TaskUpdateStatusInput) (*TaskUpdateStatusOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Status) == "" {
		return nil, fmt.Errorf("%w: missing required field: status", ErrInvalidInput)
	}
	if !producer.ValidWorkerStatusTargets[in.Status] {
		return nil, fmt.Errorf("%w: invalid status %q; allowed: %s", ErrInvalidInput, in.Status, sortedKeys(producer.ValidWorkerStatusTargets))
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	task, err := client.UpdateTaskStatus(ctx, id, producer.UpdateStatusParams{
		Status:     in.Status,
		Resolution: in.Resolution,
	})
	if err != nil {
		return nil, err
	}
	return &TaskUpdateStatusOutput{Task: toTaskView(*task)}, nil
}

// TaskUpdateStatusHandler is the MCP tool handler for task-update-status.
func (s *Server) TaskUpdateStatusHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskUpdateStatusInput) (
	*sdkmcp.CallToolResult, TaskUpdateStatusOutput, error,
) {
	output, err := s.TaskUpdateStatus(ctx, input)
	if err != nil {
		return nil, TaskUpdateStatusOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-edit
// ----------------------------------------------------------------------------

// TaskEditInput defines the input schema for the task-edit tool. id is required;
// every other field is an optional content edit applied only when provided.
type TaskEditInput struct {
	ID                 string         `json:"id" jsonschema:"Backend task id to edit; required"`
	Title              string         `json:"title,omitempty" jsonschema:"New short one-line summary"`
	Description        string         `json:"description,omitempty" jsonschema:"New full description of the issue and its context"`
	ProposedFix        string         `json:"proposed_fix,omitempty" jsonschema:"New concrete remediation; must be actionable (a contentless stub like TBD, none, n/a is rejected)"`
	ExpectedGreenState string         `json:"expected_green_state,omitempty" jsonschema:"New description of what passing/fixed looks like"`
	Severity           string         `json:"severity,omitempty" jsonschema:"New severity; one of critical, warning, info (rejected, never coerced, if invalid)"`
	Category           string         `json:"category,omitempty" jsonschema:"New category; one of plan, supervisor, validator, execute, general (rejected, never coerced, if invalid)"`
	Payload            map[string]any `json:"payload,omitempty" jsonschema:"New structured payload forwarded verbatim to the backend"`
}

// TaskEditOutput is the updated task.
type TaskEditOutput struct {
	Task TaskView `json:"task"`
}

// TaskEdit amends a filed task's content in place via PATCH /api/v1/tasks/{id},
// so an agent can fix or clarify a reported task without deny+re-report (which
// loses the id and event history). All input is validated BEFORE the client is
// built (invalid input never hits the wire): id is required, severity/category
// are rejected without coercion, a contentless proposed_fix stub is rejected,
// and at least one editable field must be provided. The backend owns recording
// the `edited` event and rejecting terminal-state edits (surfaced as 422 ->
// producer.ErrInvalidTransition).
func (s *Server) TaskEdit(ctx context.Context, in TaskEditInput) (*TaskEditOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	if err := rejectIfNotIn("severity", in.Severity, producer.ValidSeverities); err != nil {
		return nil, err
	}
	if err := rejectIfNotIn("category", in.Category, producer.ValidCategories); err != nil {
		return nil, err
	}
	if in.ProposedFix != "" {
		if c := strings.ToLower(strings.TrimSpace(in.ProposedFix)); contentlessCorrections[c] {
			return nil, fmt.Errorf("%w: proposed_fix is a contentless stub (%q); provide a concrete remediation", ErrInvalidInput, in.ProposedFix)
		}
	}
	if strings.TrimSpace(in.Title) == "" &&
		strings.TrimSpace(in.Description) == "" &&
		strings.TrimSpace(in.ProposedFix) == "" &&
		strings.TrimSpace(in.ExpectedGreenState) == "" &&
		strings.TrimSpace(in.Severity) == "" &&
		strings.TrimSpace(in.Category) == "" &&
		in.Payload == nil {
		return nil, fmt.Errorf("%w: no editable fields provided", ErrInvalidInput)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	task, err := client.EditTask(ctx, id, producer.EditTaskParams{
		Title:              in.Title,
		Description:        in.Description,
		ProposedFix:        in.ProposedFix,
		ExpectedGreenState: in.ExpectedGreenState,
		Severity:           in.Severity,
		Category:           in.Category,
		Payload:            in.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &TaskEditOutput{Task: toTaskView(*task)}, nil
}

// TaskEditHandler is the MCP tool handler for task-edit.
func (s *Server) TaskEditHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskEditInput) (
	*sdkmcp.CallToolResult, TaskEditOutput, error,
) {
	output, err := s.TaskEdit(ctx, input)
	if err != nil {
		return nil, TaskEditOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-deny
// ----------------------------------------------------------------------------

// TaskDenyInput defines the input schema for the task-deny tool. Both fields are
// required; reason is recorded with the denial.
type TaskDenyInput struct {
	ID     string `json:"id" jsonschema:"Backend task id to deny; required"`
	Reason string `json:"reason" jsonschema:"Why the task is being denied; required, non-empty"`
}

// TaskDenyOutput is the denied task.
type TaskDenyOutput struct {
	Task TaskView `json:"task"`
}

// TaskDeny denies a task, recording the supplied reason. It is a thin alias over
// the shared setTaskStatus core (status "denied") so deny/resolve/archive share
// one validated code path; the id/reason gate and client-after-validation
// ordering are enforced there.
func (s *Server) TaskDeny(ctx context.Context, in TaskDenyInput) (*TaskDenyOutput, error) {
	view, err := s.setTaskStatus(ctx, in.ID, "denied", in.Reason)
	if err != nil {
		return nil, err
	}
	return &TaskDenyOutput{Task: *view}, nil
}

// TaskDenyHandler is the MCP tool handler for task-deny.
func (s *Server) TaskDenyHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskDenyInput) (
	*sdkmcp.CallToolResult, TaskDenyOutput, error,
) {
	output, err := s.TaskDeny(ctx, input)
	if err != nil {
		return nil, TaskDenyOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-resolve (force / administrative resolve)
// ----------------------------------------------------------------------------

// TaskResolveInput defines the input schema for the task-resolve tool. Both
// fields are required; reason is recorded with the forced resolution.
type TaskResolveInput struct {
	ID     string `json:"id" jsonschema:"Backend task id to force-resolve; required"`
	Reason string `json:"reason" jsonschema:"Why the task is being force-resolved; required, non-empty"`
}

// TaskResolveOutput is the resolved task.
type TaskResolveOutput struct {
	Task TaskView `json:"task"`
}

// TaskResolve force-resolves a task (administrative endpoint), recording the
// supplied reason. It is a thin alias over the shared setTaskStatus core (status
// "resolved") so deny/resolve/archive share one validated code path; the
// id/reason gate and client-after-validation ordering are enforced there.
func (s *Server) TaskResolve(ctx context.Context, in TaskResolveInput) (*TaskResolveOutput, error) {
	view, err := s.setTaskStatus(ctx, in.ID, "resolved", in.Reason)
	if err != nil {
		return nil, err
	}
	return &TaskResolveOutput{Task: *view}, nil
}

// TaskResolveHandler is the MCP tool handler for task-resolve.
func (s *Server) TaskResolveHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskResolveInput) (
	*sdkmcp.CallToolResult, TaskResolveOutput, error,
) {
	output, err := s.TaskResolve(ctx, input)
	if err != nil {
		return nil, TaskResolveOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-set-status (id-targeted admin transition: denied | resolved | archived)
// ----------------------------------------------------------------------------

// TaskSetStatusInput defines the input schema for the task-set-status tool. All
// three fields are required; status must be one of the admin terminal targets.
type TaskSetStatusInput struct {
	ID     string `json:"id" jsonschema:"Backend task id to transition; required"`
	Status string `json:"status" jsonschema:"Target status; required; one of denied, resolved, archived"`
	Reason string `json:"reason" jsonschema:"Why the task is being transitioned; required, non-empty"`
}

// TaskSetStatusOutput is the transitioned task.
type TaskSetStatusOutput struct {
	Task TaskView `json:"task"`
}

// setTaskStatus is the shared core for the id-targeted, no-claim admin
// transitions exposed by task-set-status (and the task-deny/task-resolve
// aliases). It trims and validates id/reason (rejecting with ErrInvalidInput
// BEFORE building a client, so invalid input never reaches the network),
// validates status against the closed admin set with no coercion, then routes to
// the matching producer endpoint. status is required: the explicit non-empty
// check runs before rejectIfNotIn (which accepts an empty value) so a blank
// status is reported as a missing field rather than passing the enum gate.
func (s *Server) setTaskStatus(ctx context.Context, id, status, reason string) (*TaskView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: missing required field: reason", ErrInvalidInput)
	}
	if strings.TrimSpace(status) == "" {
		return nil, fmt.Errorf("%w: missing required field: status", ErrInvalidInput)
	}
	if err := rejectIfNotIn("status", status, producer.ValidAdminStatusTargets); err != nil {
		return nil, err
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	var task *producer.Task
	switch status {
	case "denied":
		task, err = client.Deny(ctx, id, reason)
	case "resolved":
		task, err = client.ForceResolve(ctx, id, reason)
	case "archived":
		task, err = client.Archive(ctx, id, reason)
	}
	if err != nil {
		return nil, err
	}
	view := toTaskView(*task)
	return &view, nil
}

// TaskSetStatus performs an id-targeted, no-claim admin transition of a task to
// one of denied/resolved/archived, recording the supplied reason. It delegates
// to the shared setTaskStatus core.
func (s *Server) TaskSetStatus(ctx context.Context, in TaskSetStatusInput) (*TaskSetStatusOutput, error) {
	view, err := s.setTaskStatus(ctx, in.ID, in.Status, in.Reason)
	if err != nil {
		return nil, err
	}
	return &TaskSetStatusOutput{Task: *view}, nil
}

// TaskSetStatusHandler is the MCP tool handler for task-set-status.
func (s *Server) TaskSetStatusHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskSetStatusInput) (
	*sdkmcp.CallToolResult, TaskSetStatusOutput, error,
) {
	output, err := s.TaskSetStatus(ctx, input)
	if err != nil {
		return nil, TaskSetStatusOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-link (add one prerequisite dependency edge)
// ----------------------------------------------------------------------------

// TaskLinkInput defines the input schema for the task-link tool. Both fields are
// required: id is the dependent task, prerequisite_id the task it depends on.
type TaskLinkInput struct {
	ID             string `json:"id" jsonschema:"Backend task id that should depend on the prerequisite; required"`
	PrerequisiteID string `json:"prerequisite_id" jsonschema:"Backend task id of the prerequisite this task depends on; required"`
}

// TaskLinkOutput is the updated task after the dependency edge is added.
type TaskLinkOutput struct {
	Task TaskView `json:"task"`
}

// TaskLink adds one prerequisite dependency edge to a task via POST
// /api/v1/tasks/{id}/dependencies. Both id and prerequisite_id are trimmed and
// required, and are validated BEFORE the producer client is built (invalid input
// never hits the wire). The backend owns the dependency model: it gates claiming
// on unresolved prerequisites, computes ready, and rejects a cycle/self-dep
// (surfaced as producer.ErrInvalidTransition); an unknown task surfaces as
// producer.ErrTaskNotFound.
func (s *Server) TaskLink(ctx context.Context, in TaskLinkInput) (*TaskLinkOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	prerequisiteID := strings.TrimSpace(in.PrerequisiteID)
	if prerequisiteID == "" {
		return nil, fmt.Errorf("%w: missing required field: prerequisite_id", ErrInvalidInput)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	task, err := client.Link(ctx, id, prerequisiteID)
	if err != nil {
		return nil, err
	}
	return &TaskLinkOutput{Task: toTaskView(*task)}, nil
}

// TaskLinkHandler is the MCP tool handler for task-link.
func (s *Server) TaskLinkHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskLinkInput) (
	*sdkmcp.CallToolResult, TaskLinkOutput, error,
) {
	output, err := s.TaskLink(ctx, input)
	if err != nil {
		return nil, TaskLinkOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ProjectInfo is one entry of the project-lane registry as shown to an agent:
// the project NAME (pass it as task-report's project) plus where it lives.
type ProjectInfo struct {
	Project     string `json:"project" jsonschema:"Project lane name (e.g. cli, web) — pass this as task-report's project to route a cross-project report"`
	Path        string `json:"path,omitempty" jsonschema:"Absolute install path of this project on its machine"`
	Repository  string `json:"repository,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Hostname    string `json:"hostname,omitempty" jsonschema:"Machine hosting this address of the project"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ProjectsListInput are the optional filters for the projects-list tool.
type ProjectsListInput struct {
	Hostname    string `json:"hostname,omitempty" jsonschema:"Only projects hosted on this machine hostname"`
	Fingerprint string `json:"fingerprint,omitempty" jsonschema:"Only projects hosted on this machine fingerprint"`
}

// ProjectsListOutput is the projects-list reply.
type ProjectsListOutput struct {
	Projects []ProjectInfo `json:"projects"`
	Total    int           `json:"total"`
}

// ProjectsList fetches the project-lane registry from the backend so an agent can
// pick the right `project` for a cross-project task-report (confirm the chosen
// project's path exists locally, then pass its name to task-report).
func (s *Server) ProjectsList(ctx context.Context, in ProjectsListInput) (*ProjectsListOutput, error) {
	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}
	list, err := client.ListProjectBindings(ctx, producer.ListBindingsParams{
		Hostname:    in.Hostname,
		Fingerprint: in.Fingerprint,
	})
	if err != nil {
		return nil, err
	}
	out := &ProjectsListOutput{Total: list.Total}
	out.Projects = make([]ProjectInfo, 0, len(list.Bindings))
	for _, b := range list.Bindings {
		out.Projects = append(out.Projects, ProjectInfo{
			Project:     b.ProjectName,
			Path:        b.Path,
			Repository:  b.Repository,
			Branch:      b.Branch,
			Hostname:    b.Hostname,
			Fingerprint: b.Fingerprint,
		})
	}
	return out, nil
}

// ProjectsListHandler is the MCP tool handler for projects-list.
func (s *Server) ProjectsListHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input ProjectsListInput) (
	*sdkmcp.CallToolResult, ProjectsListOutput, error,
) {
	output, err := s.ProjectsList(ctx, input)
	if err != nil {
		return nil, ProjectsListOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// Package mcp implements the Model Context Protocol server for tmux-cli.
//
// Session Detection:
// The server discovers sessions by matching project path stored in tmux session
// environment variables. No session file is needed.
//
// The server provides seventeen tools: windows-list, windows-create,
// windows-kill, windows-send, windows-message, windows-spawn-worker,
// windows-recover-workers, tasks-validate, spec-validate, hooks-config,
// sudo-execute, taskvisor-start, goal-create, goal-add-prerequisite,
// goal-prune, goal-validation-done, and task-report.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
)

// Server represents an MCP server instance with session management capabilities.
//
// The server discovers sessions by matching project path stored in tmux session
// environment variables. No session file is required.
type Server struct {
	executor   tmux.TmuxExecutor
	workingDir string // Absolute path where MCP server started
	version    string // CLI build version, sent as SystemInfo.cliVersion (NotBlank on the backend)
}

// NewServer creates a new MCP server. Never fails — graceful degradation.
// If no matching session is found, tools return ErrSessionNotFound with helpful message.
func NewServer(workingDir string) *Server {
	return NewServerWithVersion(workingDir, "")
}

// NewServerWithVersion is NewServer with an explicit CLI build version. The version
// constant lives in package main (cmd/tmux-cli), which internal/mcp cannot import, so
// the binary injects it here; it becomes SystemInfo.cliVersion on task-report, which the
// backend rejects (422) when blank. Tests use NewServer (version "") since they mock the
// producer and never hit the live validator.
func NewServerWithVersion(workingDir, version string) *Server {
	return &Server{
		executor:   tmux.NewTmuxExecutor(),
		workingDir: normalizeProjectDir(workingDir),
		version:    version,
	}
}

// NewServerWithExecutor creates a new MCP server with an injected executor (for testing).
func NewServerWithExecutor(executor tmux.TmuxExecutor, workingDir string) *Server {
	return &Server{
		executor:   executor,
		workingDir: normalizeProjectDir(workingDir),
	}
}

// normalizeProjectDir maps a per-goal worktree cwd (<base>/.tmux-cli/worktrees/<id>[/...])
// back to <base>. Non-worktree paths are returned unchanged. Thin delegate to
// the shared taskvisor.NormalizeProjectDir so the CLI goal commands and the
// MCP server resolve the base .tmux-cli control plane identically.
func normalizeProjectDir(dir string) string {
	return taskvisor.NormalizeProjectDir(dir)
}

// discoverSession finds the tmux session for this working directory.
// Returns sessionID or error if no matching session found.
func (s *Server) discoverSession() (string, error) {
	sessionID, err := s.executor.FindSessionByEnvironment(
		"TMUX_CLI_PROJECT_PATH", s.workingDir,
	)
	if err != nil {
		return "", fmt.Errorf("%w: failed to search tmux sessions: %w", ErrTmuxCommandFailed, err)
	}
	if sessionID == "" {
		return "", fmt.Errorf("%w: no tmux-cli session found for directory %s",
			ErrSessionNotFound, s.workingDir)
	}
	return sessionID, nil
}

// SpecValidateInput defines the input schema for spec-validate tool
type SpecValidateInput struct {
	File string `json:"file" jsonschema:"Absolute path to the spec .md file to validate"`
}

// SpecValidateOutput defines the output schema for spec-validate tool
type SpecValidateOutput struct {
	Valid       bool              `json:"valid" jsonschema:"True if spec passes all S0-S8 checks"`
	Gaps        []SpecValidateGap `json:"gaps,omitempty" jsonschema:"Quality gaps found, each with ID and message"`
	Stats       SpecValidateStats `json:"stats" jsonschema:"Spec statistics — test cases, acceptance criteria, code map entries"`
	DepWarnings []DepWarning      `json:"dep_warnings,omitempty" jsonschema:"Cross-goal dependency warnings from InferMissingDeps"`
}

// DepWarning represents a missing cross-goal dependency edge
type DepWarning struct {
	Consumer string `json:"consumer" jsonschema:"Goal ID that references the stem without a dependency edge"`
	Producer string `json:"producer" jsonschema:"Goal ID that produces the stem"`
	Stem     string `json:"stem" jsonschema:"File-path stem linking consumer to producer"`
	Evidence string `json:"evidence" jsonschema:"Field where consumer references the stem (acceptance or validate)"`
}

// SpecValidateGap represents a single quality gap found during validation
type SpecValidateGap struct {
	ID      string `json:"id" jsonschema:"Gap identifier (S0-S8)"`
	Message string `json:"message" jsonschema:"Human-readable description of the gap and how to fix it"`
}

// SpecValidateStats contains quantitative metrics from the spec
type SpecValidateStats struct {
	TestCases          int `json:"test_cases" jsonschema:"Number of test cases found in Test Plan"`
	AcceptanceCriteria int `json:"acceptance_criteria" jsonschema:"Number of checkbox acceptance criteria"`
	CodeMapEntries     int `json:"code_map_entries" jsonschema:"Number of file:line references in Code Map"`
}

// SpecValidateHandler is the MCP tool handler for spec-validate operation.
func (s *Server) SpecValidateHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input SpecValidateInput) (
	*sdkmcp.CallToolResult,
	SpecValidateOutput,
	error,
) {
	output, err := s.SpecValidate(input.File)
	if err != nil {
		return nil, SpecValidateOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// TasksValidateInput defines the input schema for tasks-validate tool.
type TasksValidateInput struct {
	GoalID string `json:"goal_id,omitempty" jsonschema:"Optional goal id; validates .tmux-cli/goals/<id>/tasks.yaml instead of the top-level planning-queue"`
}

// TasksValidateOutput defines the output schema for tasks-validate tool
type TasksValidateOutput struct {
	Valid  bool     `json:"valid" jsonschema:"True if tasks.yaml passes all lean format checks"`
	Errors []string `json:"errors,omitempty" jsonschema:"Validation errors with fix instructions"`
}

// TasksValidateHandler is the MCP tool handler for tasks-validate operation.
func (s *Server) TasksValidateHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TasksValidateInput) (
	*sdkmcp.CallToolResult,
	TasksValidateOutput,
	error,
) {
	output, err := s.TasksValidate(input)
	if err != nil {
		return nil, TasksValidateOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// WindowsListInput defines the input schema for windows-list tool (no parameters needed)
type WindowsListInput struct{}

// WindowsListOutput defines the output schema for windows-list tool
type WindowsListOutput struct {
	Windows []WindowListItem `json:"windows" jsonschema:"Array of window names (without IDs or UUIDs)"`
}

// WindowsSendInput defines the input schema for windows-send tool
type WindowsSendInput struct {
	WindowID string `json:"windowId" jsonschema:"Window identifier to send command to (e.g. '@0' or '@1')"`
	Command  string `json:"command" jsonschema:"Command text to execute in the window (sent exactly as provided)"`
}

// WindowsSendOutput defines the output schema for windows-send tool
type WindowsSendOutput struct {
	Success bool `json:"success" jsonschema:"True if command was sent successfully"`
}

// WindowsMessageInput defines the input schema for windows-message tool
type WindowsMessageInput struct {
	Receiver string `json:"receiver" jsonschema:"Window name or ID to send message to"`
	Message  string `json:"message" jsonschema:"Message content to send"`
}

// WindowsMessageOutput defines the output schema for windows-message tool
type WindowsMessageOutput struct {
	Success bool   `json:"success" jsonschema:"True if message was delivered"`
	Sender  string `json:"sender" jsonschema:"Name of the sending window (auto-detected from TMUX_WINDOW_UUID)"`
}

// WindowInfo represents a window returned by WindowsCreate
type WindowInfo struct {
	TmuxWindowID string `json:"tmuxWindowId"`
	Name         string `json:"name"`
	UUID         string `json:"uuid,omitempty"`
}

// WindowsCreateInput defines the input schema for windows-create tool
type WindowsCreateInput struct {
	Name    string `json:"name" jsonschema:"Name for the new window"`
	Command string `json:"command,omitempty" jsonschema:"Optional command to execute in the new window (empty = no command)"`
}

// WindowsCreateOutput defines the output schema for windows-create tool
type WindowsCreateOutput struct {
	Window *WindowInfo `json:"window" jsonschema:"Details of the created window including ID, name, and metadata"`
}

// WindowsSpawnWorkerInput defines the input schema for windows-spawn-worker tool
type WindowsSpawnWorkerInput struct {
	SupervisorWid    string `json:"supervisorWid" jsonschema:"Supervisor's tmux window name (e.g. 'supervisor'). Used in the task message and RESPONSE PROTOCOL."`
	Subtask          string `json:"subtask" jsonschema:"One-line label for the worker's task (e.g. 'audit auth module'). Appears as SUBTASK in the task message."`
	ContextFile      string `json:"contextFile" jsonschema:"Path to the context .md file the worker should read for full spec."`
	Scope            string `json:"scope" jsonschema:"Multi-line scope summary — files, directories, what to investigate or implement."`
	Context          string `json:"context,omitempty" jsonschema:"Multi-line supporting context — prior findings, constraints, non-goals. Optional."`
	Deliverable      string `json:"deliverable,omitempty" jsonschema:"Custom deliverable format to replace the default FINDINGS/RISKS/RECOMMENDATION/FILES sections. When empty, the standard deliverable is used. Use this for spec-writing workers that need a different output format."`
	CodeRules        string `json:"codeRules,omitempty" jsonschema:"Pre-rendered code-rule payload (from 'tmux-cli rules match') relayed verbatim to the worker as a CODE_RULES section. Empty omits the section. Supervisor-computed; buildTaskMessage never calls rules itself."`
	Prefix           string `json:"prefix,omitempty" jsonschema:"Window name prefix (e.g. 'investigator-'). Defaults to 'execute-' if empty. Max workers limit applies per-prefix."`
	WorkingDirectory string `json:"workingDirectory,omitempty" jsonschema:"Optional working directory the worker's shell starts in (tmux -c). Used to run a worker inside a goal's git worktree for validate isolation. When empty, the session default cwd is used."`
}

// WindowsSpawnWorkerOutput defines the output schema for windows-spawn-worker tool
type WindowsSpawnWorkerOutput struct {
	Window      *WindowInfo `json:"window" jsonschema:"Details of the created worker window"`
	WorkerName  string      `json:"workerName" jsonschema:"The execute-N name assigned to this worker"`
	TaskMessage string      `json:"taskMessage" jsonschema:"The exact task message sent to the worker"`
}

// WindowsRecoverWorkersInput defines the input schema for windows-recover-workers tool
type WindowsRecoverWorkersInput struct {
	Message   string `json:"message,omitempty" jsonschema:"Message to send after dismissing prompt. Defaults to 'continue' if empty."`
	CallerWid string `json:"callerWid,omitempty" jsonschema:"Window name of the calling supervisor (e.g. supervisor-020). When goal-namespaced, recovery is restricted to that goal's execute workers. Empty or bare names recover all execute-* workers."`
}

// WindowsRecoverWorkersOutput defines the output schema for windows-recover-workers tool
type WindowsRecoverWorkersOutput struct {
	Recovered []string `json:"recovered" jsonschema:"Names of worker windows that were recovered"`
	Message   string   `json:"message" jsonschema:"The message that was sent to each worker"`
	Count     int      `json:"count" jsonschema:"Number of workers recovered"`
}

// WindowsKillInput defines the input schema for windows-kill tool
type WindowsKillInput struct {
	WindowID string `json:"windowId" jsonschema:"Window name to terminate (e.g. 'execute-3' or 'supervisor')"`
}

// WindowsKillOutput defines the output schema for windows-kill tool
type WindowsKillOutput struct {
	Success bool `json:"success" jsonschema:"True if window was killed or already didn't exist (idempotent)"`
}

// HooksConfigInput defines the input schema for hooks-config tool
type HooksConfigInput struct {
	Action string `json:"action" jsonschema:"Action to perform: list, enable, or disable"`
	Hook   string `json:"hook,omitempty" jsonschema:"Hook name: session_notify or block_interactive. Required for enable/disable."`
}

// HooksConfigOutput defines the output schema for hooks-config tool
type HooksConfigOutput struct {
	Hooks   *setup.HooksSettings `json:"hooks"`
	Changed bool                 `json:"changed"`
}

// SudoExecuteInput defines the input schema for sudo-execute tool
type SudoExecuteInput struct {
	Command string `json:"command" jsonschema:"Shell command (ignored — tool is disabled, use tmux-cli sudo instead)"`
}

// SudoExecuteOutput defines the output schema for sudo-execute tool
type SudoExecuteOutput struct {
	Message string `json:"message" jsonschema:"Guidance message"`
}

// SudoExecuteHandler is the MCP tool handler for sudo-execute operation.
func (s *Server) SudoExecuteHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input SudoExecuteInput) (
	*sdkmcp.CallToolResult,
	SudoExecuteOutput,
	error,
) {
	_, err := s.SudoExecute(input.Command)
	if err != nil {
		return nil, SudoExecuteOutput{}, err
	}
	result, out := prependStaleWarning(SudoExecuteOutput{})
	return result, out, nil
}

// TaskvisorStartInput defines the input schema for taskvisor-start tool (no parameters needed)
type TaskvisorStartInput struct{}

// TaskvisorStartOutput defines the output schema for taskvisor-start tool
type TaskvisorStartOutput struct {
	Started bool `json:"started" jsonschema:"True if the taskvisor-start signal file was written"`
}

// TaskvisorStartHandler is the MCP tool handler for taskvisor-start operation.
func (s *Server) TaskvisorStartHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskvisorStartInput) (
	*sdkmcp.CallToolResult,
	TaskvisorStartOutput,
	error,
) {
	output, err := s.TaskvisorStart()
	if err != nil {
		return nil, TaskvisorStartOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// GoalCreateInput defines the input schema for goal-create tool
type GoalCreateInput struct {
	Description string   `json:"description" jsonschema:"Goal description — what should be achieved (max 120 chars; use acceptance for detail)"`
	Acceptance  []string `json:"acceptance,omitempty" jsonschema:"Acceptance criteria the goal must satisfy"`
	Validate    []string `json:"validate" jsonschema:"Validation steps to verify the goal"`
	Context     string   `json:"context,omitempty" jsonschema:"Background context for the goal"`
	NotInScope  string   `json:"not_in_scope,omitempty" jsonschema:"What is explicitly out of scope"`
	Phase       string   `json:"phase,omitempty" jsonschema:"Development phase (gate,scaffold,fixtures,domain,application,infrastructure,action,auth,event,cross-cutting,deployment,ci,final)"`
	MaxRetries  int      `json:"max_retries,omitempty" jsonschema:"Maximum retry attempts before failing (default 5)"`
	DependsOn   []string `json:"depends_on,omitempty" jsonschema:"IDs of goals this goal depends on (must exist in goals.yaml)"`
	Scope       []string `json:"scope,omitempty" jsonschema:"Declared file/namespace footprint (globs like internal/x/** or namespace prefixes like App\\Billing). The disjoint-scope co-scheduling gate serializes goals with overlapping or unknown scope under MaxGoals>1; omit to derive from deliverables (treated as unknown = serialize)"`
	Priority    int      `json:"priority,omitempty" jsonschema:"Dispatch priority (higher = dispatched first; default 0)"`
	Lane        string   `json:"lane,omitempty" jsonschema:"Validation lane: solo (cheap single-investigator gate, demoted to full on any failure) or full; empty defaults to full"`

	Preconditions []taskvisor.Precondition `json:"preconditions,omitempty" jsonschema:"Optional precondition gates ({kind:env|service, spec, remedy}); daemon parks the goal until each is met"`

	InvestigationConfig []InvestigatorInput `json:"investigation_config,omitempty" jsonschema:"Optional 2–4 investigators for the goal's Investigation Config; omit to auto-derive from validate rules"`
}

// InvestigatorInput is the MCP wire schema for one entry of investigation_config.
// It maps field-for-field onto taskvisor.Investigator. It exists separately so
// the wire contract (lowercase json keys + per-field jsonschema descriptions) is
// owned in the mcp package — taskvisor.Investigator carries no json tags and
// adding them is out of scope (M1). jsonschema tags use bare description text.
type InvestigatorInput struct {
	Name      string   `json:"name" jsonschema:"Investigator title (e.g. Static analysis)"`
	Type      string   `json:"type" jsonschema:"Investigation type from the Reference Table (static-analysis, quality-gate, test-execution, architecture-check, convention-audit, code-review, e2e-test, integration-test)"`
	Paths     []string `json:"paths,omitempty" jsonschema:"Paths the investigator inspects"`
	Commands  []string `json:"commands" jsonschema:"Commands to run (at least one)"`
	Pass      string   `json:"pass" jsonschema:"What a pass looks like"`
	Fail      string   `json:"fail,omitempty" jsonschema:"What a fail looks like"`
	Condition string   `json:"condition,omitempty" jsonschema:"Optional condition under which this investigator applies"`
}

// GoalCreateOutput defines the output schema for goal-create tool
type GoalCreateOutput struct {
	ID string `json:"id" jsonschema:"Generated sequential goal ID (e.g. goal-001)"`
}

// GoalCreateHandler is the MCP tool handler for goal-create operation.
func (s *Server) GoalCreateHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input GoalCreateInput) (
	*sdkmcp.CallToolResult,
	GoalCreateOutput,
	error,
) {
	var investigators []taskvisor.Investigator
	if len(input.InvestigationConfig) > 0 {
		investigators = make([]taskvisor.Investigator, len(input.InvestigationConfig))
		for i, in := range input.InvestigationConfig {
			investigators[i] = taskvisor.Investigator{
				Name:      in.Name,
				Type:      in.Type,
				Paths:     in.Paths,
				Commands:  in.Commands,
				Pass:      in.Pass,
				Fail:      in.Fail,
				Condition: in.Condition,
			}
		}
	}

	output, err := s.GoalCreate(input.Description, input.Acceptance, input.Validate, input.Context, input.NotInScope, input.Phase, input.MaxRetries, input.DependsOn, input.Preconditions, investigators, input.Scope, input.Priority, input.Lane)
	if err != nil {
		return nil, GoalCreateOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// GoalAddPrerequisiteInput defines the input schema for goal-add-prerequisite tool.
type GoalAddPrerequisiteInput struct {
	GoalID         string `json:"goal_id" jsonschema:"ID of the existing goal to add a prerequisite to (e.g. goal-005)"`
	PrerequisiteID string `json:"prerequisite_id" jsonschema:"ID of the existing goal that must complete first; appended to the target goal's depends_on"`
}

// GoalAddPrerequisiteOutput defines the output schema for goal-add-prerequisite tool.
type GoalAddPrerequisiteOutput struct {
	DependsOn       []string `json:"depends_on" jsonschema:"The target goal's depends_on after the wire"`
	EscalationCount int      `json:"escalation_count" jsonschema:"The target goal's escalation count after the wire (bounded by the escalation cap)"`
}

// GoalAddPrerequisiteHandler is the MCP tool handler for goal-add-prerequisite operation.
func (s *Server) GoalAddPrerequisiteHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input GoalAddPrerequisiteInput) (
	*sdkmcp.CallToolResult,
	GoalAddPrerequisiteOutput,
	error,
) {
	output, err := s.GoalAddPrerequisite(input.GoalID, input.PrerequisiteID)
	if err != nil {
		return nil, GoalAddPrerequisiteOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// GoalPruneInput defines the input schema for goal-prune tool (no parameters needed)
type GoalPruneInput struct{}

// GoalPruneOutput defines the output schema for goal-prune tool
type GoalPruneOutput struct {
	Pruned       bool `json:"pruned" jsonschema:"True if prune operation completed"`
	GoalsRemoved int  `json:"goals_removed" jsonschema:"Number of goals that were in goals.yaml before deletion"`
}

// GoalPruneHandler is the MCP tool handler for goal-prune operation.
func (s *Server) GoalPruneHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input GoalPruneInput) (
	*sdkmcp.CallToolResult,
	GoalPruneOutput,
	error,
) {
	output, err := s.GoalPrune()
	if err != nil {
		return nil, GoalPruneOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ValidationFinding represents a single validation finding for goal-validation-done.
//
// SYNC: mirrored field-for-field (same json tags, same semantics) by
// taskvisor.ValidationFinding in internal/taskvisor/signal.go. The two are kept
// in lock-step by TestValidationFindingStructsInSync. Never add, rename or
// retag a field here without doing the same there. jsonschema tags use bare
// description text only (no `description=` prefix — the go-sdk panics otherwise).
type ValidationFinding struct {
	Rule           string `json:"rule" jsonschema:"Validation rule that was checked"`
	Status         string `json:"status" jsonschema:"Finding status: pass, fail, blocked, or error"`
	Detail         string `json:"detail" jsonschema:"Detailed description of the finding"`
	Correction     string `json:"correction,omitempty" jsonschema:"Concrete fix instruction; required and non-empty when status is fail; a contentless stub (e.g. fix it, none, n/a) is rejected"`
	FailingCommand string `json:"failing_command,omitempty" jsonschema:"Exact command that failed; required and non-empty when status is fail"`
	OutputExcerpt  string `json:"output_excerpt,omitempty" jsonschema:"Raw output excerpt from the failing command; required and non-empty when status is fail"`
	ExpectedState  string `json:"expected_state,omitempty" jsonschema:"What should have been true; required and non-empty when status is fail"`
	FailureClass   string `json:"failure_class,omitempty" jsonschema:"Failure classification when status is not pass: code-defect, env-config, infra-flake, spec-defect, or validator-error"`
	Owner          string `json:"owner,omitempty" jsonschema:"Who owns the fix: implementer, planner, or ops"`

	// C10 incremental re-validation fields. Mirrored from
	// taskvisor.ValidationFinding to keep TestValidationFindingStructsInSync green
	// (the signal.json wire contract is the json tag set). Validators rarely set
	// these directly; the server derives fingerprints from the separate results
	// input. All omitempty so the tool input shape is unchanged when unused.
	Scope             []string `json:"scope,omitempty" jsonschema:"In-scope file paths for this finding (fingerprint input)"`
	Preconditions     []string `json:"preconditions,omitempty" jsonschema:"Stringified preconditions denormalized onto this finding (fingerprint input)"`
	InputFingerprint  string   `json:"input_fingerprint,omitempty" jsonschema:"Computed input fingerprint (server-derived; reuse decision output)"`
	ReusedFromCycle   int      `json:"reused_from_cycle,omitempty" jsonschema:"Cycle a reused pass came from (reuse decision output)"`
	ReusedFingerprint string   `json:"reused_fingerprint,omitempty" jsonschema:"Unchanged fingerprint echoed on reuse (reuse decision output)"`

	// B7 inline marker mirrored from taskvisor.ValidationFinding.ValidationMode
	// at the same ordinal slot (TestValidationFindingStructsInSync compares
	// positionally). omitempty keeps the tool input shape unchanged when unused.
	ValidationMode string `json:"validation_mode,omitempty" jsonschema:"Set to inline when the finding was produced in-window by the B9b inline route with no investigator spawn; empty otherwise"`

	// B5a structured correction. OPTIONAL machine-applicable remedy mirrored from
	// taskvisor.ValidationFinding.CorrectionEdits; advisory only and NEVER
	// auto-applied. Appended LAST to preserve field-order parity with the taskvisor
	// type; omitempty keeps the tool input shape unchanged when unused.
	CorrectionEdits []CorrectionEdit `json:"correction_edit,omitempty" jsonschema:"Optional machine-applicable edits {file,line,old,new}; advisory only, NOT auto-applied; the free-text correction is still required"`
}

// CorrectionEdit mirrors taskvisor.CorrectionEdit (same json tags). The mcp side
// carries jsonschema descriptions the taskvisor type must not. SYNC: kept in
// lock-step by TestCorrectionEditStructsInSync_TagsMatch. jsonschema tags use
// bare description text only (no `description=` prefix — the go-sdk panics).
type CorrectionEdit struct {
	File string `json:"file" jsonschema:"Repo-relative file path to edit (required, non-empty)"`
	Line int    `json:"line,omitempty" jsonschema:"1-based line anchor hint; 0 means unknown"`
	Old  string `json:"old,omitempty" jsonschema:"Exact text to replace; empty means insert"`
	New  string `json:"new,omitempty" jsonschema:"Replacement text; empty means delete"`
}

// FindingResult is one optional per-finding re-validation input to
// goal-validation-done. The server uses ScopeFiles + ChangedFiles to compute a
// stable input fingerprint and persists the result into the orchestrator-owned
// results.json ledger. Absent results leave results.json untouched.
type FindingResult struct {
	ID           string   `json:"id" jsonschema:"Finding identifier (the validation rule text)"`
	Status       string   `json:"status" jsonschema:"Finding status: pass, fail, blocked, or error"`
	ScopeFiles   []string `json:"scope_files,omitempty" jsonschema:"In-scope file paths for this finding"`
	ChangedFiles []string `json:"changed_files,omitempty" jsonschema:"Files changed this cycle (intersected with scope to detect regressions)"`
}

// GoalValidationDoneInput defines the input schema for goal-validation-done tool
type GoalValidationDoneInput struct {
	GoalID     string              `json:"goal_id" jsonschema:"Goal ID to report validation results for (e.g. goal-001)"`
	Verdict    string              `json:"verdict" jsonschema:"Validation verdict, one of: pass, fail, blocked, error"`
	Findings   []ValidationFinding `json:"findings,omitempty" jsonschema:"Validation findings with rule, status, detail, failure_class and owner; every fail finding additionally requires non-empty failing_command, output_excerpt, expected_state and correction (a contentless correction stub is rejected)"`
	NextAction string              `json:"next_action,omitempty" jsonschema:"Suggested next action when verdict is not pass"`
	Results    []FindingResult     `json:"results,omitempty" jsonschema:"Optional per-finding re-validation inputs (id, status, scope_files, changed_files); when present the server computes input fingerprints and writes the orchestrator-owned results.json ledger"`
}

// GoalValidationDoneOutput defines the output schema for goal-validation-done tool
type GoalValidationDoneOutput struct {
	Written bool `json:"written" jsonschema:"True if signal.json was written successfully"`
}

// GoalValidationDoneHandler is the MCP tool handler for goal-validation-done operation.
func (s *Server) GoalValidationDoneHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input GoalValidationDoneInput) (
	*sdkmcp.CallToolResult,
	GoalValidationDoneOutput,
	error,
) {
	output, err := s.GoalValidationDone(input.GoalID, input.Verdict, input.Findings, input.NextAction, input.Results)
	if err != nil {
		return nil, GoalValidationDoneOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// TaskReportInput defines the input schema for the task-report tool. All six
// human fields are REQUIRED and content-bearing; system_info is deliberately
// ABSENT from the wire (collected server-side so an agent cannot spoof it).
// jsonschema tags use bare description text only (no description= prefix — the
// go-sdk panics on startup otherwise).
type TaskReportInput struct {
	Category           string         `json:"category" jsonschema:"Backend category; required; one of plan, supervisor, validator, execute, general"`
	Severity           string         `json:"severity" jsonschema:"Backend severity; required; one of critical, warning, info"`
	Title              string         `json:"title" jsonschema:"Short one-line summary of the issue; required"`
	Description        string         `json:"description" jsonschema:"Full description of the issue and its context; required"`
	ProposedFix        string         `json:"proposed_fix" jsonschema:"Concrete remediation; required and must be actionable (a contentless stub like TBD, none, n/a is rejected)"`
	ExpectedGreenState string         `json:"expected_green_state" jsonschema:"What passing/fixed looks like; required"`
	Payload            map[string]any `json:"payload,omitempty" jsonschema:"Optional structured payload forwarded verbatim to the backend"`
	Project            string         `json:"project,omitempty" jsonschema:"Optional target project lane (e.g. cli, web). Omit to default to the reporting worker's own project. To report an issue about a DIFFERENT project, call projects-list, pick the project whose checkout the issue lives in, confirm it exists locally, and pass its name here."`
}

// TaskReportOutput defines the output schema for the task-report tool.
type TaskReportOutput struct {
	ID     string `json:"id" jsonschema:"Backend-assigned task ID"`
	Status string `json:"status" jsonschema:"Backend-assigned task status (e.g. queued)"`
}

// HooksConfigHandler is the MCP tool handler for hooks-config operation.
func (s *Server) HooksConfigHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input HooksConfigInput) (
	*sdkmcp.CallToolResult,
	HooksConfigOutput,
	error,
) {
	output, err := s.HooksConfig(input.Action, input.Hook)
	if err != nil {
		return nil, HooksConfigOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// WindowsListHandler is the MCP tool handler for windows-list operation.
func (s *Server) WindowsListHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsListInput) (
	*sdkmcp.CallToolResult,
	WindowsListOutput,
	error,
) {
	windows, err := s.WindowsList()
	if err != nil {
		return nil, WindowsListOutput{}, err
	}
	result, out := prependStaleWarning(WindowsListOutput{Windows: windows})
	return result, out, nil
}

// WindowsSendHandler is the MCP tool handler for windows-send operation.
func (s *Server) WindowsSendHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsSendInput) (
	*sdkmcp.CallToolResult,
	WindowsSendOutput,
	error,
) {
	success, err := s.WindowsSend(input.WindowID, input.Command)
	if err != nil {
		return nil, WindowsSendOutput{}, err
	}
	result, out := prependStaleWarning(WindowsSendOutput{Success: success})
	return result, out, nil
}

// WindowsMessageHandler is the MCP tool handler for windows-message operation.
func (s *Server) WindowsMessageHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsMessageInput) (
	*sdkmcp.CallToolResult,
	WindowsMessageOutput,
	error,
) {
	success, sender, err := s.WindowsMessage(input.Receiver, input.Message)
	if err != nil {
		return nil, WindowsMessageOutput{}, err
	}
	result, out := prependStaleWarning(WindowsMessageOutput{Success: success, Sender: sender})
	return result, out, nil
}

// WindowsCreateHandler is the MCP tool handler for windows-create operation.
func (s *Server) WindowsCreateHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsCreateInput) (
	*sdkmcp.CallToolResult,
	WindowsCreateOutput,
	error,
) {
	window, err := s.WindowsCreate(input.Name, input.Command, "")
	if err != nil {
		return nil, WindowsCreateOutput{}, err
	}
	result, out := prependStaleWarning(WindowsCreateOutput{Window: window})
	return result, out, nil
}

// WindowsKillHandler is the MCP tool handler for windows-kill operation.
func (s *Server) WindowsKillHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsKillInput) (
	*sdkmcp.CallToolResult,
	WindowsKillOutput,
	error,
) {
	success, err := s.WindowsKill(input.WindowID)
	if err != nil {
		return nil, WindowsKillOutput{}, err
	}
	result, out := prependStaleWarning(WindowsKillOutput{Success: success})
	return result, out, nil
}

// WindowsRecoverWorkersHandler is the MCP tool handler for windows-recover-workers.
func (s *Server) WindowsRecoverWorkersHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsRecoverWorkersInput) (
	*sdkmcp.CallToolResult,
	WindowsRecoverWorkersOutput,
	error,
) {
	output, err := s.WindowsRecoverWorkers(input.Message, input.CallerWid)
	if err != nil {
		return nil, WindowsRecoverWorkersOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// WindowsSpawnWorkerHandler is the MCP tool handler for windows-spawn-worker.
func (s *Server) WindowsSpawnWorkerHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsSpawnWorkerInput) (
	*sdkmcp.CallToolResult,
	WindowsSpawnWorkerOutput,
	error,
) {
	window, workerName, taskMessage, err := s.WindowsSpawnWorker(
		input.SupervisorWid, input.Subtask, input.ContextFile, input.Scope, input.Context, input.Deliverable, input.Prefix, input.WorkingDirectory, input.CodeRules,
	)
	if err != nil {
		return nil, WindowsSpawnWorkerOutput{}, err
	}
	result, out := prependStaleWarning(WindowsSpawnWorkerOutput{
		Window:      window,
		WorkerName:  workerName,
		TaskMessage: taskMessage,
	})
	return result, out, nil
}

func prependStaleWarning[Out any](output Out) (*sdkmcp.CallToolResult, Out) {
	stale, detail := setup.BinaryStale()
	if !stale {
		return nil, output
	}
	warning := fmt.Sprintf("[tmux-cli mcp is stale: %s; restart the MCP server]", detail)
	data, err := json.Marshal(output)
	if err != nil {
		return nil, output
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: warning},
			&sdkmcp.TextContent{Text: string(data)},
		},
	}, output
}

// RegisterTools registers all MCP tools with the given SDK server.
func (s *Server) RegisterTools(sdkServer *sdkmcp.Server) error {
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-list",
		Description: "List all windows in the current tmux session with IDs, names, and active status",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.WindowsListHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-send",
		Description: "Send a text command to a specific window for execution without manual switching. Supports multi-window orchestration workflows by sending commands in sequence.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.WindowsSendHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-message",
		Description: "Send formatted message to another window with auto-detected sender and reply instructions. Enables inter-window AI agent communication.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.WindowsMessageHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-create",
		Description: "Create a new window in the current tmux session for expanded workflows. Optionally execute a command in the new window. Enables dynamic window lifecycle management and parallel execution.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.WindowsCreateHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-kill",
		Description: "Terminate a specific window in the current tmux session. Supports workflow cleanup by removing temporary windows. Idempotent - returns success if window already doesn't exist. CRITICAL: Cannot kill the last window in a session (would terminate session).",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.WindowsKillHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "hooks-config",
		Description: "View and toggle hook configuration. Actions: list (show current config), enable/disable (toggle a named hook). Changes take effect on next tmux-cli start.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.HooksConfigHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-recover-workers",
		Description: "Batch-recover stuck execute-N worker windows by dismissing interactive prompts (Enter keystroke) and sending a continue message. Idempotent — safe to call multiple times. Use when workers hit rate limits or other interactive prompts.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.WindowsRecoverWorkersHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-spawn-worker",
		Description: "Atomic worker spawn: creates execute-N window, sends /tmux:execute skill, then sends structured task message with DELIVERABLE and RESPONSE PROTOCOL. Consolidates the supervisor spawn protocol into one call.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.WindowsSpawnWorkerHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "spec-validate",
		Description: "Validate a spec .md file against the S0-S8 quality catalogue. Checks: Intent (S0), Code Map references (S1), Implementation Plan structure (S2), Test Plan specificity (S3), Acceptance Criteria Given/When/Then format (S4), Boundaries & Never entries (S7), no TBD/placeholders (S8). Returns gaps with fix instructions and stats (test cases, ACs, code map entries). Use in /tmux:plan step 6 to verify worker-produced specs.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.SpecValidateHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "tasks-validate",
		Description: "Validate .tmux-cli/tasks.yaml lean format. Returns errors if tasks contain extra fields (scope, supporting_context, etc.) that belong in the context .md file. MUST be called after writing tasks.yaml and before spawning workers.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.TasksValidateHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "sudo-execute",
		Description: "DISABLED — use the CLI command instead: tmux-cli sudo \"<command>\". The CLI streams output in real-time and supports long-running operations. Example: tmux-cli sudo \"apt upgrade -y\"",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.SudoExecuteHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "taskvisor-start",
		Description: "Signal the taskvisor daemon to start processing goals. Checks goals.yaml for pending goals and writes the .tmux-cli/taskvisor-start signal file. Fails if no pending goals exist.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.TaskvisorStartHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "goal-create",
		Description: "Create a new goal in goals.yaml with a sequential ID (goal-001, goal-002, ...). Creates the goal directory under .tmux-cli/goals/<id>/. Defaults max_retries to 5 if omitted.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.GoalCreateHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "goal-add-prerequisite",
		Description: "Wire an existing goal's depends_on to include an existing prerequisite goal — the generation-side escalation backstop. Validates both IDs exist, rejects self-dependency and dependency cycles, is idempotent when the edge already exists, and enforces the escalation cap. Increments the goal's escalation_count. Generation-only: a worker must never call it (it races the daemon).",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.GoalAddPrerequisiteHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "goal-prune",
		Description: "Remove all taskvisor goal state (goals.yaml, goals/ directory, signal files) for a clean restart. Idempotent — safe to call when no goals exist. Rejects if the taskvisor daemon is active.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.GoalPruneHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "goal-validation-done",
		Description: "Report validation results for a goal. Writes signal.json atomically to the goal directory. Requires caller to be the validator window (UUID authorization). Strict reject if caller UUID does not match.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}, s.GoalValidationDoneHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-report",
		Description: "File a structured task report to the tmux-cli backend synchronously and return its {id,status}. ALL SIX fields are required: category (one of plan|supervisor|validator|execute|general), severity (one of critical|warning|info), title, description, proposed_fix (must be actionable — a contentless stub is rejected), and expected_green_state. Invalid category/severity are rejected (no coercion). system_info is collected server-side. Returns an error when reporting is disabled in .tmux-cli/setting.yaml.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskReportHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-list",
		Description: "List/search backend tasks with optional AND-combined filters: fingerprint (tasks I reported), claimed_by (tasks assigned to me), status, category, severity, since (ISO-8601), limit (1..200, default 50), offset. Returns a page of tasks plus the full filtered total. Read-only.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.TaskListHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-get",
		Description: "Fetch one backend task by id, including its full event history (created, claimed, status_changed). Errors if the id does not exist. Read-only.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.TaskGetHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-claim",
		Description: "Atomically claim the next highest-priority unclaimed task (critical > warning > info, then oldest first), optionally narrowed by category and/or severity. Stamps the task with this machine's fingerprint. Returns {claimed:true, task} or {claimed:false} when nothing matched. Not idempotent — each call may claim a different task.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskClaimHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-update-status",
		Description: "Advance a claimed task's status. status must be one of in_progress, resolved, failed (claimed is reached only via task-claim). Optional resolution object is recorded when transitioning to resolved. Only the machine that claimed the task may advance it. Allowed transitions: claimed->in_progress|resolved|failed, in_progress->resolved|failed.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskUpdateStatusHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-edit",
		Description: "Edit a filed task's CONTENT in place (PATCHes the task resource) so you can fix or clarify a reported task without deny+re-report — preserving its id and event history. id is required; pass any subset of title, description, proposed_fix, expected_green_state, severity, category, payload and only the provided fields change. severity/category are rejected (never coerced) if outside the valid enum, a contentless proposed_fix stub is rejected, and at least one editable field is required. The backend records an `edited` event and rejects edits to terminal-state tasks (surfaced as an invalid-transition error). Mutating and not idempotent.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskEditHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-deny",
		Description: "Deny a backend task, recording why. Both id and reason are required (a blank reason is rejected). POSTs to the task /deny endpoint and returns the task with status denied. Mutating and not idempotent.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskDenyHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-resolve",
		Description: "FORCE-resolve a backend task via the administrative /resolve endpoint — distinct from task-update-status transitioning to resolved (which is claim-gated and follows from->to rules). Use this to clear a terminal failed task or otherwise resolve a task out-of-band. Both id and reason are required (a blank reason is rejected). Returns the task with status resolved. Mutating and not idempotent.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskResolveHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-set-status",
		Description: "Set a backend task's status by id WITHOUT claiming it — the consolidated admin transition tool. status must be one of denied, resolved, archived (unknown values are rejected, never coerced). Routes to the task /deny, /resolve, or /archive endpoint accordingly. Use this to retire an erroneous/duplicate task (archived), clear a terminal failed task (resolved), or reject one (denied) out-of-band. Both id and reason are required (a blank reason is rejected). Distinct from the claim-gated task-update-status. Mutating and not idempotent.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskSetStatusHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "projects-list",
		Description: "List the available projects from the tmux-cli project-lane registry: each project NAME (e.g. cli, web) and its addresses (machine hostname/fingerprint, absolute install path, git repository). Use this to route a cross-project task-report: pick the project whose checkout the issue lives in, confirm its path exists locally, then pass its name as task-report's project. Optional filters: hostname, fingerprint. Read-only.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.ProjectsListHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-artifact-upload",
		Description: "Attach a local file as an artifact to a backend task: uploads the file's bytes (multipart) to the task and returns {artifact_id, sha256, size, filename, role}. id and path are required (path must be a readable file); role is an optional free-form label (e.g. log, screenshot, diff, spec). Use this instead of cramming support material into a task's bounded text payload or referencing local paths the consumer cannot reach. Mutating and not idempotent.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.TaskArtifactUploadHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-artifact-list",
		Description: "List the artifacts attached to a backend task: returns each artifact's metadata (id, filename, sha256, size, role, mime_type, created_at) plus the total. id is required. Read-only.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.TaskArtifactListHandler)

	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "task-artifact-get",
		Description: "Download a task artifact to a local file and verify its integrity: streams the bytes to dest, computes their sha256, and verifies it against the backend-advertised checksum (and an optional caller-supplied sha256), erroring on mismatch without leaving a truncated file. id, artifact_id and dest are required (dest's parent directory must exist); sha256 is optional. Returns {path, sha256, size, verified}. Read-only on the backend.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.TaskArtifactGetHandler)

	return nil
}

// Package mcp implements the Model Context Protocol server for tmux-cli.
//
// Session Detection:
// The server discovers sessions by matching project path stored in tmux session
// environment variables. No session file is needed.
//
// The server provides five window management operations: windows-list, windows-create,
// windows-kill, windows-send, and windows-message.
package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tmux"
)

// Server represents an MCP server instance with session management capabilities.
//
// The server discovers sessions by matching project path stored in tmux session
// environment variables. No session file is required.
type Server struct {
	executor   tmux.TmuxExecutor
	workingDir string // Absolute path where MCP server started
}

// NewServer creates a new MCP server. Never fails — graceful degradation.
// If no matching session is found, tools return ErrSessionNotFound with helpful message.
func NewServer(workingDir string) *Server {
	return &Server{
		executor:   tmux.NewTmuxExecutor(),
		workingDir: workingDir,
	}
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
	Sudo     bool   `json:"sudo,omitempty" jsonschema:"When true, automatically creates a temp window, runs the command with sudo using cached password, captures output, and destroys the temp window. Do NOT manually create a window for sudo."`
}

// WindowsSendOutput defines the output schema for windows-send tool
type WindowsSendOutput struct {
	Success bool   `json:"success" jsonschema:"True if command was sent successfully"`
	Output  string `json:"output,omitempty" jsonschema:"Captured output from sudo command execution (only present when sudo=true)"`
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
	SupervisorWid string `json:"supervisorWid" jsonschema:"Supervisor's tmux window name (e.g. 'supervisor'). Used in the task message and RESPONSE PROTOCOL."`
	Subtask       string `json:"subtask" jsonschema:"One-line label for the worker's task (e.g. 'audit auth module'). Appears as SUBTASK in the task message."`
	ContextFile   string `json:"contextFile" jsonschema:"Path to the context .md file the worker should read for full spec."`
	Scope         string `json:"scope" jsonschema:"Multi-line scope summary — files, directories, what to investigate or implement."`
	Context       string `json:"context,omitempty" jsonschema:"Multi-line supporting context — prior findings, constraints, non-goals. Optional."`
}

// WindowsSpawnWorkerOutput defines the output schema for windows-spawn-worker tool
type WindowsSpawnWorkerOutput struct {
	Window      *WindowInfo `json:"window" jsonschema:"Details of the created worker window"`
	WorkerName  string      `json:"workerName" jsonschema:"The execute-N name assigned to this worker"`
	TaskMessage string      `json:"taskMessage" jsonschema:"The exact task message sent to the worker"`
}

// WindowsKillInput defines the input schema for windows-kill tool
type WindowsKillInput struct {
	WindowID string `json:"windowId" jsonschema:"The tmux window ID to terminate (e.g. '@0' or '@1')"`
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

	return nil, *output, nil
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

	return nil, WindowsListOutput{Windows: windows}, nil
}

// WindowsSendHandler is the MCP tool handler for windows-send operation.
func (s *Server) WindowsSendHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsSendInput) (
	*sdkmcp.CallToolResult,
	WindowsSendOutput,
	error,
) {
	success, output, err := s.WindowsSend(input.WindowID, input.Command, input.Sudo)
	if err != nil {
		return nil, WindowsSendOutput{}, err
	}

	return nil, WindowsSendOutput{Success: success, Output: output}, nil
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

	return nil, WindowsMessageOutput{Success: success, Sender: sender}, nil
}

// WindowsCreateHandler is the MCP tool handler for windows-create operation.
func (s *Server) WindowsCreateHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsCreateInput) (
	*sdkmcp.CallToolResult,
	WindowsCreateOutput,
	error,
) {
	window, err := s.WindowsCreate(input.Name, input.Command)
	if err != nil {
		return nil, WindowsCreateOutput{}, err
	}

	return nil, WindowsCreateOutput{Window: window}, nil
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

	return nil, WindowsKillOutput{Success: success}, nil
}

// WindowsSpawnWorkerHandler is the MCP tool handler for windows-spawn-worker.
func (s *Server) WindowsSpawnWorkerHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input WindowsSpawnWorkerInput) (
	*sdkmcp.CallToolResult,
	WindowsSpawnWorkerOutput,
	error,
) {
	window, workerName, taskMessage, err := s.WindowsSpawnWorker(
		input.SupervisorWid, input.Subtask, input.ContextFile, input.Scope, input.Context,
	)
	if err != nil {
		return nil, WindowsSpawnWorkerOutput{}, err
	}

	return nil, WindowsSpawnWorkerOutput{
		Window:      window,
		WorkerName:  workerName,
		TaskMessage: taskMessage,
	}, nil
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
		Description: "Send a text command to a specific window for execution without manual switching. Supports multi-window orchestration workflows by sending commands in sequence. When sudo=true, a persistent 'sudo' shell window is automatically created (or reused if it exists), the command runs with elevated privileges using the cached password (from --sudo session flag), and output is captured and returned. Do NOT create a separate window for sudo commands — it is handled automatically.",
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
		Name:        "windows-spawn-worker",
		Description: "Atomic worker spawn: creates execute-N window, sends /tmux:execute skill, then sends structured task message with DELIVERABLE and RESPONSE PROTOCOL. Consolidates the supervisor spawn protocol into one call.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}, s.WindowsSpawnWorkerHandler)

	return nil
}

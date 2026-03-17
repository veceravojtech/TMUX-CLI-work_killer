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

// WindowsKillInput defines the input schema for windows-kill tool
type WindowsKillInput struct {
	WindowID string `json:"windowId" jsonschema:"The tmux window ID to terminate (e.g. '@0' or '@1')"`
}

// WindowsKillOutput defines the output schema for windows-kill tool
type WindowsKillOutput struct {
	Success bool `json:"success" jsonschema:"True if window was killed or already didn't exist (idempotent)"`
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
	success, err := s.WindowsSend(input.WindowID, input.Command)
	if err != nil {
		return nil, WindowsSendOutput{}, err
	}

	return nil, WindowsSendOutput{Success: success}, nil
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

	return nil
}

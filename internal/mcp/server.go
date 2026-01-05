// Package mcp implements the Model Context Protocol server for tmux-cli.
//
// Session Detection:
// The server auto-detects session files from the current working directory.
// It looks for .tmux-cli-session.json in the directory where the MCP server starts.
// No configuration files, environment variables, or CLI flags are used (zero-config).
//
// The server provides five window management operations: windows-list, windows-create,
// windows-kill, windows-send, and windows-message.
package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// Server represents an MCP server instance with session management capabilities.
//
// The server auto-detects session files from the current working directory
// and provides direct access to internal tmux-cli packages (session.Manager,
// store.SessionStore, tmux.Executor) without subprocess execution.
type Server struct {
	// Session management
	store    store.SessionStore
	executor tmux.TmuxExecutor

	// Configuration
	workingDir  string // Absolute path where MCP server started (from os.Getwd())
	sessionFile string // Full path to .tmux-cli-session.json file
}

// NewServer creates a new MCP server with automatic session file detection.
//
// Detection Algorithm:
// 1. Get current working directory using os.Getwd()
// 2. Look for .tmux-cli-session.json in that directory
// 3. Return error if file doesn't exist (no fallback logic)
//
// This implements zero-configuration session detection (FR19):
// - No CLI flags required
// - No environment variables
// - No configuration files
// - No directory traversal
//
// Performance: Session detection completes in <50ms, exceeding NFR-P2 (<500ms).
//
// Error Handling:
// Returns ErrSessionNotFound if session file doesn't exist in working directory.
// Returns ErrWorkingDirNotFound if os.Getwd() fails (rare edge case).
func NewServer() (*Server, error) {
	// Get current working directory where tmux-cli mcp was executed
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWorkingDirNotFound, err)
	}

	// Session file expected at: {workingDir}/.tmux-session
	sessionFile := filepath.Join(workingDir, store.SessionFileName)

	// Verify file exists
	if _, err := os.Stat(sessionFile); err != nil {
		return nil, fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, workingDir, sessionFile)
	}

	// Create store pointing to detected session file directory
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create session store: %w", err)
	}

	return &Server{
		workingDir:  workingDir,
		sessionFile: sessionFile,
		store:       fileStore,
		executor:    tmux.NewTmuxExecutor(),
	}, nil
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

// WindowsCreateInput defines the input schema for windows-create tool
type WindowsCreateInput struct {
	Name    string `json:"name" jsonschema:"Name for the new window"`
	Command string `json:"command,omitempty" jsonschema:"Optional command to execute in the new window (empty = no command)"`
}

// WindowsCreateOutput defines the output schema for windows-create tool
type WindowsCreateOutput struct {
	Window *store.Window `json:"window" jsonschema:"Details of the created window including ID, name, and metadata"`
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
// It wraps the WindowsList() method to match the MCP SDK handler signature.
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
// It wraps the WindowsSend() method to match the MCP SDK handler signature.
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
// It wraps the WindowsMessage() method to match the MCP SDK handler signature.
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
// It wraps the WindowsCreate() method to match the MCP SDK handler signature.
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
// It wraps the WindowsKill() method to match the MCP SDK handler signature.
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
// This includes windows-list, windows-create, windows-kill,
// windows-send, and windows-message tools.
//
// Tool registration uses type-safe handlers with automatic JSON schema
// generation from Go struct types (MCP SDK v1.2.0+ pattern).
func (s *Server) RegisterTools(sdkServer *sdkmcp.Server) error {
	// Register windows-list tool (FR1: List all windows in current session)
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-list",
		Description: "List all windows in the current tmux session with IDs, names, and active status",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.WindowsListHandler)

	// Register windows-send tool (FR8, FR9, FR10: Send commands to windows for remote execution and orchestration)
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-send",
		Description: "Send a text command to a specific window for execution without manual switching. Supports multi-window orchestration workflows by sending commands in sequence.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false, // Modifies window state
			IdempotentHint: false, // Sending same command twice executes it twice
		},
	}, s.WindowsSendHandler)

	// Register windows-message tool (Inter-window AI agent communication)
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-message",
		Description: "Send formatted message to another window with auto-detected sender and reply instructions. Enables inter-window AI agent communication.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false, // Modifies window state by sending message
			IdempotentHint: false, // Sending same message twice delivers it twice
		},
	}, s.WindowsMessageHandler)

	// Register windows-create tool (FR11, FR13: Create new windows for dynamic workflow expansion)
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-create",
		Description: "Create a new window in the current tmux session for expanded workflows. Optionally execute a command in the new window. Enables dynamic window lifecycle management and parallel execution.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false, // Creates new window (modifies state)
			IdempotentHint: false, // Creating same window twice creates duplicates
		},
	}, s.WindowsCreateHandler)

	// Register windows-kill tool (FR12, FR13: Terminate specific windows for cleanup and lifecycle management)
	sdkmcp.AddTool(sdkServer, &sdkmcp.Tool{
		Name:        "windows-kill",
		Description: "Terminate a specific window in the current tmux session. Supports workflow cleanup by removing temporary windows. Idempotent - returns success if window already doesn't exist. CRITICAL: Cannot kill the last window in a session (would terminate session).",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   false, // Kills window (modifies state)
			IdempotentHint: true,  // Killing same window twice succeeds (idempotent)
		},
	}, s.WindowsKillHandler)

	return nil
}

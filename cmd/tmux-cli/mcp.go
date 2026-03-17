package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/console/tmux-cli/internal/mcp"
)

// mcpCmd represents the mcp command that starts the MCP protocol server
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP (Model Context Protocol) server for AI assistant integration",
	Long: `Start the MCP protocol server to enable AI assistants like Claude
to manage tmux windows through the Model Context Protocol.

The server auto-discovers the tmux session for the current working directory
and listens for MCP requests via stdin/stdout.

Examples:
  # Start MCP server (blocks until Ctrl+C)
  tmux-cli mcp

The server will shut down gracefully on SIGINT (Ctrl+C) or SIGTERM.`,
	RunE: runMCPServer,
}

// runMCPServer initializes and starts the MCP server with session discovery
// and graceful shutdown handling.
func runMCPServer(cmd *cobra.Command, args []string) error {
	// Get working directory for session discovery
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Initialize MCP server (never fails — graceful degradation)
	mcpServer := mcp.NewServer(workingDir)

	// Configure MCP SDK server
	impl := &sdkmcp.Implementation{
		Name:    "tmux-cli-mcp",
		Version: "1.0.0",
	}

	sdkServer := sdkmcp.NewServer(impl, nil)

	// Register MCP tools (windows-list, windows-create, etc.)
	if err := mcpServer.RegisterTools(sdkServer); err != nil {
		return fmt.Errorf("failed to register MCP tools: %w", err)
	}

	// Start server with stdio transport and signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals in a separate goroutine
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutdown signal received, stopping MCP server...")
		cancel()
	}()

	// Run server (blocks until context is cancelled or error occurs)
	if err := sdkServer.Run(ctx, &sdkmcp.StdioTransport{}); err != nil && err != context.Canceled {
		return fmt.Errorf("MCP server error: %w", err)
	}

	return nil
}

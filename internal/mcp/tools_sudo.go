package mcp

import "fmt"

// SudoExecute is deprecated — directs callers to use the CLI command instead.
func (s *Server) SudoExecute(command string) (*SudoExecuteOutput, error) {
	return nil, fmt.Errorf("%w: sudo-execute MCP tool is disabled — use the CLI command instead: tmux-cli sudo %q (streams output in real-time)", ErrInvalidInput, command)
}

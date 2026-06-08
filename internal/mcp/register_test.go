package mcp

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestRegisterTools_NoSchemaPanic registers every tool against a real SDK server.
// The go-sdk reflects each tool's input/output struct into a JSON schema at
// AddTool time and panics on a malformed jsonschema tag, so a clean
// RegisterTools is the guard that the task-* (and all other) tool schemas are
// well-formed. Recover any panic and fail loudly with its message.
func TestRegisterTools_NoSchemaPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterTools panicked (malformed tool schema?): %v", r)
		}
	}()

	srv := NewServer(t.TempDir())
	sdkServer := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "tmux-cli-mcp", Version: "test"}, nil)
	require.NoError(t, srv.RegisterTools(sdkServer))
}

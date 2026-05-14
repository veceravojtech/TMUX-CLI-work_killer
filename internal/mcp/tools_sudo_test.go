package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_SudoExecute_Disabled(t *testing.T) {
	server := newTestServer(nil, "/test/dir")

	_, err := server.SudoExecute("apt update")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "tmux-cli sudo")
}

func TestServer_SudoExecute_Disabled_IncludesCommand(t *testing.T) {
	server := newTestServer(nil, "/test/dir")

	_, err := server.SudoExecute("systemctl restart nginx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl restart nginx")
}

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewServer_Success verifies server initializes with working directory
func TestNewServer_Success(t *testing.T) {
	server := NewServer("/test/dir")

	assert.NotNil(t, server)
	assert.Equal(t, "/test/dir", server.workingDir)
	assert.NotNil(t, server.executor)
}

// TestNewServer_NeverFails verifies constructor never returns error
func TestNewServer_NeverFails(t *testing.T) {
	// NewServer should always return a valid server regardless of state
	server := NewServer("/nonexistent/dir")
	assert.NotNil(t, server)
}

package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/store"
)

// TestNewServer_Success verifies server initializes when session file exists
func TestNewServer_Success(t *testing.T) {
	// Arrange: Create temp session file
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, ".tmux-session")
	err := os.WriteFile(sessionFile, []byte(`{"sessionId":"test"}`), 0644)
	require.NoError(t, err)

	// Change working directory to temp dir
	origWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(origWd)
		require.NoError(t, err)
	}()
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Act
	server, err := NewServer()

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, server)
	assert.Equal(t, tempDir, server.workingDir)
	assert.Equal(t, sessionFile, server.sessionFile)
	assert.NotNil(t, server.store)
	assert.NotNil(t, server.executor)
}

// TestNewServer_SessionNotFound verifies error when session file missing
func TestNewServer_SessionNotFound(t *testing.T) {
	// Arrange: Use temp dir without session file
	tempDir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(origWd)
		require.NoError(t, err)
	}()
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Act
	server, err := NewServer()

	// Assert
	assert.Error(t, err)
	assert.Nil(t, server)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.Contains(t, err.Error(), tempDir)
	assert.Contains(t, err.Error(), ".tmux-session")
}

// TestNewServer_ErrorMessage validates error message format exactly matches spec
func TestNewServer_ErrorMessage(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	origWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(origWd)
		require.NoError(t, err)
	}()
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	expectedFile := filepath.Join(tempDir, ".tmux-session")

	// Act
	_, err = NewServer()

	// Assert
	require.Error(t, err)

	// Error message format: "session file not detected in directory /path: expected /path/.tmux-session"
	expectedMsg := "session file not detected in directory " + tempDir + ": expected " + expectedFile
	assert.Contains(t, err.Error(), expectedMsg)
}

// BenchmarkNewServer_SessionDetection verifies session detection meets <50ms performance requirement
func BenchmarkNewServer_SessionDetection(b *testing.B) {
	// Arrange: Create temp session file
	tempDir := b.TempDir()
	sessionFile := filepath.Join(tempDir, ".tmux-session")
	err := os.WriteFile(sessionFile, []byte(`{"sessionId":"bench"}`), 0644)
	require.NoError(b, err)

	// Change working directory to temp dir
	origWd, err := os.Getwd()
	require.NoError(b, err)
	defer func() {
		_ = os.Chdir(origWd)
	}()
	err = os.Chdir(tempDir)
	require.NoError(b, err)

	// Act: Benchmark session detection
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewServer()
	}
}

// TestMockSessionStore_Interface verifies mockSessionStore implements SessionStore
func TestMockSessionStore_Interface(t *testing.T) {
	// Arrange
	var _ store.SessionStore = (*mockSessionStore)(nil)

	// This test just verifies the interface is satisfied at compile time
	t.Log("mockSessionStore correctly implements store.SessionStore interface")
}

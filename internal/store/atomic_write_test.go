package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicWrite_Success_CreatesFileWithCorrectContent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := atomicWrite(path, session)
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Verify file content
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, session.SessionID, restored.SessionID)
	assert.Equal(t, session.ProjectPath, restored.ProjectPath)
	assert.Equal(t, session.Windows, restored.Windows)
}

func TestAtomicWrite_Success_FileHasCorrectPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := atomicWrite(path, session)
	require.NoError(t, err)

	// Verify file permissions
	info, err := os.Stat(path)
	require.NoError(t, err)

	// Verify file has 0644 permissions (rw-r--r--)
	assert.Equal(t, os.FileMode(FilePerms), info.Mode().Perm())
}

func TestAtomicWrite_Success_TempFileRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := atomicWrite(path, session)
	require.NoError(t, err)

	// List files in directory
	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	// Verify only the final file exists, no temp files
	assert.Len(t, files, 1)
	assert.Equal(t, "test-session.json", files[0].Name())
}

func TestAtomicWrite_Success_JSONIndented(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "editor",
			},
		},
	}

	err := atomicWrite(path, session)
	require.NoError(t, err)

	// Read raw file content
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Verify JSON is indented (contains newlines and spaces)
	content := string(data)
	assert.Contains(t, content, "\n")
	assert.Contains(t, content, "  ") // 2-space indentation
}

func TestAtomicWrite_ExistingFile_Overwrites(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	// Write initial session
	initial := &Session{
		SessionID:   "initial-uuid",
		ProjectPath: "/tmp/initial",
		Windows:     []Window{},
	}
	err := atomicWrite(path, initial)
	require.NoError(t, err)

	// Overwrite with new session
	updated := &Session{
		SessionID:   "updated-uuid",
		ProjectPath: "/tmp/updated",
		Windows:     []Window{},
	}
	err = atomicWrite(path, updated)
	require.NoError(t, err)

	// Verify file contains updated content
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "updated-uuid", restored.SessionID)
	assert.Equal(t, "/tmp/updated", restored.ProjectPath)
}

func TestAtomicWrite_InvalidDirectory_ReturnsError(t *testing.T) {
	// Use non-existent directory
	path := "/nonexistent/directory/test-session.json"

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := atomicWrite(path, session)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

func TestAtomicWrite_WithWindows_PreservesAllData(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-session.json")

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "editor",
			},
			{
				TmuxWindowID: "@1",
				Name:         "tests",
			},
		},
	}

	err := atomicWrite(path, session)
	require.NoError(t, err)

	// Read and verify all window data preserved
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, session.SessionID, restored.SessionID)
	assert.Equal(t, session.ProjectPath, restored.ProjectPath)
	require.Len(t, restored.Windows, 2)
	assert.Equal(t, session.Windows[0], restored.Windows[0])
	assert.Equal(t, session.Windows[1], restored.Windows[1])
}

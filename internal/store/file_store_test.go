package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileSessionStore_Success(t *testing.T) {
	store, err := NewFileSessionStore()
	require.NoError(t, err)
	assert.NotNil(t, store)
	assert.NotEmpty(t, store.homeDir)
}

func TestFileSessionStore_Save_ValidSession_CreatesFile(t *testing.T) {
	// Create store with temp directory
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Verify file exists
	filePath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	_, err = os.Stat(filePath)
	require.NoError(t, err)
}

func TestFileSessionStore_Save_WithWindows_SavesAllData(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []Window{
			{
				TmuxWindowID:    "@0",
				Name:            "editor",
				RecoveryCommand: "vim main.go",
			},
		},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Verify file content
	filePath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, session.SessionID, restored.SessionID)
	assert.Equal(t, session.ProjectPath, restored.ProjectPath)
	assert.Equal(t, session.Windows, restored.Windows)
}

func TestFileSessionStore_Save_NilSession_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Save(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Save_EmptySessionID_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	session := &Session{
		SessionID:   "",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := store.Save(session)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Save_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Verify directories were created
	sessionsPath := filepath.Join(tmpDir, SessionsDir)
	_, err = os.Stat(sessionsPath)
	require.NoError(t, err)

	endedPath := filepath.Join(tmpDir, EndedDir)
	_, err = os.Stat(endedPath)
	require.NoError(t, err)
}

func TestFileSessionStore_Load_ExistingSession_ReturnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Save session first
	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}
	err := store.Save(original)
	require.NoError(t, err)

	// Load session
	loaded, err := store.Load("test-uuid")
	require.NoError(t, err)
	assert.Equal(t, original.SessionID, loaded.SessionID)
	assert.Equal(t, original.ProjectPath, loaded.ProjectPath)
}

func TestFileSessionStore_Load_NonExistentSession_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	_, err := store.Load("nonexistent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestFileSessionStore_Load_EmptyID_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	_, err := store.Load("")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Load_CorruptedJSON_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Create directory
	sessionsPath := filepath.Join(tmpDir, SessionsDir)
	err := os.MkdirAll(sessionsPath, DirPerms)
	require.NoError(t, err)

	// Write corrupted JSON
	filePath := filepath.Join(sessionsPath, "corrupted.json")
	err = os.WriteFile(filePath, []byte("not valid json"), FilePerms)
	require.NoError(t, err)

	// Attempt to load
	_, err = store.Load("corrupted")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Delete_ExistingSession_RemovesFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Save session first
	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}
	err := store.Save(session)
	require.NoError(t, err)

	// Delete session
	err = store.Delete("test-uuid")
	require.NoError(t, err)

	// Verify file is gone
	filePath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	_, err = os.Stat(filePath)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestFileSessionStore_Delete_NonExistentSession_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Delete("nonexistent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestFileSessionStore_Delete_EmptyID_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Delete("")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_List_EmptyDirectory_ReturnsEmptySlice(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	sessions, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestFileSessionStore_List_MultipleSessions_ReturnsAll(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Save multiple sessions
	session1 := &Session{
		SessionID:   "session-1",
		ProjectPath: "/tmp/test1",
		Windows:     []Window{},
	}
	session2 := &Session{
		SessionID:   "session-2",
		ProjectPath: "/tmp/test2",
		Windows:     []Window{},
	}

	err := store.Save(session1)
	require.NoError(t, err)
	err = store.Save(session2)
	require.NoError(t, err)

	// List sessions
	sessions, err := store.List()
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// Verify both sessions are present
	ids := make(map[string]bool)
	for _, s := range sessions {
		ids[s.SessionID] = true
	}
	assert.True(t, ids["session-1"])
	assert.True(t, ids["session-2"])
}

func TestFileSessionStore_List_SkipsNonJSONFiles(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Create sessions directory
	sessionsPath := filepath.Join(tmpDir, SessionsDir)
	err := os.MkdirAll(sessionsPath, DirPerms)
	require.NoError(t, err)

	// Save one valid session
	session := &Session{
		SessionID:   "valid-session",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}
	err = store.Save(session)
	require.NoError(t, err)

	// Create non-JSON file
	txtPath := filepath.Join(sessionsPath, "readme.txt")
	err = os.WriteFile(txtPath, []byte("test"), FilePerms)
	require.NoError(t, err)

	// Create temp file
	tmpPath := filepath.Join(sessionsPath, "session-123.tmp")
	err = os.WriteFile(tmpPath, []byte("{}"), FilePerms)
	require.NoError(t, err)

	// List should only return valid session
	sessions, err := store.List()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "valid-session", sessions[0].SessionID)
}

func TestFileSessionStore_Move_ExistingSession_MovesFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Save session
	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}
	err := store.Save(session)
	require.NoError(t, err)

	// Move to ended directory
	err = store.Move("test-uuid", "ended")
	require.NoError(t, err)

	// Verify source file is gone
	srcPath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	_, err = os.Stat(srcPath)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	// Verify destination file exists
	dstPath := filepath.Join(tmpDir, EndedDir, "test-uuid.json")
	_, err = os.Stat(dstPath)
	require.NoError(t, err)

	// Verify content preserved
	data, err := os.ReadFile(dstPath)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	assert.Equal(t, session.SessionID, restored.SessionID)
}

func TestFileSessionStore_Move_NonExistentSession_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Move("nonexistent", "ended")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestFileSessionStore_Move_EmptyID_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Move("", "ended")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Move_EmptyDestination_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	err := store.Move("test-uuid", "")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

// TestFileSessionStore_Move_PreservesData verifies file move preserves ALL JSON data
// This is CRITICAL for data integrity (NFR20)
func TestFileSessionStore_Move_PreservesData(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Create a session with complex data
	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/project",
		Windows: []Window{
			{TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim main.go"},
			{TmuxWindowID: "@1", Name: "tests", RecoveryCommand: "go test -watch"},
		},
	}

	// Save session
	err := store.Save(session)
	require.NoError(t, err)

	// Read source file data before move
	sourcePath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	sourceData, err := os.ReadFile(sourcePath)
	require.NoError(t, err)

	// Move to ended/
	err = store.Move("test-uuid", "ended")
	require.NoError(t, err)

	// Verify file moved to destination
	destPath := filepath.Join(tmpDir, SessionsDir, "ended", "test-uuid.json")
	destData, err := os.ReadFile(destPath)
	require.NoError(t, err, "Destination file must exist after move")

	// Verify source file deleted
	_, err = os.ReadFile(sourcePath)
	assert.True(t, os.IsNotExist(err), "Source file must be deleted after move")

	// CRITICAL: Verify data is identical byte-for-byte
	assert.Equal(t, sourceData, destData, "Data must be preserved during move - NO data loss allowed")

	// Verify JSON is still valid and contains all fields
	var movedSession Session
	err = json.Unmarshal(destData, &movedSession)
	require.NoError(t, err, "Moved file must contain valid JSON")

	assert.Equal(t, session.SessionID, movedSession.SessionID)
	assert.Equal(t, session.ProjectPath, movedSession.ProjectPath)
	assert.Equal(t, len(session.Windows), len(movedSession.Windows))
	for i, window := range session.Windows {
		assert.Equal(t, window.TmuxWindowID, movedSession.Windows[i].TmuxWindowID)
		assert.Equal(t, window.Name, movedSession.Windows[i].Name)
		assert.Equal(t, window.RecoveryCommand, movedSession.Windows[i].RecoveryCommand)
	}
}

// TestFileSessionStore_Move_DestinationDirectoryHandling verifies dest dir creation
func TestFileSessionStore_Move_DestinationDirectoryHandling(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{homeDir: tmpDir}

	// Create and save a session
	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/project",
		Windows:     []Window{},
	}
	err := store.Save(session)
	require.NoError(t, err)

	// Move to a custom destination (e.g., "archived")
	// This tests that Move creates the destination directory if needed
	err = store.Move("test-uuid", "archived")
	require.NoError(t, err)

	// Verify archived/ directory was created
	archivedDir := filepath.Join(tmpDir, SessionsDir, "archived")
	stat, err := os.Stat(archivedDir)
	require.NoError(t, err, "archived/ directory must be created by Move()")
	assert.True(t, stat.IsDir(), "archived/ must be a directory")

	// Verify file exists in archived/
	filePath := filepath.Join(archivedDir, "test-uuid.json")
	_, err = os.Stat(filePath)
	require.NoError(t, err, "Session file must exist in archived/ after move")

	// Verify source file was deleted
	sourcePath := filepath.Join(tmpDir, SessionsDir, "test-uuid.json")
	_, err = os.Stat(sourcePath)
	assert.True(t, os.IsNotExist(err), "Source file must be deleted after move")
}

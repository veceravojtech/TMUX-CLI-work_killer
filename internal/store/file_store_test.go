package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileSessionStore_Success(t *testing.T) {
	store, err := NewFileSessionStore()
	require.NoError(t, err)
	assert.NotNil(t, store)
}

func TestFileSessionStore_Save_ValidSession_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows:     []Window{},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Verify file exists in project directory
	filePath := filepath.Join(tmpDir, SessionFileName)
	_, err = os.Stat(filePath)
	require.NoError(t, err)
}

func TestFileSessionStore_Save_WithWindows_SavesAllData(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "editor",
			},
		},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Verify file content
	filePath := filepath.Join(tmpDir, SessionFileName)
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
	store := &FileSessionStore{}

	err := store.Save(nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Save_EmptySessionID_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "",
		ProjectPath: tmpDir,
		Windows:     []Window{},
	}

	err := store.Save(session)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Save_EmptyProjectPath_ReturnsError(t *testing.T) {
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "",
		Windows:     []Window{},
	}

	err := store.Save(session)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Load_ExistingSession_ReturnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	// Save session first
	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows:     []Window{},
	}
	err := store.Save(original)
	require.NoError(t, err)

	// Load session by project path
	loaded, err := store.Load(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, original.SessionID, loaded.SessionID)
	assert.Equal(t, original.ProjectPath, loaded.ProjectPath)
}

func TestFileSessionStore_Load_NonExistentSession_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	_, err := store.Load(tmpDir)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestFileSessionStore_Load_EmptyPath_ReturnsError(t *testing.T) {
	store := &FileSessionStore{}

	_, err := store.Load("")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Load_CorruptedJSON_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	// Write corrupted JSON
	filePath := filepath.Join(tmpDir, SessionFileName)
	err := os.WriteFile(filePath, []byte("not valid json"), FilePerms)
	require.NoError(t, err)

	// Attempt to load
	_, err = store.Load(tmpDir)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestFileSessionStore_Load_WithWindows_LoadsAllData(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	// Create a session with multiple windows
	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows: []Window{
			{TmuxWindowID: "@0", Name: "editor"},
			{TmuxWindowID: "@1", Name: "server"},
		},
	}

	// Save session
	err := store.Save(original)
	require.NoError(t, err)

	// Load session
	loaded, err := store.Load(tmpDir)
	require.NoError(t, err)

	// Verify all data loaded correctly
	assert.Equal(t, original.SessionID, loaded.SessionID)
	assert.Equal(t, original.ProjectPath, loaded.ProjectPath)
	assert.Len(t, loaded.Windows, 2)
	assert.Equal(t, original.Windows[0], loaded.Windows[0])
	assert.Equal(t, original.Windows[1], loaded.Windows[1])
}

func TestFileSessionStore_Load_RelativePath_NormalizesToAbsolute(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	// Create a project directory
	projectDir := filepath.Join(tmpDir, "testproject")
	err := os.MkdirAll(projectDir, DirPerms)
	require.NoError(t, err)

	// Save session with absolute path
	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: projectDir,
		Windows:     []Window{},
	}
	err = store.Save(session)
	require.NoError(t, err)

	// Load using the same absolute path
	loaded, err := store.Load(projectDir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "test-uuid", loaded.SessionID)
}

func TestFileSessionStore_Save_OverwritesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	// Save initial session
	session1 := &Session{
		SessionID:   "uuid-1",
		ProjectPath: tmpDir,
		Windows: []Window{
			{Name: "old", TmuxWindowID: "@0"},
		},
	}
	err := store.Save(session1)
	require.NoError(t, err)

	// Save updated session (new ID, same path)
	session2 := &Session{
		SessionID:   "uuid-2",
		ProjectPath: tmpDir,
		Windows: []Window{
			{Name: "new", TmuxWindowID: "@0"},
		},
	}
	err = store.Save(session2)
	require.NoError(t, err)

	// Load should return the latest session
	loaded, err := store.Load(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "uuid-2", loaded.SessionID)
	assert.Equal(t, "new", loaded.Windows[0].Name)
}

func TestLoad_BackwardCompatibility_MissingTimestamps(t *testing.T) {
	// Setup: Write old-format session file without timestamps
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, SessionFileName)

	oldFormatJSON := `{
		"sessionId": "test-uuid",
		"projectPath": "` + tmpDir + `",
		"windows": []
	}`

	err := os.WriteFile(sessionFile, []byte(oldFormatJSON), FilePerms)
	require.NoError(t, err)

	// Test: Load should succeed and populate createdAt
	store, _ := NewFileSessionStore()
	session, err := store.Load(tmpDir)

	require.NoError(t, err)
	assert.Equal(t, "test-uuid", session.SessionID)
	assert.NotEmpty(t, session.CreatedAt, "CreatedAt should be populated")
	assert.Empty(t, session.LastRecoveryAt, "LastRecoveryAt should be empty (never recovered)")

	// Verify timestamp is valid RFC3339
	_, err = time.Parse(time.RFC3339, session.CreatedAt)
	assert.NoError(t, err, "CreatedAt should be valid RFC3339")
}

func TestLoad_PreservesExistingTimestamps(t *testing.T) {
	// Setup: Write new-format session with timestamps
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, SessionFileName)

	newFormatJSON := `{
		"sessionId": "test-uuid",
		"projectPath": "` + tmpDir + `",
		"createdAt": "2026-01-01T10:00:00Z",
		"lastRecoveryAt": "2026-01-02T11:00:00Z",
		"windows": []
	}`

	err := os.WriteFile(sessionFile, []byte(newFormatJSON), FilePerms)
	require.NoError(t, err)

	// Test: Load should preserve existing timestamps
	store, _ := NewFileSessionStore()
	session, err := store.Load(tmpDir)

	require.NoError(t, err)
	assert.Equal(t, "2026-01-01T10:00:00Z", session.CreatedAt)
	assert.Equal(t, "2026-01-02T11:00:00Z", session.LastRecoveryAt)
}

func TestLoad_MalformedJSON_ReturnsError(t *testing.T) {
	// Setup: Write invalid JSON
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, SessionFileName)

	invalidJSON := `{invalid json}`
	err := os.WriteFile(sessionFile, []byte(invalidJSON), FilePerms)
	require.NoError(t, err)

	// Test: Load should fail with ErrInvalidSession
	store, _ := NewFileSessionStore()
	session, err := store.Load(tmpDir)

	assert.Error(t, err)
	assert.Nil(t, session)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestLoad_MissingRequiredFields_ReturnsError(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "missing sessionId",
			json: `{"projectPath": "/tmp/test", "windows": []}`,
		},
		{
			name: "missing projectPath",
			json: `{"sessionId": "test-id", "windows": []}`,
		},
		{
			name: "missing windows",
			json: `{"sessionId": "test-id", "projectPath": "/tmp/test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			sessionFile := filepath.Join(tmpDir, SessionFileName)

			err := os.WriteFile(sessionFile, []byte(tt.json), FilePerms)
			require.NoError(t, err)

			store, _ := NewFileSessionStore()
			session, err := store.Load(tmpDir)

			assert.Error(t, err)
			assert.Nil(t, session)
			assert.ErrorIs(t, err, ErrInvalidSession)
		})
	}
}

func TestSave_WritesTimestamps(t *testing.T) {
	tmpDir := t.TempDir()

	session := &Session{
		SessionID:      "test-uuid",
		ProjectPath:    tmpDir,
		CreatedAt:      "2026-01-01T10:00:00Z",
		LastRecoveryAt: "2026-01-02T11:00:00Z",
		Windows:        []Window{},
	}

	store, _ := NewFileSessionStore()
	err := store.Save(session)
	require.NoError(t, err)

	// Verify file contains timestamps
	data, err := os.ReadFile(filepath.Join(tmpDir, SessionFileName))
	require.NoError(t, err)

	assert.Contains(t, string(data), `"createdAt": "2026-01-01T10:00:00Z"`)
	assert.Contains(t, string(data), `"lastRecoveryAt": "2026-01-02T11:00:00Z"`)
}

func TestSave_OmitsEmptyLastRecoveryAt(t *testing.T) {
	tmpDir := t.TempDir()

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		CreatedAt:   "2026-01-01T10:00:00Z",
		// LastRecoveryAt omitted (never recovered)
		Windows: []Window{},
	}

	store, _ := NewFileSessionStore()
	err := store.Save(session)
	require.NoError(t, err)

	// Verify file contains createdAt but not lastRecoveryAt
	data, err := os.ReadFile(filepath.Join(tmpDir, SessionFileName))
	require.NoError(t, err)

	assert.Contains(t, string(data), `"createdAt": "2026-01-01T10:00:00Z"`)
	assert.NotContains(t, string(data), `"lastRecoveryAt"`)
}

package store

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_JSONMarshaling_EmptyWindows(t *testing.T) {
	session := &Session{
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		ProjectPath: "/home/user/my-project",
		Windows:     []Window{},
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)

	// Verify JSON format matches PRD specification (FR18)
	expected := `{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/home/user/my-project",
  "windows": []
}`
	assert.JSONEq(t, expected, string(data))
}

func TestSession_JSONMarshaling_WithWindows(t *testing.T) {
	session := &Session{
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		ProjectPath: "/home/user/my-project",
		Windows: []Window{
			{
				TmuxWindowID:    "@0",
				Name:            "editor",
				RecoveryCommand: "vim main.go",
			},
			{
				TmuxWindowID:    "@1",
				Name:            "tests",
				RecoveryCommand: "go test -watch",
			},
		},
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)

	// Verify JSON format matches PRD specification
	expected := `{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/home/user/my-project",
  "windows": [
    {
      "tmuxWindowId": "@0",
      "name": "editor",
      "recoveryCommand": "vim main.go"
    },
    {
      "tmuxWindowId": "@1",
      "name": "tests",
      "recoveryCommand": "go test -watch"
    }
  ]
}`
	assert.JSONEq(t, expected, string(data))
}

func TestSession_JSONUnmarshaling_ValidJSON(t *testing.T) {
	jsonData := `{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/home/user/my-project",
  "windows": [
    {
      "tmuxWindowId": "@0",
      "name": "editor",
      "recoveryCommand": "vim main.go"
    }
  ]
}`

	var session Session
	err := json.Unmarshal([]byte(jsonData), &session)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", session.SessionID)
	assert.Equal(t, "/home/user/my-project", session.ProjectPath)
	assert.Len(t, session.Windows, 1)
	assert.Equal(t, "@0", session.Windows[0].TmuxWindowID)
	assert.Equal(t, "editor", session.Windows[0].Name)
	assert.Equal(t, "vim main.go", session.Windows[0].RecoveryCommand)
}

func TestSession_JSONRoundTrip_PreservesAllData(t *testing.T) {
	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []Window{
			{
				TmuxWindowID:    "@0",
				Name:            "editor",
				RecoveryCommand: "vim",
			},
		},
	}

	// Marshal
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal
	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	// Verify data preserved
	assert.Equal(t, original.SessionID, restored.SessionID)
	assert.Equal(t, original.ProjectPath, restored.ProjectPath)
	assert.Equal(t, original.Windows, restored.Windows)
}

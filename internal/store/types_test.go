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
				TmuxWindowID: "@0",
				Name:         "editor",
			},
			{
				TmuxWindowID: "@1",
				Name:         "tests",
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
      "name": "editor"
    },
    {
      "tmuxWindowId": "@1",
      "name": "tests"
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
      "name": "editor"
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
}

func TestSession_JSONRoundTrip_PreservesAllData(t *testing.T) {
	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "editor",
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

func TestSession_TimestampFields(t *testing.T) {
	session := Session{
		SessionID:      "test-id",
		ProjectPath:    "/tmp/test",
		CreatedAt:      "2026-01-02T10:00:00Z",
		LastRecoveryAt: "2026-01-02T11:00:00Z",
		Windows:        []Window{},
	}

	assert.Equal(t, "2026-01-02T10:00:00Z", session.CreatedAt)
	assert.Equal(t, "2026-01-02T11:00:00Z", session.LastRecoveryAt)
}

func TestSession_TimestampsOptional(t *testing.T) {
	// Verify fields are optional (can be empty)
	session := Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	assert.Empty(t, session.CreatedAt)
	assert.Empty(t, session.LastRecoveryAt)
}

func TestWindow_JSONMarshaling_WithUUID(t *testing.T) {
	window := Window{
		TmuxWindowID: "@0",
		Name:         "editor",
		UUID:         "550e8400-e29b-41d4-a716-446655440000",
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(window, "", "  ")
	require.NoError(t, err)

	// Verify JSON includes UUID field with camelCase
	expected := `{
  "tmuxWindowId": "@0",
  "name": "editor",
  "uuid": "550e8400-e29b-41d4-a716-446655440000"
}`
	assert.JSONEq(t, expected, string(data))
}

func TestWindow_JSONMarshaling_WithoutUUID(t *testing.T) {
	window := Window{
		TmuxWindowID: "@0",
		Name:         "editor",
		UUID:         "",
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(window, "", "  ")
	require.NoError(t, err)

	// Verify JSON omits empty UUID field (omitempty)
	expected := `{
  "tmuxWindowId": "@0",
  "name": "editor"
}`
	assert.JSONEq(t, expected, string(data))
}

func TestWindow_JSONUnmarshaling_WithUUID(t *testing.T) {
	jsonData := `{
  "tmuxWindowId": "@0",
  "name": "editor",
  "uuid": "550e8400-e29b-41d4-a716-446655440000"
}`

	var window Window
	err := json.Unmarshal([]byte(jsonData), &window)
	require.NoError(t, err)

	assert.Equal(t, "@0", window.TmuxWindowID)
	assert.Equal(t, "editor", window.Name)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", window.UUID)
}

// Tests for PostCommandConfig
func TestPostCommandConfig_JSONMarshaling_FullConfig(t *testing.T) {
	config := &PostCommandConfig{
		Enabled: true,
		Commands: []string{
			`claude --dangerously-skip-permissions --session-id="$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions --resume "$TMUX_WINDOW_UUID"`,
			`claude --dangerously-skip-permissions`,
		},
		ErrorPatterns: []string{
			"already in use",
			"No conversation found",
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err)

	expected := `{
  "enabled": true,
  "commands": [
    "claude --dangerously-skip-permissions --session-id=\"$TMUX_WINDOW_UUID\"",
    "claude --dangerously-skip-permissions --resume \"$TMUX_WINDOW_UUID\"",
    "claude --dangerously-skip-permissions"
  ],
  "errorPatterns": [
    "already in use",
    "No conversation found"
  ]
}`
	assert.JSONEq(t, expected, string(data))
}

func TestPostCommandConfig_JSONMarshaling_DisabledConfig(t *testing.T) {
	config := &PostCommandConfig{
		Enabled: false,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err)

	expected := `{
  "enabled": false
}`
	assert.JSONEq(t, expected, string(data))
}

func TestSession_WithPostCommand(t *testing.T) {
	session := &Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		PostCommand: &PostCommandConfig{
			Enabled: true,
			Commands: []string{
				`claude --session-id="$TMUX_WINDOW_UUID"`,
			},
			ErrorPatterns: []string{"error"},
		},
		Windows: []Window{},
	}

	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)

	// Verify PostCommand is included
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	assert.Contains(t, result, "postCommand")
	postCmd := result["postCommand"].(map[string]interface{})
	assert.Equal(t, true, postCmd["enabled"])
}

func TestSession_WithoutPostCommand(t *testing.T) {
	session := &Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		Windows:     []Window{},
	}

	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)

	// Verify PostCommand is omitted when nil (omitempty)
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	assert.NotContains(t, result, "postCommand")
}

func TestWindow_WithPostCommandOverride(t *testing.T) {
	window := Window{
		TmuxWindowID: "@0",
		Name:         "editor",
		UUID:         "test-uuid",
		PostCommand: &PostCommandConfig{
			Enabled: false,
		},
	}

	data, err := json.MarshalIndent(window, "", "  ")
	require.NoError(t, err)

	expected := `{
  "tmuxWindowId": "@0",
  "name": "editor",
  "uuid": "test-uuid",
  "postCommand": {
    "enabled": false
  }
}`
	assert.JSONEq(t, expected, string(data))
}

func TestPostCommandConfig_BackwardCompatibility(t *testing.T) {
	// Old session file without postCommand field
	jsonData := `{
  "sessionId": "test-id",
  "projectPath": "/tmp/test",
  "windows": []
}`

	var session Session
	err := json.Unmarshal([]byte(jsonData), &session)
	require.NoError(t, err)

	assert.Equal(t, "test-id", session.SessionID)
	assert.Equal(t, "/tmp/test", session.ProjectPath)
	assert.Nil(t, session.PostCommand)
}

// Tests for GetEffectivePostCommand
func TestSession_GetEffectivePostCommand_SessionLevel(t *testing.T) {
	session := &Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		PostCommand: &PostCommandConfig{
			Enabled:  true,
			Commands: []string{"session-level-command"},
		},
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "window1",
				// No window-level override
			},
		},
	}

	config := session.GetEffectivePostCommand(&session.Windows[0])
	require.NotNil(t, config)
	assert.True(t, config.Enabled)
	assert.Equal(t, []string{"session-level-command"}, config.Commands)
}

func TestSession_GetEffectivePostCommand_WindowOverride(t *testing.T) {
	session := &Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		PostCommand: &PostCommandConfig{
			Enabled:  true,
			Commands: []string{"session-level-command"},
		},
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "window1",
				PostCommand: &PostCommandConfig{
					Enabled: false, // Window disables post-command
				},
			},
		},
	}

	config := session.GetEffectivePostCommand(&session.Windows[0])
	require.NotNil(t, config)
	assert.False(t, config.Enabled) // Window override takes precedence
}

func TestSession_GetEffectivePostCommand_NoConfig(t *testing.T) {
	session := &Session{
		SessionID:   "test-id",
		ProjectPath: "/tmp/test",
		// No session-level config
		Windows: []Window{
			{
				TmuxWindowID: "@0",
				Name:         "window1",
				// No window-level config
			},
		},
	}

	config := session.GetEffectivePostCommand(&session.Windows[0])
	assert.Nil(t, config) // No config at any level
}

// Tests for DefaultPostCommandConfig
func TestDefaultPostCommandConfig_HasCorrectStructure(t *testing.T) {
	config := DefaultPostCommandConfig()

	assert.True(t, config.Enabled)
	assert.Len(t, config.Commands, 3)
	assert.Len(t, config.ErrorPatterns, 2)

	// Verify command structure
	assert.Contains(t, config.Commands[0], "--session-id")
	assert.Contains(t, config.Commands[1], "--resume")
	assert.NotContains(t, config.Commands[2], "--session-id")
	assert.NotContains(t, config.Commands[2], "--resume")

	// Verify error patterns
	assert.Contains(t, config.ErrorPatterns[0], "already in use")
	assert.Contains(t, config.ErrorPatterns[1], "No conversation found")
}

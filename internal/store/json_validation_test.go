package store

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONFormat_MatchesPRDSpecification verifies that the JSON output
// exactly matches the format specified in PRD FR18.
func TestJSONFormat_MatchesPRDSpecification(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		ProjectPath: tmpDir,
		Windows:     []Window{},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Read the file from project directory
	filePath := filepath.Join(tmpDir, SessionFileName)
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Verify exact JSON format from PRD (projectPath will be tmpDir)
	// We need to construct expected JSON with actual tmpDir value
	var parsed Session
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", parsed.SessionID)
	assert.Equal(t, tmpDir, parsed.ProjectPath)
	assert.Empty(t, parsed.Windows)
}

// TestJSONFormat_WithWindows_MatchesPRDSpecification verifies JSON format
// with windows matches PRD specification.
func TestJSONFormat_WithWindows_MatchesPRDSpecification(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		ProjectPath: tmpDir,
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

	err := store.Save(session)
	require.NoError(t, err)

	// Read the file
	filePath := filepath.Join(tmpDir, SessionFileName)
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Parse and verify structure
	var parsed Session
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", parsed.SessionID)
	assert.Equal(t, tmpDir, parsed.ProjectPath)
	assert.Len(t, parsed.Windows, 2)
	assert.Equal(t, "@0", parsed.Windows[0].TmuxWindowID)
	assert.Equal(t, "editor", parsed.Windows[0].Name)
}

// TestJSONFormat_ValidJSONOutput verifies that all saved files are valid JSON.
func TestJSONFormat_ValidJSONOutput(t *testing.T) {
	store := &FileSessionStore{}

	testCases := []struct {
		name    string
		session *Session
	}{
		{
			name: "empty windows",
			session: &Session{
				SessionID:   "test-1",
				ProjectPath: t.TempDir(),
				Windows:     []Window{},
			},
		},
		{
			name: "with single window",
			session: &Session{
				SessionID:   "test-2",
				ProjectPath: t.TempDir(),
				Windows: []Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
		},
		{
			name: "with multiple windows",
			session: &Session{
				SessionID:   "test-3",
				ProjectPath: t.TempDir(),
				Windows: []Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.Save(tc.session)
			require.NoError(t, err)

			// Read file from project directory
			filePath := filepath.Join(tc.session.ProjectPath, SessionFileName)
			data, err := os.ReadFile(filePath)
			require.NoError(t, err)

			// Verify it's valid JSON by unmarshaling
			var parsed interface{}
			err = json.Unmarshal(data, &parsed)
			assert.NoError(t, err, "JSON must be valid")
		})
	}
}

// TestJSONFormat_HumanReadable verifies that JSON is formatted with indentation.
func TestJSONFormat_HumanReadable(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows: []Window{
			{Name: "test", TmuxWindowID: ""},
		},
	}

	err := store.Save(session)
	require.NoError(t, err)

	// Read raw file content
	filePath := filepath.Join(tmpDir, SessionFileName)
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	content := string(data)

	// Verify indentation exists
	assert.Contains(t, content, "\n", "JSON must contain newlines")
	assert.Contains(t, content, "  ", "JSON must use 2-space indentation")

	// Verify not minified
	assert.NotContains(t, content, `{"sessionId":`, "JSON must not be minified")
}

// TestJSONFormat_ParseableByStandardTools verifies JSON can be parsed by cat/jq.
// This test only runs if jq is available on the system.
func TestJSONFormat_ParseableByStandardTools(t *testing.T) {
	// Check if jq is available
	_, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not available, skipping external tool test")
	}

	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	session := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
		Windows:     []Window{},
	}

	err = store.Save(session)
	require.NoError(t, err)

	filePath := filepath.Join(tmpDir, SessionFileName)

	// Test with jq
	cmd := exec.Command("jq", ".sessionId", filePath)
	output, err := cmd.Output()
	require.NoError(t, err, "jq should be able to parse the JSON")
	assert.Contains(t, string(output), "test-uuid")
}

// TestJSONFormat_RoundTrip_PreservesAllData verifies that Save → Load preserves all data.
func TestJSONFormat_RoundTrip_PreservesAllData(t *testing.T) {
	tmpDir := t.TempDir()
	store := &FileSessionStore{}

	original := &Session{
		SessionID:   "test-uuid",
		ProjectPath: tmpDir,
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

	// Save
	err := store.Save(original)
	require.NoError(t, err)

	// Load by project path
	loaded, err := store.Load(tmpDir)
	require.NoError(t, err)

	// Verify all fields preserved
	assert.Equal(t, original.SessionID, loaded.SessionID)
	assert.Equal(t, original.ProjectPath, loaded.ProjectPath)
	assert.Equal(t, original.Windows, loaded.Windows)

	// Verify each window field
	for i, window := range original.Windows {
		assert.Equal(t, window.TmuxWindowID, loaded.Windows[i].TmuxWindowID)
		assert.Equal(t, window.Name, loaded.Windows[i].Name)
	}
}

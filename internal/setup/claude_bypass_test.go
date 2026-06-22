package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedClaudeBypass_CreatesFileWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")

	require.NoError(t, seedClaudeBypassAt(path))

	assert.FileExists(t, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &obj))

	var b bool
	require.NoError(t, json.Unmarshal(obj[bypassAcceptedKey], &b))
	assert.True(t, b)
}

func TestSeedClaudeBypass_PreservesUnrelatedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	original := `{"projects":{"/x":{"a":1}},"hasCompletedOnboarding":true}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	require.NoError(t, seedClaudeBypassAt(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &obj))

	var b bool
	require.NoError(t, json.Unmarshal(obj[bypassAcceptedKey], &b))
	assert.True(t, b)

	assert.JSONEq(t, `{"/x":{"a":1}}`, string(obj["projects"]))
	assert.JSONEq(t, `true`, string(obj["hasCompletedOnboarding"]))
}

func TestSeedClaudeBypass_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"projects":{"/x":{"a":1}},"hasCompletedOnboarding":true}`), 0o644))

	require.NoError(t, seedClaudeBypassAt(path))

	first, err := os.ReadFile(path)
	require.NoError(t, err)

	// Second call must be a no-op (file unchanged, value still true).
	require.NoError(t, seedClaudeBypassAt(path))

	second, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second), "already-true file must not be rewritten")

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(second, &obj))
	var b bool
	require.NoError(t, json.Unmarshal(obj[bypassAcceptedKey], &b))
	assert.True(t, b)
	assert.JSONEq(t, `{"/x":{"a":1}}`, string(obj["projects"]))
	assert.JSONEq(t, `true`, string(obj["hasCompletedOnboarding"]))
}

func TestSeedClaudeBypass_FlipsFalseToTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"bypassPermissionsModeAccepted":false,"projects":{}}`), 0o644))

	require.NoError(t, seedClaudeBypassAt(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &obj))

	var b bool
	require.NoError(t, json.Unmarshal(obj[bypassAcceptedKey], &b))
	assert.True(t, b)
	assert.JSONEq(t, `{}`, string(obj["projects"]))
}

func TestSeedClaudeBypass_EmptyFileTreatedAsObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	require.NoError(t, seedClaudeBypassAt(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &obj))
	assert.Len(t, obj, 1)

	var b bool
	require.NoError(t, json.Unmarshal(obj[bypassAcceptedKey], &b))
	assert.True(t, b)
}

func TestSeedClaudeBypass_MalformedJSONErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	err := seedClaudeBypassAt(path)
	require.Error(t, err)

	// File content must be left untouched.
	data, err2 := os.ReadFile(path)
	require.NoError(t, err2)
	assert.Equal(t, "not json", string(data))
}

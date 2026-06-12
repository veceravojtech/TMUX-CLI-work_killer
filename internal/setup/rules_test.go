package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRules_MaterializesTree(t *testing.T) {
	root := t.TempDir()
	err := WriteRules(root, map[string]string{
		"manifest.yaml":  "version: 1",
		"_base/cmd.md":   "rule body",
		"php/rules.yaml": "- id: X",
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, ".tmux-cli", "rules", "_base", "cmd.md"))
	require.NoError(t, err)
	assert.Equal(t, "rule body", string(data))
}

func TestWriteRules_CleanSlatesEmbeddedButPreservesLocal(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".tmux-cli", "rules")

	// Simulate a prior install with a stale embedded file plus user-owned local rules.
	require.NoError(t, os.MkdirAll(filepath.Join(rulesDir, "stale-pack"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "stale-pack", "old.md"), []byte("stale"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(rulesDir, "local", "code-rules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "local", "code-rules", "team.yaml"), []byte("- id: TEAM-001"), 0o644))

	require.NoError(t, WriteRules(root, map[string]string{"manifest.yaml": "version: 1"}))

	_, err := os.Stat(filepath.Join(rulesDir, "stale-pack"))
	assert.True(t, os.IsNotExist(err), "stale embedded pack must be clean-slated")

	data, err := os.ReadFile(filepath.Join(rulesDir, "local", "code-rules", "team.yaml"))
	require.NoError(t, err, "local rules must survive re-setup")
	assert.Equal(t, "- id: TEAM-001", string(data))
}

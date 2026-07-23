package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAPIURL_DefaultWhenNoConfig(t *testing.T) {
	t.Setenv("TMUX_CLI_API_URL", "")
	assert.Equal(t, defaultAPIURL, LoadAPIURL(t.TempDir()))
}

func TestLoadAPIURL_EnvOverridesDefault(t *testing.T) {
	t.Setenv("TMUX_CLI_API_URL", "https://env.test")
	assert.Equal(t, "https://env.test", LoadAPIURL(t.TempDir()))
}

func TestLoadAPIURL_FlatKeyWins(t *testing.T) {
	root := t.TempDir()
	writeSetting(t, root, "apiUrl: https://flat.test\napi:\n  url: https://nested.test\n")
	t.Setenv("TMUX_CLI_API_URL", "https://env.test")
	assert.Equal(t, "https://flat.test", LoadAPIURL(root))
}

func TestLoadAPIURL_NestedFallback(t *testing.T) {
	root := t.TempDir()
	writeSetting(t, root, "api:\n  url: https://nested.test\n")
	assert.Equal(t, "https://nested.test", LoadAPIURL(root))
}

func writeSetting(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(body), 0o644))
}

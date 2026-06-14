package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProjectSettings scaffolds a minimal .tmux-cli/setting.yaml under root.
func writeProjectSettings(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(content), 0o644))
}

func TestApiProjectCmd_PrintsAutoDerivedLane(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, "apiEnabled: true\n")

	withCwd(t, root, func() {
		var buf bytes.Buffer
		apiProjectCmd.SetOut(&buf)
		require.NoError(t, apiProjectCmd.RunE(apiProjectCmd, nil))
		out := strings.TrimSpace(buf.String())
		// lane == "<fingerprint>:<abs cwd>" — assert shape, not the exact (machine-
		// and tmpdir-dependent) value. os.Getwd may return a symlink-resolved path.
		require.NotEmpty(t, out)
		assert.Contains(t, out, ":", "lane is <fingerprint>:<abs-path>")
		resolved, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(out, ":"+root) || strings.HasSuffix(out, ":"+resolved),
			"lane must end with the absolute working dir, got %q", out)
	})
}

func TestApiProjectCmd_PrintsConfiguredOverride(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, "apiEnabled: true\nproject: laptop:/x/cli\n")

	withCwd(t, root, func() {
		var buf bytes.Buffer
		apiProjectCmd.SetOut(&buf)
		require.NoError(t, apiProjectCmd.RunE(apiProjectCmd, nil))
		assert.Equal(t, "laptop:/x/cli", strings.TrimSpace(buf.String()))
	})
}

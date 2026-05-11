package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
)

func newHooksTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	s := setup.DefaultSettings()
	require.NoError(t, setup.SaveSettings(root, s))
	mock := new(testutil.MockTmuxExecutor)
	return newTestServer(mock, root), root
}

func TestHooksConfig_List(t *testing.T) {
	server, _ := newHooksTestServer(t)

	out, err := server.HooksConfig("list", "")
	require.NoError(t, err)

	assert.False(t, out.Changed)
	assert.NotNil(t, out.Hooks)
	assert.True(t, out.Hooks.SessionNotify)
	assert.True(t, out.Hooks.BlockInteractive)
}

func TestHooksConfig_DisableSessionNotify(t *testing.T) {
	server, root := newHooksTestServer(t)

	out, err := server.HooksConfig("disable", "session_notify")
	require.NoError(t, err)

	assert.True(t, out.Changed)
	assert.False(t, out.Hooks.SessionNotify)
	assert.True(t, out.Hooks.BlockInteractive)

	reloaded, err := setup.LoadSettings(root)
	require.NoError(t, err)
	assert.False(t, reloaded.Hooks.SessionNotify)
}

func TestHooksConfig_EnableBlockInteractive(t *testing.T) {
	server, root := newHooksTestServer(t)

	// First disable it
	_, err := server.HooksConfig("disable", "block_interactive")
	require.NoError(t, err)

	// Then enable
	out, err := server.HooksConfig("enable", "block_interactive")
	require.NoError(t, err)

	assert.True(t, out.Changed)
	assert.True(t, out.Hooks.BlockInteractive)

	reloaded, err := setup.LoadSettings(root)
	require.NoError(t, err)
	assert.True(t, reloaded.Hooks.BlockInteractive)
}

func TestHooksConfig_InvalidAction(t *testing.T) {
	server, _ := newHooksTestServer(t)

	_, err := server.HooksConfig("delete", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "invalid action")
}

func TestHooksConfig_InvalidHookName(t *testing.T) {
	server, _ := newHooksTestServer(t)

	_, err := server.HooksConfig("enable", "nonexistent_hook")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "invalid hook name")
}

func TestHooksConfig_ListNoSettingsFile(t *testing.T) {
	root := t.TempDir()
	mock := new(testutil.MockTmuxExecutor)
	server := newTestServer(mock, root)

	out, err := server.HooksConfig("list", "")
	require.NoError(t, err)

	assert.False(t, out.Changed)
	assert.True(t, out.Hooks.SessionNotify)
	assert.True(t, out.Hooks.BlockInteractive)

	// Verify settings.yaml was auto-created
	_, err = os.Stat(filepath.Join(root, ".tmux-cli", "settings.yaml"))
	assert.NoError(t, err)
}

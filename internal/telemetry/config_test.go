package telemetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSetting(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte(body), 0o644))
}

func TestEnabled_DefaultsTrueWhenNoSettingFile(t *testing.T) {
	assert.True(t, Enabled(t.TempDir()), "telemetry defaults ON when setting.yaml is absent")
}

func TestEnabled_DefaultsTrueWhenKeyAbsent(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "supervisor:\n  max_cycles: 3\n")
	assert.True(t, Enabled(dir), "telemetry defaults ON when the telemetry block is absent")
}

func TestEnabled_RespectsExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: false\n")
	assert.False(t, Enabled(dir), "explicit telemetry.enabled:false must disable emit")
}

func TestEnabled_RespectsExplicitTrue(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n")
	assert.True(t, Enabled(dir))
}

func TestResolveIdentity_ProjectFromEnvOverridesBasename(t *testing.T) {
	t.Setenv("TMUX_CLI_PROJECT", "web")
	id := ResolveIdentity(t.TempDir())
	assert.Equal(t, "web", id.Project)
	assert.Len(t, id.Fingerprint, 64, "fingerprint is the 64-hex machine identity")
}

func TestResolveIdentity_ProjectFallsBackToDirBasename(t *testing.T) {
	os.Unsetenv("TMUX_CLI_PROJECT")
	dir := filepath.Join(t.TempDir(), "cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	id := ResolveIdentity(dir)
	assert.Equal(t, "cli", id.Project)
}

func TestResolveIdentity_SessionFromEnv(t *testing.T) {
	t.Setenv("TMUX_CLI_SESSION_ID", "my-session")
	id := ResolveIdentity(t.TempDir())
	assert.Equal(t, "my-session", id.SessionID)
}

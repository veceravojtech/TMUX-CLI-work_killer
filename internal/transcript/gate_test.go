package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSetting(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte(content), 0o644))
}

// fakeLogin points the XDG-resolved auth store at a temp dir holding a valid
// 0600 auth.json, so shipper.LoggedIn sees a logged-in machine.
func fakeLogin(t *testing.T) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"),
		[]byte(`{"api_url":"https://tmux.vojta.ai","account":"t@example.com","access_token":"tok","refresh_token":"ref","expires_at":"2030-01-01T00:00:00Z","scopes":["telemetry:write"]}`), 0o600))
}

func fakeLoggedOut(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config dir → no auth.json
}

func TestArmed_TrueOnlyWhenAllThreeGatesPass(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	fakeLogin(t)
	assert.True(t, Armed(dir))
}

func TestArmed_FalseWhenTranscriptsOff(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: false\n")
	fakeLogin(t)
	assert.False(t, Armed(dir))
}

func TestArmed_FalseWhenTranscriptsAbsent_OptInDefault(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n")
	fakeLogin(t)
	assert.False(t, Armed(dir), "transcripts is OPT-IN: an absent key must read as false")
}

func TestArmed_FalseWhenTelemetryDisabled_KillSwitch(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: false\n  transcripts: true\n")
	fakeLogin(t)
	assert.False(t, Armed(dir), "telemetry.enabled=false is the kill switch for transcripts too")
}

func TestArmed_FalseWhenLoggedOut(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	fakeLoggedOut(t)
	assert.False(t, Armed(dir))
}

func TestArmed_FalseWhenNoSettingFile(t *testing.T) {
	fakeLogin(t)
	assert.False(t, Armed(t.TempDir()))
}

func TestCapturePipeCommand_EmptyWhenNotArmed(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: false\n")
	fakeLogin(t)
	assert.Empty(t, CapturePipeCommand(dir, "sess", "supervisor", ""))
}

func TestCapturePipeCommand_CaptureOnlyWithoutPaneLog(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	fakeLogin(t)
	cmd := CapturePipeCommand(dir, "sess-1", "supervisor", "")
	require.NotEmpty(t, cmd)
	assert.NotContains(t, cmd, "tee", "windows without a pane log pipe straight into capture")
	assert.Contains(t, cmd, "logs capture")
	assert.Contains(t, cmd, `--window "supervisor"`)
	assert.Contains(t, cmd, `--session "sess-1"`)
	assert.Contains(t, cmd, `--dir "`+dir+`"`)
}

func TestCapturePipeCommand_TeesThroughExistingPaneLog(t *testing.T) {
	dir := t.TempDir()
	writeSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	fakeLogin(t)
	paneLog := filepath.Join(dir, ".tmux-cli", "logs", "panes", "execute-1.log")
	cmd := CapturePipeCommand(dir, "sess-1", "execute-1", paneLog)
	require.NotEmpty(t, cmd)
	assert.Contains(t, cmd, `tee -a "`+paneLog+`" | `, "worker windows keep their pane log via tee (one pipe per pane)")
	assert.Contains(t, cmd, `--window "execute-1"`)
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func spoolFiles(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli", "logs", "spool")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "events-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func resetTelemetryFlags() {
	telemetryEmitEvent = ""
	telemetryEmitWindow = ""
	telemetryEmitPayloadJSON = ""
}

func TestTelemetryEmit_WritesEventToSpool(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	resetTelemetryFlags()
	telemetryEmitEvent = "hook.fired"
	telemetryEmitWindow = "supervisor"
	telemetryEmitPayloadJSON = `{"hook":"Stop","action":"restart"}`

	runTelemetryEmit()

	files := spoolFiles(t, root)
	require.Len(t, files, 1, "emit must create exactly one spool segment")
	raw, err := os.ReadFile(filepath.Join(root, ".tmux-cli", "logs", "spool", files[0]))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"event":"hook.fired"`)
	assert.Contains(t, string(raw), `"hook":"Stop"`)
	assert.True(t, strings.HasSuffix(string(raw), "\n"))
}

func TestTelemetryEmit_NoEvent_IsNoOpButSucceeds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	resetTelemetryFlags()
	// No --event set.
	assert.NotPanics(t, func() { runTelemetryEmit() })
	assert.Empty(t, spoolFiles(t, root), "a missing --event emits nothing")
}

func TestTelemetryEmit_Disabled_IsNoOp(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".tmux-cli", "setting.yaml"), []byte("telemetry:\n  enabled: false\n"), 0o644))
	t.Chdir(root)
	resetTelemetryFlags()
	telemetryEmitEvent = "hook.fired"

	runTelemetryEmit()
	assert.Empty(t, spoolFiles(t, root), "telemetry.enabled:false disables emit entirely")
}

func TestTelemetryEmit_MalformedPayload_StillSucceeds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	resetTelemetryFlags()
	telemetryEmitEvent = "hook.fired"
	telemetryEmitPayloadJSON = `{not valid json`

	assert.NotPanics(t, func() { runTelemetryEmit() })
	// The event is still spooled; the bad payload degrades to an empty object.
	files := spoolFiles(t, root)
	require.Len(t, files, 1)
	raw, _ := os.ReadFile(filepath.Join(root, ".tmux-cli", "logs", "spool", files[0]))
	assert.Contains(t, string(raw), `"payload":{}`)
}

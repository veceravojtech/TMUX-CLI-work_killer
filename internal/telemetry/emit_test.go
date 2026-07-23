package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmit_NoOpUntilSetDefault(t *testing.T) {
	// Reset any prior default (test order independence).
	SetDefault(nil)
	// Must not panic and must write nothing anywhere.
	assert.NotPanics(t, func() { Emit("goal.status", "", map[string]any{"x": 1}) })
}

func TestEmit_UsesInstalledDefault(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(Options{Dir: dir, Identity: testIdentity(), Enabled: true, PID: 3})
	SetDefault(w)
	t.Cleanup(func() { SetDefault(nil) })

	Emit("goal.status", "supervisor-001", map[string]any{"from": "pending", "to": "running"})

	evs := readSegments(t, dir)
	require.Len(t, evs, 1)
	assert.Equal(t, "goal.status", evs[0].Event)
	assert.Equal(t, "pending", evs[0].Payload["from"])
}

func TestInstallDefault_RespectsGating(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte("telemetry:\n  enabled: false\n"), 0o644))
	InstallDefault(dir)
	t.Cleanup(func() { SetDefault(nil) })

	Emit("goal.status", "", nil)

	spool := filepath.Join(dir, ".tmux-cli", "logs", "spool")
	entries, _ := os.ReadDir(spool)
	var count int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "events-") {
			count++
		}
	}
	assert.Zero(t, count, "InstallDefault must honor telemetry.enabled:false")
}

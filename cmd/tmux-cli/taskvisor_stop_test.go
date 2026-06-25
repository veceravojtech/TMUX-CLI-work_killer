package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findSub(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// TestTaskvisorStop_WiredAtTopLevel is the core of the user's complaint: stop
// must be a first-class `taskvisor stop`, a sibling of start/restart — not buried
// under `taskvisor goal stop`.
func TestTaskvisorStop_WiredAtTopLevel(t *testing.T) {
	stop := findSub(taskvisorCmd, "stop")
	require.NotNil(t, stop, "taskvisor must expose a top-level `stop` subcommand")
	assert.NotEmpty(t, stop.Short, "stop command needs a Short description")
}

// TestDoTaskvisorStop_WritesStopSignal: stop must ask the daemon to go IDLE by
// writing the taskvisor-stop signal file — NOT kill the process. A live daemon
// (whose PID file is present) must be left running; only the signal is written.
func TestDoTaskvisorStop_WritesStopSignal(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// A PID file the OLD (kill-based) implementation would have deleted; stop must
	// leave it untouched — the daemon process stays alive.
	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte("424242"), 0o644))

	require.NoError(t, doTaskvisorStop(tmp))

	data, err := os.ReadFile(filepath.Join(tmuxDir, "taskvisor-stop"))
	require.NoError(t, err, "stop signal file must be written")
	assert.Equal(t, "stop", string(data))

	_, err = os.Stat(pidPath)
	assert.NoError(t, err, "PID file must be left intact — stop does not kill the process")
}

// TestDoTaskvisorStop_CreatesTmuxDir: stop is robust even if .tmux-cli does not
// exist yet (idempotent no-op success), creating the dir for the signal.
func TestDoTaskvisorStop_CreatesTmuxDir(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, doTaskvisorStop(tmp))
	_, err := os.Stat(filepath.Join(tmp, ".tmux-cli", "taskvisor-stop"))
	require.NoError(t, err)
}

// TestTaskvisorGoalStop_DelegatesToStop: the legacy `taskvisor goal stop` alias
// must keep working (no break for muscle memory) and be marked Deprecated so users
// migrate to the top-level `taskvisor stop`.
func TestTaskvisorGoalStop_DelegatesToStop(t *testing.T) {
	goalStop := findSub(taskvisorGoalCmd, "stop")
	require.NotNil(t, goalStop, "legacy `taskvisor goal stop` alias must remain")
	assert.NotEmpty(t, goalStop.Deprecated, "legacy alias should be marked Deprecated, pointing at `taskvisor stop`")
}

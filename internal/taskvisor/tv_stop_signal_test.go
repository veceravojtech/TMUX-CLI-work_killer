package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func writeStopSignal(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, ".tmux-cli", "taskvisor-stop")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("stop"), 0o644))
	return p
}

// A stop signal while ACTIVE must drop the daemon to IDLE (the graceful inverse
// of start) — NOT kill the process. poll() consumes the signal and deactivates.
func TestPoll_StopSignal_ActiveDeactivatesToIdle(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir) // taskvisor-active marker present (as activate() would leave it)
	stopPath := writeStopSignal(t, dir)

	// deactivate(): no in-flight goal → no kills; ensureWindow0Supervisor sees
	// window-0 "supervisor" live → no-op.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	require.NoError(t, d.poll(context.Background()))

	assert.Equal(t, modeIdle, d.mode, "daemon must be IDLE after a stop signal")
	_, statErr := os.Stat(stopPath)
	assert.True(t, os.IsNotExist(statErr), "stop signal must be consumed")
	_, statErr = os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-active"))
	assert.True(t, os.IsNotExist(statErr), "taskvisor-active marker cleared by deactivate")
}

// A stop signal while already IDLE is a no-op: the stray signal is consumed so it
// cannot trigger an immediate deactivate right after the next start/activate.
func TestPoll_StopSignal_IdleConsumesStraySignal(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.mode = modeIdle
	writeSettings(t, dir, true, true)
	stopPath := writeStopSignal(t, dir)

	require.NoError(t, d.poll(context.Background()))

	assert.Equal(t, modeIdle, d.mode)
	_, statErr := os.Stat(stopPath)
	assert.True(t, os.IsNotExist(statErr), "stray stop signal cleared while idle")
}

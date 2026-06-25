package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

// taskvisor-stop must ask the daemon to go IDLE by writing the taskvisor-stop
// signal file — NOT kill the process. A present PID file must be left intact.
func TestTaskvisorStop_WritesStopSignal(t *testing.T) {
	tmpDir := t.TempDir()
	tmuxDir := filepath.Join(tmpDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte("424242"), 0o644))

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.TaskvisorStop()
	require.NoError(t, err)
	assert.True(t, out.Stopped)

	data, err := os.ReadFile(filepath.Join(tmuxDir, "taskvisor-stop"))
	require.NoError(t, err, "stop signal file must be written")
	assert.Equal(t, "stop", string(data))

	_, statErr := os.Stat(pidPath)
	assert.NoError(t, statErr, "PID file must be left intact — stop does not kill the daemon")
}

// Stopping when .tmux-cli does not yet exist is a success no-op (creates the dir).
func TestTaskvisorStop_CreatesTmuxDir(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	out, err := server.TaskvisorStop()
	require.NoError(t, err)
	assert.True(t, out.Stopped)

	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "taskvisor-stop"))
	require.NoError(t, statErr)
}

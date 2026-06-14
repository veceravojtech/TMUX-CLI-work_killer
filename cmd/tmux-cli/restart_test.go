//go:build integration

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTaskvisorRestart_StopAndRelaunch(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// Fork a subprocess that sleeps (simulates a running daemon).
	cmd := exec.Command("bash", "-c", "trap 'exit 0' TERM; while true; do sleep 0.1; done")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	sessionID := "test-session-123"

	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmp).Return(sessionID, nil)
	mockExec.On("ListWindows", sessionID).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "taskvisor"},
	}, nil)
	// Simulate the daemon restarting by writing a new PID file on SendMessage.
	mockExec.On("SendMessage", sessionID, "@1", "tmux-cli taskvisor --run").Run(func(args mock.Arguments) {
		_ = os.WriteFile(pidPath, []byte("99999"), 0o644)
	}).Return(nil)

	err := doTaskvisorRestart(tmp, mockExec)
	require.NoError(t, err)

	mockExec.AssertCalled(t, "SendMessage", sessionID, "@1", "tmux-cli taskvisor --run")
}

func TestTaskvisorRestart_NoDaemon(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")

	// No PID file — stopDaemonProcess returns nil, restart should still relaunch.
	mockExec := new(testutil.MockTmuxExecutor)
	sessionID := "test-session-456"

	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmp).Return(sessionID, nil)
	mockExec.On("ListWindows", sessionID).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "taskvisor"},
	}, nil)
	mockExec.On("SendMessage", sessionID, "@1", "tmux-cli taskvisor --run").Run(func(args mock.Arguments) {
		_ = os.WriteFile(pidPath, []byte("88888"), 0o644)
	}).Return(nil)

	err := doTaskvisorRestart(tmp, mockExec)
	require.NoError(t, err)

	mockExec.AssertCalled(t, "SendMessage", sessionID, "@1", "tmux-cli taskvisor --run")
}

func TestTaskvisorRestart_NoSession(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// PID file exists but daemon already dead.
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmp).Return("", fmt.Errorf("no session found"))

	err := doTaskvisorRestart(tmp, mockExec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tmux-cli session found")

	// SendMessage should NOT have been called.
	mockExec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

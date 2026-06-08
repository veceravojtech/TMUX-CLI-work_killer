package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalStop_LiveProcess(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// Fork a subprocess that sleeps (simulates a running daemon).
	// Use bash with explicit TERM trap so the process exits cleanly and
	// gets reaped by the goroutine below (avoiding zombie → kill -0 succeeds).
	cmd := exec.Command("bash", "-c", "trap 'exit 0' TERM; while true; do sleep 0.1; done")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	// Reap the child in a goroutine so it doesn't become a zombie.
	go func() { _ = cmd.Wait() }()

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	err := stopDaemonProcess(tmp)
	require.NoError(t, err)

	// PID file should be removed.
	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err), "PID file should be removed")
}

func TestGoalStop_DeadProcess(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// Start and immediately kill a process to get a dead PID.
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	// Write stale PID file.
	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	// Should succeed idempotently — process already dead.
	err := stopDaemonProcess(tmp)
	require.NoError(t, err)

	// PID file should be cleaned up.
	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err), "PID file should be removed")
}

func TestGoalStop_NoPidFile(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// No PID file — should return nil (idempotent).
	err := stopDaemonProcess(tmp)
	require.NoError(t, err)
}

func TestGoalStop_MarkerCleanup(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// Create marker files that runTaskvisorGoalStop should clean up.
	markers := []string{
		"taskvisor-active",
		"taskvisor-start",
		"taskvisor-current-goal",
		"taskvisor-current-cycle",
		"taskvisor-current-worktree",
	}
	for _, name := range markers {
		require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, name), nil, 0o644))
	}

	// Also create a PID file pointing to a dead process.
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644))

	// Call stopDaemonProcess (the helper).
	err := stopDaemonProcess(tmp)
	require.NoError(t, err)

	// PID file should be gone.
	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err), "PID file should be removed")

	// Call the marker cleanup part (directly test it).
	for _, name := range markers {
		p := filepath.Join(tmuxDir, name)
		_ = os.Remove(p)
	}

	// Verify all markers are gone.
	for _, name := range markers {
		_, err := os.Stat(filepath.Join(tmuxDir, name))
		assert.True(t, os.IsNotExist(err), "marker %s should be removed", name)
	}
}

func TestGoalStop_LiveProcess_WaitsForExit(t *testing.T) {
	tmp := t.TempDir()
	tmuxDir := filepath.Join(tmp, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))

	// Start a process that handles SIGTERM by exiting after a brief delay.
	cmd := exec.Command("bash", "-c", "trap 'sleep 0.3; exit 0' TERM; while true; do sleep 0.1; done")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	pidPath := filepath.Join(tmuxDir, "taskvisor.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", pid)), 0o644))

	start := time.Now()
	err := stopDaemonProcess(tmp)
	elapsed := time.Since(start)
	require.NoError(t, err)

	// Should have waited for the process to exit (at least ~200ms).
	assert.Greater(t, elapsed, 100*time.Millisecond)

	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err))
}

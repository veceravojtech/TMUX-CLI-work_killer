//go:build integration

package tmux

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmuxRun runs a tmux command and fails the test on error, returning trimmed stdout.
func tmuxRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	require.NoErrorf(t, err, "tmux %s: %s", strings.Join(args, " "), string(out))
	return strings.TrimSpace(string(out))
}

// processAlive reports whether pid names a live process (syscall.Kill(pid, 0)),
// the same liveness probe TerminateWindowProcess uses.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// newIntegrationSession creates a detached tmux session and returns its
// (sessionID, windowID), registering cleanup that kills the session.
func newIntegrationSession(t *testing.T, name string) (string, string) {
	t.Helper()
	_ = exec.Command("tmux", "kill-session", "-t", name).Run() // clear any stale session
	tmuxRun(t, "new-session", "-d", "-s", name, "-x", "200", "-y", "50")
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	windowID := tmuxRun(t, "list-windows", "-t", name, "-F", "#{window_id}")
	return name, windowID
}

// panePID reads #{pane_pid} for a window.
func panePID(t *testing.T, windowID string) int {
	t.Helper()
	pid, err := strconv.Atoi(tmuxRun(t, "display-message", "-p", "-t", windowID, "#{pane_pid}"))
	require.NoError(t, err)
	return pid
}

// waitForChild polls pgrep -P panePID until a child appears (or fails).
func waitForChild(t *testing.T, pp int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if kids := paneForegroundChildren(pp); len(kids) > 0 {
			return kids[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no foreground child appeared under pane pid %d", pp)
	return 0
}

// TestTerminateWindowProcess_KillsSigintIgnoringProcess proves that a single
// C-c does NOT kill a SIGINT-ignoring foreground process, but
// TerminateWindowProcess does — while the window survives.
func TestTerminateWindowProcess_KillsSigintIgnoringProcess(t *testing.T) {
	sessionID, windowID := newIntegrationSession(t, "tmux-cli-term-sigint-test")
	pp := panePID(t, windowID)

	// Launch a foreground child that traps (ignores) INT.
	tmuxRun(t, "send-keys", "-t", windowID,
		`sh -c 'trap "" INT; while true; do sleep 0.2; done'`, "Enter")
	childPID := waitForChild(t, pp)

	// A single C-c must be ignored: the child is still alive after it.
	tmuxRun(t, "send-keys", "-t", windowID, "C-c")
	time.Sleep(500 * time.Millisecond)
	assert.True(t, processAlive(childPID), "child must still be alive after single C-c")

	// TerminateWindowProcess must actually kill it.
	executor := NewTmuxExecutor()
	require.NoError(t, executor.TerminateWindowProcess(windowID))

	assert.False(t, processAlive(childPID), "child must be gone after TerminateWindowProcess")

	// Pane is back at a shell and the window still exists.
	fg := tmuxRun(t, "display-message", "-p", "-t", windowID, "#{pane_current_command}")
	assert.True(t, isShellCommand(fg), "pane should be back at a shell, got %q", fg)
	windows := tmuxRun(t, "list-windows", "-t", sessionID, "-F", "#{window_id}")
	assert.Contains(t, windows, windowID, "window must be preserved")
}

// TestTerminateWindowProcess_PaneAlreadyShell proves that terminating a pane
// already idling at a shell (no foreground child) is a no-op returning nil,
// leaving the shell and window intact.
func TestTerminateWindowProcess_PaneAlreadyShell(t *testing.T) {
	sessionID, windowID := newIntegrationSession(t, "tmux-cli-term-idle-test")
	pp := panePID(t, windowID)
	require.Empty(t, paneForegroundChildren(pp), "pane should start with no foreground child")

	executor := NewTmuxExecutor()
	require.NoError(t, executor.TerminateWindowProcess(windowID))

	// The pane shell itself must survive (never killed).
	assert.True(t, processAlive(pp), "pane shell must still be alive")
	windows := tmuxRun(t, "list-windows", "-t", sessionID, "-F", "#{window_id}")
	assert.Contains(t, windows, windowID, "window must be preserved")
}

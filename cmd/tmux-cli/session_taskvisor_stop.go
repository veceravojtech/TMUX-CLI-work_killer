package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// stopDaemonProcess terminates the running taskvisor daemon via its PID file. It
// is idempotent: a missing PID file, an unparseable PID, or an already-dead
// process all resolve to nil (and the PID file is cleaned up). Shared by the stop
// and restart paths.
func stopDaemonProcess(cwd string) error {
	pidPath := filepath.Join(cwd, ".tmux-cli", "taskvisor.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = proc // keep vet happy
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "warning: daemon PID %d did not exit within 10s after SIGTERM\n", pid)
	_ = os.Remove(pidPath)
	return nil
}

// doTaskvisorStop asks the running daemon to drop to IDLE by writing the
// taskvisor-stop signal file (the inverse of the taskvisor-start signal). The
// daemon consumes it on its next poll and calls deactivate() — tearing down
// in-flight goal windows and returning to IDLE WITHOUT being killed, so it stays
// up ready for the next `taskvisor start`. Writing the signal is idempotent and
// harmless when no daemon is running (a fresh daemon consumes the stray signal
// as an idle no-op). Shared by the top-level `taskvisor stop`, the legacy
// `taskvisor goal stop` alias, and the taskvisor-stop MCP tool.
//
// To fully terminate the daemon PROCESS (e.g. for a binary swap), use
// `taskvisor restart`, which stopDaemonProcess-kills then relaunches it.
func doTaskvisorStop(cwd string) error {
	signalPath := filepath.Join(cwd, ".tmux-cli", "taskvisor-stop")
	if err := os.MkdirAll(filepath.Dir(signalPath), 0o755); err != nil {
		return fmt.Errorf("create .tmux-cli dir: %w", err)
	}
	if err := os.WriteFile(signalPath, []byte("stop"), 0o644); err != nil {
		return fmt.Errorf("write stop signal: %w", err)
	}
	return nil
}

func runTaskvisorStop(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	if err := doTaskvisorStop(cwd); err != nil {
		return err
	}
	fmt.Println("Taskvisor stop signal sent — the daemon will return to IDLE (process stays up)")
	return nil
}

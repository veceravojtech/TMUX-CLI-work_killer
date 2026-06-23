package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
)

var watchdogCmd = &cobra.Command{
	Use:                "watchdog",
	Short:              "Run the installed window-watchdog hook (--watch/--once/--dry-run)",
	DisableFlagParsing: true,
	RunE:               runWatchdog,
}

// runWatchdog locates the installed tmux-window-watchdog.sh hook under the BASE
// project's .tmux-cli/hooks/ and syscall.Execs it with all CLI args forwarded
// verbatim. The periodic loop lives in the shell script; this is a thin shim.
func runWatchdog(cmd *cobra.Command, args []string) error {
	root, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	script := filepath.Join(root, ".tmux-cli", "hooks", "tmux-window-watchdog.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("window-watchdog hook not found at %s — start a tmux-cli session (or run setup) so the hook is installed by WriteHookScripts: %w", script, err)
	}

	argv := append([]string{script}, args...)
	return syscall.Exec(script, argv, os.Environ())
}

func init() {
	rootCmd.AddCommand(watchdogCmd)
}

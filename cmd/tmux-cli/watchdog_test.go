package main

import (
	"os"
	"strings"
	"testing"
)

func TestWatchdogCmd_IsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"watchdog"})
	if err != nil {
		t.Fatalf("watchdog command not found: %v", err)
	}
	if cmd.Use != "watchdog" {
		t.Errorf("Command Use = %q, want %q", cmd.Use, "watchdog")
	}
}

func TestWatchdogCmd_DisablesFlagParsing(t *testing.T) {
	if !watchdogCmd.DisableFlagParsing {
		t.Error("watchdogCmd.DisableFlagParsing = false, want true (so --watch/--once/--dry-run forward to the script)")
	}
}

func TestWatchdogCmd_HasRunE(t *testing.T) {
	if watchdogCmd.RunE == nil {
		t.Error("watchdogCmd.RunE is nil, want a non-nil RunE")
	}
}

func TestRunWatchdog_MissingHook_ErrorsCleanly(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(orig)

	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}

	err = runWatchdog(watchdogCmd, []string{"--once"})
	if err == nil {
		t.Fatal("runWatchdog returned nil error for a missing hook, want non-nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tmux-window-watchdog.sh") {
		t.Errorf("error message %q does not name the script path", msg)
	}
	if !strings.Contains(msg, "not found") {
		t.Errorf("error message %q lacks 'not found' / setup guidance", msg)
	}
}

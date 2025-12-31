package main

import (
	"testing"
)

func TestRootCmd_Execute_NoArgs_ShowsHelp(t *testing.T) {
	// This test ensures root command executes without error when called with no args
	// The command should show help text rather than erroring

	err := rootCmd.Execute()
	if err != nil {
		t.Errorf("rootCmd.Execute() failed with error: %v", err)
	}
}

func TestRootCmd_Version_IsSet(t *testing.T) {
	// Verify that version is properly configured on root command
	if rootCmd.Version != version {
		t.Errorf("rootCmd.Version = %q, want %q", rootCmd.Version, version)
	}
}

func TestDetermineExitCode_NilError_ReturnsSuccess(t *testing.T) {
	code := determineExitCode(nil)
	if code != ExitSuccess {
		t.Errorf("determineExitCode(nil) = %d, want %d", code, ExitSuccess)
	}
}

func TestDetermineExitCode_GeneralError_ReturnsGeneralError(t *testing.T) {
	// For now, all non-nil errors return ExitGeneralError
	// This will be enhanced when specific error types are defined
	err := &testError{msg: "test error"}
	code := determineExitCode(err)
	if code != ExitGeneralError {
		t.Errorf("determineExitCode(error) = %d, want %d", code, ExitGeneralError)
	}
}

func TestExitCodes_FollowAR8Standard(t *testing.T) {
	// Verify exit code constants match AR8 specification
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"ExitSuccess", ExitSuccess, 0},
		{"ExitGeneralError", ExitGeneralError, 1},
		{"ExitUsageError", ExitUsageError, 2},
		{"ExitCommandNotFound", ExitCommandNotFound, 126},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.code, tt.expected)
			}
		})
	}
}

func TestRootCmd_WindowsSendCommand_IsRegistered(t *testing.T) {
	// Verify that the windows-send command is registered with the root command
	cmd, _, err := rootCmd.Find([]string{"windows-send"})
	if err != nil {
		t.Fatalf("windows-send command not found: %v", err)
	}
	if cmd.Use != "windows-send" {
		t.Errorf("Command Use = %q, want %q", cmd.Use, "windows-send")
	}
}

// testError is a simple error type for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/sudo"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartCmd_HasSudoFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("sudo")
	assert.NotNil(t, flag, "--sudo flag should exist on start command")
}

func TestStartAttachCmd_HasSudoFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("sudo")
	assert.NotNil(t, flag, "--sudo flag should exist on start-attach command")
}

func TestPromptAndStoreSudoPassword_Success(t *testing.T) {
	original := readPassword
	defer func() { readPassword = original }()

	readPassword = func() (string, error) {
		return "test-pass", nil
	}

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("SetSessionEnvironment", "test-session", "TMUX_CLI_SUDO_PASS", "test-pass").Return(nil)

	err := promptAndStoreSudoPassword(mockExec, "test-session")
	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestPromptAndStoreSudoPassword_ReadError(t *testing.T) {
	original := readPassword
	defer func() { readPassword = original }()

	readPassword = func() (string, error) {
		return "", fmt.Errorf("terminal not available")
	}

	mockExec := new(testutil.MockTmuxExecutor)

	err := promptAndStoreSudoPassword(mockExec, "test-session")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read sudo password")
}

func TestSudoCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sudo"})
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Equal(t, "sudo", cmd.Use)
}

func TestSudoCmd_HasTimeoutFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sudo"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("timeout")
	assert.NotNil(t, flag, "--timeout flag should exist on sudo command")
	assert.Equal(t, "0", flag.DefValue, "default timeout should be 0")
}

func TestSudoCmd_RequiresExactlyOneArg(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sudo"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	assert.NotNil(t, cmd.Args, "sudo command should have Args validator")
}

func TestSudoCmd_HasRunE(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sudo"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	assert.NotNil(t, cmd.RunE, "sudo command should have RunE function")
}

func TestLogSudoResult_Success(t *testing.T) {
	tmpDir := t.TempDir()

	logSudoResult(tmpDir, "apt-get update", nil, 150*time.Millisecond)

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var entry sudo.LogEntry
	require.NoError(t, json.Unmarshal(data, &entry))

	assert.Equal(t, "apt-get update", entry.Command)
	assert.Equal(t, 0, entry.ExitCode)
	assert.Equal(t, int64(150), entry.DurationMs)
	assert.Equal(t, 0, entry.StdoutLen)
	assert.Equal(t, 0, entry.StderrLen)
	assert.Empty(t, entry.Error)
}

func TestLogSudoResult_WithError(t *testing.T) {
	tmpDir := t.TempDir()

	logSudoResult(tmpDir, "systemctl restart nginx", fmt.Errorf("exit status 1"), 500*time.Millisecond)

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var entry sudo.LogEntry
	require.NoError(t, json.Unmarshal(data, &entry))

	assert.Equal(t, "systemctl restart nginx", entry.Command)
	assert.Equal(t, 1, entry.ExitCode)
	assert.Equal(t, int64(500), entry.DurationMs)
	assert.Equal(t, "exit status 1", entry.Error)
	assert.Equal(t, 0, entry.StdoutLen)
	assert.Equal(t, 0, entry.StderrLen)
}

func TestLogSudoResult_StreamingHasZeroLengths(t *testing.T) {
	tmpDir := t.TempDir()

	logSudoResult(tmpDir, "cat /etc/hosts", nil, 10*time.Millisecond)

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var entry sudo.LogEntry
	require.NoError(t, json.Unmarshal(data, &entry))

	assert.Equal(t, 0, entry.StdoutLen, "streaming mode cannot capture stdout length")
	assert.Equal(t, 0, entry.StderrLen, "streaming mode cannot capture stderr length")
}

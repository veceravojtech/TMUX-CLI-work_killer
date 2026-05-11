package main

import (
	"fmt"
	"testing"

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

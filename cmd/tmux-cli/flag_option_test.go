package main

import (
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestStartCmd_HasFlagFlag pins the repeatable --flag option on `start` (mirrors
// --model). The flag records TMUX_CLI_FLAGS in the session environment and
// injects its values verbatim into every claude launch.
func TestStartCmd_HasFlagFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("flag")
	require.NotNil(t, flag, "--flag option should exist on start command")
	assert.Equal(t, "stringArray", flag.Value.Type(),
		"--flag is repeatable (StringArray), not a single String")
	assert.Equal(t, "[]", flag.DefValue, "--flag defaults to no flags")
}

// TestStartAttachCmd_HasFlagFlag pins the repeatable --flag option on
// `start-attach`.
func TestStartAttachCmd_HasFlagFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("flag")
	require.NotNil(t, flag, "--flag option should exist on start-attach command")
	assert.Equal(t, "stringArray", flag.Value.Type(),
		"--flag is repeatable (StringArray), not a single String")
	assert.Equal(t, "[]", flag.DefValue, "--flag defaults to no flags")
}

// TestApplyFlagsToExistingSession verifies that reusing a session records
// TMUX_CLI_FLAGS (newline-joined) when flags are non-empty, and is a no-op
// (never writes the env) when flags are empty — mirroring
// applyModelToExistingSession.
func TestApplyFlagsToExistingSession(t *testing.T) {
	t.Run("non-empty flags record TMUX_CLI_FLAGS", func(t *testing.T) {
		mockExec := new(testutil.MockTmuxExecutor)
		mockExec.On("SetSessionEnvironment", "sess-1", "TMUX_CLI_FLAGS", "--chrome\n--verbose").Return(nil)

		applyFlagsToExistingSession(mockExec, "sess-1", []string{"--chrome", "--verbose"})

		mockExec.AssertCalled(t, "SetSessionEnvironment", "sess-1", "TMUX_CLI_FLAGS", "--chrome\n--verbose")
	})

	t.Run("empty flags are a no-op", func(t *testing.T) {
		mockExec := new(testutil.MockTmuxExecutor)

		applyFlagsToExistingSession(mockExec, "sess-1", nil)
		applyFlagsToExistingSession(mockExec, "sess-1", []string{})

		mockExec.AssertNotCalled(t, "SetSessionEnvironment", "sess-1", "TMUX_CLI_FLAGS", mock.Anything)
	})
}

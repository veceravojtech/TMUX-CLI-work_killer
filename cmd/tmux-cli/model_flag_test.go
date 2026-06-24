package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartCmd_HasModelFlag pins the --model flag on `start` (mirrors --sudo). The
// flag records TMUX_CLI_MODEL in the session environment and injects --model into
// claude launches.
func TestStartCmd_HasModelFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("model")
	require.NotNil(t, flag, "--model flag should exist on start command")
	assert.Equal(t, "string", flag.Value.Type())
	assert.Equal(t, "", flag.DefValue, "--model defaults to empty (inherit Claude's default model)")
}

// TestStartAttachCmd_HasModelFlag pins the --model flag on `start-attach`.
func TestStartAttachCmd_HasModelFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("model")
	require.NotNil(t, flag, "--model flag should exist on start-attach command")
	assert.Equal(t, "string", flag.Value.Type())
	assert.Equal(t, "", flag.DefValue, "--model defaults to empty (inherit Claude's default model)")
}

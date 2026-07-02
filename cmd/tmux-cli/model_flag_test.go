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

// TestE2EBootstrapCmd_ModelDefaultsToOpus48 pins the e2e-evaluator self-test
// harness to the STRONG model: unlike `start` (which inherits the account
// default), e2e-bootstrap must default the target session + every daemon
// window/worker to Opus 4.8, never a cheaper account-default (e.g. Fable 5). The
// value threads through `tmux-cli start --model` → TMUX_CLI_MODEL → all workers.
func TestE2EBootstrapCmd_ModelDefaultsToOpus48(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"e2e-bootstrap"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("model")
	require.NotNil(t, flag, "--model flag should exist on e2e-bootstrap command")
	assert.Equal(t, "string", flag.Value.Type())
	assert.Equal(t, "claude-opus-4-8", flag.DefValue,
		"e2e-bootstrap must default the test instance to Opus 4.8, not the account default")
}

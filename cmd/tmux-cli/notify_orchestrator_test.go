package main

import (
	"errors"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNotifyOrchestratorCmd_Exists verifies the command is registered with the
// expected positional-arg contract (cobra.ExactArgs(1)).
func TestNotifyOrchestratorCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"notify-orchestrator"})
	require.NoError(t, err, "notify-orchestrator command should be registered")
	require.NotNil(t, cmd)
	assert.Equal(t, "notify-orchestrator <message>", cmd.Use)
	// ExactArgs(1): zero args must be rejected, one accepted.
	assert.Error(t, cmd.Args(cmd, []string{}), "zero args must be rejected")
	assert.NoError(t, cmd.Args(cmd, []string{"hello"}), "exactly one arg is valid")
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}), "two args must be rejected")
}

// TestNotifyOrchestrator_SendsTextThenEnter verifies the testable core delivers
// the message to the resolved pane via NotifyPane exactly once.
func TestNotifyOrchestrator_SendsTextThenEnter(t *testing.T) {
	m := &testutil.MockTmuxExecutor{}
	m.On("NotifyPane", "%3", "hello").Return(nil)

	err := notifyOrchestrator(m, "%3", "hello")

	require.NoError(t, err)
	m.AssertCalled(t, "NotifyPane", "%3", "hello")
	m.AssertNumberOfCalls(t, "NotifyPane", 1)
}

// TestNotifyOrchestrator_MissingEnv_FailsLoud verifies an empty pane id yields a
// UsageError naming the env var and never calls NotifyPane.
func TestNotifyOrchestrator_MissingEnv_FailsLoud(t *testing.T) {
	m := &testutil.MockTmuxExecutor{}
	// No .On — any call would panic, asserting NotifyPane is NOT invoked.

	err := notifyOrchestrator(m, "", "hello")

	require.Error(t, err)
	var usageErr UsageError
	assert.True(t, errors.As(err, &usageErr), "missing pane must be a UsageError (exit 2)")
	assert.Contains(t, err.Error(), "TMUX_CLI_ORCHESTRATOR_PANE")
	m.AssertNotCalled(t, "NotifyPane")
}

// TestNotifyOrchestrator_WrapperTrimsEnv verifies the RunE wrapper TrimSpaces the
// env var: a whitespace-only TMUX_CLI_ORCHESTRATOR_PANE resolves to empty and the
// command fails loudly BEFORE touching tmux (so this stays tmux-free).
func TestNotifyOrchestrator_WrapperTrimsEnv(t *testing.T) {
	t.Setenv("TMUX_CLI_ORCHESTRATOR_PANE", "   ")

	err := runNotifyOrchestrator(notifyOrchestratorCmd, []string{"hello"})

	require.Error(t, err)
	var usageErr UsageError
	assert.True(t, errors.As(err, &usageErr), "whitespace pane must resolve to a UsageError")
	assert.Contains(t, err.Error(), "TMUX_CLI_ORCHESTRATOR_PANE")
}

// TestNotifyOrchestrator_WrapperMissingEnv verifies the wrapper rejects a fully
// unset env var the same way — loud UsageError, no tmux contact.
func TestNotifyOrchestrator_WrapperMissingEnv(t *testing.T) {
	t.Setenv("TMUX_CLI_ORCHESTRATOR_PANE", "")

	err := runNotifyOrchestrator(notifyOrchestratorCmd, []string{"hello"})

	require.Error(t, err)
	var usageErr UsageError
	assert.True(t, errors.As(err, &usageErr))
	assert.Contains(t, err.Error(), "TMUX_CLI_ORCHESTRATOR_PANE")
}

// TestNotifyOrchestrator_EmptyMessage_Delivered verifies an empty message is a
// valid bare-Enter heartbeat ping and is forwarded to NotifyPane.
func TestNotifyOrchestrator_EmptyMessage_Delivered(t *testing.T) {
	m := &testutil.MockTmuxExecutor{}
	m.On("NotifyPane", "%1", "").Return(nil)

	err := notifyOrchestrator(m, "%1", "")

	require.NoError(t, err)
	m.AssertCalled(t, "NotifyPane", "%1", "")
}

// TestNotifyOrchestrator_PropagatesExecutorError verifies a NotifyPane failure is
// wrapped and surfaced (so dead-pane / tmux-missing errors reach the exit-code path).
func TestNotifyOrchestrator_PropagatesExecutorError(t *testing.T) {
	m := &testutil.MockTmuxExecutor{}
	sentinel := errors.New("send-keys failed")
	m.On("NotifyPane", "%9", "x").Return(sentinel)

	err := notifyOrchestrator(m, "%9", "x")

	require.Error(t, err)
	assert.True(t, errors.Is(err, sentinel), "underlying executor error must be wrapped with %%w")
}

// compile-time guard: notifyOrchestrator accepts the cobra RunE shape's executor.
var _ = func(cmd *cobra.Command, args []string) error { return runNotifyOrchestrator(cmd, args) }

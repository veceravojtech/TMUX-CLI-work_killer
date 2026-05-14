package sudo

import (
	"testing"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

func TestResolveTimeout_InputOverride(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, setup.SaveSettings(root, &setup.Settings{Sudo: setup.SudoSettings{Timeout: 45}}))

	result := ResolveTimeout(intPtr(60), root)
	assert.Equal(t, 60, result)
}

func TestResolveTimeout_ExplicitZero_Unlimited(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, setup.SaveSettings(root, &setup.Settings{Sudo: setup.SudoSettings{Timeout: 45}}))

	result := ResolveTimeout(intPtr(0), root)
	assert.Equal(t, 0, result, "explicit 0 must mean unlimited (no timeout)")
}

func TestResolveTimeout_NilFallsToConfig(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, setup.SaveSettings(root, &setup.Settings{Sudo: setup.SudoSettings{Timeout: 45}}))

	result := ResolveTimeout(nil, root)
	assert.Equal(t, 45, result)
}

func TestResolveTimeout_ConfigZero_Unlimited(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, setup.SaveSettings(root, &setup.Settings{Sudo: setup.SudoSettings{Timeout: 0}}))

	result := ResolveTimeout(nil, root)
	assert.Equal(t, 0, result, "config timeout=0 must mean unlimited")
}

func TestResolveTimeout_DefaultFallback(t *testing.T) {
	root := t.TempDir()

	result := ResolveTimeout(nil, root)
	assert.Equal(t, DefaultTimeout, result)
}

func TestResolveTimeout_NoConfigFile(t *testing.T) {
	root := t.TempDir()

	result := ResolveTimeout(nil, root)
	assert.Equal(t, DefaultTimeout, result)
}

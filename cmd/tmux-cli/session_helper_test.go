package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/tmux"
)

// TestResolveWindowIdentifier_CLI_WithWindowID tests that window IDs are returned as-is
func TestResolveWindowIdentifier_CLI_WithWindowID(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "bmad-worker"},
	}

	tests := []struct {
		name       string
		identifier string
		expected   string
	}{
		{"ID @0", "@0", "@0"},
		{"ID @1", "@1", "@1"},
		{"ID @99", "@99", "@99"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveWindowIdentifier(windows, tt.identifier)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestResolveWindowIdentifier_CLI_WithWindowName tests that window names are resolved to IDs
func TestResolveWindowIdentifier_CLI_WithWindowName(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "bmad-worker"},
		{TmuxWindowID: "@2", Name: "dev-server"},
	}

	tests := []struct {
		name       string
		identifier string
		expected   string
	}{
		{"Name supervisor", "supervisor", "@0"},
		{"Name bmad-worker", "bmad-worker", "@1"},
		{"Name dev-server", "dev-server", "@2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveWindowIdentifier(windows, tt.identifier)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestResolveWindowIdentifier_CLI_WithInvalidName tests error handling for invalid names
func TestResolveWindowIdentifier_CLI_WithInvalidName(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "bmad-worker"},
	}

	result, err := ResolveWindowIdentifier(windows, "invalid-name")
	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Contains(t, err.Error(), "window name \"invalid-name\" not found")
	assert.Contains(t, err.Error(), "supervisor")
	assert.Contains(t, err.Error(), "bmad-worker")
}

// TestResolveWindowIdentifier_CLI_CaseSensitive tests case-sensitive matching
func TestResolveWindowIdentifier_CLI_CaseSensitive(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}

	tests := []struct {
		name        string
		identifier  string
		shouldError bool
	}{
		{"Exact match", "supervisor", false},
		{"Wrong case - uppercase", "Supervisor", true},
		{"Wrong case - all caps", "SUPERVISOR", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveWindowIdentifier(windows, tt.identifier)
			if tt.shouldError {
				require.Error(t, err)
				assert.Equal(t, "", result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "@0", result)
			}
		})
	}
}

// TestResolveWindowIdentifier_CLI_EmptyIdentifier tests error for empty identifier
func TestResolveWindowIdentifier_CLI_EmptyIdentifier(t *testing.T) {
	windows := []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}

	result, err := ResolveWindowIdentifier(windows, "")
	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Contains(t, err.Error(), "window identifier cannot be empty")
}

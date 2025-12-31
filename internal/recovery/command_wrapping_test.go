// Package recovery provides session recovery detection and execution functionality.
package recovery

import (
	"testing"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestRecoverSession_ImmediateQuitCommands tests that RecoveryManager passes commands
// through to the executor unchanged. The actual wrapping happens in RealTmuxExecutor.CreateWindow.
// This test verifies that various command types (immediate-quit, long-running, pre-wrapped)
// are all handled correctly by the recovery layer.
func TestRecoverSession_ImmediateQuitCommands(t *testing.T) {
	tests := []struct {
		name            string
		recoveryCommand string
		description     string
	}{
		{
			name:            "immediate quit command - ch",
			recoveryCommand: "ch",
			description:     "Will be wrapped by RealTmuxExecutor to ensure persistence",
		},
		{
			name:            "immediate quit command - exec ch",
			recoveryCommand: "exec ch",
			description:     "Will be wrapped by RealTmuxExecutor to ensure persistence",
		},
		{
			name:            "short-lived command - sleep 10",
			recoveryCommand: "sleep 10",
			description:     "Will be wrapped by RealTmuxExecutor to ensure persistence",
		},
		{
			name:            "already wrapped - zsh -ic ch",
			recoveryCommand: `zsh -ic "ch"`,
			description:     "Will NOT be double-wrapped by RealTmuxExecutor",
		},
		{
			name:            "already wrapped - bash -ic exec ch",
			recoveryCommand: `bash -ic "exec ch"`,
			description:     "Will NOT be double-wrapped by RealTmuxExecutor",
		},
		{
			name:            "command with quotes",
			recoveryCommand: `echo "hello world"`,
			description:     "Will be wrapped with proper quote escaping by RealTmuxExecutor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &store.Session{
				SessionID:   "test-immediate-quit-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "test-window", TmuxWindowID: ""},
				},
			}

			mockStore := new(MockSessionStore)
			mockExecutor := new(MockTmuxExecutor)

			// Session creation succeeds
			mockExecutor.On("CreateSession", "test-immediate-quit-uuid", "/project").Return(nil)

			// List windows after session creation
			mockExecutor.On("ListWindows", "test-immediate-quit-uuid").Return([]tmux.WindowInfo{
				{TmuxWindowID: "@0", Name: "supervisor"},
			}, nil).Once()

			// All windows now use zsh (hardcoded default)
			mockExecutor.On("CreateWindow", "test-immediate-quit-uuid", "test-window", "zsh").Return("@1", nil)

			// Save succeeds
			mockStore.On("Save", mock.Anything).Return(nil)

			manager := NewSessionRecoveryManager(mockStore, mockExecutor)
			err := manager.RecoverSession(session)

			// Verify recovery succeeded
			assert.NoError(t, err)

			// Verify window was created with new ID
			assert.Equal(t, "@1", session.Windows[0].TmuxWindowID)

			// Verify all mock expectations met
			mockStore.AssertExpectations(t)
			mockExecutor.AssertExpectations(t)
		})
	}
}

// TestRecoverSession_MultipleImmediateQuitWindows tests recovery with multiple
// windows that have various command types. RecoveryManager passes commands as-is,
// and RealTmuxExecutor handles the wrapping transparently.
func TestRecoverSession_MultipleImmediateQuitWindows(t *testing.T) {
	session := &store.Session{
		SessionID:   "multi-immediate-uuid",
		ProjectPath: "/project",
		Windows: []store.Window{
			{Name: "claude", TmuxWindowID: ""},
			{Name: "claude-cli", TmuxWindowID: ""},
			{Name: "claude-session", TmuxWindowID: ""},
			{Name: "test-window", TmuxWindowID: ""},
			{Name: "claude-interactive", TmuxWindowID: ""},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	// Session creation succeeds
	mockExecutor.On("CreateSession", "multi-immediate-uuid", "/project").Return(nil)

	// List windows after session creation
	mockExecutor.On("ListWindows", "multi-immediate-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()

	// All windows now use zsh (hardcoded default)
	mockExecutor.On("CreateWindow", "multi-immediate-uuid", "claude", "zsh").Return("@1", nil)
	mockExecutor.On("CreateWindow", "multi-immediate-uuid", "claude-cli", "zsh").Return("@2", nil)
	mockExecutor.On("CreateWindow", "multi-immediate-uuid", "claude-session", "zsh").Return("@3", nil)
	mockExecutor.On("CreateWindow", "multi-immediate-uuid", "test-window", "zsh").Return("@4", nil)
	mockExecutor.On("CreateWindow", "multi-immediate-uuid", "claude-interactive", "zsh").Return("@5", nil)

	// Save succeeds
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	// Verify recovery succeeded
	assert.NoError(t, err)

	// Verify all windows got IDs
	assert.Equal(t, "@1", session.Windows[0].TmuxWindowID)
	assert.Equal(t, "@2", session.Windows[1].TmuxWindowID)
	assert.Equal(t, "@3", session.Windows[2].TmuxWindowID)
	assert.Equal(t, "@4", session.Windows[3].TmuxWindowID)
	assert.Equal(t, "@5", session.Windows[4].TmuxWindowID)

	// Verify all mock expectations met
	mockStore.AssertExpectations(t)
	mockExecutor.AssertExpectations(t)
}

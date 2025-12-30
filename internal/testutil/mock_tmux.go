// Package testutil provides test utilities and mock implementations.
// It includes mock interfaces for testing without external dependencies.
package testutil

import (
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/mock"
)

// MockTmuxExecutor is a mock implementation of the TmuxExecutor interface
// using testify/mock for structured testing.
type MockTmuxExecutor struct {
	mock.Mock
}

// CreateSession mocks creating a new tmux session
func (m *MockTmuxExecutor) CreateSession(id, path string) error {
	args := m.Called(id, path)
	return args.Error(0)
}

// KillSession mocks terminating a tmux session
func (m *MockTmuxExecutor) KillSession(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

// HasSession mocks checking if a session exists
func (m *MockTmuxExecutor) HasSession(id string) (bool, error) {
	args := m.Called(id)
	return args.Bool(0), args.Error(1)
}

// ListSessions mocks listing all active sessions
func (m *MockTmuxExecutor) ListSessions() ([]string, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// CreateWindow mocks creating a new window in a session with command.
// Returns the tmux-assigned window ID or error.
func (m *MockTmuxExecutor) CreateWindow(sessionID, name, command string) (string, error) {
	args := m.Called(sessionID, name, command)
	return args.String(0), args.Error(1)
}

// ListWindows mocks listing windows in a session with metadata
func (m *MockTmuxExecutor) ListWindows(sessionID string) ([]tmux.WindowInfo, error) {
	args := m.Called(sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]tmux.WindowInfo), args.Error(1)
}

// SendMessage mocks sending a text message to a window
func (m *MockTmuxExecutor) SendMessage(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

// KillWindow mocks killing a window in a session
func (m *MockTmuxExecutor) KillWindow(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

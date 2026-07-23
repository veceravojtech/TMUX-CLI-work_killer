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

// CreateWindowInDir mocks creating a new window with an explicit start directory.
// It mirrors RealTmuxExecutor.CreateWindowInDir (a concrete method NOT on the
// TmuxExecutor interface) so cwd-aware callers — windows-spawn-worker's optional
// workingDirectory (E1-1c) — can type-assert to it in tests.
func (m *MockTmuxExecutor) CreateWindowInDir(sessionID, name, command, cwd string) (string, error) {
	args := m.Called(sessionID, name, command, cwd)
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

// SendEnter mocks sending a bare Enter keystroke to a window
func (m *MockTmuxExecutor) SendEnter(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

// SendMessage mocks sending a text message to a window
func (m *MockTmuxExecutor) SendMessage(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

// NotifyPane mocks delivering a message + separate Enter directly to a pane id
func (m *MockTmuxExecutor) NotifyPane(paneID, message string) error {
	args := m.Called(paneID, message)
	return args.Error(0)
}

// SendMessageWithDelay mocks sending a text message with a 1-second delay
func (m *MockTmuxExecutor) SendMessageWithDelay(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

// KillWindow mocks killing a window in a session
func (m *MockTmuxExecutor) KillWindow(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

// InterruptWindow mocks interrupting a window's running process without destroying the window
func (m *MockTmuxExecutor) InterruptWindow(windowID string) error {
	args := m.Called(windowID)
	return args.Error(0)
}

// TerminateWindowProcess mocks terminating a window's foreground child process without destroying the window
func (m *MockTmuxExecutor) TerminateWindowProcess(windowID string) error {
	args := m.Called(windowID)
	return args.Error(0)
}

// SetWindowOption mocks setting a user-defined window option
func (m *MockTmuxExecutor) SetWindowOption(sessionID, windowID, optionName, value string) error {
	args := m.Called(sessionID, windowID, optionName, value)
	return args.Error(0)
}

// GetWindowOption mocks retrieving a user-defined window option
func (m *MockTmuxExecutor) GetWindowOption(sessionID, windowID, optionName string) (string, error) {
	args := m.Called(sessionID, windowID, optionName)
	return args.String(0), args.Error(1)
}

// CaptureWindowOutput mocks capturing pane output
func (m *MockTmuxExecutor) CaptureWindowOutput(sessionID, windowID string) (string, error) {
	args := m.Called(sessionID, windowID)
	return args.String(0), args.Error(1)
}

// SendMessageWithFeedback mocks sending message with feedback
func (m *MockTmuxExecutor) SendMessageWithFeedback(sessionID, windowID, message string) (string, error) {
	args := m.Called(sessionID, windowID, message)
	return args.String(0), args.Error(1)
}

// SetSessionEnvironment mocks setting a session environment variable
func (m *MockTmuxExecutor) SetSessionEnvironment(sessionID, key, value string) error {
	args := m.Called(sessionID, key, value)
	return args.Error(0)
}

// GetSessionEnvironment mocks reading a session environment variable
func (m *MockTmuxExecutor) GetSessionEnvironment(sessionID, key string) (string, error) {
	args := m.Called(sessionID, key)
	return args.String(0), args.Error(1)
}

// FindSessionByEnvironment mocks finding a session by environment variable
func (m *MockTmuxExecutor) FindSessionByEnvironment(key, value string) (string, error) {
	args := m.Called(key, value)
	return args.String(0), args.Error(1)
}

// AttachSession mocks attaching to a tmux session
func (m *MockTmuxExecutor) AttachSession(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

// PipePane mocks starting pipe-pane output streaming
func (m *MockTmuxExecutor) PipePane(sessionID, windowID, logPath string) error {
	args := m.Called(sessionID, windowID, logPath)
	return args.Error(0)
}

// PipePaneCommand mocks starting pipe-pane streaming through a shell command
func (m *MockTmuxExecutor) PipePaneCommand(sessionID, windowID, command string) error {
	args := m.Called(sessionID, windowID, command)
	return args.Error(0)
}

// ClosePipePane mocks closing an active pipe-pane
func (m *MockTmuxExecutor) ClosePipePane(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

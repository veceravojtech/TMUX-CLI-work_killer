package session

import (
	"errors"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockTmuxExecutor is a mock implementation for testing
type MockTmuxExecutor struct {
	mock.Mock
}

func (m *MockTmuxExecutor) CreateSession(id, path string) error {
	args := m.Called(id, path)
	return args.Error(0)
}

func (m *MockTmuxExecutor) KillSession(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockTmuxExecutor) HasSession(id string) (bool, error) {
	args := m.Called(id)
	return args.Bool(0), args.Error(1)
}

func (m *MockTmuxExecutor) ListSessions() ([]string, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockTmuxExecutor) CreateWindow(sessionID, name, command string) (string, error) {
	args := m.Called(sessionID, name, command)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) ListWindows(sessionID string) ([]tmux.WindowInfo, error) {
	args := m.Called(sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]tmux.WindowInfo), args.Error(1)
}

func (m *MockTmuxExecutor) SendMessage(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

func (m *MockTmuxExecutor) SendMessageWithDelay(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

func (m *MockTmuxExecutor) KillWindow(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

func (m *MockTmuxExecutor) SetWindowOption(sessionID, windowID, optionName, value string) error {
	args := m.Called(sessionID, windowID, optionName, value)
	return args.Error(0)
}

func (m *MockTmuxExecutor) GetWindowOption(sessionID, windowID, optionName string) (string, error) {
	args := m.Called(sessionID, windowID, optionName)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) CaptureWindowOutput(sessionID, windowID string) (string, error) {
	args := m.Called(sessionID, windowID)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) SendMessageWithFeedback(sessionID, windowID, message string) (string, error) {
	args := m.Called(sessionID, windowID, message)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) SetSessionEnvironment(sessionID, key, value string) error {
	args := m.Called(sessionID, key, value)
	return args.Error(0)
}

func (m *MockTmuxExecutor) GetSessionEnvironment(sessionID, key string) (string, error) {
	args := m.Called(sessionID, key)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) FindSessionByEnvironment(key, value string) (string, error) {
	args := m.Called(key, value)
	return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) AttachSession(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

// TestNewSessionManager_ReturnsInstance verifies constructor works
func TestNewSessionManager_ReturnsInstance(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	manager := NewSessionManager(mockExec)
	require.NotNil(t, manager)
}

// TestSessionManager_CreateSession_Success tests successful session creation
func TestSessionManager_CreateSession_Success(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("HasSession", "test-id").Return(false, nil)
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-id").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", Running: true},
	}, nil)
	mockExec.On("SetWindowOption", "test-id", "@0", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-id", "@0", mock.Anything).Return("", nil)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/tmp")

	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
	mockExec.AssertCalled(t, "SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp")
}

// TestSessionManager_CreateSession_PathNotExist tests error when path doesn't exist
func TestSessionManager_CreateSession_PathNotExist(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/nonexistent-path-12345")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	mockExec.AssertNotCalled(t, "CreateSession")
}

// TestSessionManager_CreateSession_SessionAlreadyExists tests error when session exists
func TestSessionManager_CreateSession_SessionAlreadyExists(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("HasSession", "existing-id").Return(true, nil)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("existing-id", "/tmp")

	assert.Error(t, err)
	assert.ErrorIs(t, err, tmux.ErrSessionAlreadyExists)
}

// TestSessionManager_CreateSession_TmuxNotFound tests error when tmux is not installed
func TestSessionManager_CreateSession_TmuxNotFound(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("HasSession", "test-id").Return(false, tmux.ErrTmuxNotFound)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/tmp")

	assert.Error(t, err)
	assert.ErrorIs(t, err, tmux.ErrTmuxNotFound)
}

// TestSessionManager_CreateSession_TmuxCreateFails tests error when tmux command fails
func TestSessionManager_CreateSession_TmuxCreateFails(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("HasSession", "test-id").Return(false, nil)
	mockExec.On("CreateSession", "test-id", "/tmp").Return(errors.New("tmux error"))

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create tmux session")
}

// TestSessionManager_CreateSession_ListWindowsFails tests cleanup when ListWindows fails
func TestSessionManager_CreateSession_ListWindowsFails(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("HasSession", "test-id").Return(false, nil)
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-id").Return(nil, errors.New("failed to list windows"))
	mockExec.On("KillSession", "test-id").Return(nil)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list windows")
	mockExec.AssertCalled(t, "KillSession", "test-id")
}

// TestSessionManager_KillSession_Success tests successful kill
func TestSessionManager_KillSession_Success(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("KillSession", "test-id").Return(nil)

	manager := NewSessionManager(mockExec)
	err := manager.KillSession("test-id")

	assert.NoError(t, err)
}

// TestSessionManager_KillSession_EmptyID tests error for empty ID
func TestSessionManager_KillSession_EmptyID(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	manager := NewSessionManager(mockExec)
	err := manager.KillSession("")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session ID is required")
}

// TestSessionManager_KillSession_Idempotent tests killing already-dead session
func TestSessionManager_KillSession_Idempotent(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("KillSession", "test-id").Return(errors.New("session not found"))

	manager := NewSessionManager(mockExec)
	err := manager.KillSession("test-id")

	// Should not error - kill is idempotent
	assert.NoError(t, err)
}

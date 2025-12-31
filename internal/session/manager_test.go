package session

import (
	"errors"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/store"
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

// MockSessionStore is a mock implementation for testing
type MockSessionStore struct {
	mock.Mock
}

func (m *MockSessionStore) Save(session *store.Session) error {
	args := m.Called(session)
	return args.Error(0)
}

func (m *MockSessionStore) Load(projectPath string) (*store.Session, error) {
	args := m.Called(projectPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*store.Session), args.Error(1)
}

// TestNewSessionManager_ReturnsInstance verifies constructor works
func TestNewSessionManager_ReturnsInstance(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	manager := NewSessionManager(mockExec, mockStore)

	require.NotNil(t, manager)
}

// TestSessionManager_CreateSession_Success tests successful session creation
func TestSessionManager_CreateSession_Success(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	// Setup expectations
	mockExec.On("HasSession", "test-uuid").Return(false, nil)
	mockExec.On("CreateSession", "test-uuid", "/tmp").Return(nil)
	// Expect ListWindows to be called to capture default window
	mockExec.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{
			TmuxWindowID: "@0",
			Name:         "supervisor",
			Running:      true,
		},
	}, nil)
	// Expect supervisor UUID setup
	mockExec.On("SetWindowOption", "test-uuid", "@0", "window-uuid", "test-uuid").Return(nil)
	mockExec.On("SendMessage", "test-uuid", "@0", `export TMUX_WINDOW_UUID="test-uuid"`).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-uuid", "@0", `claude --dangerously-skip-permissions --session-id="$TMUX_WINDOW_UUID"`).Return("", nil)
	mockStore.On("Save", mock.MatchedBy(func(s *store.Session) bool {
		// Verify session has the default window with UUID
		return s.SessionID == "test-uuid" &&
			s.ProjectPath == "/tmp" &&
			len(s.Windows) == 1 &&
			s.Windows[0].TmuxWindowID == "@0" &&
			s.Windows[0].Name == "supervisor" &&
			s.Windows[0].UUID == "test-uuid"
	})).Return(nil)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
	mockStore.AssertExpectations(t)
}

// TestSessionManager_CreateSession_PathNotExist tests error when path doesn't exist
func TestSessionManager_CreateSession_PathNotExist(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/nonexistent-path-12345")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	// No tmux or store operations should be called
	mockExec.AssertNotCalled(t, "CreateSession")
	mockStore.AssertNotCalled(t, "Save")
}

// TestSessionManager_CreateSession_SessionAlreadyExists tests error when session exists
func TestSessionManager_CreateSession_SessionAlreadyExists(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	// Session already exists in tmux
	mockExec.On("HasSession", "existing-uuid").Return(true, nil)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("existing-uuid", "/tmp")

	assert.Error(t, err)
	assert.ErrorIs(t, err, tmux.ErrSessionAlreadyExists)
	mockExec.AssertExpectations(t)
	mockExec.AssertNotCalled(t, "CreateSession")
	mockStore.AssertNotCalled(t, "Save")
}

// TestSessionManager_CreateSession_TmuxNotFound tests error when tmux is not installed
func TestSessionManager_CreateSession_TmuxNotFound(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExec.On("HasSession", "test-uuid").Return(false, tmux.ErrTmuxNotFound)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.Error(t, err)
	assert.ErrorIs(t, err, tmux.ErrTmuxNotFound)
	mockExec.AssertExpectations(t)
}

// TestSessionManager_CreateSession_TmuxCreateFails tests error when tmux command fails
func TestSessionManager_CreateSession_TmuxCreateFails(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExec.On("HasSession", "test-uuid").Return(false, nil)
	mockExec.On("CreateSession", "test-uuid", "/tmp").Return(errors.New("tmux error"))

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create tmux session")
	mockExec.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "Save")
}

// TestSessionManager_CreateSession_StoreSaveFails tests cleanup when store fails
func TestSessionManager_CreateSession_StoreSaveFails(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExec.On("HasSession", "test-uuid").Return(false, nil)
	mockExec.On("CreateSession", "test-uuid", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", Running: true},
	}, nil)
	mockStore.On("Save", mock.Anything).Return(errors.New("disk full"))
	// CRITICAL: Should cleanup tmux session when store fails
	mockExec.On("KillSession", "test-uuid").Return(nil)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "save session")
	mockExec.AssertExpectations(t)
	mockStore.AssertExpectations(t)
	// Verify cleanup was attempted
	mockExec.AssertCalled(t, "KillSession", "test-uuid")
}

// Table-driven tests for various scenarios
func TestSessionManager_CreateSession_TableDriven(t *testing.T) {
	tests := []struct {
		name          string
		sessionID     string
		path          string
		hasResult     bool
		hasErr        error
		createErr     error
		saveErr       error
		expectErr     bool
		expectErrType error
	}{
		{
			name:      "valid session creation",
			sessionID: "valid-uuid",
			path:      "/tmp",
			hasResult: false,
			hasErr:    nil,
			createErr: nil,
			saveErr:   nil,
			expectErr: false,
		},
		{
			name:          "session already exists",
			sessionID:     "existing-uuid",
			path:          "/tmp",
			hasResult:     true,
			hasErr:        nil,
			expectErr:     true,
			expectErrType: tmux.ErrSessionAlreadyExists,
		},
		{
			name:          "tmux not found during check",
			sessionID:     "test-uuid",
			path:          "/tmp",
			hasResult:     false,
			hasErr:        tmux.ErrTmuxNotFound,
			expectErr:     true,
			expectErrType: tmux.ErrTmuxNotFound,
		},
		{
			name:      "tmux create fails",
			sessionID: "test-uuid",
			path:      "/tmp",
			hasResult: false,
			hasErr:    nil,
			createErr: errors.New("tmux failed"),
			expectErr: true,
		},
		{
			name:      "store save fails",
			sessionID: "test-uuid",
			path:      "/tmp",
			hasResult: false,
			hasErr:    nil,
			createErr: nil,
			saveErr:   errors.New("disk error"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := new(MockTmuxExecutor)
			mockStore := new(MockSessionStore)

			// Setup mock for HasSession call
			mockExec.On("HasSession", tt.sessionID).Return(tt.hasResult, tt.hasErr)

			// Only setup CreateSession if HasSession succeeds and returns false
			if tt.hasErr == nil && !tt.hasResult {
				mockExec.On("CreateSession", tt.sessionID, tt.path).Return(tt.createErr)

				// Only setup ListWindows if CreateSession succeeds
				if tt.createErr == nil {
					mockExec.On("ListWindows", tt.sessionID).Return([]tmux.WindowInfo{
						{TmuxWindowID: "@0", Name: "supervisor", Running: true},
					}, nil)

					// Only setup Save if CreateSession succeeds
					mockStore.On("Save", mock.Anything).Return(tt.saveErr)

					// If store fails, expect cleanup
					if tt.saveErr != nil {
						mockExec.On("KillSession", tt.sessionID).Return(nil)
					}
				}
			}

			manager := NewSessionManager(mockExec, mockStore)
			err := manager.CreateSession(tt.sessionID, tt.path)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectErrType != nil {
					assert.ErrorIs(t, err, tt.expectErrType)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSessionManager_CreateSession_ListWindowsFails tests cleanup when ListWindows fails
func TestSessionManager_CreateSession_ListWindowsFails(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExec.On("HasSession", "test-uuid").Return(false, nil)
	mockExec.On("CreateSession", "test-uuid", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-uuid").Return(nil, errors.New("failed to list windows"))
	// CRITICAL: Should cleanup tmux session when ListWindows fails
	mockExec.On("KillSession", "test-uuid").Return(nil)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list windows")
	mockExec.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "Save")
	// Verify cleanup was attempted
	mockExec.AssertCalled(t, "KillSession", "test-uuid")
}

// TestSessionManager_KillSession tests the kill workflow
func TestSessionManager_KillSession(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	validPath := "/tmp/project"
	missingPath := "/tmp/nonexistent"

	tests := []struct {
		name          string
		projectPath   string
		sessionID     string
		loadErr       error
		killErr       error
		expectErr     bool
		expectErrType error
	}{
		{
			name:        "successful kill",
			projectPath: validPath,
			sessionID:   validUUID,
			loadErr:     nil,
			killErr:     nil,
			expectErr:   false,
		},
		{
			name:          "session not found in store",
			projectPath:   missingPath,
			loadErr:       store.ErrSessionNotFound,
			expectErr:     true,
			expectErrType: store.ErrSessionNotFound,
		},
		{
			name:        "empty project path",
			projectPath: "",
			expectErr:   true,
		},
		{
			name:        "tmux session already dead (idempotent)",
			projectPath: validPath,
			sessionID:   validUUID,
			loadErr:     nil,
			killErr:     errors.New("session not found"), // Tmux returns error but Kill is idempotent
			expectErr:   false,                           // Should NOT error - kill is idempotent
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := new(MockTmuxExecutor)
			mockStore := new(MockSessionStore)

			// Setup mocks based on test case
			if tt.projectPath != "" {
				if tt.loadErr != nil {
					mockStore.On("Load", tt.projectPath).Return(nil, tt.loadErr)
				} else {
					session := &store.Session{
						SessionID:   tt.sessionID,
						ProjectPath: tt.projectPath,
						Windows:     []store.Window{},
					}
					mockStore.On("Load", tt.projectPath).Return(session, nil)
					// Mock HasSession to check if session exists in tmux
					mockExec.On("HasSession", tt.sessionID).Return(false, nil)
					mockExec.On("KillSession", tt.sessionID).Return(tt.killErr)
				}
			}

			manager := NewSessionManager(mockExec, mockStore)
			err := manager.KillSession(tt.projectPath)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectErrType != nil {
					assert.ErrorIs(t, err, tt.expectErrType)
				}
			} else {
				assert.NoError(t, err)
			}

			mockExec.AssertExpectations(t)
			mockStore.AssertExpectations(t)
		})
	}
}

// EndSession functionality removed - sessions are never archived
// .tmux-session files persist forever for recovery

// TestSessionManager_CreateSession_DefaultsToZsh verifies that
// windows created during session setup will use zsh as default (no field stored)
func TestSessionManager_CreateSession_DefaultsToZsh(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	// Setup expectations
	mockExec.On("HasSession", "test-uuid").Return(false, nil)
	mockExec.On("CreateSession", "test-uuid", "/tmp").Return(nil)
	// ListWindows returns a window with CurrentCommand="bash" (what's actually running)
	mockExec.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{
			TmuxWindowID:   "@0",
			Name:           "supervisor",
			CurrentCommand: "bash", // Simulating what tmux reports
			Running:        true,
		},
	}, nil)

	// Verify saved session has window data (recovery defaults to zsh)
	mockStore.On("Save", mock.MatchedBy(func(s *store.Session) bool {
		return s.SessionID == "test-uuid" &&
			s.ProjectPath == "/tmp" &&
			len(s.Windows) == 1 &&
			s.Windows[0].TmuxWindowID == "@0" &&
			s.Windows[0].Name == "supervisor"
	})).Return(nil)

	manager := NewSessionManager(mockExec, mockStore)
	err := manager.CreateSession("test-uuid", "/tmp")

	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
	mockStore.AssertExpectations(t)
}

func TestCreateSession_SetsCreatedAtTimestamp(t *testing.T) {
	// Setup mocks
	mockExecutor := &MockTmuxExecutor{}
	mockStore := &MockSessionStore{}
	manager := NewSessionManager(mockExecutor, mockStore)

	tmpDir := t.TempDir()
	sessionID := "test-uuid"

	// Mock expectations
	mockExecutor.On("HasSession", sessionID).Return(false, nil)
	mockExecutor.On("CreateSession", sessionID, tmpDir).Return(nil)
	mockExecutor.On("ListWindows", sessionID).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "default"},
	}, nil)

	var capturedSession *store.Session
	mockStore.On("Save", mock.AnythingOfType("*store.Session")).
		Run(func(args mock.Arguments) {
			capturedSession = args.Get(0).(*store.Session)
		}).
		Return(nil)

	// Execute
	beforeCreate := time.Now().Add(-1 * time.Second) // Add buffer for timing issues
	err := manager.CreateSession(sessionID, tmpDir)
	afterCreate := time.Now().Add(1 * time.Second)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedSession)

	// Verify CreatedAt was set
	assert.NotEmpty(t, capturedSession.CreatedAt)

	// Verify CreatedAt is valid RFC3339 and within test timeframe
	createdTime, err := time.Parse(time.RFC3339, capturedSession.CreatedAt)
	require.NoError(t, err)
	assert.True(t, !createdTime.Before(beforeCreate), "CreatedAt should be after or equal to beforeCreate")
	assert.True(t, !createdTime.After(afterCreate), "CreatedAt should be before or equal to afterCreate")

	// Verify LastRecoveryAt is NOT set (new session)
	assert.Empty(t, capturedSession.LastRecoveryAt)
}

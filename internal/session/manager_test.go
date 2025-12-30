package session

import (
	"errors"
	"testing"

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

// MockSessionStore is a mock implementation for testing
type MockSessionStore struct {
	mock.Mock
}

func (m *MockSessionStore) Save(session *store.Session) error {
	args := m.Called(session)
	return args.Error(0)
}

func (m *MockSessionStore) Load(id string) (*store.Session, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*store.Session), args.Error(1)
}

func (m *MockSessionStore) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockSessionStore) List() ([]*store.Session, error) {
	args := m.Called()
	return args.Get(0).([]*store.Session), args.Error(1)
}

func (m *MockSessionStore) Move(id string, destination string) error {
	args := m.Called(id, destination)
	return args.Error(0)
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
	mockStore.On("Save", mock.MatchedBy(func(s *store.Session) bool {
		return s.SessionID == "test-uuid" && s.ProjectPath == "/tmp"
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

				// Only setup Save if CreateSession succeeds
				if tt.createErr == nil {
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

// TestSessionManager_KillSession tests the kill workflow
func TestSessionManager_KillSession(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	missingUUID := "550e8400-e29b-41d4-a716-446655440001"

	tests := []struct {
		name          string
		sessionID     string
		loadErr       error
		killErr       error
		expectErr     bool
		expectErrType error
	}{
		{
			name:      "successful kill",
			sessionID: validUUID,
			loadErr:   nil,
			killErr:   nil,
			expectErr: false,
		},
		{
			name:          "session not found in store",
			sessionID:     missingUUID,
			loadErr:       store.ErrSessionNotFound,
			expectErr:     true,
			expectErrType: store.ErrSessionNotFound,
		},
		{
			name:      "invalid UUID format",
			sessionID: "not-a-uuid",
			expectErr: true,
		},
		{
			name:      "tmux session already dead (idempotent)",
			sessionID: validUUID,
			loadErr:   nil,
			killErr:   errors.New("session not found"), // Tmux returns error but Kill is idempotent
			expectErr: false,                           // Should NOT error - kill is idempotent
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := new(MockTmuxExecutor)
			mockStore := new(MockSessionStore)

			// Setup mocks based on test case
			if tt.sessionID != "not-a-uuid" { // Skip mock for invalid UUID test
				if tt.loadErr != nil {
					mockStore.On("Load", tt.sessionID).Return(nil, tt.loadErr)
				} else {
					session := &store.Session{
						SessionID:   tt.sessionID,
						ProjectPath: "/tmp",
						Windows:     []store.Window{},
					}
					mockStore.On("Load", tt.sessionID).Return(session, nil)
					mockExec.On("KillSession", tt.sessionID).Return(tt.killErr)
				}
			}

			manager := NewSessionManager(mockExec, mockStore)
			err := manager.KillSession(tt.sessionID)

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

// TestSessionManager_EndSession tests the end workflow
func TestSessionManager_EndSession(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	missingUUID := "550e8400-e29b-41d4-a716-446655440001"

	tests := []struct {
		name          string
		sessionID     string
		loadErr       error
		killErr       error
		moveErr       error
		expectErr     bool
		expectErrType error
	}{
		{
			name:      "successful end and archive",
			sessionID: validUUID,
			loadErr:   nil,
			killErr:   nil,
			moveErr:   nil,
			expectErr: false,
		},
		{
			name:          "session not found in store",
			sessionID:     missingUUID,
			loadErr:       store.ErrSessionNotFound,
			expectErr:     true,
			expectErrType: store.ErrSessionNotFound,
		},
		{
			name:      "invalid UUID format",
			sessionID: "not-a-uuid",
			expectErr: true,
		},
		{
			name:      "tmux session already dead (end still archives)",
			sessionID: validUUID,
			loadErr:   nil,
			killErr:   errors.New("session not found"), // Tmux error but we ignore it
			moveErr:   nil,
			expectErr: false, // Should NOT error - kill is best-effort for end
		},
		{
			name:      "file move fails",
			sessionID: validUUID,
			loadErr:   nil,
			killErr:   nil,
			moveErr:   errors.New("filesystem error"),
			expectErr: true, // Move failure IS an error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := new(MockTmuxExecutor)
			mockStore := new(MockSessionStore)

			// Setup mocks based on test case
			if tt.sessionID != "not-a-uuid" { // Skip mock for invalid UUID test
				if tt.loadErr != nil {
					mockStore.On("Load", tt.sessionID).Return(nil, tt.loadErr)
				} else {
					session := &store.Session{
						SessionID:   tt.sessionID,
						ProjectPath: "/tmp",
						Windows:     []store.Window{},
					}
					mockStore.On("Load", tt.sessionID).Return(session, nil)
					mockExec.On("KillSession", tt.sessionID).Return(tt.killErr)
					mockStore.On("Move", tt.sessionID, "ended").Return(tt.moveErr)
				}
			}

			manager := NewSessionManager(mockExec, mockStore)
			err := manager.EndSession(tt.sessionID)

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

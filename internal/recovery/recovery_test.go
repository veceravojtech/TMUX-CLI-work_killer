// Package recovery provides session recovery detection and execution functionality.
package recovery

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

// MockSessionStore for testing
type MockSessionStore struct {
	mock.Mock
}

func (m *MockSessionStore) Load(id string) (*store.Session, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*store.Session), args.Error(1)
}

func (m *MockSessionStore) Save(session *store.Session) error {
	args := m.Called(session)
	return args.Error(0)
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

func (m *MockSessionStore) FindByPath(path string) (*store.Session, error) {
	args := m.Called(path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*store.Session), args.Error(1)
}

func (m *MockSessionStore) WriteSessionFile(projectPath string, sessionID string) error {
	args := m.Called(projectPath, sessionID)
	return args.Error(0)
}

func (m *MockSessionStore) ReadSessionFile(projectPath string) (string, error) {
	args := m.Called(projectPath)
	return args.String(0), args.Error(1)
}

func (m *MockSessionStore) DeleteSessionFile(projectPath string) error {
	args := m.Called(projectPath)
	return args.Error(0)
}

// MockTmuxExecutor for testing
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

// TestIsRecoveryNeeded tests the recovery detection logic with table-driven tests
// Updated to accept session object instead of sessionID to fix API mismatch bug
func TestIsRecoveryNeeded(t *testing.T) {
	tests := []struct {
		name         string
		session      *store.Session
		setupMocks   func(*MockSessionStore, *MockTmuxExecutor)
		wantRecovery bool
		wantErr      bool
		errContains  string
	}{
		{
			name: "session active - tmux session exists",
			session: &store.Session{
				SessionID:   "active-session-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Tmux session exists
				exec.On("HasSession", "active-session-uuid").Return(true, nil)
			},
			wantRecovery: false, // No recovery needed - session is alive
			wantErr:      false,
		},
		{
			name: "session killed - tmux session doesn't exist",
			session: &store.Session{
				SessionID:   "killed-session-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Tmux session does NOT exist
				exec.On("HasSession", "killed-session-uuid").Return(false, nil)
			},
			wantRecovery: true, // RECOVERY NEEDED!
			wantErr:      false,
		},
		{
			name: "tmux check fails",
			session: &store.Session{
				SessionID:   "error-check-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Tmux check fails (e.g., tmux not installed)
				exec.On("HasSession", "error-check-uuid").Return(false, errors.New("tmux not found"))
			},
			wantRecovery: false, // Can't determine state
			wantErr:      true,
			errContains:  "check tmux session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mocks
			mockStore := new(MockSessionStore)
			mockExecutor := new(MockTmuxExecutor)

			// Setup mock expectations
			tt.setupMocks(mockStore, mockExecutor)

			// Create recovery manager
			manager := NewSessionRecoveryManager(mockStore, mockExecutor)

			// Execute - now passing session object instead of sessionID
			recoveryNeeded, err := manager.IsRecoveryNeeded(tt.session)

			// Assert
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantRecovery, recoveryNeeded)
			}

			// Verify all mock expectations met
			mockStore.AssertExpectations(t)
			mockExecutor.AssertExpectations(t)
		})
	}
}

// TestNewSessionRecoveryManager tests the constructor
func TestNewSessionRecoveryManager(t *testing.T) {
	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	assert.NotNil(t, manager)
	assert.Equal(t, mockStore, manager.store)
	assert.Equal(t, mockExecutor, manager.executor)
}

// BenchmarkIsRecoveryNeeded benchmarks the recovery detection performance
func BenchmarkIsRecoveryNeeded(b *testing.B) {
	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	session := &store.Session{
		SessionID:   "bench-uuid",
		ProjectPath: "/project",
		Windows:     []store.Window{},
	}
	mockExecutor.On("HasSession", mock.Anything).Return(false, nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.IsRecoveryNeeded(session)
	}
}

// TestRecoverSession tests the complete recovery workflow with table-driven tests
func TestRecoverSession(t *testing.T) {
	tests := []struct {
		name          string
		session       *store.Session
		setupMocks    func(*MockSessionStore, *MockTmuxExecutor)
		wantErr       bool
		errContains   string
		verifySession func(*testing.T, *store.Session)
	}{
		{
			name: "successful recovery with no windows",
			session: &store.Session{
				SessionID:   "test-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "test-uuid", "/project").Return(nil)

				// List windows after session creation
				exec.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "supervisor"},
				}, nil).Once()

				// Save succeeds
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false,
			verifySession: func(t *testing.T, session *store.Session) {
				// Verify session identity preserved
				assert.Equal(t, "test-uuid", session.SessionID)
				assert.Equal(t, "/project", session.ProjectPath)
				assert.Empty(t, session.Windows)
			},
		},
		{
			name: "successful recovery with multiple windows",
			session: &store.Session{
				SessionID:   "multi-window-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "editor", TmuxWindowID: ""},
					{Name: "tests", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "multi-window-uuid", "/project").Return(nil)

				// List windows after session creation
				exec.On("ListWindows", "multi-window-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "supervisor"},
				}, nil).Once()

				// Window creation succeeds, returns new window IDs
				exec.On("CreateWindow", "multi-window-uuid", "editor", "zsh").Return("@1", nil)
				exec.On("CreateWindow", "multi-window-uuid", "tests", "zsh").Return("@2", nil)

				// Save succeeds
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false,
			verifySession: func(t *testing.T, session *store.Session) {
				// Verify windows recreated with new IDs
				assert.Len(t, session.Windows, 2)
				assert.Equal(t, "@1", session.Windows[0].TmuxWindowID)
				assert.Equal(t, "@2", session.Windows[1].TmuxWindowID)

				// Verify names and commands preserved
				assert.Equal(t, "editor", session.Windows[0].Name)
				assert.Equal(t, "tests", session.Windows[1].Name)
			},
		},
		{
			name: "session creation fails - entire recovery fails",
			session: &store.Session{
				SessionID:   "failed-session-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation fails
				exec.On("CreateSession", "failed-session-uuid", "/project").Return(errors.New("tmux not found"))

				// CreateWindow should NOT be called
				// Save should NOT be called
			},
			wantErr:     true,
			errContains: "recreate session",
		},
		{
			name: "some windows fail to recreate - recovery continues",
			session: &store.Session{
				SessionID:   "partial-recovery-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "window1", TmuxWindowID: ""},
					{Name: "window2", TmuxWindowID: ""},
					{Name: "window3", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "partial-recovery-uuid", "/project").Return(nil)

				// List windows after session creation
				exec.On("ListWindows", "partial-recovery-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "supervisor"},
				}, nil).Once()

				// First window succeeds
				exec.On("CreateWindow", mock.Anything, "window1", "zsh").Return("@1", nil).Once()
				// UUID restoration for first window (no UUID, so not called)

				// Second window fails
				exec.On("CreateWindow", mock.Anything, "window2", "zsh").Return("", errors.New("command failed")).Once()

				// Third window succeeds
				exec.On("CreateWindow", mock.Anything, "window3", "zsh").Return("@2", nil).Once()
				// UUID restoration for third window (no UUID, so not called)

				// Save succeeds
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false, // Recovery succeeds despite one window failure
			verifySession: func(t *testing.T, session *store.Session) {
				// Verify successful windows have IDs
				assert.Equal(t, "@1", session.Windows[0].TmuxWindowID)
				assert.Equal(t, "", session.Windows[1].TmuxWindowID) // Failed window has no ID
				assert.Equal(t, "@2", session.Windows[2].TmuxWindowID)
			},
		},
		{
			name: "all windows fail but session still saved",
			session: &store.Session{
				SessionID:   "all-windows-fail-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "all-windows-fail-uuid", "/project").Return(nil)

				// List windows after session creation
				exec.On("ListWindows", "all-windows-fail-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "supervisor"},
				}, nil).Once()

				// All windows fail
				exec.On("CreateWindow", mock.Anything, mock.Anything, "zsh").Return("", errors.New("command failed"))
				exec.On("CreateWindow", mock.Anything, mock.Anything, "zsh").Return("", errors.New("command failed"))

				// Save still called (and succeeds)
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false, // Recovery "succeeds" - session recreated, just no windows
			verifySession: func(t *testing.T, session *store.Session) {
				// All windows have empty IDs
				for _, window := range session.Windows {
					assert.Empty(t, window.TmuxWindowID)
				}
			},
		},
		{
			name: "save fails after successful recovery",
			session: &store.Session{
				SessionID:   "save-fails-uuid",
				ProjectPath: "/project",
				Windows:     []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "save-fails-uuid", "/project").Return(nil)

				// List windows after session creation
				exec.On("ListWindows", "save-fails-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "supervisor"},
				}, nil).Once()

				// Save fails
				mockStore.On("Save", mock.Anything).Return(errors.New("disk full"))
			},
			wantErr:     true,
			errContains: "save session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mocks
			mockStore := new(MockSessionStore)
			mockExecutor := new(MockTmuxExecutor)

			// Setup mock expectations
			tt.setupMocks(mockStore, mockExecutor)

			// Create recovery manager
			manager := NewSessionRecoveryManager(mockStore, mockExecutor)

			// Execute recovery
			err := manager.RecoverSession(tt.session)

			// Assert error expectations
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)

				// Verify session state if provided
				if tt.verifySession != nil {
					tt.verifySession(t, tt.session)
				}
			}

			// Verify all mock expectations met
			mockStore.AssertExpectations(t)
			mockExecutor.AssertExpectations(t)
		})
	}
}

// TestRecoverSession_PreservesSessionIdentity tests FR15 compliance
func TestRecoverSession_PreservesSessionIdentity(t *testing.T) {
	originalSession := &store.Session{
		SessionID:   "preserve-uuid",
		ProjectPath: "/original/path",
		Windows: []store.Window{
			{Name: "original-window", TmuxWindowID: ""},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	// Setup mocks
	mockExecutor.On("CreateSession", "preserve-uuid", "/original/path").Return(nil)
	mockExecutor.On("ListWindows", "preserve-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	mockExecutor.On("CreateWindow", mock.Anything, mock.Anything, "zsh").Return("@1", nil)
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(originalSession)

	assert.NoError(t, err)

	// CRITICAL: Verify identity preserved (FR15)
	assert.Equal(t, "preserve-uuid", originalSession.SessionID)
	assert.Equal(t, "/original/path", originalSession.ProjectPath)
	assert.Equal(t, "original-window", originalSession.Windows[0].Name)

	// Verify window ID updated but name preserved
	assert.Equal(t, "@1", originalSession.Windows[0].TmuxWindowID)
}

// BenchmarkRecoverSession_NoWindows benchmarks recovery with no windows
func BenchmarkRecoverSession_NoWindows(b *testing.B) {
	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	session := &store.Session{
		SessionID:   "bench-uuid",
		ProjectPath: "/project",
		Windows:     []store.Window{},
	}

	mockExecutor.On("CreateSession", mock.Anything, mock.Anything).Return(nil)
	mockExecutor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.RecoverSession(session)
	}
}

// BenchmarkRecoverSession_MultipleWindows benchmarks recovery with 10 windows
func BenchmarkRecoverSession_MultipleWindows(b *testing.B) {
	// Create session with 10 windows (test NFR5 - must complete within 30 seconds)
	windows := make([]store.Window, 10)
	for i := 0; i < 10; i++ {
		windows[i] = store.Window{
			Name: "window-" + string(rune(i)),
		}
	}

	session := &store.Session{
		SessionID:   "bench-uuid",
		ProjectPath: "/project",
		Windows:     windows,
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	mockExecutor.On("CreateSession", mock.Anything, mock.Anything).Return(nil)
	mockExecutor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExecutor.On("CreateWindow", mock.Anything, mock.Anything, mock.Anything).Return("@1", nil)
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.RecoverSession(session)
	}
}

// TestVerifyRecovery tests the recovery verification logic with table-driven tests
// Updated to accept session object instead of sessionID to fix API mismatch bug
func TestVerifyRecovery(t *testing.T) {
	tests := []struct {
		name        string
		session     *store.Session
		setupMocks  func(*MockSessionStore, *MockTmuxExecutor)
		wantErr     bool
		errContains string
	}{
		{
			name: "session running with all windows exist - success",
			session: &store.Session{
				SessionID:   "test-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session exists
				exec.On("HasSession", "test-uuid").Return(true, nil)

				// List windows - returns both windows
				exec.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "editor"},
					{TmuxWindowID: "@1", Name: "tests"},
				}, nil)
			},
			wantErr: false,
		},
		{
			name: "session not running after recovery - error",
			session: &store.Session{
				SessionID: "dead-session",
				Windows:   []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session doesn't exist
				exec.On("HasSession", "dead-session").Return(false, nil)
			},
			wantErr:     true,
			errContains: "not running after recovery",
		},
		{
			name: "window missing after recovery - error",
			session: &store.Session{
				SessionID: "partial-recovery",
				Windows: []store.Window{
					{Name: "test", TmuxWindowID: "@1"}, // Window should exist but is missing
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				exec.On("HasSession", "partial-recovery").Return(true, nil)

				// Only @0 window exists, but session expects @1
				exec.On("ListWindows", "partial-recovery").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "exists"},
				}, nil)
			},
			wantErr:     true,
			errContains: "not found after recovery",
		},
		{
			name: "skip windows with empty IDs - success",
			session: &store.Session{
				SessionID: "partial-create",
				Windows: []store.Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				exec.On("HasSession", "partial-create").Return(true, nil)

				// Only one window in tmux (the one that was created successfully)
				exec.On("ListWindows", "partial-create").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "created"},
				}, nil)
			},
			wantErr: false, // Success! Empty ID window skipped
		},
		{
			name: "session check fails - error",
			session: &store.Session{
				SessionID: "check-fails",
				Windows:   []store.Window{},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				exec.On("HasSession", "check-fails").Return(false, errors.New("tmux not running"))
			},
			wantErr:     true,
			errContains: "check session exists",
		},
		{
			name: "list windows fails - error",
			session: &store.Session{
				SessionID: "list-fails",
				Windows: []store.Window{
					{Name: "test", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				exec.On("HasSession", "list-fails").Return(true, nil)
				exec.On("ListWindows", "list-fails").Return(nil, errors.New("permission denied"))
			},
			wantErr:     true,
			errContains: "list windows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := new(MockSessionStore)
			mockExecutor := new(MockTmuxExecutor)

			tt.setupMocks(mockStore, mockExecutor)

			manager := NewSessionRecoveryManager(mockStore, mockExecutor)
			err := manager.VerifyRecovery(tt.session)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}

			mockStore.AssertExpectations(t)
			mockExecutor.AssertExpectations(t)
		})
	}
}

func TestRecoverSession_UpdatesLastRecoveryAt(t *testing.T) {
	// Setup mocks
	mockExecutor := &MockTmuxExecutor{}
	mockStore := &MockSessionStore{}
	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	// Create session to recover
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/tmp/test",
		CreatedAt:   "2026-01-01T10:00:00Z",
		Windows: []store.Window{
			{Name: "test", TmuxWindowID: ""},
		},
	}

	// Mock expectations
	mockExecutor.On("CreateSession", "test-uuid", "/tmp/test").Return(nil)
	mockExecutor.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	mockExecutor.On("CreateWindow", mock.Anything, mock.Anything, "zsh").
		Return("@1", nil)

	var capturedSession *store.Session
	mockStore.On("Save", mock.AnythingOfType("*store.Session")).
		Run(func(args mock.Arguments) {
			capturedSession = args.Get(0).(*store.Session)
		}).
		Return(nil)

	// Execute
	beforeRecover := time.Now().Add(-1 * time.Second) // Buffer for timing
	err := manager.RecoverSession(session)
	afterRecover := time.Now().Add(1 * time.Second)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedSession)

	// Verify LastRecoveryAt was set
	assert.NotEmpty(t, capturedSession.LastRecoveryAt)

	// Verify timestamp is valid and within timeframe
	recoveryTime, err := time.Parse(time.RFC3339, capturedSession.LastRecoveryAt)
	require.NoError(t, err)
	assert.True(t, !recoveryTime.Before(beforeRecover), "LastRecoveryAt should be after or equal to beforeRecover")
	assert.True(t, !recoveryTime.After(afterRecover), "LastRecoveryAt should be before or equal to afterRecover")

	// Verify CreatedAt was NOT changed
	assert.Equal(t, "2026-01-01T10:00:00Z", capturedSession.CreatedAt)
}

// TestRecoverSession_DoesNotDuplicateSupervisorWindow tests that recovery doesn't create
// an extra supervisor window beyond the one automatically created by CreateSession
// FIXED: CreateSession creates a "supervisor" window, recovery finds and reuses it
func TestRecoverSession_DoesNotDuplicateSupervisorWindow(t *testing.T) {
	// Session has 2 windows: supervisor (empty command) + test (with command)
	// This represents the typical session state after creation
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/project",
		Windows: []store.Window{
			{Name: "test", TmuxWindowID: ""},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	// CreateSession creates a "supervisor" window automatically (via -n supervisor flag)
	mockExecutor.On("CreateSession", "test-uuid", "/project").Return(nil)

	// After CreateSession, we list windows to find the supervisor window ID
	// tmux assigns a global window ID (could be any number like @11, @12, etc.)
	mockExecutor.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@11", Name: "supervisor"}, // CreateSession created this
	}, nil).Once() // Only expect this ListWindows call once (after CreateSession)

	// FIXED: CreateWindow should only be called ONCE for "test" window
	// The supervisor window is reused from CreateSession, not recreated
	// Recovery always uses "zsh" since RecoveryCommand was removed
	mockExecutor.On("CreateWindow", "test-uuid", "test", "zsh").Return("@12", nil)

	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	require.NoError(t, err)

	// Verify mock expectations - CreateWindow was only called once
	mockExecutor.AssertExpectations(t)

	// Session should have 1 window: the "test" window
	// The supervisor window is not added to the Windows array
	require.Len(t, session.Windows, 1, "session should have exactly 1 window")
	assert.Equal(t, "test", session.Windows[0].Name)
	assert.Equal(t, "@12", session.Windows[0].TmuxWindowID, "test window was created and assigned @12")
}

// TestRecoverSession_ReusesSupervisorWithShellCommand tests that recovery reuses the supervisor
// window even when it has a shell command (zsh, bash, sh, fish) as RecoveryCommand
// BUG: Currently fails because recovery.go:93 only checks for empty RecoveryCommand
func TestRecoverSession_ReusesSupervisorWithShellCommand(t *testing.T) {
	// REAL-WORLD SCENARIO: When a session is created, the supervisor window runs a shell (e.g., zsh)
	// The session manager captures this as RecoveryCommand in manager.go:68
	// During recovery, we should reuse this supervisor window, NOT create a duplicate
	session := &store.Session{
		SessionID:   "test-shell-uuid",
		ProjectPath: "/project",
		Windows: []store.Window{
			{Name: "test", TmuxWindowID: ""},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	// CreateSession creates a "supervisor" window automatically
	mockExecutor.On("CreateSession", "test-shell-uuid", "/project").Return(nil)

	// After CreateSession, list windows to find the supervisor window ID
	mockExecutor.On("ListWindows", "test-shell-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@20", Name: "supervisor"}, // CreateSession created this
	}, nil).Once()

	// CRITICAL: CreateWindow should NOT be called for supervisor
	// The supervisor window should be reused from CreateSession
	// If CreateWindow is called with "supervisor", the test will fail

	// However, CreateWindow SHOULD be called for the "test" window since it's not named "supervisor"
	mockExecutor.On("CreateWindow", "test-shell-uuid", "test", "zsh").Return("@21", nil)

	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	require.NoError(t, err)

	// Verify CreateWindow was called for "test" window but NOT for supervisor
	mockExecutor.AssertExpectations(t)

	// Session should have 1 window ("test") with the new ID @21
	assert.Equal(t, "@21", session.Windows[0].TmuxWindowID, "test window was created")
}

// TestRecoverSession_RestoresWindowUUIDs tests that window UUIDs are restored to tmux user-options
func TestRecoverSession_RestoresWindowUUIDs(t *testing.T) {
	session := &store.Session{
		SessionID:   "test-uuid",
		ProjectPath: "/project",
		Windows: []store.Window{
			{Name: "editor", UUID: "550e8400-e29b-41d4-a716-446655440001"},
			{Name: "tests", UUID: "550e8400-e29b-41d4-a716-446655440002"},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	mockExecutor.On("CreateSession", "test-uuid", "/project").Return(nil)
	mockExecutor.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()

	mockExecutor.On("CreateWindow", mock.Anything, "editor", "zsh").Return("@1", nil)
	mockExecutor.On("CreateWindow", mock.Anything, "tests", "zsh").Return("@2", nil)

	// CRITICAL: Verify SetWindowOption is called for each UUID
	mockExecutor.On("SetWindowOption", "test-uuid", "@1", tmux.WindowUUIDOption, "550e8400-e29b-41d4-a716-446655440001").Return(nil).Once()
	mockExecutor.On("SetWindowOption", "test-uuid", "@2", tmux.WindowUUIDOption, "550e8400-e29b-41d4-a716-446655440002").Return(nil).Once()
	// CRITICAL: Verify SendMessage is called to export UUIDs in shell and execute post-commands
	mockExecutor.On("SendMessage", "test-uuid", "@1", mock.Anything).Return(nil).Maybe()
	mockExecutor.On("SendMessage", "test-uuid", "@2", mock.Anything).Return(nil).Maybe()

	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	require.NoError(t, err)
	mockExecutor.AssertExpectations(t) // Fails if SetWindowOption not called!
}

// TestRecoverSession_SkipsEmptyUUIDs tests that windows without UUIDs don't call SetWindowOption
func TestRecoverSession_SkipsEmptyUUIDs(t *testing.T) {
	// Test that windows without UUIDs don't call SetWindowOption
	session := &store.Session{
		SessionID: "test-uuid",
		Windows: []store.Window{
			{Name: "legacy", UUID: ""}, // No UUID - should skip
		},
	}

	mockExecutor := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExecutor.On("CreateSession", mock.Anything, mock.Anything).Return(nil)
	mockExecutor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExecutor.On("CreateWindow", mock.Anything, "legacy", "zsh").Return("@1", nil)

	// SetWindowOption should NOT be called for empty UUID
	// If it is called, AssertExpectations will fail

	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	require.NoError(t, err)
	mockExecutor.AssertExpectations(t)

	// Verify SetWindowOption was never called
	mockExecutor.AssertNotCalled(t, "SetWindowOption")
}

// TestRecoverSession_HandlesSetWindowOptionFailure tests that SetWindowOption errors cause recovery to fail
func TestRecoverSession_HandlesSetWindowOptionFailure(t *testing.T) {
	// Test that SetWindowOption errors cause recovery to fail (strict error propagation)
	session := &store.Session{
		SessionID: "test-uuid",
		Windows: []store.Window{
			{Name: "window1", UUID: "550e8400-e29b-41d4-a716-446655440001"},
		},
	}

	mockExecutor := new(MockTmuxExecutor)
	mockStore := new(MockSessionStore)

	mockExecutor.On("CreateSession", mock.Anything, mock.Anything).Return(nil)
	mockExecutor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExecutor.On("CreateWindow", mock.Anything, "window1", "zsh").Return("@1", nil)

	// SetWindowOption fails - recovery should FAIL
	mockExecutor.On("SetWindowOption", mock.Anything, "@1", tmux.WindowUUIDOption, "550e8400-e29b-41d4-a716-446655440001").
		Return(errors.New("permission denied"))

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(session)

	// CRITICAL: Recovery must FAIL when UUID restoration fails (Rule 0.5: no fallback)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore window @1 UUID")
	// Store should NOT be called (no partial recovery saved)
	mockStore.AssertNotCalled(t, "Save")
}

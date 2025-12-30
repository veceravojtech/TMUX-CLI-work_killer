// Package recovery provides session recovery detection and execution functionality.
package recovery

import (
	"errors"
	"testing"

	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

// TestIsRecoveryNeeded tests the recovery detection logic with table-driven tests
func TestIsRecoveryNeeded(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		setupMocks   func(*MockSessionStore, *MockTmuxExecutor)
		wantRecovery bool
		wantErr      bool
		errContains  string
	}{
		{
			name:      "session active - both file and tmux exist",
			sessionID: "active-session-uuid",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session file exists
				session := &store.Session{
					SessionID:   "active-session-uuid",
					ProjectPath: "/project",
					Windows:     []store.Window{},
				}
				mockStore.On("Load", "active-session-uuid").Return(session, nil)

				// Tmux session exists
				exec.On("HasSession", "active-session-uuid").Return(true, nil)
			},
			wantRecovery: false, // No recovery needed - session is alive
			wantErr:      false,
		},
		{
			name:      "session killed - file exists but tmux doesn't",
			sessionID: "killed-session-uuid",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session file exists
				session := &store.Session{
					SessionID:   "killed-session-uuid",
					ProjectPath: "/project",
					Windows:     []store.Window{},
				}
				mockStore.On("Load", "killed-session-uuid").Return(session, nil)

				// Tmux session does NOT exist
				exec.On("HasSession", "killed-session-uuid").Return(false, nil)
			},
			wantRecovery: true, // RECOVERY NEEDED!
			wantErr:      false,
		},
		{
			name:      "session file not found",
			sessionID: "nonexistent-session-uuid",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session file does NOT exist
				mockStore.On("Load", "nonexistent-session-uuid").Return(nil, errors.New("session not found"))
				// HasSession should NOT be called
			},
			wantRecovery: false, // Can't recover what doesn't exist
			wantErr:      true,
			errContains:  "load session",
		},
		{
			name:      "tmux check fails",
			sessionID: "error-check-uuid",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session file exists
				session := &store.Session{
					SessionID:   "error-check-uuid",
					ProjectPath: "/project",
					Windows:     []store.Window{},
				}
				mockStore.On("Load", "error-check-uuid").Return(session, nil)

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

			// Execute
			recoveryNeeded, err := manager.IsRecoveryNeeded(tt.sessionID)

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
	mockStore.On("Load", mock.Anything).Return(session, nil)
	mockExecutor.On("HasSession", mock.Anything).Return(false, nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.IsRecoveryNeeded("bench-uuid")
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
					{Name: "editor", RecoveryCommand: "vim", TmuxWindowID: ""},
					{Name: "tests", RecoveryCommand: "go test -watch", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "multi-window-uuid", "/project").Return(nil)

				// Window creation succeeds, returns new window IDs
				exec.On("CreateWindow", "multi-window-uuid", "editor", "vim").Return("@0", nil)
				exec.On("CreateWindow", "multi-window-uuid", "tests", "go test -watch").Return("@1", nil)

				// Save succeeds
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false,
			verifySession: func(t *testing.T, session *store.Session) {
				// Verify windows recreated with new IDs
				assert.Len(t, session.Windows, 2)
				assert.Equal(t, "@0", session.Windows[0].TmuxWindowID)
				assert.Equal(t, "@1", session.Windows[1].TmuxWindowID)

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
					{Name: "good-window", RecoveryCommand: "vim", TmuxWindowID: ""},
					{Name: "bad-window", RecoveryCommand: "invalid-command", TmuxWindowID: ""},
					{Name: "another-good-window", RecoveryCommand: "ls", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "partial-recovery-uuid", "/project").Return(nil)

				// First window succeeds
				exec.On("CreateWindow", "partial-recovery-uuid", "good-window", "vim").Return("@0", nil)

				// Second window fails
				exec.On("CreateWindow", "partial-recovery-uuid", "bad-window", "invalid-command").Return("", errors.New("command failed"))

				// Third window succeeds
				exec.On("CreateWindow", "partial-recovery-uuid", "another-good-window", "ls").Return("@1", nil)

				// Save succeeds
				mockStore.On("Save", mock.Anything).Return(nil)
			},
			wantErr: false, // Recovery succeeds despite one window failure
			verifySession: func(t *testing.T, session *store.Session) {
				// Verify successful windows have IDs
				assert.Equal(t, "@0", session.Windows[0].TmuxWindowID)
				assert.Equal(t, "", session.Windows[1].TmuxWindowID) // Failed window has no ID
				assert.Equal(t, "@1", session.Windows[2].TmuxWindowID)
			},
		},
		{
			name: "all windows fail but session still saved",
			session: &store.Session{
				SessionID:   "all-windows-fail-uuid",
				ProjectPath: "/project",
				Windows: []store.Window{
					{Name: "fail1", RecoveryCommand: "bad1", TmuxWindowID: ""},
					{Name: "fail2", RecoveryCommand: "bad2", TmuxWindowID: ""},
				},
			},
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Session creation succeeds
				exec.On("CreateSession", "all-windows-fail-uuid", "/project").Return(nil)

				// All windows fail
				exec.On("CreateWindow", "all-windows-fail-uuid", "fail1", "bad1").Return("", errors.New("command failed"))
				exec.On("CreateWindow", "all-windows-fail-uuid", "fail2", "bad2").Return("", errors.New("command failed"))

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
			{Name: "original-window", RecoveryCommand: "vim", TmuxWindowID: ""},
		},
	}

	mockStore := new(MockSessionStore)
	mockExecutor := new(MockTmuxExecutor)

	// Setup mocks
	mockExecutor.On("CreateSession", "preserve-uuid", "/original/path").Return(nil)
	mockExecutor.On("CreateWindow", "preserve-uuid", "original-window", "vim").Return("@0", nil)
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)
	err := manager.RecoverSession(originalSession)

	assert.NoError(t, err)

	// CRITICAL: Verify identity preserved (FR15)
	assert.Equal(t, "preserve-uuid", originalSession.SessionID)
	assert.Equal(t, "/original/path", originalSession.ProjectPath)
	assert.Equal(t, "original-window", originalSession.Windows[0].Name)

	// Verify window ID updated but name/command preserved
	assert.Equal(t, "@0", originalSession.Windows[0].TmuxWindowID)
	assert.Equal(t, "vim", originalSession.Windows[0].RecoveryCommand)
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
			Name:            "window-" + string(rune(i)),
			RecoveryCommand: "vim",
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
	mockExecutor.On("CreateWindow", mock.Anything, mock.Anything, mock.Anything).Return("@0", nil)
	mockStore.On("Save", mock.Anything).Return(nil)

	manager := NewSessionRecoveryManager(mockStore, mockExecutor)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.RecoverSession(session)
	}
}

// TestVerifyRecovery tests the recovery verification logic with table-driven tests
func TestVerifyRecovery(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		setupMocks  func(*MockSessionStore, *MockTmuxExecutor)
		wantErr     bool
		errContains string
	}{
		{
			name:      "session running with all windows exist - success",
			sessionID: "test-uuid",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				// Load session
				session := &store.Session{
					SessionID:   "test-uuid",
					ProjectPath: "/project",
					Windows: []store.Window{
						{TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim"},
						{TmuxWindowID: "@1", Name: "tests", RecoveryCommand: "go test"},
					},
				}
				mockStore.On("Load", "test-uuid").Return(session, nil)

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
			name:      "session not running after recovery - error",
			sessionID: "dead-session",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				session := &store.Session{
					SessionID: "dead-session",
					Windows:   []store.Window{},
				}
				mockStore.On("Load", "dead-session").Return(session, nil)

				// Session doesn't exist
				exec.On("HasSession", "dead-session").Return(false, nil)
			},
			wantErr:     true,
			errContains: "not running after recovery",
		},
		{
			name:      "window missing after recovery - error",
			sessionID: "partial-recovery",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				session := &store.Session{
					SessionID: "partial-recovery",
					Windows: []store.Window{
						{TmuxWindowID: "@0", Name: "exists", RecoveryCommand: "vim"},
						{TmuxWindowID: "@1", Name: "missing", RecoveryCommand: "ls"},
					},
				}
				mockStore.On("Load", "partial-recovery").Return(session, nil)

				exec.On("HasSession", "partial-recovery").Return(true, nil)

				// Only one window exists
				exec.On("ListWindows", "partial-recovery").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "exists"},
				}, nil)
			},
			wantErr:     true,
			errContains: "not found after recovery",
		},
		{
			name:      "skip windows with empty IDs - success",
			sessionID: "partial-create",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				session := &store.Session{
					SessionID: "partial-create",
					Windows: []store.Window{
						{TmuxWindowID: "@0", Name: "created", RecoveryCommand: "vim"},
						{TmuxWindowID: "", Name: "failed", RecoveryCommand: "bad"}, // Empty ID = failed during recovery
					},
				}
				mockStore.On("Load", "partial-create").Return(session, nil)

				exec.On("HasSession", "partial-create").Return(true, nil)

				// Only one window in tmux (the one that was created successfully)
				exec.On("ListWindows", "partial-create").Return([]tmux.WindowInfo{
					{TmuxWindowID: "@0", Name: "created"},
				}, nil)
			},
			wantErr: false, // Success! Empty ID window skipped
		},
		{
			name:      "load session fails - error",
			sessionID: "load-fails",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				mockStore.On("Load", "load-fails").Return(nil, errors.New("file not found"))
			},
			wantErr:     true,
			errContains: "load session",
		},
		{
			name:      "session check fails - error",
			sessionID: "check-fails",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				session := &store.Session{
					SessionID: "check-fails",
					Windows:   []store.Window{},
				}
				mockStore.On("Load", "check-fails").Return(session, nil)
				exec.On("HasSession", "check-fails").Return(false, errors.New("tmux not running"))
			},
			wantErr:     true,
			errContains: "check session exists",
		},
		{
			name:      "list windows fails - error",
			sessionID: "list-fails",
			setupMocks: func(mockStore *MockSessionStore, exec *MockTmuxExecutor) {
				session := &store.Session{
					SessionID: "list-fails",
					Windows: []store.Window{
						{TmuxWindowID: "@0", Name: "window", RecoveryCommand: "vim"},
					},
				}
				mockStore.On("Load", "list-fails").Return(session, nil)
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
			err := manager.VerifyRecovery(tt.sessionID)

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

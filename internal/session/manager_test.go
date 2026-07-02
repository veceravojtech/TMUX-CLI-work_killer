package session

import (
	"errors"
	"strings"
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

func (m *MockTmuxExecutor) SendEnter(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

func (m *MockTmuxExecutor) SendMessage(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

func (m *MockTmuxExecutor) SendMessageWithDelay(sessionID, windowID, message string) error {
	args := m.Called(sessionID, windowID, message)
	return args.Error(0)
}

func (m *MockTmuxExecutor) NotifyPane(paneID, message string) error {
	args := m.Called(paneID, message)
	return args.Error(0)
}

func (m *MockTmuxExecutor) KillWindow(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
	return args.Error(0)
}

func (m *MockTmuxExecutor) InterruptWindow(windowID string) error {
	args := m.Called(windowID)
	return args.Error(0)
}

func (m *MockTmuxExecutor) TerminateWindowProcess(windowID string) error {
	args := m.Called(windowID)
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

func (m *MockTmuxExecutor) PipePane(sessionID, windowID, logPath string) error {
	args := m.Called(sessionID, windowID, logPath)
	return args.Error(0)
}

func (m *MockTmuxExecutor) ClosePipePane(sessionID, windowID string) error {
	args := m.Called(sessionID, windowID)
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

	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
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

// TestCreateSession_Window0StaysSupervisor pins the load-bearing window-0
// guarantee (manager.go:85): the first window MUST be named "supervisor" for the
// UUID stamp to fire. P1 namespaces goal windows but never renames window-0, so
// this guard must keep holding — the daemon's deactivate ensure-exists and the
// goal-skip sweep both depend on window-0 staying bare "supervisor".
func TestCreateSession_Window0StaysSupervisor(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
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
	require.NoError(t, manager.CreateSession("test-id", "/tmp"))

	// The UUID stamp fires ONLY when window-0 is named "supervisor"; asserting the
	// SetWindowOption call confirms window-0 kept the bare name.
	mockExec.AssertCalled(t, "SetWindowOption", "test-id", "@0", "window-uuid", mock.AnythingOfType("string"))
}

// TestCreateSession_WithModel_InjectsModelIntoLaunch verifies WithModel injects
// `--model '<model>'` into the supervisor window's claude launch command AND
// records the model in the session environment as TMUX_CLI_MODEL so the separate
// worker-spawning processes (MCP server, recovery) can retrieve and re-inject it.
func TestCreateSession_WithModel_InjectsModelIntoLaunch(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	const wantModel = "claude-opus-4-6[1m]"

	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_MODEL", wantModel).Return(nil)
	mockExec.On("ListWindows", "test-id").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", Running: true},
	}, nil)
	mockExec.On("SetWindowOption", "test-id", "@0", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return(nil)
	// The launch command (first fallback) MUST carry --model '<model>'.
	mockExec.On("SendMessageWithFeedback", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "--model 'claude-opus-4-6[1m]'") && strings.Contains(s, "claude --dangerously-skip-permissions")
	})).Return("", nil)

	manager := NewSessionManager(mockExec).WithModel(wantModel)
	require.NoError(t, manager.CreateSession("test-id", "/tmp"))

	mockExec.AssertCalled(t, "SetSessionEnvironment", "test-id", "TMUX_CLI_MODEL", wantModel)
	mockExec.AssertCalled(t, "SendMessageWithFeedback", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "--model 'claude-opus-4-6[1m]'")
	}))
}

// TestWithSource_RecordsTMUXCLISRCEnv verifies WithSource records TMUX_CLI_SRC=<dir>
// in the new session's environment at CreateSession time (mirrors the WithModel
// exemplar's mock chain). RED: the stub does not wire the env, so this fails.
func TestWithSource_RecordsTMUXCLISRCEnv(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	const wantSource = "/src/dir"

	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_SRC", wantSource).Return(nil)
	mockExec.On("ListWindows", "test-id").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", Running: true},
	}, nil)
	mockExec.On("SetWindowOption", "test-id", "@0", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return("", nil)

	manager := NewSessionManager(mockExec).WithSource(wantSource)
	require.NoError(t, manager.CreateSession("test-id", "/tmp"))

	mockExec.AssertCalled(t, "SetSessionEnvironment", "test-id", "TMUX_CLI_SRC", wantSource)
}

// TestWithSource_ReturnsReceiverForChaining pins the builder-chaining contract:
// WithSource returns the same manager pointer. Green against the stub — the RED
// signal for internal/session comes from TestWithSource_RecordsTMUXCLISRCEnv.
func TestWithSource_ReturnsReceiverForChaining(t *testing.T) {
	mgr := NewSessionManager(new(MockTmuxExecutor))
	assert.Same(t, mgr, mgr.WithSource("/x"), "WithSource must return the receiver for chaining")
}

// TestCreateSession_NoModel_NoModelFlagOrEnv verifies the default manager (no model
// configured) never writes TMUX_CLI_MODEL and launches claude with no --model flag
// — byte-identical to pre-flag behavior.
func TestCreateSession_NoModel_NoModelFlagOrEnv(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-id").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", Running: true},
	}, nil)
	mockExec.On("SetWindowOption", "test-id", "@0", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return(nil)
	mockExec.On("SendMessageWithFeedback", "test-id", "@0", mock.MatchedBy(func(s string) bool {
		return !strings.Contains(s, "--model")
	})).Return("", nil)

	manager := NewSessionManager(mockExec)
	require.NoError(t, manager.CreateSession("test-id", "/tmp"))

	mockExec.AssertNotCalled(t, "SetSessionEnvironment", "test-id", "TMUX_CLI_MODEL", mock.Anything)
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
	mockExec.On("HasSession", "test-id").Return(false, nil).Once()
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()
	mockExec.On("SetSessionEnvironment", "test-id", "TMUX_CLI_PROJECT_PATH", "/tmp").Return(nil)
	mockExec.On("ListWindows", "test-id").Return(nil, errors.New("failed to list windows"))
	mockExec.On("KillSession", "test-id").Return(nil)

	manager := NewSessionManager(mockExec)
	err := manager.CreateSession("test-id", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list windows")
	mockExec.AssertCalled(t, "KillSession", "test-id")
}

// TestSessionManager_CreateSession_WaitsForServerReady tests retry when server is slow to start
func TestSessionManager_CreateSession_WaitsForServerReady(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("HasSession", "test-id").Return(false, nil).Once() // pre-create check
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("HasSession", "test-id").Return(false, nil).Once() // readiness poll 1: not ready
	mockExec.On("HasSession", "test-id").Return(false, nil).Once() // readiness poll 2: not ready
	mockExec.On("HasSession", "test-id").Return(true, nil).Once()  // readiness poll 3: ready
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
	manager.sessionReadyInterval = 0
	err := manager.CreateSession("test-id", "/tmp")

	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
}

// TestSessionManager_CreateSession_ServerNeverReady tests error when server never becomes reachable
func TestSessionManager_CreateSession_ServerNeverReady(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("HasSession", "test-id").Return(false, nil)
	mockExec.On("CreateSession", "test-id", "/tmp").Return(nil)
	mockExec.On("KillSession", "test-id").Return(nil)

	manager := NewSessionManager(mockExec)
	manager.sessionReadyAttempts = 3
	manager.sessionReadyInterval = 0
	err := manager.CreateSession("test-id", "/tmp")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable after creation")
	mockExec.AssertCalled(t, "KillSession", "test-id")
	mockExec.AssertNotCalled(t, "SetSessionEnvironment", mock.Anything, mock.Anything, mock.Anything)
}

func TestEnsureTaskvisorWindow_CreatesWhenAbsent(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	mockExec.On("CreateWindow", "sess-1", "taskvisor", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "sess-1", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "sess-1", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("SendMessage", "sess-1", "@1", "tmux-cli taskvisor --run").Return(nil)

	manager := NewSessionManager(mockExec)
	err := manager.EnsureTaskvisorWindow("sess-1")

	require.NoError(t, err)
	mockExec.AssertCalled(t, "CreateWindow", "sess-1", "taskvisor", "")
	mockExec.AssertCalled(t, "SetWindowOption", "sess-1", "@1", "window-uuid", mock.AnythingOfType("string"))
	mockExec.AssertCalled(t, "SendMessage", "sess-1", "@1", "tmux-cli taskvisor --run")
}

func TestEnsureTaskvisorWindow_RestartWhenIdle(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		{TmuxWindowID: "@1", Name: "taskvisor", CurrentCommand: "zsh"},
	}, nil)
	mockExec.On("SendMessage", "sess-1", "@1", "tmux-cli taskvisor --run").Return(nil)

	manager := NewSessionManager(mockExec)
	err := manager.EnsureTaskvisorWindow("sess-1")

	require.NoError(t, err)
	mockExec.AssertNotCalled(t, "CreateWindow", mock.Anything, mock.Anything, mock.Anything)
	mockExec.AssertCalled(t, "SendMessage", "sess-1", "@1", "tmux-cli taskvisor --run")
}

func TestEnsureTaskvisorWindow_SkipsWhenRunning(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		{TmuxWindowID: "@1", Name: "taskvisor", CurrentCommand: "tmux-cli"},
	}, nil)

	manager := NewSessionManager(mockExec)
	err := manager.EnsureTaskvisorWindow("sess-1")

	require.NoError(t, err)
	mockExec.AssertNotCalled(t, "CreateWindow", mock.Anything, mock.Anything, mock.Anything)
	mockExec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestEnsureTaskvisorWindow_ListWindowsError(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("ListWindows", "sess-1").Return(nil, errors.New("tmux error"))

	manager := NewSessionManager(mockExec)
	err := manager.EnsureTaskvisorWindow("sess-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list windows")
	mockExec.AssertNotCalled(t, "CreateWindow", mock.Anything, mock.Anything, mock.Anything)
	mockExec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestEnsureTaskvisorWindow_CreateWindowError(t *testing.T) {
	mockExec := new(MockTmuxExecutor)

	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	mockExec.On("CreateWindow", "sess-1", "taskvisor", "").Return("", errors.New("create failed"))

	manager := NewSessionManager(mockExec)
	err := manager.EnsureTaskvisorWindow("sess-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create taskvisor window")
	mockExec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

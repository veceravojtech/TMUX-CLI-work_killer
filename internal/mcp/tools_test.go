package mcp

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

// TestServer_WindowsList_Success_MultipleWindows verifies that WindowsList
// returns all windows from a session with multiple windows.
func TestServer_WindowsList_Success_MultipleWindows(t *testing.T) {
	// Arrange: Mock SessionStore with test data
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main", UUID: "uuid-1"},
				{TmuxWindowID: "@1", Name: "editor", UUID: "uuid-2"},
				{TmuxWindowID: "@2", Name: "logs", UUID: "uuid-3"},
			},
		},
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsList
	windows, err := server.WindowsList()

	// Assert: Verify results (only names, no IDs or UUIDs)
	require.NoError(t, err)
	assert.Len(t, windows, 3)
	assert.Equal(t, "main", windows[0].Name)
	assert.Equal(t, "editor", windows[1].Name)
	assert.Equal(t, "logs", windows[2].Name)
}

// TestServer_WindowsList_Success_SingleWindow verifies that WindowsList
// works correctly with a session containing only one window.
func TestServer_WindowsList_Success_SingleWindow(t *testing.T) {
	// Arrange: Session with single window
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "single-window-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main", UUID: "uuid-1"},
			},
		},
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act
	windows, err := server.WindowsList()

	// Assert (only names, no IDs or UUIDs)
	require.NoError(t, err)
	assert.Len(t, windows, 1)
	assert.Equal(t, "main", windows[0].Name)
}

// TestServer_WindowsList_Success_EmptyWindows verifies that WindowsList
// returns an empty array (not nil) when session has no windows.
func TestServer_WindowsList_Success_EmptyWindows(t *testing.T) {
	// Arrange: Session with no windows
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "empty-session",
			Windows:   []store.Window{}, // Empty array
		},
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test",
		sessionFile: "/test/.tmux-cli-session.json",
	}

	// Act
	windows, err := server.WindowsList()

	// Assert: Returns empty array, not nil
	require.NoError(t, err)
	assert.NotNil(t, windows)
	assert.Len(t, windows, 0)
}

// TestServer_WindowsList_Error_SessionNotFound verifies that WindowsList
// returns ErrSessionNotFound with context when session file cannot be loaded.
func TestServer_WindowsList_Error_SessionNotFound(t *testing.T) {
	// Arrange: Mock store returns error
	mockStore := &mockSessionStore{
		loadError: store.ErrSessionNotFound,
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act
	windows, err := server.WindowsList()

	// Assert: Error is wrapped with context
	require.Error(t, err)
	assert.Nil(t, windows)
	assert.True(t, errors.Is(err, ErrSessionNotFound), "Error should wrap ErrSessionNotFound")
	assert.Contains(t, err.Error(), "/test/dir", "Error should include working directory in context")
}

// ========================================
// resolveWindowIdentifier Tests
// ========================================

// TestResolveWindowIdentifier_WithWindowID verifies that identifiers
// starting with "@" are returned as-is (treated as window IDs).
func TestResolveWindowIdentifier_WithWindowID(t *testing.T) {
	// Arrange: Session with windows
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "main"},
			{TmuxWindowID: "@1", Name: "editor"},
		},
	}

	// Act: Resolve identifier starting with "@"
	windowID, err := resolveWindowIdentifier(session, "@0")

	// Assert: Returns as-is without lookup
	require.NoError(t, err)
	assert.Equal(t, "@0", windowID)
}

// TestResolveWindowIdentifier_WithWindowID_NonExistent verifies that
// window IDs starting with "@" are returned even if they don't exist in session.
// The existence check happens in the calling function (WindowsSend).
func TestResolveWindowIdentifier_WithWindowID_NonExistent(t *testing.T) {
	// Arrange: Session with windows @0 and @1
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "main"},
		},
	}

	// Act: Request non-existent ID @99
	windowID, err := resolveWindowIdentifier(session, "@99")

	// Assert: Returns as-is (existence check is caller's responsibility)
	require.NoError(t, err)
	assert.Equal(t, "@99", windowID)
}

// TestResolveWindowIdentifier_WithValidName verifies that a valid
// window name resolves to the correct window ID.
func TestResolveWindowIdentifier_WithValidName(t *testing.T) {
	// Arrange: Session with named windows
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "supervisor"},
			{TmuxWindowID: "@1", Name: "bmad-worker"},
			{TmuxWindowID: "@2", Name: "dev-server"},
		},
	}

	// Act: Resolve by window name
	windowID, err := resolveWindowIdentifier(session, "bmad-worker")

	// Assert: Returns correct window ID
	require.NoError(t, err)
	assert.Equal(t, "@1", windowID)
}

// TestResolveWindowIdentifier_WithInvalidName verifies that an invalid
// window name returns ErrWindowNotFound with available names listed.
func TestResolveWindowIdentifier_WithInvalidName(t *testing.T) {
	// Arrange: Session with windows
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "supervisor"},
			{TmuxWindowID: "@1", Name: "bmad-worker"},
		},
	}

	// Act: Try to resolve non-existent name
	windowID, err := resolveWindowIdentifier(session, "invalid")

	// Assert: Returns error with helpful context
	require.Error(t, err)
	assert.Empty(t, windowID)
	assert.True(t, errors.Is(err, ErrWindowNotFound))
	assert.Contains(t, err.Error(), "window name \"invalid\" not found")
	assert.Contains(t, err.Error(), "supervisor")
	assert.Contains(t, err.Error(), "bmad-worker")
}

// TestResolveWindowIdentifier_CaseSensitive verifies that window name
// matching is case-sensitive (e.g., "Supervisor" != "supervisor").
func TestResolveWindowIdentifier_CaseSensitive(t *testing.T) {
	// Arrange: Session with lowercase window names
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "supervisor"},
			{TmuxWindowID: "@1", Name: "worker"},
		},
	}

	// Act: Try uppercase variant
	windowID, err := resolveWindowIdentifier(session, "Supervisor")

	// Assert: Case mismatch returns error
	require.Error(t, err)
	assert.Empty(t, windowID)
	assert.True(t, errors.Is(err, ErrWindowNotFound))
	assert.Contains(t, err.Error(), "window name \"Supervisor\" not found")
}

// TestResolveWindowIdentifier_EmptyIdentifier verifies that empty
// identifier returns ErrInvalidWindowID.
func TestResolveWindowIdentifier_EmptyIdentifier(t *testing.T) {
	// Arrange: Session with windows
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "main"},
		},
	}

	// Act: Try empty identifier
	windowID, err := resolveWindowIdentifier(session, "")

	// Assert: Returns validation error
	require.Error(t, err)
	assert.Empty(t, windowID)
	assert.True(t, errors.Is(err, ErrInvalidWindowID))
	assert.Contains(t, err.Error(), "identifier cannot be empty")
}

// TestResolveWindowIdentifier_FirstMatch verifies that when multiple
// windows have the same name, the first match is returned.
func TestResolveWindowIdentifier_FirstMatch(t *testing.T) {
	// Arrange: Session with duplicate window names (tmux allows this)
	session := &store.Session{
		SessionID: "test-session",
		Windows: []store.Window{
			{TmuxWindowID: "@0", Name: "worker"},
			{TmuxWindowID: "@1", Name: "worker"}, // Duplicate name
			{TmuxWindowID: "@2", Name: "main"},
		},
	}

	// Act: Resolve duplicate name
	windowID, err := resolveWindowIdentifier(session, "worker")

	// Assert: Returns first match
	require.NoError(t, err)
	assert.Equal(t, "@0", windowID, "Should return first matching window")
}

// TestResolveWindowIdentifier_WithEmptySession verifies behavior
// when session has no windows.
func TestResolveWindowIdentifier_WithEmptySession(t *testing.T) {
	// Arrange: Session with no windows
	session := &store.Session{
		SessionID: "empty-session",
		Windows:   []store.Window{},
	}

	// Act: Try to resolve a name
	windowID, err := resolveWindowIdentifier(session, "anything")

	// Assert: Returns not found error with empty available list
	require.Error(t, err)
	assert.Empty(t, windowID)
	assert.True(t, errors.Is(err, ErrWindowNotFound))
	assert.Contains(t, err.Error(), "window name \"anything\" not found")
	assert.Contains(t, err.Error(), "[]") // Empty available names list
}

// ========================================
// WindowsSend Tests
// ========================================

// TestServer_WindowsSend_Success verifies that WindowsSend
// sends a command to a window successfully and returns true.
func TestServer_WindowsSend_Success(t *testing.T) {
	// Arrange: Mock store with session, mock executor
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main"},
			},
		},
	}

	var commandSent bool
	var capturedSessionID, capturedWindowID, capturedMessage string
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("SendMessageWithDelay", "test-session", "@0", "npm run build").Return(nil).Run(func(args mock.Arguments) {
		capturedSessionID = args.String(0)
		capturedWindowID = args.String(1)
		capturedMessage = args.String(2)
		commandSent = true
	})

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Use @-prefixed window ID
	success, err := server.WindowsSend("@0", "npm run build")

	// Assert
	require.NoError(t, err)
	assert.True(t, success)
	assert.True(t, commandSent, "SendMessageWithDelay should have been called")
	assert.Equal(t, "test-session", capturedSessionID)
	assert.Equal(t, "@0", capturedWindowID)
	assert.Equal(t, "npm run build", capturedMessage)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsSend_Error_EmptyWindowID verifies that WindowsSend
// returns ErrInvalidWindowID when given an empty window identifier.
func TestServer_WindowsSend_Error_EmptyWindowID(t *testing.T) {
	// Arrange
	server := &Server{workingDir: "/test"}

	// Act: Request with empty window identifier
	success, err := server.WindowsSend("", "npm run build")

	// Assert
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrInvalidWindowID), "Error should wrap ErrInvalidWindowID")
	assert.Contains(t, err.Error(), "windowIdentifier cannot be empty")
}

// TestServer_WindowsSend_Error_EmptyCommand verifies that WindowsSend
// returns ErrInvalidInput when given an empty command.
func TestServer_WindowsSend_Error_EmptyCommand(t *testing.T) {
	// Arrange
	server := &Server{workingDir: "/test"}

	// Act: Request with empty command
	success, err := server.WindowsSend("0", "")

	// Assert
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrInvalidInput), "Error should wrap ErrInvalidInput")
	assert.Contains(t, err.Error(), "command cannot be empty")
}

// TestServer_WindowsSend_Error_WindowNotFound verifies that WindowsSend
// returns ErrWindowNotFound when the requested window doesn't exist.
func TestServer_WindowsSend_Error_WindowNotFound(t *testing.T) {
	// Arrange: Session with window "@0", request window "@99"
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{{TmuxWindowID: "@0", Name: "main"}},
		},
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	// Act: Request non-existent window ID
	success, err := server.WindowsSend("@99", "npm run build")

	// Assert
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrWindowNotFound), "Error should wrap ErrWindowNotFound")
	assert.Contains(t, err.Error(), "windowID=@99")
	assert.Contains(t, err.Error(), "session=test-session")
}

// TestServer_WindowsSend_Error_SessionNotFound verifies that WindowsSend
// returns ErrSessionNotFound when session file cannot be loaded.
func TestServer_WindowsSend_Error_SessionNotFound(t *testing.T) {
	// Arrange: Mock store returns error
	mockStore := &mockSessionStore{
		loadError: store.ErrSessionNotFound,
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test/dir",
	}

	// Act
	success, err := server.WindowsSend("0", "npm run build")

	// Assert: Error is wrapped with context
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrSessionNotFound), "Error should wrap ErrSessionNotFound")
	assert.Contains(t, err.Error(), "/test/dir", "Error should include working directory in context")
}

// TestServer_WindowsSend_Error_TmuxCommandFailed verifies that WindowsSend
// returns ErrTmuxCommandFailed when tmux send-keys command fails.
func TestServer_WindowsSend_Error_TmuxCommandFailed(t *testing.T) {
	// Arrange: Window exists but tmux command fails
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{{TmuxWindowID: "@0", Name: "main"}},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("SendMessageWithDelay", "test-session", "@0", "npm run build").Return(errors.New("tmux not running"))

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Use @-prefixed window ID
	success, err := server.WindowsSend("@0", "npm run build")

	// Assert
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrTmuxCommandFailed), "Error should wrap ErrTmuxCommandFailed")
	assert.Contains(t, err.Error(), "session=test-session")
	assert.Contains(t, err.Error(), "window=@0")
	assert.Contains(t, err.Error(), "npm run build")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsSend_SpecialCharacters verifies that WindowsSend
// sends commands with special characters exactly as provided (no modification).
func TestServer_WindowsSend_SpecialCharacters(t *testing.T) {
	// Arrange: Test that commands with special characters are sent as-is
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{{TmuxWindowID: "@0", Name: "main"}},
		},
	}

	specialCommand := "echo 'Hello World' && ls -la"
	var capturedCommand string
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("SendMessageWithDelay", "test-session", "@0", specialCommand).Return(nil).Run(func(args mock.Arguments) {
		capturedCommand = args.String(2)
	})

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Use @-prefixed window ID
	success, err := server.WindowsSend("@0", specialCommand)

	// Assert
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, specialCommand, capturedCommand, "Command should be sent exactly as provided")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsSend_WithWindowName verifies that WindowsSend
// resolves window name to ID and sends command successfully.
func TestServer_WindowsSend_WithWindowName(t *testing.T) {
	// Arrange: Session with named windows
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor"},
				{TmuxWindowID: "@1", Name: "bmad-worker"},
			},
		},
	}

	var capturedWindowID string
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("SendMessageWithDelay", "test-session", "@1", "echo test").Return(nil).Run(func(args mock.Arguments) {
		capturedWindowID = args.String(1)
	})

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Send using window name instead of ID
	success, err := server.WindowsSend("bmad-worker", "echo test")

	// Assert: Name resolved to @1 and command sent
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "@1", capturedWindowID, "Window name should resolve to correct ID")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsSend_WithWindowName_NotFound verifies that WindowsSend
// returns helpful error when window name doesn't exist.
func TestServer_WindowsSend_WithWindowName_NotFound(t *testing.T) {
	// Arrange: Session with windows
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor"},
				{TmuxWindowID: "@1", Name: "bmad-worker"},
			},
		},
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	// Act: Try to send to non-existent window name
	success, err := server.WindowsSend("invalid-name", "echo test")

	// Assert: Returns helpful error with available names
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrWindowNotFound))
	assert.Contains(t, err.Error(), "window name \"invalid-name\" not found")
	assert.Contains(t, err.Error(), "supervisor")
	assert.Contains(t, err.Error(), "bmad-worker")
}

// TestServer_WindowsSend_WithWindowID_BackwardCompatibility verifies that
// existing behavior with "@" prefix window IDs still works (backward compatible).
func TestServer_WindowsSend_WithWindowID_BackwardCompatibility(t *testing.T) {
	// Arrange: Session with @-prefixed window IDs
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main"},
				{TmuxWindowID: "@1", Name: "editor"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("SendMessageWithDelay", "test-session", "@0", "echo test").Return(nil)

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Use @-prefixed ID (existing behavior)
	success, err := server.WindowsSend("@0", "echo test")

	// Assert: Works as before
	require.NoError(t, err)
	assert.True(t, success)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsSend_WindowNameCaseSensitive verifies that window name
// matching is case-sensitive.
func TestServer_WindowsSend_WindowNameCaseSensitive(t *testing.T) {
	// Arrange: Session with lowercase window name
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor"},
			},
		},
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	// Act: Try uppercase variant
	success, err := server.WindowsSend("Supervisor", "echo test")

	// Assert: Case mismatch returns error
	require.Error(t, err)
	assert.False(t, success)
	assert.True(t, errors.Is(err, ErrWindowNotFound))
	assert.Contains(t, err.Error(), "window name \"Supervisor\" not found")
}

// ============================================================================
// WindowsCreate Tests
// ============================================================================

// TestServer_WindowsCreate_Success_NameOnly verifies that WindowsCreate
// creates a window with only a name (no command) and sets UUID, exports env, etc.
func TestServer_WindowsCreate_Success_NameOnly(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "existing", UUID: "uuid-1"},
			},
		},
	}

	createdWindowID := "1"
	mockExecutor := &testutil.MockTmuxExecutor{}
	// Mock recovery check (session exists, no recovery needed)
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	// Mock window creation with "zsh" (command parameter ignored)
	mockExecutor.On("CreateWindow", "test-session", "new-window", "zsh").Return(createdWindowID, nil)
	// Mock UUID option setting
	mockExecutor.On("SetWindowOption", "test-session", createdWindowID, "window-uuid", mock.MatchedBy(func(uuid string) bool {
		// Verify UUID is valid format
		return len(uuid) == 36 // UUID v4 format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	})).Return(nil)
	// Mock environment variable export
	mockExecutor.On("SendMessage", "test-session", createdWindowID, mock.MatchedBy(func(cmd string) bool {
		// Verify export command format
		return strings.HasPrefix(cmd, "export TMUX_WINDOW_UUID=\"") && strings.HasSuffix(cmd, "\"")
	})).Return(nil)
	// Mock postcommand execution (SendMessageWithFeedback returns output)
	// Note: This is called conditionally based on postcommand config, use Maybe()
	mockExecutor.On("SendMessageWithFeedback", "test-session", createdWindowID, mock.Anything).Return("", nil).Maybe()

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate("new-window", "ignored-command")

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Equal(t, createdWindowID, window.TmuxWindowID)
	assert.Equal(t, "new-window", window.Name)
	assert.NotEmpty(t, window.UUID, "UUID should be generated and set")
	// Verify UUID is valid v4 format
	err = session.ValidateUUID(window.UUID)
	assert.NoError(t, err, "UUID should be valid v4 format")
	// Verify window was added to session
	assert.Len(t, mockStore.session.Windows, 2, "Session should have original + new window")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsCreate_Success_WithCommand verifies that WindowsCreate
// creates a window with name and IGNORES command parameter (uses zsh).
func TestServer_WindowsCreate_Success_WithCommand(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	// Mock recovery check
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	// CRITICAL: Command parameter ignored, always uses "zsh"
	mockExecutor.On("CreateWindow", "test-session", "build-window", "zsh").Return("@2", nil)
	// Mock UUID setup
	mockExecutor.On("SetWindowOption", "test-session", "@2", "window-uuid", mock.Anything).Return(nil)
	mockExecutor.On("SendMessage", "test-session", "@2", mock.Anything).Return(nil)
	mockExecutor.On("SendMessageWithFeedback", "test-session", "@2", mock.Anything).Return("", nil).Maybe()

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act: Pass command parameter (should be ignored)
	window, err := server.WindowsCreate("build-window", "npm run build")

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Equal(t, "@2", window.TmuxWindowID)
	assert.Equal(t, "build-window", window.Name)
	assert.NotEmpty(t, window.UUID)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsCreate_Error_EmptyName verifies that WindowsCreate
// returns ErrInvalidInput when name is empty.
func TestServer_WindowsCreate_Error_EmptyName(t *testing.T) {
	// Arrange
	server := &Server{workingDir: "/test"}

	// Act
	window, err := server.WindowsCreate("", "npm test")

	// Assert
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrInvalidInput), "Error should wrap ErrInvalidInput")
	assert.Contains(t, err.Error(), "name cannot be empty")
}

// TestServer_WindowsCreate_Error_SessionNotFound verifies that WindowsCreate
// returns ErrSessionNotFound when session file cannot be loaded.
func TestServer_WindowsCreate_Error_SessionNotFound(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		loadError: errors.New("session file not found"),
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate("test-window", "")

	// Assert
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrSessionNotFound), "Error should wrap ErrSessionNotFound")
	assert.Contains(t, err.Error(), "/test")
}

// TestServer_WindowsCreate_CleanupOnSetWindowOptionFailure verifies that
// window is killed if SetWindowOption fails during UUID setup.
func TestServer_WindowsCreate_CleanupOnSetWindowOptionFailure(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{},
		},
	}

	createdWindowID := "1"
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	mockExecutor.On("CreateWindow", "test-session", "test-window", "zsh").Return(createdWindowID, nil)
	// SetWindowOption fails
	mockExecutor.On("SetWindowOption", "test-session", createdWindowID, "window-uuid", mock.Anything).
		Return(errors.New("tmux option set failed"))
	// Cleanup: window should be killed
	mockExecutor.On("KillWindow", "test-session", createdWindowID).Return(nil)

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate("test-window", "")

	// Assert: Error returned, window cleaned up
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrTmuxCommandFailed))
	assert.Contains(t, err.Error(), "set window UUID")
	mockExecutor.AssertExpectations(t) // Verify KillWindow was called
}

// TestServer_WindowsCreate_CleanupOnSendMessageFailure verifies that
// window is killed if SendMessage fails during UUID export.
func TestServer_WindowsCreate_CleanupOnSendMessageFailure(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{},
		},
	}

	createdWindowID := "1"
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	mockExecutor.On("CreateWindow", "test-session", "test-window", "zsh").Return(createdWindowID, nil)
	mockExecutor.On("SetWindowOption", "test-session", createdWindowID, "window-uuid", mock.Anything).Return(nil)
	// SendMessage fails (UUID export fails)
	mockExecutor.On("SendMessage", "test-session", createdWindowID, mock.Anything).
		Return(errors.New("tmux send-keys failed"))
	// Cleanup: window should be killed
	mockExecutor.On("KillWindow", "test-session", createdWindowID).Return(nil)

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate("test-window", "")

	// Assert: Error returned, window cleaned up
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrTmuxCommandFailed))
	assert.Contains(t, err.Error(), "export TMUX_WINDOW_UUID")
	mockExecutor.AssertExpectations(t) // Verify KillWindow was called
}

// TestServer_WindowsCreate_Error_TmuxCommandFailed verifies that WindowsCreate
// returns ErrWindowCreateFailed when tmux command fails.
func TestServer_WindowsCreate_Error_TmuxCommandFailed(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	// Window creation fails
	mockExecutor.On("CreateWindow", "test-session", "test-window", "zsh").Return("", errors.New("tmux not running"))

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate("test-window", "")

	// Assert
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrWindowCreateFailed), "Error should wrap ErrWindowCreateFailed")
	assert.Contains(t, err.Error(), "test-session")
	assert.Contains(t, err.Error(), "test-window")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsCreate_SpecialCharacters verifies that WindowsCreate
// handles window names with special characters correctly.
func TestServer_WindowsCreate_SpecialCharacters(t *testing.T) {
	// Arrange: Test that window names with special characters work
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows:   []store.Window{},
		},
	}

	specialName := "my-build-2024_v1.0"
	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	mockExecutor.On("CreateWindow", "test-session", specialName, "zsh").Return("3", nil)
	mockExecutor.On("SetWindowOption", "test-session", "3", "window-uuid", mock.Anything).Return(nil)
	mockExecutor.On("SendMessage", "test-session", "3", mock.Anything).Return(nil)
	mockExecutor.On("SendMessageWithFeedback", "test-session", "3", mock.Anything).Return("", nil).Maybe()

	server := &Server{
		store:      mockStore,
		executor:   mockExecutor,
		workingDir: "/test",
	}

	// Act
	window, err := server.WindowsCreate(specialName, "")

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, window)
	assert.Equal(t, "3", window.TmuxWindowID)
	assert.Equal(t, specialName, window.Name)
	assert.NotEmpty(t, window.UUID)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsCreate_Error_DuplicateName_ExactMatch verifies that WindowsCreate
// returns ErrWindowCreateFailed when a window with the exact same name already exists.
func TestServer_WindowsCreate_Error_DuplicateName_ExactMatch(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
			},
		},
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	// Act - attempt to create window with duplicate name
	window, err := server.WindowsCreate("supervisor", "")

	// Assert
	require.Error(t, err)
	assert.Nil(t, window)
	assert.True(t, errors.Is(err, ErrWindowCreateFailed), "Error should wrap ErrWindowCreateFailed")
	assert.Contains(t, err.Error(), "window name \"supervisor\" already exists")
	assert.Contains(t, err.Error(), "case-insensitive match")
}

// TestServer_WindowsCreate_Error_DuplicateName_CaseInsensitive verifies that WindowsCreate
// returns ErrWindowCreateFailed when a window with the same name (different case) already exists.
func TestServer_WindowsCreate_Error_DuplicateName_CaseInsensitive(t *testing.T) {
	// Arrange
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "hook-test", UUID: "uuid-1"},
			},
		},
	}

	server := &Server{
		store:      mockStore,
		workingDir: "/test",
	}

	testCases := []struct {
		name      string
		inputName string
	}{
		{"uppercase", "HOOK-TEST"},
		{"mixed case", "Hook-Test"},
		{"alternate case", "HOOK-test"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			window, err := server.WindowsCreate(tc.inputName, "")

			// Assert
			require.Error(t, err)
			assert.Nil(t, window)
			assert.True(t, errors.Is(err, ErrWindowCreateFailed), "Error should wrap ErrWindowCreateFailed")
			assert.Contains(t, err.Error(), "already exists")
			assert.Contains(t, err.Error(), "hook-test", "should mention existing window name")
		})
	}
}

// TestServer_WindowsKill_WithWindowName tests WindowsKill with window names
func TestServer_WindowsKill_WithWindowName(t *testing.T) {
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
				{TmuxWindowID: "@1", Name: "bmad-worker", UUID: "uuid-2"},
			},
		},
		saveError: nil,
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	mockExecutor.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "bmad-worker"},
	}, nil)
	mockExecutor.On("KillWindow", "test-session", "@1").Return(nil)

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	success, err := server.WindowsKill("bmad-worker")
	require.NoError(t, err)
	assert.True(t, success)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsKill_WithWindowID_Error tests that WindowsKill rejects window IDs
func TestServer_WindowsKill_WithWindowID_Error(t *testing.T) {
	server := &Server{
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Try to kill using window ID (should fail)
	success, err := server.WindowsKill("@1")

	// Assert: Returns error for window IDs
	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrInvalidWindowID)
	assert.Contains(t, err.Error(), "window IDs not allowed")
	assert.Contains(t, err.Error(), "@1")
}

// TestServer_WindowsKill_WindowNotFound_Strict tests that WindowsKill returns error when window doesn't exist
func TestServer_WindowsKill_WindowNotFound_Strict(t *testing.T) {
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
				{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-2"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(true, nil)
	mockExecutor.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "worker"},
	}, nil)

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Try to kill non-existent window by name
	success, err := server.WindowsKill("nonexistent")

	// Assert: Returns error from name resolution (strict mode)
	require.Error(t, err)
	assert.False(t, success)
	assert.ErrorIs(t, err, ErrWindowNotFound)
	assert.Contains(t, err.Error(), "nonexistent")
	assert.Contains(t, err.Error(), "not found")
	// Note: ListWindows should NOT be called because name resolution fails first
}

// TestServer_WindowsKill_TmuxNotRunning_Error tests that WindowsKill returns error when tmux is not running
func TestServer_WindowsKill_TmuxNotRunning_Error(t *testing.T) {
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
			},
		},
	}

	mockExecutor := &testutil.MockTmuxExecutor{}
	mockExecutor.On("HasSession", "test-session").Return(false, errors.New("tmux not running"))

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Try to kill when tmux is not running
	success, err := server.WindowsKill("supervisor")

	// Assert: Returns error during recovery check (before ListWindows is called)
	require.Error(t, err)
	assert.False(t, success)
	assert.Contains(t, err.Error(), "check recovery needed")
	assert.Contains(t, err.Error(), "tmux not running")
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsMessage_Success verifies that WindowsMessage
// sends a formatted message to a receiver window with auto-detected sender.
func TestServer_WindowsMessage_Success(t *testing.T) {
	// Arrange: Set environment variable to simulate running from "supervisor" window
	os.Setenv("TMUX_WINDOW_UUID", "uuid-supervisor")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	// Arrange: Mock SessionStore with test session
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID:   "sender-session-uuid",
			ProjectPath: "/home/user/projects/my-project",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-supervisor"},
				{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-worker"},
			},
		},
	}

	// Mock executor to verify formatted message is sent with sender window name
	mockExecutor := new(testutil.MockTmuxExecutor)
	expectedMessage := "New message from: supervisor\n\nPlease run the build command.\n"
	mockExecutor.On("SendMessageWithDelay", "sender-session-uuid", "@1", expectedMessage).Return(nil)

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage with receiver window name
	success, sender, err := server.WindowsMessage("worker", "Please run the build command.")

	// Assert: Verify results (sender should be window name "supervisor")
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "supervisor", sender)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsMessage_Success_WithWindowID verifies that WindowsMessage
// works with window ID (e.g., "@1") instead of window name.
func TestServer_WindowsMessage_Success_WithWindowID(t *testing.T) {
	// Arrange: Set environment variable to simulate running from "main" window
	os.Setenv("TMUX_WINDOW_UUID", "uuid-main")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	// Arrange: Mock SessionStore
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID:   "test-session",
			ProjectPath: "/projects/tmux-cli",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "main", UUID: "uuid-main"},
				{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-worker"},
			},
		},
	}

	// Mock executor (sender should be window name "main")
	mockExecutor := new(testutil.MockTmuxExecutor)
	expectedMessage := "New message from: main\n\nStatus update\n"
	mockExecutor.On("SendMessageWithDelay", "test-session", "@1", expectedMessage).Return(nil)

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage with window ID
	success, sender, err := server.WindowsMessage("@1", "Status update")

	// Assert (sender should be window name "main")
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "main", sender)
	mockExecutor.AssertExpectations(t)
}

// TestServer_WindowsMessage_Error_EmptyReceiver verifies that WindowsMessage
// returns ErrInvalidInput when receiver is empty.
func TestServer_WindowsMessage_Error_EmptyReceiver(t *testing.T) {
	// Arrange: Server with mock store (won't be called)
	server := &Server{
		store:       &mockSessionStore{},
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage with empty receiver
	success, sender, err := server.WindowsMessage("", "test message")

	// Assert: Verify error
	require.Error(t, err)
	assert.False(t, success)
	assert.Empty(t, sender)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "receiver cannot be empty")
}

// TestServer_WindowsMessage_Error_EmptyMessage verifies that WindowsMessage
// returns ErrInvalidInput when message is empty.
func TestServer_WindowsMessage_Error_EmptyMessage(t *testing.T) {
	// Arrange: Server with mock store (won't be called)
	server := &Server{
		store:       &mockSessionStore{},
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage with empty message
	success, sender, err := server.WindowsMessage("worker", "")

	// Assert: Verify error
	require.Error(t, err)
	assert.False(t, success)
	assert.Empty(t, sender)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "message cannot be empty")
}

// TestServer_WindowsMessage_Error_ReceiverNotFound verifies that WindowsMessage
// returns ErrWindowNotFound with helpful error when receiver doesn't exist.
func TestServer_WindowsMessage_Error_ReceiverNotFound(t *testing.T) {
	// Arrange: Mock SessionStore with known windows
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID: "test-session",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-1"},
				{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-2"},
			},
		},
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage with non-existent receiver
	success, sender, err := server.WindowsMessage("invalid-window", "test message")

	// Assert: Verify error includes available windows
	require.Error(t, err)
	assert.False(t, success)
	assert.Empty(t, sender)
	assert.ErrorIs(t, err, ErrWindowNotFound)
	assert.Contains(t, err.Error(), "invalid-window")
	assert.Contains(t, err.Error(), "supervisor")
	assert.Contains(t, err.Error(), "worker")
}

// TestServer_WindowsMessage_Error_SessionNotFound verifies that WindowsMessage
// returns ErrSessionNotFound when session load fails.
func TestServer_WindowsMessage_Error_SessionNotFound(t *testing.T) {
	// Arrange: Mock SessionStore that returns load error
	mockStore := &mockSessionStore{
		loadError: errors.New("session file not found"),
	}

	server := &Server{
		store:       mockStore,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage
	success, sender, err := server.WindowsMessage("worker", "test message")

	// Assert: Verify error
	require.Error(t, err)
	assert.False(t, success)
	assert.Empty(t, sender)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.Contains(t, err.Error(), "/test/dir")
	assert.Contains(t, err.Error(), ".tmux-cli-session.json")
}

// TestServer_WindowsMessage_Error_SendMessageFailed verifies that WindowsMessage
// returns ErrTmuxCommandFailed when executor.SendMessage fails.
func TestServer_WindowsMessage_Error_SendMessageFailed(t *testing.T) {
	// Arrange: Set environment variable to simulate running from "supervisor" window
	os.Setenv("TMUX_WINDOW_UUID", "uuid-supervisor")
	defer os.Unsetenv("TMUX_WINDOW_UUID")

	// Arrange: Mock SessionStore with valid session
	mockStore := &mockSessionStore{
		session: &store.Session{
			SessionID:   "test-session",
			ProjectPath: "/var/projects/api-server",
			Windows: []store.Window{
				{TmuxWindowID: "@0", Name: "supervisor", UUID: "uuid-supervisor"},
				{TmuxWindowID: "@1", Name: "worker", UUID: "uuid-worker"},
			},
		},
	}

	// Mock executor that returns error (sender should be window name "supervisor")
	mockExecutor := new(testutil.MockTmuxExecutor)
	expectedMessage := "New message from: supervisor\n\ntest message\n"
	tmuxError := errors.New("tmux send-keys failed")
	mockExecutor.On("SendMessageWithDelay", "test-session", "@1", expectedMessage).Return(tmuxError)

	server := &Server{
		store:       mockStore,
		executor:    mockExecutor,
		workingDir:  "/test/dir",
		sessionFile: "/test/dir/.tmux-cli-session.json",
	}

	// Act: Call WindowsMessage
	success, sender, err := server.WindowsMessage("worker", "test message")

	// Assert: Verify error
	require.Error(t, err)
	assert.False(t, success)
	assert.Empty(t, sender)
	assert.ErrorIs(t, err, ErrTmuxCommandFailed)
	assert.Contains(t, err.Error(), "test-session")
	assert.Contains(t, err.Error(), "@1")
	mockExecutor.AssertExpectations(t)
}

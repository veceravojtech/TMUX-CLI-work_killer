package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupNewSessionMock(t *testing.T, mockExec *testutil.MockTmuxExecutor) {
	t.Helper()
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", mock.AnythingOfType("string")).Return("", nil)
	mockExec.On("HasSession", mock.AnythingOfType("string")).Return(false, nil).Once()
	mockExec.On("HasSession", mock.AnythingOfType("string")).Return(true, nil)
	mockExec.On("CreateSession", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SetSessionEnvironment", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)
	mockExec.On("ListWindows", mock.AnythingOfType("string")).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	mockExec.On("SetWindowOption", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockExec.On("SendMessage", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockExec.On("SendMessageWithFeedback", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	mockExec.On("CreateWindow", mock.Anything, "taskvisor", "").Return("@1", nil)
	mockExec.On("AttachSession", mock.AnythingOfType("string")).Return(nil)
}

func TestProjectCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"project"})
	require.NoError(t, err)
	assert.NotNil(t, cmd)
}

func TestProjectInitCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"project", "init"})
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "init")
}

func TestProjectInitCmd_AcceptsOptionalPath(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"project", "init"})
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"/tmp/test"}))
	assert.Error(t, cmd.Args(cmd, []string{"/tmp/a", "/tmp/b"}))
}

func TestProjectInitCmd_HasNoAttachFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"project", "init"})
	require.NoError(t, err)
	require.NotNil(t, cmd)
	flag := cmd.Flags().Lookup("no-attach")
	assert.NotNil(t, flag, "--no-attach flag should exist")
}

func TestRunProjectInit_ScaffoldsNewProject(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "myproject")

	mockExec := new(testutil.MockTmuxExecutor)
	setupNewSessionMock(t, mockExec)

	_ = captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, projectDir, true)
		require.NoError(t, err)
	})

	assert.FileExists(t, filepath.Join(projectDir, ".tmux-cli", "setting.yaml"))
	assert.DirExists(t, filepath.Join(projectDir, ".tmux-cli", "goals"))
	assert.FileExists(t, filepath.Join(projectDir, "CLAUDE.md"))

	content, err := os.ReadFile(filepath.Join(projectDir, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, "# myproject\n", string(content))
}

func TestRunProjectInit_IdempotentScaffold(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli", "goals"), 0o755))

	customSettings := "hooks:\n  session_notify: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte(customSettings), 0o644))

	customClaude := "# Custom content\nDo not overwrite.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(customClaude), 0o644))

	mockExec := new(testutil.MockTmuxExecutor)
	setupNewSessionMock(t, mockExec)

	_ = captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, dir, true)
		require.NoError(t, err)
	})

	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, customClaude, string(got), "CLAUDE.md should not be overwritten")
}

func TestRunProjectInit_SkipsGitInitIfExists(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	setupNewSessionMock(t, mockExec)

	_ = captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, dir, true)
		assert.NoError(t, err)
	})
}

func TestRunProjectInit_ReusesExistingSession(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))

	existingID := "tmux-cli-existing-20260607T120000"
	canonicalPath, _ := filepath.EvalSymlinks(dir)

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", canonicalPath).Return(existingID, nil)
	mockExec.On("HasSession", existingID).Return(true, nil)
	mockExec.On("ListWindows", existingID).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		{TmuxWindowID: "@1", Name: "taskvisor", CurrentCommand: "tmux-cli"},
	}, nil)
	mockExec.On("CreateSession", mock.Anything, mock.Anything).Return(nil)

	output := captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, dir, true)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "Session: "+existingID)
	mockExec.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything)
}

func TestRunProjectInit_CreatesNewSession(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	setupNewSessionMock(t, mockExec)

	output := captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, dir, true)
		require.NoError(t, err)
	})

	assert.True(t, strings.HasPrefix(output, "Session: "), "output should start with 'Session: '")
	mockExec.AssertCalled(t, "CreateSession", mock.AnythingOfType("string"), mock.AnythingOfType("string"))
}

func TestRunProjectInit_PrintsSessionToStdout(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	setupNewSessionMock(t, mockExec)

	output := captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, dir, true)
		require.NoError(t, err)
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(lastLine, "Session: "), "last stdout line should have 'Session: ' prefix, got: %s", lastLine)
	sessionID := strings.TrimPrefix(lastLine, "Session: ")
	assert.True(t, strings.HasPrefix(sessionID, "tmux-cli-"), "session ID should start with 'tmux-cli-'")
}

func TestRunProjectInit_SymlinkResolution(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(realDir, ".git", "info"), 0o755))

	linkDir := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(realDir, linkDir))

	canonicalPath, err := filepath.EvalSymlinks(linkDir)
	require.NoError(t, err)

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", canonicalPath).Return("", nil)
	mockExec.On("HasSession", mock.AnythingOfType("string")).Return(false, nil).Once()
	mockExec.On("HasSession", mock.AnythingOfType("string")).Return(true, nil)
	mockExec.On("CreateSession", mock.AnythingOfType("string"), canonicalPath).Return(nil)
	mockExec.On("SetSessionEnvironment", mock.AnythingOfType("string"), "TMUX_CLI_PROJECT_PATH", canonicalPath).Return(nil)
	mockExec.On("SetSessionEnvironment", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)
	mockExec.On("ListWindows", mock.AnythingOfType("string")).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	mockExec.On("SetWindowOption", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockExec.On("SendMessage", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockExec.On("SendMessageWithFeedback", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	mockExec.On("CreateWindow", mock.Anything, "taskvisor", "").Return("@1", nil)

	_ = captureStdout(t, func() {
		err := runProjectInitWithExecutor(mockExec, linkDir, true)
		require.NoError(t, err)
	})

	mockExec.AssertCalled(t, "FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", canonicalPath)
	mockExec.AssertCalled(t, "CreateSession", mock.AnythingOfType("string"), canonicalPath)
}

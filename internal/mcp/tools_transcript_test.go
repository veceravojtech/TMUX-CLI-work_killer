package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestWindowsSpawnWorker_TranscriptsArmed_TeesCaptureThroughPaneLog proves the
// P3 privacy gate flips the spawn-time pane pipe from the plain pane-log pipe
// to a tee'd transcript capture command (one pipe per pane serves both), while
// the default unarmed state keeps PipePane (covered by the existing
// TestWindowsSpawnWorker_PipePane* tests).
func TestWindowsSpawnWorker_TranscriptsArmed_TeesCaptureThroughPaneLog(t *testing.T) {
	t.Setenv("TMUX_WINDOW_UUID", "")
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"),
		[]byte("telemetry:\n  enabled: true\n  transcripts: true\n"), 0o644))
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg, "tmux-cli"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cfg, "tmux-cli", "auth.json"),
		[]byte(`{"api_url":"https://tmux.vojta.ai","account":"t@example.com","access_token":"tok","refresh_token":"ref","expires_at":"2030-01-01T00:00:00Z","scopes":["telemetry:write"]}`), 0o600))

	paneLog := filepath.Join(dir, ".tmux-cli", "logs", "panes", "execute-1.log")
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("CreateWindow", "test-session", "execute-1", "").Return("@1", nil)
	mockExec.On("SetWindowOption", "test-session", "@1", "window-uuid", mock.AnythingOfType("string")).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", mock.MatchedBy(func(s string) bool {
		return strings.HasPrefix(s, "export TMUX_WINDOW_UUID=")
	})).Return(nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_MODEL").Return("", nil)
	mockExec.On("GetSessionEnvironment", mock.Anything, "TMUX_CLI_FLAGS").Return("", nil)
	mockExec.On("SendMessageWithFeedback", "test-session", "@1", mock.Anything).Return("", nil)
	mockExec.On("PipePaneCommand", "test-session", "@1", mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, `tee -a "`+paneLog+`"`) &&
			strings.Contains(cmd, "logs capture") &&
			strings.Contains(cmd, `--window "execute-1"`) &&
			strings.Contains(cmd, `--session "test-session"`) &&
			strings.Contains(cmd, `--dir "`+dir+`"`)
	})).Return(nil)
	mockExec.On("SendMessage", "test-session", "@1", "/tmux:execute").Return(nil)
	mockExec.On("SendMessageWithDelay", "test-session", "@1", mock.Anything).Return(nil)

	server := newTestServer(mockExec, dir)
	_, _, _, err := server.WindowsSpawnWorker("supervisor", "audit auth", "ctx.md", "check endpoints", "", "", "", "", "")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
	mockExec.AssertNotCalled(t, "PipePane", mock.Anything, mock.Anything, mock.Anything)
}

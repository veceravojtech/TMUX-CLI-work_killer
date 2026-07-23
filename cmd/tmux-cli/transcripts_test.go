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

func writeTranscriptSetting(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte(content), 0o644))
}

// fakeTranscriptLogin points the XDG-resolved auth store at a temp dir with a
// valid 0600 auth.json so the transcript gate sees a logged-in machine.
func fakeTranscriptLogin(t *testing.T) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"),
		[]byte(`{"api_url":"https://tmux.vojta.ai","account":"t@example.com","access_token":"tok","refresh_token":"ref","expires_at":"2030-01-01T00:00:00Z","scopes":["telemetry:write"]}`), 0o600))
}

func transcriptTestWindows() []tmux.WindowInfo {
	return []tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "taskvisor"},
		{TmuxWindowID: "@2", Name: "execute-1"},
		{TmuxWindowID: "@3", Name: "htop"}, // unmanaged — never captured
	}
}

func TestMaybeArmTranscripts_Armed_WiresManagedWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	writeTranscriptSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	fakeTranscriptLogin(t)

	exec := new(testutil.MockTmuxExecutor)
	exec.On("ListWindows", "sess-1").Return(transcriptTestWindows(), nil)
	for _, wid := range []string{"@0", "@1", "@2"} {
		exec.On("ClosePipePane", "sess-1", wid).Return(nil)
		exec.On("PipePaneCommand", "sess-1", wid, mock.Anything).Return(nil)
	}

	maybeArmTranscripts(dir, "sess-1", exec)

	exec.AssertExpectations(t)
	exec.AssertNotCalled(t, "PipePaneCommand", "sess-1", "@3", mock.Anything)

	// Content assertions: supervisor pipes straight into capture; the worker
	// window tees through its existing pane log.
	for _, call := range exec.Calls {
		if call.Method != "PipePaneCommand" {
			continue
		}
		cmd := call.Arguments.String(2)
		assert.Contains(t, cmd, "logs capture")
		assert.Contains(t, cmd, `--dir "`+dir+`"`)
		switch call.Arguments.String(1) {
		case "@0":
			assert.Contains(t, cmd, `--window "supervisor"`)
			assert.NotContains(t, cmd, "tee")
		case "@2":
			assert.Contains(t, cmd, `--window "execute-1"`)
			assert.Contains(t, cmd, "tee -a "+`"`+filepath.Join(dir, ".tmux-cli", "logs", "panes", "execute-1.log")+`"`)
		}
	}
}

func TestMaybeArmTranscripts_TranscriptsOff_WiresNothing(t *testing.T) {
	dir := t.TempDir()
	writeTranscriptSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: false\n")
	fakeTranscriptLogin(t)

	exec := new(testutil.MockTmuxExecutor)
	maybeArmTranscripts(dir, "sess-1", exec)

	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
	exec.AssertNotCalled(t, "PipePaneCommand", mock.Anything, mock.Anything, mock.Anything)
}

func TestMaybeArmTranscripts_TelemetryDisabled_WiresNothing(t *testing.T) {
	dir := t.TempDir()
	writeTranscriptSetting(t, dir, "telemetry:\n  enabled: false\n  transcripts: true\n")
	fakeTranscriptLogin(t)

	exec := new(testutil.MockTmuxExecutor)
	maybeArmTranscripts(dir, "sess-1", exec)

	exec.AssertNotCalled(t, "PipePaneCommand", mock.Anything, mock.Anything, mock.Anything)
}

func TestMaybeArmTranscripts_LoggedOut_WiresNothing(t *testing.T) {
	dir := t.TempDir()
	writeTranscriptSetting(t, dir, "telemetry:\n  enabled: true\n  transcripts: true\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no auth.json → logged out

	exec := new(testutil.MockTmuxExecutor)
	maybeArmTranscripts(dir, "sess-1", exec)

	exec.AssertNotCalled(t, "PipePaneCommand", mock.Anything, mock.Anything, mock.Anything)
}

// TestRunSessionStart_WiresTranscriptSweep content-asserts the start path
// invokes the transcript sweep (the gate tests above prove the sweep itself is
// a no-op unless armed).
func TestRunSessionStart_WiresTranscriptSweep(t *testing.T) {
	data, err := os.ReadFile("session.go")
	require.NoError(t, err)
	content := string(data)
	start := content[strings.Index(content, "func runSessionStart"):]
	start = start[:strings.Index(start, "\nfunc ")]
	assert.Contains(t, start, "maybeArmTranscripts(", "runSessionStart must arm transcript capture")
	attach := content[strings.Index(content, "func runStartAttach"):]
	attach = attach[:strings.Index(attach, "\nfunc ")]
	assert.Contains(t, attach, "maybeArmTranscripts(", "runStartAttach must arm transcript capture")
}

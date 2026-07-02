package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// noEnv is a Getenv stub that reports every variable as unset.
func noEnv(string) string { return "" }

// envWith returns a Getenv stub that answers value for key and "" otherwise.
func envWith(key, value string) func(string) string {
	return func(k string) string {
		if k == key {
			return value
		}
		return ""
	}
}

func TestResolveSourceDir_FlagWins(t *testing.T) {
	cfg := selfUpdateConfig{
		ProjectDir:       t.TempDir(),
		SourceFlag:       "/a",
		SettingSourceDir: "/c",
		Getenv:           envWith("TMUX_CLI_SRC", "/b"),
	}

	dir, err := resolveSourceDir(cfg)
	require.NoError(t, err)
	assert.Equal(t, "/a", dir)
}

func TestResolveSourceDir_EnvFallback(t *testing.T) {
	cfg := selfUpdateConfig{
		ProjectDir:       t.TempDir(),
		SourceFlag:       "",
		SettingSourceDir: "/c",
		Getenv:           envWith("TMUX_CLI_SRC", "/b"),
	}

	dir, err := resolveSourceDir(cfg)
	require.NoError(t, err)
	assert.Equal(t, "/b", dir)
}

func TestResolveSourceDir_SettingFallback(t *testing.T) {
	cfg := selfUpdateConfig{
		ProjectDir:       t.TempDir(),
		SourceFlag:       "",
		SettingSourceDir: "/c",
		Getenv:           noEnv,
	}

	dir, err := resolveSourceDir(cfg)
	require.NoError(t, err)
	assert.Equal(t, "/c", dir)
}

func TestResolveSourceDir_NoneSet_Errors(t *testing.T) {
	cfg := selfUpdateConfig{
		ProjectDir: t.TempDir(),
		Getenv:     noEnv,
	}

	_, err := resolveSourceDir(cfg)
	require.Error(t, err)
}

func TestResolveSourceDir_RefusesSelfTarget(t *testing.T) {
	projectDir := t.TempDir()
	cfg := selfUpdateConfig{
		ProjectDir: projectDir,
		SourceFlag: projectDir,
		Getenv:     noEnv,
	}

	_, err := resolveSourceDir(cfg)
	require.Error(t, err)
}

// TestResolveSourceDir_AllowsSelfWhenCliCheckout — the source==project refusal
// is relaxed ONLY when the dir IS a tmux-cli source checkout (module path +
// Makefile): the default max_goals=1 inline mode has buildDir == workDir in the
// dogfood repo, and the repair-cycle self-reinstall hook must be able to build
// it. The guard's intent ("never build an arbitrary target project") is
// preserved via setup.IsCliSourceCheckout; a non-cli dir still refuses
// (TestResolveSourceDir_RefusesSelfTarget above).
func TestResolveSourceDir_AllowsSelfWhenCliCheckout(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "Makefile"), []byte("install:\n\ttrue\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "go.mod"),
		[]byte("module github.com/console/tmux-cli\n\ngo 1.25.5\n"), 0o644))
	cfg := selfUpdateConfig{
		ProjectDir: projectDir,
		SourceFlag: projectDir,
		Getenv:     noEnv,
	}

	dir, err := resolveSourceDir(cfg)
	require.NoError(t, err)
	assert.Equal(t, projectDir, dir)
}

func TestBinaryChanged_DetectsRewrite(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "tmux-cli")
	require.NoError(t, os.WriteFile(binPath, []byte("old-binary"), 0o755))
	before, err := os.Stat(binPath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(binPath, []byte("new-binary-with-different-size"), 0o755))

	changed, err := binaryChanged(binPath, before)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestBinaryChanged_UnchangedFalse(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "tmux-cli")
	require.NoError(t, os.WriteFile(binPath, []byte("stable-binary"), 0o755))
	before, err := os.Stat(binPath)
	require.NoError(t, err)

	changed, err := binaryChanged(binPath, before)
	require.NoError(t, err)
	assert.False(t, changed)
}

// daemonModeConfig builds a doSelfUpdate config for restartDaemon with a
// fake install binary under projectDir-independent temp dirs and an
// injectable BuildCmd — no real make install, no tmux server.
func daemonModeConfig(t *testing.T, buildCmd []string, stderr *bytes.Buffer) (selfUpdateConfig, string, string) {
	t.Helper()
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".tmux-cli"), 0o755))

	installDir := t.TempDir()
	installPath := filepath.Join(installDir, "tmux-cli")
	require.NoError(t, os.WriteFile(installPath, []byte("original-binary"), 0o755))

	cfg := selfUpdateConfig{
		ProjectDir:  projectDir,
		SourceFlag:  t.TempDir(),
		InstallPath: installPath,
		Mode:        restartDaemon,
		Getenv:      noEnv,
		BuildCmd:    buildCmd,
		Stderr:      stderr,
	}
	marker := filepath.Join(projectDir, ".tmux-cli", "taskvisor-restart")
	return cfg, installPath, marker
}

func TestDoSelfUpdate_DaemonModeWritesRestartMarker(t *testing.T) {
	var stderr bytes.Buffer
	cfg, installPath, marker := daemonModeConfig(t, nil, &stderr)
	// Fake build mutates the installed binary so the change is detected.
	cfg.BuildCmd = []string{"sh", "-c", "echo updated >> " + installPath}

	mockExec := new(testutil.MockTmuxExecutor)

	result, err := doSelfUpdate(cfg, mockExec)
	require.NoError(t, err)
	assert.True(t, result.BinaryChanged)
	assert.FileExists(t, marker)
}

func TestDoSelfUpdate_BuildFailNoMarkerNoRestart(t *testing.T) {
	var stderr bytes.Buffer
	cfg, _, marker := daemonModeConfig(t, []string{"false"}, &stderr)

	mockExec := new(testutil.MockTmuxExecutor)

	result, err := doSelfUpdate(cfg, mockExec)
	require.Error(t, err)
	assert.Equal(t, "build", result.Stage)
	assert.NoFileExists(t, marker)
	mockExec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestDoSelfUpdate_NoopBinaryUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	// Build succeeds but leaves the installed binary untouched.
	cfg, _, marker := daemonModeConfig(t, []string{"sh", "-c", "true"}, &stderr)

	mockExec := new(testutil.MockTmuxExecutor)

	result, err := doSelfUpdate(cfg, mockExec)
	require.NoError(t, err)
	assert.False(t, result.BinaryChanged)
	assert.NoFileExists(t, marker)
}

func TestDoSelfUpdate_SessionModeWithoutResumeStateRefuses(t *testing.T) {
	var stderr bytes.Buffer
	sentinel := filepath.Join(t.TempDir(), "build-ran")
	cfg, _, marker := daemonModeConfig(t, []string{"sh", "-c", "touch " + sentinel}, &stderr)
	cfg.Mode = restartSession
	cfg.ResumeState = ""

	mockExec := new(testutil.MockTmuxExecutor)

	_, err := doSelfUpdate(cfg, mockExec)
	require.Error(t, err)
	// Refusal happens before build and restart: no build ran, no marker.
	assert.NoFileExists(t, sentinel)
	assert.NoFileExists(t, marker)
}

// chdirToTemp changes into a fresh temp dir for the duration of the test so
// ExecutePostCommandWithFallback's best-effort .tmux-cli/logs writes never
// pollute the repo. Restores the original cwd on cleanup.
func chdirToTemp(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// claudeRestartMock wires a MockTmuxExecutor with exactly one tmux session and
// a supervisor window so dispatchClaudeRestart resolves a target. ListWindows /
// ListSessions are Maybe() so they don't force a call count.
func claudeRestartMock() *testutil.MockTmuxExecutor {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1"}, nil).Maybe()
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Maybe()
	return mockExec
}

// TestDispatchClaudeRestart_TerminatesBeforeRelaunch proves the supervisor
// Claude process is terminated (TerminateWindowProcess) BEFORE the first
// relaunch command is typed (SendMessageWithFeedback) — the ordering that makes
// the relaunch land on a shell rather than as chat into a still-running Claude.
func TestDispatchClaudeRestart_TerminatesBeforeRelaunch(t *testing.T) {
	chdirToTemp(t)
	mockExec := claudeRestartMock()

	var order []string
	mockExec.On("TerminateWindowProcess", "@0").Run(func(mock.Arguments) {
		order = append(order, "terminate")
	}).Return(nil)
	mockExec.On("SendMessageWithFeedback", "sess-1", "supervisor", mock.Anything).Run(func(mock.Arguments) {
		order = append(order, "relaunch")
	}).Return("", nil)

	require.NoError(t, dispatchClaudeRestart(mockExec))

	mockExec.AssertCalled(t, "TerminateWindowProcess", "@0")
	require.NotEmpty(t, order)
	assert.Equal(t, "terminate", order[0], "terminate must precede any relaunch send")
}

// TestDispatchClaudeRestart_DoesNotUseInterruptWindow is the regression guard:
// the restart path must no longer rely on a single C-c (InterruptWindow), which
// Claude Code ignores.
func TestDispatchClaudeRestart_DoesNotUseInterruptWindow(t *testing.T) {
	chdirToTemp(t)
	mockExec := claudeRestartMock()
	mockExec.On("TerminateWindowProcess", "@0").Return(nil)
	mockExec.On("SendMessageWithFeedback", "sess-1", "supervisor", mock.Anything).Return("", nil)

	require.NoError(t, dispatchClaudeRestart(mockExec))

	mockExec.AssertNotCalled(t, "InterruptWindow", mock.Anything)
}

// TestDispatchClaudeRestart_TerminateErrorAbortsRelaunch proves a terminate
// failure returns the wrapped error and never types the relaunch commands.
func TestDispatchClaudeRestart_TerminateErrorAbortsRelaunch(t *testing.T) {
	chdirToTemp(t)
	mockExec := claudeRestartMock()
	mockExec.On("TerminateWindowProcess", "@0").Return(fmt.Errorf("child survived SIGKILL"))

	err := dispatchClaudeRestart(mockExec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminate supervisor Claude before relaunch")
	assert.Contains(t, err.Error(), "child survived SIGKILL")
	mockExec.AssertNotCalled(t, "SendMessageWithFeedback", mock.Anything, mock.Anything, mock.Anything)
}

func TestDoSelfUpdate_WarnsWhenExecutableNotInstallPath(t *testing.T) {
	var stderr bytes.Buffer
	cfg, installPath, _ := daemonModeConfig(t, nil, &stderr)
	// InstallPath lives in a t.TempDir and can never equal os.Executable()
	// (the test binary), so the stale-executable warning must fire.
	cfg.BuildCmd = []string{"sh", "-c", "echo updated >> " + installPath}

	mockExec := new(testutil.MockTmuxExecutor)

	_, err := doSelfUpdate(cfg, mockExec)
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "warning")
}

package main

import (
	"bytes"
	"fmt"
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

// TestMain guards against the session-restart relaunch fork-bomb. When
// dispatchSessionRestart self-execs `os.Executable()` (the test binary in
// tests) with a `start-attach ...` argv, a plain `go test` binary would ignore
// the unknown positional and RE-RUN THE WHOLE SUITE — which re-enters
// TestDispatchSessionRestart_CapturesUUIDBeforeKill and spawns another
// subprocess, recursing without bound ([[test-suite-spawns-live-sessions-restart-loop]]).
// Normal `go test` always invokes the binary with `-test.*` flags (first arg
// begins with '-'); a relaunch passes a bare subcommand as the first arg, so we
// detect that and exit 0 immediately — making the relaunch the harmless no-op
// the spec assumes, with NO real session launched.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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

	require.NoError(t, dispatchClaudeRestart(selfUpdateConfig{Getenv: noEnv}, mockExec))

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

	require.NoError(t, dispatchClaudeRestart(selfUpdateConfig{Getenv: noEnv}, mockExec))

	mockExec.AssertNotCalled(t, "InterruptWindow", mock.Anything)
}

// TestDispatchClaudeRestart_TerminateErrorAbortsRelaunch proves a terminate
// failure returns the wrapped error and never types the relaunch commands.
func TestDispatchClaudeRestart_TerminateErrorAbortsRelaunch(t *testing.T) {
	chdirToTemp(t)
	mockExec := claudeRestartMock()
	mockExec.On("TerminateWindowProcess", "@0").Return(fmt.Errorf("child survived SIGKILL"))

	err := dispatchClaudeRestart(selfUpdateConfig{Getenv: noEnv}, mockExec)
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

// TestBuildSessionRestartArgs_IncludesCapturedUUID proves a captured supervisor
// UUID is threaded through the relaunch argv as `--session-uuid <uuid>` so the
// recreated window reuses it (and resumes the same Claude conversation).
func TestBuildSessionRestartArgs_IncludesCapturedUUID(t *testing.T) {
	cfg := selfUpdateConfig{ResumeState: "/s.json", SupervisorUUID: "U"}
	got := buildSessionRestartArgs(cfg)
	assert.Equal(t, []string{"start-attach", "--resume-state", "/s.json", "--session-uuid", "U", "--force"}, got)
}

// TestBuildSessionRestartArgs_OmitsWhenEmpty proves an empty captured UUID
// (best-effort capture failed) omits the flag entirely — start-attach then
// mints a fresh UUID and the restart still succeeds (graceful degrade).
func TestBuildSessionRestartArgs_OmitsWhenEmpty(t *testing.T) {
	cfg := selfUpdateConfig{ResumeState: "/s.json", SupervisorUUID: ""}
	got := buildSessionRestartArgs(cfg)
	assert.Equal(t, []string{"start-attach", "--resume-state", "/s.json", "--force"}, got)
	assert.NotContains(t, got, "--session-uuid")
}

// TestBuildSessionRestartArgs_IncludesForce proves the restart self-exec argv
// always ends with --force (so start-attach recreates non-interactively),
// whether or not a supervisor UUID was captured.
func TestBuildSessionRestartArgs_IncludesForce(t *testing.T) {
	withUUID := buildSessionRestartArgs(selfUpdateConfig{ResumeState: "/s.json", SupervisorUUID: "U"})
	assert.Equal(t, "--force", withUUID[len(withUUID)-1], "argv must end with --force when a UUID is present")
	assert.Contains(t, withUUID, "--force")

	noUUID := buildSessionRestartArgs(selfUpdateConfig{ResumeState: "/s.json", SupervisorUUID: ""})
	assert.Equal(t, "--force", noUUID[len(noUUID)-1], "argv must end with --force when no UUID is present")
	assert.Contains(t, noUUID, "--force")
}

// TestRecreateDirForSession_UsesKilledSessionDir is the core cross-dir
// regression assertion: the recreate dir comes from the KILLED session's own
// TMUX_CLI_PROJECT_PATH (/dir/B), NOT the cfg.ProjectDir fallback (/dir/A).
func TestRecreateDirForSession_UsesKilledSessionDir(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("GetSessionEnvironment", "sess-X", "TMUX_CLI_PROJECT_PATH").Return("/dir/B", nil)

	got := recreateDirForSession(mockExec, "sess-X", "/dir/A")
	assert.Equal(t, "/dir/B", got, "recreate dir must be the killed session's own dir, not the fallback")
}

// TestRecreateDirForSession_FallsBackWhenEnvMissing proves an unreadable/empty
// session environment falls back to cfg.ProjectDir (legacy single-session path
// preserved, restart never fails on this).
func TestRecreateDirForSession_FallsBackWhenEnvMissing(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("GetSessionEnvironment", "sess-X", "TMUX_CLI_PROJECT_PATH").Return("", fmt.Errorf("no such variable"))

	got := recreateDirForSession(mockExec, "sess-X", "/dir/A")
	assert.Equal(t, "/dir/A", got, "unreadable env must fall back to the provided fallback dir")
}

// TestDispatchSessionRestart_ResolvesDirBeforeKill mirrors
// TestDispatchSessionRestart_CapturesUUIDBeforeKill: it asserts the killed
// session's dir is read (GetSessionEnvironment) BEFORE KillSession — a dead
// session has no environment — and that KillSession targets exactly the
// resolved sessionID and no other. The relaunch Run() error is ignored
// (os.Executable() is the test binary, a no-op per TestMain).
func TestDispatchSessionRestart_ResolvesDirBeforeKill(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".tmux-cli"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1"}, nil)
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption).Return("U", nil)

	var order []string
	mockExec.On("GetSessionEnvironment", "sess-1", "TMUX_CLI_PROJECT_PATH").Run(func(mock.Arguments) {
		order = append(order, "resolve-dir")
	}).Return("/dir/B", nil)
	mockExec.On("KillSession", "sess-1").Run(func(mock.Arguments) {
		order = append(order, "kill")
	}).Return(nil)

	cfg := selfUpdateConfig{ProjectDir: projectDir, ResumeState: "/s.json", Getenv: noEnv}
	_ = dispatchSessionRestart(cfg, mockExec, &bytes.Buffer{})

	mockExec.AssertCalled(t, "GetSessionEnvironment", "sess-1", "TMUX_CLI_PROJECT_PATH")
	mockExec.AssertCalled(t, "KillSession", "sess-1")
	mockExec.AssertNumberOfCalls(t, "KillSession", 1) // only the resolved session, never a second
	require.GreaterOrEqual(t, len(order), 2)
	assert.Equal(t, "resolve-dir", order[0], "dir resolution must precede KillSession")
	assert.Equal(t, "kill", order[1])
}

// TestCaptureSupervisorUUID_ReadsSupervisorWindow proves the helper resolves
// the supervisor window by name and returns its window-uuid option.
func TestCaptureSupervisorUUID_ReadsSupervisorWindow(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption).Return("U", nil)

	assert.Equal(t, "U", captureSupervisorUUID(mockExec, "sess-1"))
}

// TestCaptureSupervisorUUID_ReturnsEmptyOnError proves capture is best-effort:
// any error reading the UUID yields "" so the restart never breaks.
func TestCaptureSupervisorUUID_ReturnsEmptyOnError(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	mockExec.On("GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption).Return("", fmt.Errorf("boom"))

	assert.Equal(t, "", captureSupervisorUUID(mockExec, "sess-1"))
}

// TestDispatchSessionRestart_CapturesUUIDBeforeKill mirrors
// TestDispatchClaudeRestart_TerminatesBeforeRelaunch: it records call order and
// asserts the supervisor UUID is read (GetWindowOption) BEFORE KillSession, so
// the pre-restart UUID is captured while the window still exists. The relaunch
// Run() error is ignored — os.Executable() is the test binary, so no real
// session is launched.
func TestDispatchSessionRestart_CapturesUUIDBeforeKill(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".tmux-cli"), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1"}, nil)
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	var order []string
	mockExec.On("GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption).Run(func(mock.Arguments) {
		order = append(order, "capture")
	}).Return("U", nil)
	// recreateDirForSession reads the killed session's dir before KillSession; the
	// result is unused here (relaunch is a no-op test binary), so Maybe().
	mockExec.On("GetSessionEnvironment", "sess-1", "TMUX_CLI_PROJECT_PATH").Return("", nil).Maybe()
	mockExec.On("KillSession", "sess-1").Run(func(mock.Arguments) {
		order = append(order, "kill")
	}).Return(nil)

	// Getenv: noEnv so a stray TMUX_WINDOW_UUID in the worker's env can't leak into
	// resolveRestartSession and trigger an extra GetWindowOption scan before Kill.
	cfg := selfUpdateConfig{ProjectDir: projectDir, ResumeState: "/s.json", Getenv: noEnv}
	// Ignore the relaunch Run() error: the exec target is the test binary, not a
	// real tmux-cli, so no live session is spawned (see [[test-suite-spawns-live-sessions-restart-loop]]).
	_ = dispatchSessionRestart(cfg, mockExec, &bytes.Buffer{})

	mockExec.AssertCalled(t, "GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption)
	mockExec.AssertCalled(t, "KillSession", "sess-1")
	require.GreaterOrEqual(t, len(order), 2)
	assert.Equal(t, "capture", order[0], "UUID capture must precede KillSession")
	assert.Equal(t, "kill", order[1])
}

// TestResolveRestartSession_SessionFlagWins — an explicit --session takes
// precedence over everything and is returned when present among the running
// sessions, even on a multi-session host.
func TestResolveRestartSession_SessionFlagWins(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)

	cfg := selfUpdateConfig{SessionFlag: "sess-2", Getenv: noEnv}
	sessionID, err := resolveRestartSession(cfg, mockExec)
	require.NoError(t, err)
	assert.Equal(t, "sess-2", sessionID)
}

// TestResolveRestartSession_SessionFlagMissingErrors — a --session naming a
// session that does not exist errors rather than falling through to a wrong
// session.
func TestResolveRestartSession_SessionFlagMissingErrors(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)

	cfg := selfUpdateConfig{SessionFlag: "ghost", Getenv: noEnv}
	sessionID, err := resolveRestartSession(cfg, mockExec)
	require.Error(t, err)
	assert.Empty(t, sessionID)
	assert.Contains(t, err.Error(), "ghost")
}

// TestResolveRestartSession_EnvWindowUUIDResolvesSession — with no flag, the
// caller's TMUX_WINDOW_UUID resolves to the session owning the matching
// window, even across multiple sessions.
func TestResolveRestartSession_EnvWindowUUIDResolvesSession(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)
	mockExec.On("ListWindows", "sess-1").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Maybe()
	mockExec.On("ListWindows", "sess-2").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor"},
	}, nil).Maybe()
	mockExec.On("GetWindowOption", "sess-1", "@0", tmux.WindowUUIDOption).Return("other", nil).Maybe()
	mockExec.On("GetWindowOption", "sess-2", "@1", tmux.WindowUUIDOption).Return("U", nil).Maybe()

	cfg := selfUpdateConfig{Getenv: envWith("TMUX_WINDOW_UUID", "U")}
	sessionID, err := resolveRestartSession(cfg, mockExec)
	require.NoError(t, err)
	assert.Equal(t, "sess-2", sessionID)
}

// TestResolveRestartSession_FallbackSingleSession — no flag, no env hint, and
// exactly one session ⇒ the singleSessionID fallback returns it.
func TestResolveRestartSession_FallbackSingleSession(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1"}, nil)

	cfg := selfUpdateConfig{Getenv: noEnv}
	sessionID, err := resolveRestartSession(cfg, mockExec)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", sessionID)
}

// TestResolveRestartSession_MultiSessionNoHintErrors — no flag, no env hint,
// several sessions ⇒ the singleSessionID fallback preserves the existing
// "expected exactly 1" refusal.
func TestResolveRestartSession_MultiSessionNoHintErrors(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)

	cfg := selfUpdateConfig{Getenv: noEnv}
	sessionID, err := resolveRestartSession(cfg, mockExec)
	require.Error(t, err)
	assert.Empty(t, sessionID)
	assert.Contains(t, err.Error(), "expected exactly 1")
}

// TestSessionForWindowUUID_EmptyUUIDReturnsEmpty — an empty uuid short-circuits
// to "" without touching the executor at all.
func TestSessionForWindowUUID_EmptyUUIDReturnsEmpty(t *testing.T) {
	mockExec := new(testutil.MockTmuxExecutor)

	assert.Equal(t, "", sessionForWindowUUID(mockExec, ""))
	mockExec.AssertNotCalled(t, "ListSessions")
}

// TestDispatchClaudeRestart_TargetsFlaggedSessionMultiSession — THE
// multi-session targeting test the acceptance criteria require: with >1
// session and --session sess-2, the terminate + relaunch fire for sess-2's
// supervisor window and NOT the "expected exactly 1" error.
func TestDispatchClaudeRestart_TargetsFlaggedSessionMultiSession(t *testing.T) {
	chdirToTemp(t)
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)
	mockExec.On("ListWindows", "sess-2").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@7", Name: "supervisor"},
	}, nil)
	mockExec.On("TerminateWindowProcess", "@7").Return(nil)
	mockExec.On("SendMessageWithFeedback", "sess-2", "supervisor", mock.Anything).Return("", nil)

	cfg := selfUpdateConfig{SessionFlag: "sess-2", Getenv: noEnv}
	require.NoError(t, dispatchClaudeRestart(cfg, mockExec))

	mockExec.AssertCalled(t, "TerminateWindowProcess", "@7")
	mockExec.AssertCalled(t, "SendMessageWithFeedback", "sess-2", "supervisor", mock.Anything)
	mockExec.AssertNotCalled(t, "ListWindows", "sess-1")
}

// TestResolveAutoMode_MultiSessionWithFlagPicksClaude — daemon dead, several
// sessions, but a --session flag resolves ⇒ restartClaude (not "").
func TestResolveAutoMode_MultiSessionWithFlagPicksClaude(t *testing.T) {
	projectDir := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)

	cfg := selfUpdateConfig{ProjectDir: projectDir, SessionFlag: "sess-2", Getenv: noEnv}
	assert.Equal(t, restartClaude, resolveAutoMode(cfg, mockExec))
}

// TestResolveAutoMode_MultiSessionNoHintNoRestart — daemon dead, several
// sessions, no flag/env hint ⇒ "" (no restart; the update stays installed).
func TestResolveAutoMode_MultiSessionNoHintNoRestart(t *testing.T) {
	projectDir := t.TempDir()
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("ListSessions").Return([]string{"sess-1", "sess-2"}, nil)

	cfg := selfUpdateConfig{ProjectDir: projectDir, Getenv: noEnv}
	assert.Equal(t, restartMode(""), resolveAutoMode(cfg, mockExec))
}

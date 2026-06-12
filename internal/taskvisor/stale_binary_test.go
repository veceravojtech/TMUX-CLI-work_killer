package taskvisor

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func staleBinaryDaemon(t *testing.T, stale bool, haltOnStale bool) (*Daemon, *GoalsFile, string) {
	t.Helper()
	dir := t.TempDir()
	writeGoalsYaml(t, dir, `current_goal: goal-001
goals:
  - id: goal-001
    description: test goal
    status: running
    max_retries: 5
    retries: 5
    code_retries: 3
    max_code_retries: 3
    spec_retries: 1
    max_spec_retries: 1
    validation_retries: 1
    max_validation_retries: 1
`)
	writeSettingsMinimal(t, dir)

	if stale {
		tmp := filepath.Join(dir, "fake-bin")
		require.NoError(t, os.WriteFile(tmp, []byte("old"), 0o755))
		setup.ResetBuildStampForTest()
		setup.SetExecutablePathForTest(func() (string, error) { return tmp, nil })
		setup.InitBuildStampForTest()
		require.NoError(t, os.WriteFile(tmp, []byte("new-content-different"), 0o755))
		t.Cleanup(func() { setup.RestoreExecutablePathForTest() })
	} else {
		tmp := filepath.Join(dir, "fake-bin")
		require.NoError(t, os.WriteFile(tmp, []byte("stable"), 0o755))
		setup.ResetBuildStampForTest()
		setup.SetExecutablePathForTest(func() (string, error) { return tmp, nil })
		setup.InitBuildStampForTest()
		t.Cleanup(func() { setup.RestoreExecutablePathForTest() })
	}

	executor := new(testutil.MockTmuxExecutor)
	executor.On("FindSessionByEnvironment", mock.Anything, mock.Anything).Return("test-session", nil).Maybe()
	executor.On("ClosePipePane", mock.Anything, mock.Anything).Return(nil).Maybe()
	executor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	// deactivate() (stale-binary halt path) now emits a [TASKVISOR:STATE from=active
	// to=idle] notification to the "supervisor" window (@0); tolerate the SendMessageWithDelay.
	executor.On("SendMessageWithDelay", mock.Anything, "@0", mock.Anything).Return(nil).Maybe()

	d := New(dir, executor)
	d.mode = modeActive
	d.session = "test-session"
	d.currentGoal = "goal-001"
	d.haltOnStaleBinary = haltOnStale
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.clock = fixedClock(now)
	d.activatedAt = now.Add(-1 * time.Minute)

	goals, err := LoadGoals(dir)
	require.NoError(t, err)

	return d, goals, dir
}

func writeSettingsMinimal(t *testing.T, dir string) {
	t.Helper()
	settingsDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "setting.yaml"), []byte(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
  max_workers: 4
plan:
  auto_approve: true
  auto_execute: true
`), 0o644))
}

func TestTick_StaleBinary_BannerOnly(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.NotEmpty(t, d.staleBanner, "staleBanner must be set when binary is stale")
	assert.Contains(t, d.staleBanner, "BINARY STALE")
	assert.Equal(t, modeActive, d.mode, "mode must stay active when HaltOnStaleBinary=false")
	assert.Empty(t, d.haltReason, "haltReason must be empty when not halting")
}

func TestTick_StaleBinary_HaltAfterGoal(t *testing.T) {
	d, goals, dir := staleBinaryDaemon(t, true, true)

	executor := d.executor.(*testutil.MockTmuxExecutor)
	executor.On("KillWindow", mock.Anything, mock.Anything).Return(nil).Maybe()
	executor.On("HasSession", mock.Anything).Return(true, nil).Maybe()
	executor.On("CreateWindow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("@1", nil).Maybe()
	executor.On("CaptureWindowOutput", mock.Anything, mock.Anything).Return("$", nil).Maybe()
	executor.On("SendKeys", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	executor.On("SetWindowEnvironment", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.NotEmpty(t, d.haltReason, "haltReason must be set when HaltOnStaleBinary=true and binary is stale")
	assert.Contains(t, d.haltReason, "HALTED")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	for _, g := range reloaded.Goals {
		assert.Equal(t, GoalRunning, g.Status, "goal statuses must be untouched by stale-binary halt")
	}
}

func TestTick_StaleBinaryCheck_Throttled(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.clock = fixedClock(now)

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)
	assert.NotEmpty(t, d.staleBanner)

	d.staleBanner = ""

	err = d.checkStaleBinary(goals)
	require.NoError(t, err)
	assert.Empty(t, d.staleBanner, "staleBanner must stay empty when throttled (<1min)")

	d.clock = fixedClock(now.Add(61 * time.Second))

	err = d.checkStaleBinary(goals)
	require.NoError(t, err)
	assert.NotEmpty(t, d.staleBanner, "staleBanner must be set after throttle expires (>=1min)")
}

func TestTick_NotStale_NoBanner(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, false, false)

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.Empty(t, d.staleBanner, "staleBanner must be empty when binary is not stale")
	assert.Empty(t, d.haltReason, "haltReason must be empty when binary is not stale")
}

func writeGoalsYaml(t *testing.T, dir string, content string) {
	t.Helper()
	goalsDir := filepath.Join(dir, ".tmux-cli", "goals")
	require.NoError(t, os.MkdirAll(goalsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "goals.yaml"), []byte(content), 0o644))

	goalDir := filepath.Join(goalsDir, "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# goal-001\ntest goal"), 0o644))
}

// Unused in this file but required by the test harness for compatibility with
// writeSettings (shared helper).
var _ = context.Background

func TestTick_StaleBinary_Restart_ExecsWithCorrectArgs(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)
	d.restartOnStaleBinary = true

	var capturedPath string
	var capturedArgs []string
	var capturedEnv []string
	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		capturedPath = path
		capturedArgs = args
		capturedEnv = env
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.NotEmpty(t, capturedPath, "execReplaceFn must be called with a non-empty path")
	assert.Equal(t, os.Args, capturedArgs, "execReplaceFn must receive os.Args")
	assert.Equal(t, os.Environ(), capturedEnv, "execReplaceFn must receive os.Environ()")
	assert.True(t, d.restartAttempted, "restartAttempted must be set after exec")
}

func TestTick_StaleBinary_Restart_ExecFails_StaysInPollLoop(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)
	d.restartOnStaleBinary = true

	d.setupSignalHandler(context.Background())
	defer d.cancel()

	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		return fmt.Errorf("exec failed")
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "mode must stay active after exec failure")
	assert.Empty(t, d.haltReason, "haltReason must remain empty after exec failure")

	select {
	case <-d.ctx.Done():
		t.Fatal("ctx must not be cancelled after exec failure")
	default:
	}
}

func TestTick_StaleBinary_Restart_ExecFails_NoRetry(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)
	d.restartOnStaleBinary = true

	var callCount atomic.Int32
	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		callCount.Add(1)
		return fmt.Errorf("exec failed")
	})

	d.setupSignalHandler(context.Background())
	defer d.cancel()

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.clock = fixedClock(now.Add(2 * time.Minute))

	err = d.checkStaleBinary(goals)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load(), "execReplaceFn must NOT be called a second time")
}

func TestTick_StaleBinary_Restart_PrecedenceOverHalt(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, true)
	d.restartOnStaleBinary = true

	var execCalled bool
	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		execCalled = true
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.True(t, execCalled, "execReplaceFn must be called when restart is enabled")
	assert.Empty(t, d.haltReason, "haltReason must remain empty — restart takes precedence over halt")
}

func TestTick_StaleBinary_Restart_SignalHandlerStopped(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)
	d.restartOnStaleBinary = true

	d.signalCh = make(chan os.Signal, 1)
	signal.Notify(d.signalCh, syscall.SIGTERM, syscall.SIGINT)

	var signalStoppedBeforeExec bool
	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		select {
		case d.signalCh <- nil:
			signalStoppedBeforeExec = true
		default:
			signalStoppedBeforeExec = false
		}
		return fmt.Errorf("exec failed to test re-registration")
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.True(t, signalStoppedBeforeExec, "signal.Stop must be called before execReplaceFn — channel should accept a send after Stop")

	// drain the test nil so re-registered Notify doesn't see it
	select {
	case <-d.signalCh:
	default:
	}

	signal.Stop(d.signalCh)
}

func TestStaleBinary_RefreshesCommands_BannerOnly(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)

	called := false
	d.SetCommandRefreshFn(func() error {
		called = true
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.True(t, called, "commandRefreshFn must be invoked on the banner-only stale path")
	assert.NotEmpty(t, d.staleBanner, "banner must still be set")
	assert.Equal(t, modeActive, d.mode, "mode must stay active")
}

func TestStaleBinary_RefreshesCommands_BeforeRestart(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)
	d.restartOnStaleBinary = true

	var counter atomic.Int32
	var refreshSeq, execSeq int32
	d.SetCommandRefreshFn(func() error {
		refreshSeq = counter.Add(1)
		return nil
	})
	d.SetExecReplaceFnForTest(func(path string, args []string, env []string) error {
		execSeq = counter.Add(1)
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.NotZero(t, refreshSeq, "commandRefreshFn must be called")
	assert.NotZero(t, execSeq, "execReplaceFn must be called")
	assert.Less(t, refreshSeq, execSeq, "refresh must fire BEFORE exec-replace")
}

func TestStaleBinary_RefreshSkipped_WhenCommandsDisabled(t *testing.T) {
	d, goals, dir := staleBinaryDaemon(t, true, false)

	// Rewrite setting.yaml with commands disabled.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"), []byte(`hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: false
supervisor:
  max_cycles: 0
  max_workers: 4
plan:
  auto_approve: true
  auto_execute: true
`), 0o644))

	called := false
	d.SetCommandRefreshFn(func() error {
		called = true
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.False(t, called, "commandRefreshFn must NOT be called when Commands.Enabled=false")
	assert.NotEmpty(t, d.staleBanner, "banner must still be set")
}

func TestStaleBinary_RefreshFailure_DoesNotAbort(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, true)

	executor := d.executor.(*testutil.MockTmuxExecutor)
	executor.On("KillWindow", mock.Anything, mock.Anything).Return(nil).Maybe()
	executor.On("HasSession", mock.Anything).Return(true, nil).Maybe()

	d.SetCommandRefreshFn(func() error {
		return fmt.Errorf("boom")
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err, "checkStaleBinary must return nil even when refresh fails")
	assert.NotEmpty(t, d.haltReason, "halt must still occur after a refresh failure")
}

func TestStaleBinary_NotStale_NoRefresh(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, false, false)

	called := false
	d.SetCommandRefreshFn(func() error {
		called = true
		return nil
	})

	err := d.checkStaleBinary(goals)
	require.NoError(t, err)

	assert.False(t, called, "commandRefreshFn must NOT be called when binary is not stale")
}

func TestStaleBinary_RefreshNilFn_NoPanic(t *testing.T) {
	d, goals, _ := staleBinaryDaemon(t, true, false)

	// No SetCommandRefreshFn — commandRefreshFn stays nil.
	require.NotPanics(t, func() {
		err := d.checkStaleBinary(goals)
		require.NoError(t, err)
	})
	assert.NotEmpty(t, d.staleBanner, "banner must still be set with a nil refresh fn")
}

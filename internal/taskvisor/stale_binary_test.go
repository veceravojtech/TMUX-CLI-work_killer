package taskvisor

import (
	"context"
	"os"
	"path/filepath"
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
	executor.On("ListWindows", mock.Anything).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()

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

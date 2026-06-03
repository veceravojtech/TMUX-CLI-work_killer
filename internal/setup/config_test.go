package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSettings_Values(t *testing.T) {
	s := DefaultSettings()

	assert.False(t, s.Hooks.SessionNotify)
	assert.True(t, s.Hooks.BlockInteractive)
	assert.Nil(t, s.Hooks.Custom)
	assert.True(t, s.Commands.Enabled)
	assert.Equal(t, 0, s.Supervisor.MaxCycles)
	assert.Equal(t, 5, s.Supervisor.CycleDelay)
	assert.True(t, s.Supervisor.UnplannedAudit)
}

func TestLoadSettings_SupervisorMaxCycles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 5
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, 5, s.Supervisor.MaxCycles)
}

func TestLoadSettings_SupervisorMaxCycles_Unlimited(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, 0, s.Supervisor.MaxCycles)
}

func TestSaveSettings_SupervisorRoundTrip(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
		},
		Commands:   CommandsSettings{Enabled: true},
		Supervisor: SupervisorSettings{MaxCycles: 10, CycleDelay: 3, UnplannedAudit: true},
	}

	err := SaveSettings(root, original)
	require.NoError(t, err)

	loaded, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, 10, loaded.Supervisor.MaxCycles)
	assert.Equal(t, 3, loaded.Supervisor.CycleDelay)
}

func TestLoadSettings_FileMissing_CreatesDefault(t *testing.T) {
	root := t.TempDir()

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.False(t, s.Hooks.SessionNotify)
	assert.True(t, s.Hooks.BlockInteractive)
	assert.True(t, s.Commands.Enabled)

	_, err = os.Stat(filepath.Join(root, ".tmux-cli", "setting.yaml"))
	assert.NoError(t, err, "setting.yaml should be created on disk")
}

func TestLoadSettings_FileExists(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: false
commands:
  enabled: false
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.False(t, s.Hooks.SessionNotify)
	assert.False(t, s.Hooks.BlockInteractive)
	assert.False(t, s.Commands.Enabled)
}

func TestLoadSettings_InvalidYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte("{{{{bad yaml"), 0o644))

	_, err := LoadSettings(root)
	assert.Error(t, err)
}

func TestLoadSettings_CustomHooks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: true
  block_interactive: true
  custom:
    - event: pre_attach
      matcher: "dev-*"
      command: "echo hello"
      timeout: 5
    - event: post_detach
      command: "cleanup.sh"
      timeout: 10
commands:
  enabled: true
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	require.Len(t, s.Hooks.Custom, 2)

	assert.Equal(t, "pre_attach", s.Hooks.Custom[0].Event)
	assert.Equal(t, "dev-*", s.Hooks.Custom[0].Matcher)
	assert.Equal(t, "echo hello", s.Hooks.Custom[0].Command)
	assert.Equal(t, 5, s.Hooks.Custom[0].Timeout)

	assert.Equal(t, "post_detach", s.Hooks.Custom[1].Event)
	assert.Equal(t, "", s.Hooks.Custom[1].Matcher)
	assert.Equal(t, "cleanup.sh", s.Hooks.Custom[1].Command)
	assert.Equal(t, 10, s.Hooks.Custom[1].Timeout)
}

func TestDefaultSettings_MaxWorkers(t *testing.T) {
	s := DefaultSettings()
	assert.Equal(t, 4, s.Supervisor.MaxWorkers, "default max_workers should be 4")
}

func TestLoadSettings_SupervisorMaxWorkers(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_workers: 4
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)
	assert.Equal(t, 4, s.Supervisor.MaxWorkers)
}

func TestSaveSettings_MaxWorkersRoundTrip(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks:      HooksSettings{BlockInteractive: true},
		Commands:   CommandsSettings{Enabled: true},
		Supervisor: SupervisorSettings{MaxWorkers: 3, MaxCycles: 5, CycleDelay: 5, UnplannedAudit: true},
	}

	require.NoError(t, SaveSettings(root, original))

	loaded, err := LoadSettings(root)
	require.NoError(t, err)
	assert.Equal(t, 3, loaded.Supervisor.MaxWorkers)
}

func TestDefaultSettings_PlanFields(t *testing.T) {
	s := DefaultSettings()

	assert.True(t, s.Plan.AutoApprove)
	assert.True(t, s.Plan.AutoExecute)
}

func TestLoadSettings_WithPlanFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
plan:
  auto_approve: true
  auto_execute: false
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.True(t, s.Plan.AutoApprove)
	assert.False(t, s.Plan.AutoExecute)
}

func TestSaveLoadRoundTrip_PlanFields(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks:    HooksSettings{BlockInteractive: true},
		Commands: CommandsSettings{Enabled: true},
		Plan:     PlanSettings{AutoApprove: true, AutoExecute: true},
	}

	err := SaveSettings(root, original)
	require.NoError(t, err)

	loaded, err := LoadSettings(root)
	require.NoError(t, err)

	assert.True(t, loaded.Plan.AutoApprove)
	assert.True(t, loaded.Plan.AutoExecute)
}

func TestLoadSettings_BackfillsMissingSections(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlContent := `hooks:
  session_notify: true
  block_interactive: false
commands:
  enabled: false
`
	settingFile := filepath.Join(dir, "setting.yaml")
	require.NoError(t, os.WriteFile(settingFile, []byte(yamlContent), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.True(t, s.Hooks.SessionNotify)
	assert.False(t, s.Hooks.BlockInteractive)
	assert.False(t, s.Commands.Enabled)
	assert.Equal(t, 0, s.Supervisor.MaxCycles)

	raw, err := os.ReadFile(settingFile)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "supervisor:")
}

func TestDefaultSettings_SudoTimeout(t *testing.T) {
	s := DefaultSettings()
	assert.Equal(t, 30, s.Sudo.Timeout, "default sudo timeout should be 30")
}

func TestSettings_SudoYAMLRoundTrip(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks:    HooksSettings{BlockInteractive: true},
		Commands: CommandsSettings{Enabled: true},
		Sudo:     SudoSettings{Timeout: 60},
	}

	require.NoError(t, SaveSettings(root, original))

	loaded, err := LoadSettings(root)
	require.NoError(t, err)
	assert.Equal(t, 60, loaded.Sudo.Timeout)
}

func TestDefaultSettings_TaskvisorDefaults(t *testing.T) {
	s := DefaultSettings()
	assert.Equal(t, 3600, s.Taskvisor.DispatchTimeout)
	// ValidateTimeout is seeded from the worker budget via DeriveValidateTimeout,
	// NOT a hardcoded literal — DeriveValidateTimeout(600,4,4) = 660.
	assert.Equal(t, DeriveValidateTimeout(WorkerBudgetSec, DefaultMaxWorkers, DefaultMaxWorkers), s.Taskvisor.ValidateTimeout)
	assert.Equal(t, 660, s.Taskvisor.ValidateTimeout)
	assert.Greater(t, s.Taskvisor.ValidateTimeout, 300)
	assert.Equal(t, 5, s.Taskvisor.PollInterval)
	// C4-cont transient-retry knobs.
	assert.Equal(t, 3, s.Taskvisor.TransientRetryMaxAttempts)
	assert.Equal(t, 500, s.Taskvisor.TransientRetryBackoffMs)
}

// TestLoadSettings_TransientRetryRoundTrip proves the C4-cont knobs load from an
// explicit setting.yaml AND that a legacy yaml omitting them backfills to 3/500.
func TestLoadSettings_TransientRetryRoundTrip(t *testing.T) {
	t.Run("explicit values load", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".tmux-cli")
		require.NoError(t, os.MkdirAll(dir, 0o755))

		yaml := `taskvisor:
  transient_retry_max_attempts: 5
  transient_retry_backoff_ms: 250
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

		s, err := LoadSettings(root)
		require.NoError(t, err)
		assert.Equal(t, 5, s.Taskvisor.TransientRetryMaxAttempts)
		assert.Equal(t, 250, s.Taskvisor.TransientRetryBackoffMs)
	})

	t.Run("legacy yaml without keys backfills to 3/500", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".tmux-cli")
		require.NoError(t, os.MkdirAll(dir, 0o755))

		yaml := `taskvisor:
  dispatch_timeout: 3600
  poll_interval: 5
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

		s, err := LoadSettings(root)
		require.NoError(t, err)
		assert.Equal(t, 3, s.Taskvisor.TransientRetryMaxAttempts)
		assert.Equal(t, 500, s.Taskvisor.TransientRetryBackoffMs)
	})
}

func TestDeriveValidateTimeout_SingleWorker(t *testing.T) {
	// 600*ceil(1/1)+max(60,60) = 660
	assert.Equal(t, 660, DeriveValidateTimeout(600, 1, 1))
}

func TestDeriveValidateTimeout_ParallelUnderMax(t *testing.T) {
	// 600*ceil(3/3)+60 = 660
	assert.Equal(t, 660, DeriveValidateTimeout(600, 3, 3))
}

func TestDeriveValidateTimeout_OverMax(t *testing.T) {
	// 600*ceil(3/2)+max(60,120) = 600*2+120 = 1320
	assert.Equal(t, 1320, DeriveValidateTimeout(600, 2, 3))
}

func TestDeriveValidateTimeout_ManyWavesFloor(t *testing.T) {
	// 600*3+max(60,180) = 1980, which is >= 1800
	got := DeriveValidateTimeout(600, 1, 3)
	assert.GreaterOrEqual(t, got, 1800)
	assert.Equal(t, 1980, got)
}

func TestDeriveValidateTimeout_ZeroMaxWorkers(t *testing.T) {
	// maxWorkers<=0 coerced to 1: equivalent to (600,1,3) = 1980, no panic/div-zero
	assert.NotPanics(t, func() {
		assert.Equal(t, 1980, DeriveValidateTimeout(600, 0, 3))
	})
}

func TestLoadSettings_TaskvisorRoundTrip(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yaml := `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
taskvisor:
  dispatch_timeout: 7200
  validate_timeout: 600
  poll_interval: 10
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(yaml), 0o644))

	s, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, 7200, s.Taskvisor.DispatchTimeout)
	assert.Equal(t, 600, s.Taskvisor.ValidateTimeout)
	assert.Equal(t, 10, s.Taskvisor.PollInterval)
}

func TestSaveLoadRoundTrip_TaskvisorFields(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks:    HooksSettings{BlockInteractive: true},
		Commands: CommandsSettings{Enabled: true},
		Taskvisor: TaskvisorSettings{
			DispatchTimeout: 1800,
			ValidateTimeout: 120,
			PollInterval:    3,
		},
	}

	require.NoError(t, SaveSettings(root, original))

	loaded, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, 1800, loaded.Taskvisor.DispatchTimeout)
	assert.Equal(t, 120, loaded.Taskvisor.ValidateTimeout)
	assert.Equal(t, 3, loaded.Taskvisor.PollInterval)
}

func TestLoadSettings_BackfillsTaskvisor(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlContent := `hooks:
  session_notify: true
  block_interactive: false
commands:
  enabled: false
`
	settingFile := filepath.Join(dir, "setting.yaml")
	require.NoError(t, os.WriteFile(settingFile, []byte(yamlContent), 0o644))

	_, err := LoadSettings(root)
	require.NoError(t, err)

	raw, err := os.ReadFile(settingFile)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "taskvisor:")
}

func TestSaveSettings_WritesYAML(t *testing.T) {
	root := t.TempDir()

	original := &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
			Custom: []CustomHook{
				{Event: "pre_attach", Matcher: "prod-*", Command: "notify.sh", Timeout: 3},
			},
		},
		Commands: CommandsSettings{Enabled: false},
	}

	err := SaveSettings(root, original)
	require.NoError(t, err)

	loaded, err := LoadSettings(root)
	require.NoError(t, err)

	assert.Equal(t, original.Hooks.SessionNotify, loaded.Hooks.SessionNotify)
	assert.Equal(t, original.Hooks.BlockInteractive, loaded.Hooks.BlockInteractive)
	assert.Equal(t, original.Commands.Enabled, loaded.Commands.Enabled)
	require.Len(t, loaded.Hooks.Custom, 1)
	assert.Equal(t, "pre_attach", loaded.Hooks.Custom[0].Event)
	assert.Equal(t, "prod-*", loaded.Hooks.Custom[0].Matcher)
	assert.Equal(t, "notify.sh", loaded.Hooks.Custom[0].Command)
	assert.Equal(t, 3, loaded.Hooks.Custom[0].Timeout)
}

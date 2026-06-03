package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSettingsYAML(t *testing.T, dir string, content string) {
	t.Helper()
	settingsDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(settingsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "setting.yaml"), []byte(content), 0o644))
}

func TestModel_InitializesFromSettings(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: true
  block_interactive: false
commands:
  enabled: true
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)

	assert.Len(t, m.items, 17)
	assert.Equal(t, "hooks.session_notify", m.items[0].key)
	assert.True(t, m.items[0].value)
	assert.Equal(t, "hooks.block_interactive", m.items[1].key)
	assert.False(t, m.items[1].value)
	assert.Equal(t, "commands.enabled", m.items[2].key)
	assert.True(t, m.items[2].value)
	assert.Equal(t, "supervisor.max_workers", m.items[3].key)
	assert.Equal(t, "supervisor.cycle_delay", m.items[4].key)
	assert.Equal(t, "supervisor.max_cycles", m.items[5].key)
	assert.Equal(t, "supervisor.unplanned_audit", m.items[6].key)
	assert.Equal(t, "taskvisor.dispatch_timeout", m.items[10].key)
	assert.Equal(t, "taskvisor.validate_timeout", m.items[11].key)
	assert.Equal(t, "taskvisor.poll_interval", m.items[12].key)
	assert.Equal(t, "taskvisor.circuit_breaker_k", m.items[13].key)
	assert.Equal(t, "taskvisor.auto_resume_interval_sec", m.items[14].key)
	assert.Equal(t, "taskvisor.transient_retry_max_attempts", m.items[15].key)
	assert.Equal(t, "taskvisor.transient_retry_backoff_ms", m.items[16].key)
	assert.Equal(t, 0, m.cursor)
}

func TestModel_ToggleSetting(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	assert.False(t, m.items[0].value)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	assert.True(t, m.items[0].value)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	assert.False(t, m.items[0].value)
}

func TestModel_Navigation(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	assert.Equal(t, 0, m.cursor)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 1, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 2, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 3, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 4, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 5, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 6, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 7, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 8, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 9, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 10, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 11, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 12, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 13, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 14, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 15, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 16, m.cursor)

	// Can't go past last item
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 16, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 15, m.cursor)

	// Can't go above first item
	for i := 0; i < 17; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = updated.(Model)
	}
	assert.Equal(t, 0, m.cursor)
}

func TestModel_ToSettings_PreservesSupervisorFields(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 10
  cycle_delay: 15
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)

	// Toggle session_notify on
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)

	result := m.ToSettings()
	assert.True(t, result.Hooks.SessionNotify)
	assert.True(t, result.Hooks.BlockInteractive)
	assert.True(t, result.Commands.Enabled)
	assert.Equal(t, 10, result.Supervisor.MaxCycles, "supervisor.max_cycles must be preserved")
	assert.Equal(t, 15, result.Supervisor.CycleDelay, "supervisor.cycle_delay must be preserved")
}

func TestModel_ToSettings_PreservesCustomHooks(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: true
  block_interactive: true
  custom:
    - event: on_session_start
      command: echo hello
      timeout: 30
commands:
  enabled: true
supervisor:
  max_cycles: 5
  cycle_delay: 3
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	require.Len(t, result.Hooks.Custom, 1, "custom hooks must be preserved")
	assert.Equal(t, "on_session_start", result.Hooks.Custom[0].Event)
	assert.Equal(t, "echo hello", result.Hooks.Custom[0].Command)
	assert.Equal(t, 30, result.Hooks.Custom[0].Timeout)
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
	assert.Equal(t, 3, result.Supervisor.CycleDelay)
}

func TestModel_ToSettings_DefaultSettingsStillWork(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Toggle first item on
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)

	result := m.ToSettings()
	assert.True(t, result.Hooks.SessionNotify)
	assert.True(t, result.Hooks.BlockInteractive)
	assert.True(t, result.Commands.Enabled)
	assert.Equal(t, 0, result.Supervisor.MaxCycles)
	assert.Equal(t, 5, result.Supervisor.CycleDelay)
}

func TestModel_UnplannedAuditItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "supervisor.unplanned_audit" {
			found = true
			assert.True(t, item.value, "default should be true")
		}
	}
	assert.True(t, found, "unplanned_audit must be in TUI items")
}

func TestModel_ToSettings_PreservesUnplannedAudit(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 5
  cycle_delay: 3
  unplanned_audit: true
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.True(t, result.Supervisor.UnplannedAudit, "unplanned_audit must be preserved")
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
}

func TestModel_ToSettings_UnplannedAuditToggle(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Find the unplanned_audit item index
	var auditIdx int
	for i, item := range m.items {
		if item.key == "supervisor.unplanned_audit" {
			auditIdx = i
			break
		}
	}

	// Navigate to it and toggle off
	for i := 0; i < auditIdx; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)

	result := m.ToSettings()
	assert.False(t, result.Supervisor.UnplannedAudit)
}

func TestNewModel_IncludesPlanItems(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	keys := make([]string, len(m.items))
	for i, item := range m.items {
		keys[i] = item.key
	}
	assert.Contains(t, keys, "plan.auto_approve")
	assert.Contains(t, keys, "plan.auto_execute")
}

func TestToSettings_PlanAutoApprove(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Toggle auto_approve off (default is true)
	for i, item := range m.items {
		if item.key == "plan.auto_approve" {
			m.items[i].value = false
		}
	}

	result := m.ToSettings()
	assert.False(t, result.Plan.AutoApprove)
	assert.True(t, result.Plan.AutoExecute)
}

func TestToSettings_PlanAutoExecute(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Toggle auto_execute off (default is true)
	for i, item := range m.items {
		if item.key == "plan.auto_execute" {
			m.items[i].value = false
		}
	}

	result := m.ToSettings()
	assert.True(t, result.Plan.AutoApprove)
	assert.False(t, result.Plan.AutoExecute)
}

func TestToSettings_PreservesBaseSettings_WithPlan(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 10
  cycle_delay: 15
plan:
  auto_approve: true
  auto_execute: false
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.True(t, result.Plan.AutoApprove, "plan.auto_approve must be preserved from base")
	assert.False(t, result.Plan.AutoExecute)
	assert.Equal(t, 10, result.Supervisor.MaxCycles, "non-TUI fields must be preserved")
	assert.Equal(t, 15, result.Supervisor.CycleDelay, "non-TUI fields must be preserved")
}

func TestModel_QuitReturnsTeaQuit(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for _, key := range []string{"q", "esc"} {
		var msg tea.KeyMsg
		if key == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}

		_, cmd := m.Update(msg)
		require.NotNil(t, cmd, "pressing %q must return a cmd", key)

		result := cmd()
		_, isQuit := result.(tea.QuitMsg)
		assert.True(t, isQuit, "pressing %q must return tea.Quit (tea.QuitMsg), got %T", key, result)
	}
}

func TestModel_SaveOnQuit_PostRunPathway(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: true
  block_interactive: true
commands:
  enabled: true
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)

	// Toggle session_notify off
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)

	// Press q — should return tea.Quit directly (no intermediate save Cmd)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	require.NotNil(t, cmd)
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	require.True(t, isQuit, "q must produce tea.QuitMsg, got %T", quitMsg)

	// Simulate the post-run save pathway (what Run() does after p.Run() returns)
	result := m.ToSettings()
	saveErr := setup.SaveSettings(dir, result)
	require.NoError(t, saveErr)

	// Verify saved correctly
	reloaded, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	assert.False(t, reloaded.Hooks.SessionNotify)
	assert.True(t, reloaded.Hooks.BlockInteractive)
	assert.True(t, reloaded.Commands.Enabled)
}

func TestModel_View(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	view := m.View()
	assert.Contains(t, view, "tmux-cli Settings")
	assert.Contains(t, view, "Session Notify")
	assert.Contains(t, view, "Block Interactive")
	assert.Contains(t, view, "Commands Enabled")
}

func TestNewModel_IncludesNumericItems(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "supervisor.max_workers" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 4, item.intVal)
		}
	}
	assert.True(t, found, "supervisor.max_workers must be in TUI items")
}

func TestModel_NumericItem_IncrementDecrement(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Find max_workers index
	var idx int
	for i, item := range m.items {
		if item.key == "supervisor.max_workers" {
			idx = i
			break
		}
	}

	// Navigate to it
	for i := 0; i < idx; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}

	start := m.items[idx].intVal

	// Increment with right arrow
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	assert.Equal(t, start+1, m.items[idx].intVal)

	// Increment more
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	assert.Equal(t, start+2, m.items[idx].intVal)

	// Decrement with left arrow
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	assert.Equal(t, start+1, m.items[idx].intVal)

	// Can't go below 0
	for i := 0; i < start+5; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = updated.(Model)
	}
	assert.Equal(t, 0, m.items[idx].intVal)
}

func TestModel_NumericItem_SpaceEnterNoToggle(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Find max_workers index
	var idx int
	for i, item := range m.items {
		if item.key == "supervisor.max_workers" {
			idx = i
			break
		}
	}

	// Navigate to it
	for i := 0; i < idx; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}

	original := m.items[idx].intVal
	// Space/enter should not change numeric item
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	assert.Equal(t, original, m.items[idx].intVal)
}

func TestModel_ToSettings_MaxWorkers(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Set max_workers directly
	for i, item := range m.items {
		if item.key == "supervisor.max_workers" {
			m.items[i].intVal = 5
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 5, result.Supervisor.MaxWorkers)
}

func TestModel_ToSettings_PreservesMaxWorkers(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_workers: 8
  max_cycles: 5
  cycle_delay: 3
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.Equal(t, 8, result.Supervisor.MaxWorkers, "max_workers must be preserved")
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
}

func TestModel_NumericItem_View(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	settings.Supervisor.MaxWorkers = 4
	m := NewModel(dir, settings)

	view := m.View()
	assert.Contains(t, view, "Max Workers")
	assert.Contains(t, view, "4")
}

func TestModel_SudoTimeoutItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "sudo.timeout" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 30, item.intVal)
		}
	}
	assert.True(t, found, "sudo.timeout must be in TUI items")
}

func TestToSettings_SudoTimeout(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "sudo.timeout" {
			m.items[i].intVal = 120
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 120, result.Sudo.Timeout)
}

func TestModel_MaxCyclesItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "supervisor.max_cycles" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 0, item.intVal, "default max_cycles should be 0 (unlimited)")
		}
	}
	assert.True(t, found, "supervisor.max_cycles must be in TUI items")
}

func TestToSettings_MaxCycles(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "supervisor.max_cycles" {
			m.items[i].intVal = 5
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
}

func TestNewModel_IncludesTaskvisorItems(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	assert.Len(t, m.items, 17)

	keys := make([]string, len(m.items))
	for i, item := range m.items {
		keys[i] = item.key
	}
	assert.Contains(t, keys, "taskvisor.dispatch_timeout")
	assert.Contains(t, keys, "taskvisor.validate_timeout")
	assert.Contains(t, keys, "taskvisor.poll_interval")
	assert.Contains(t, keys, "taskvisor.circuit_breaker_k")
	assert.Contains(t, keys, "taskvisor.auto_resume_interval_sec")
	assert.Contains(t, keys, "taskvisor.transient_retry_max_attempts")
	assert.Contains(t, keys, "taskvisor.transient_retry_backoff_ms")

	for _, item := range m.items {
		switch item.key {
		case "taskvisor.dispatch_timeout":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 3600, item.intVal)
		case "taskvisor.validate_timeout":
			assert.Equal(t, "int", item.kind)
			// Default seeded via DeriveValidateTimeout(600,4,4) = 660 (C4).
			assert.Equal(t, 660, item.intVal)
		case "taskvisor.poll_interval":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 5, item.intVal)
		case "taskvisor.circuit_breaker_k":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 2, item.intVal)
		case "taskvisor.auto_resume_interval_sec":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 30, item.intVal)
		case "taskvisor.transient_retry_max_attempts":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 3, item.intVal)
		case "taskvisor.transient_retry_backoff_ms":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 500, item.intVal)
		}
	}
}

// TestNewModel_IncludesTransientRetryItems proves the C4-cont transient-retry
// knobs surface in the TUI items list with the correct kind and default intVals.
func TestNewModel_IncludesTransientRetryItems(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	assert.Len(t, m.items, 17)

	keys := make([]string, len(m.items))
	for i, item := range m.items {
		keys[i] = item.key
	}
	assert.Contains(t, keys, "taskvisor.transient_retry_max_attempts")
	assert.Contains(t, keys, "taskvisor.transient_retry_backoff_ms")

	for _, item := range m.items {
		switch item.key {
		case "taskvisor.transient_retry_max_attempts":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 3, item.intVal)
		case "taskvisor.transient_retry_backoff_ms":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 500, item.intVal)
		}
	}
}

// TestToSettings_TransientRetryMaxAttempts proves an edited
// transient_retry_max_attempts overlays onto Taskvisor.TransientRetryMaxAttempts
// while sibling/undisplayed Settings fields survive (AGENTS.md TUI INVARIANT).
func TestToSettings_TransientRetryMaxAttempts(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  poll_interval: 3
  circuit_breaker_k: 4
  auto_resume_interval_sec: 30
  transient_retry_max_attempts: 3
  transient_retry_backoff_ms: 500
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.transient_retry_max_attempts" {
			m.items[i].intVal = 7
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 7, result.Taskvisor.TransientRetryMaxAttempts, "edited value overlays onto settings")
	// Sibling/undisplayed fields preserved.
	assert.Equal(t, 500, result.Taskvisor.TransientRetryBackoffMs)
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

// TestToSettings_TransientRetryBackoffMs proves an edited
// transient_retry_backoff_ms overlays onto Taskvisor.TransientRetryBackoffMs.
func TestToSettings_TransientRetryBackoffMs(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.transient_retry_backoff_ms" {
			m.items[i].intVal = 750
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 750, result.Taskvisor.TransientRetryBackoffMs)
}

// TestToSettings_AutoResumeInterval proves an edited auto_resume_interval_sec
// overlays onto Taskvisor.AutoResumeIntervalSec and that sibling/undisplayed
// Settings fields survive the overlay (AGENTS.md TUI INVARIANT).
func TestToSettings_AutoResumeInterval(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  cycle_delay: 9
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  poll_interval: 3
  circuit_breaker_k: 4
  auto_resume_interval_sec: 30
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.auto_resume_interval_sec" {
			m.items[i].intVal = 45
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 45, result.Taskvisor.AutoResumeIntervalSec, "edited value overlays onto settings")
	// Sibling/undisplayed fields preserved.
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 3, result.Taskvisor.PollInterval)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

// TestToSettings_CircuitBreakerK proves an edited circuit_breaker_k overlays
// onto Taskvisor.CircuitBreakerK and that undisplayed/sibling Settings fields
// survive the overlay (AGENTS.md TUI INVARIANT — overlay onto baseSettings).
func TestToSettings_CircuitBreakerK(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  cycle_delay: 9
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  poll_interval: 3
  circuit_breaker_k: 2
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.circuit_breaker_k" {
			m.items[i].intVal = 5
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 5, result.Taskvisor.CircuitBreakerK, "edited value overlays onto settings")
	// Sibling/undisplayed fields preserved.
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 3, result.Taskvisor.PollInterval)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

func TestToSettings_TaskvisorDispatchTimeout(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.dispatch_timeout" {
			m.items[i].intVal = 7200
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 7200, result.Taskvisor.DispatchTimeout)
}

func TestToSettings_TaskvisorValidateTimeout(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.validate_timeout" {
			m.items[i].intVal = 600
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 600, result.Taskvisor.ValidateTimeout)
}

func TestToSettings_TaskvisorPollInterval(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.poll_interval" {
			m.items[i].intVal = 10
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 10, result.Taskvisor.PollInterval)
}

func TestToSettings_PreservesTaskvisorFromBase(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
taskvisor:
  dispatch_timeout: 1800
  validate_timeout: 120
  poll_interval: 3
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.Equal(t, 1800, result.Taskvisor.DispatchTimeout, "dispatch_timeout must be preserved from base")
	assert.Equal(t, 120, result.Taskvisor.ValidateTimeout, "validate_timeout must be preserved from base")
	assert.Equal(t, 3, result.Taskvisor.PollInterval, "poll_interval must be preserved from base")
}

func TestModel_VimKeys(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	assert.Equal(t, 1, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	assert.Equal(t, 0, m.cursor)
}

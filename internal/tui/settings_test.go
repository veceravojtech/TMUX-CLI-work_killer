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

	assert.Len(t, m.items, 7)
	assert.Equal(t, "hooks.session_notify", m.items[0].key)
	assert.True(t, m.items[0].value)
	assert.Equal(t, "hooks.block_interactive", m.items[1].key)
	assert.False(t, m.items[1].value)
	assert.Equal(t, "commands.enabled", m.items[2].key)
	assert.True(t, m.items[2].value)
	assert.Equal(t, "supervisor.max_workers", m.items[3].key)
	assert.Equal(t, "supervisor.unplanned_audit", m.items[4].key)
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

	// Can't go past last item
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 6, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 5, m.cursor)

	// Can't go above first item
	for i := 0; i < 10; i++ {
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

	// Find plan.auto_approve and toggle it on
	for i, item := range m.items {
		if item.key == "plan.auto_approve" {
			m.items[i].value = true
		}
	}

	result := m.ToSettings()
	assert.True(t, result.Plan.AutoApprove)
	assert.False(t, result.Plan.AutoExecute)
}

func TestToSettings_PlanAutoExecute(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "plan.auto_execute" {
			m.items[i].value = true
		}
	}

	result := m.ToSettings()
	assert.False(t, result.Plan.AutoApprove)
	assert.True(t, result.Plan.AutoExecute)
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
			assert.Equal(t, 0, item.intVal)
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

	// Increment with right arrow
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	assert.Equal(t, 1, m.items[idx].intVal)

	// Increment more
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	assert.Equal(t, 2, m.items[idx].intVal)

	// Decrement with left arrow
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	assert.Equal(t, 1, m.items[idx].intVal)

	// Can't go below 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
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

	// Space/enter should not change numeric item
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	assert.Equal(t, 0, m.items[idx].intVal)
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

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

	assert.Len(t, m.items, 32)
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
	assert.Equal(t, "supervisor.max_goals", m.items[17].key)
	assert.Equal(t, "supervisor.max_stuck_retries", m.items[18].key)
	assert.Equal(t, "taskvisor.progress_timeout_sec", m.items[19].key)
	assert.Equal(t, "taskvisor.validate_script_timeout_sec", m.items[20].key)
	assert.Equal(t, "taskvisor.auto_commit", m.items[26].key)
	assert.Equal(t, "plan.audit", m.items[27].key)
	assert.Equal(t, "bool", m.items[27].kind)
	assert.True(t, m.items[27].value)
	assert.Equal(t, "taskvisor.auto_push", m.items[28].key)
	assert.Equal(t, "bool", m.items[28].kind)
	assert.False(t, m.items[28].value)
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

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 17, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 18, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 19, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 20, m.cursor)

	// One more down to reach the 22nd item
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 21, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 22, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 23, m.cursor)

	// Step down through the remaining items to the last one (32 items → max index 31)
	for want := 24; want <= 31; want++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
		assert.Equal(t, want, m.cursor)
	}

	// Can't go past last item (32 items → max index 31)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 31, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 30, m.cursor)

	// Can't go above first item
	for i := 0; i < 32; i++ {
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

func TestModel_ToSettings_PreservesGoalTransition(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: true
  block_interactive: true
  goal_transition: "tmux-cli notify-orchestrator \"goal-$GOAL_ID $NEW_STATUS\""
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

	// goal_transition is an unsurfaced (no items entry) hook string; the ToSettings
	// overlay onto baseSettings must carry it through the round-trip unchanged.
	assert.Equal(t, `tmux-cli notify-orchestrator "goal-$GOAL_ID $NEW_STATUS"`, result.Hooks.GoalTransition)
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

// TestModel_ToSettings_PlanAuditToggle proves flipping the plan.audit item to
// false writes a non-nil false pointer back via ToSettings (the pointer
// write-back idiom shared with taskvisor.auto_commit).
func TestModel_ToSettings_PlanAuditToggle(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "plan.audit" {
			m.items[i].value = false
		}
	}

	result := m.ToSettings()
	require.NotNil(t, result.Plan.Audit)
	assert.False(t, *result.Plan.Audit)
	assert.False(t, result.Plan.AuditEnabled())
}

// TestModel_ToSettings_PreservesPlanAudit proves an explicit audit=false in the
// base settings survives an untouched ToSettings round-trip (mirroring
// TestModel_ToSettings_PreservesUnplannedAudit).
func TestModel_ToSettings_PreservesPlanAudit(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	off := false
	settings.Plan.Audit = &off

	m := NewModel(dir, settings)
	result := m.ToSettings()

	require.NotNil(t, result.Plan.Audit)
	assert.False(t, *result.Plan.Audit, "explicit plan.audit=false must be preserved")
	assert.False(t, result.Plan.AuditEnabled())
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

func TestModel_ToSettings_PreservesAPIBlock(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `commands:
  enabled: true
api:
  enabled: true
  url: https://example.test
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	// The api block is no longer a TUI item; ToSettings overlays onto baseSettings,
	// so it survives the round-trip. LoadSettings force-corrects it to the canonical
	// values, so the round-tripped block reflects those (not the hand-edited url).
	assert.True(t, result.API.Enabled, "api.enabled must survive the TUI round-trip")
	assert.Equal(t, "https://tmux.vojta.ai", result.API.URL, "api.url must survive the TUI round-trip, force-corrected to canonical")
}

func TestDefaultSettings_EnablesAPIReporting(t *testing.T) {
	s := setup.DefaultSettings()
	assert.True(t, s.API.Enabled, "reporting is on by default for new projects")
	assert.Equal(t, "https://tmux.vojta.ai", s.API.URL)
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

func TestModel_ToSettings_MaxGoals(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	found := false
	for i, item := range m.items {
		if item.key == "supervisor.max_goals" {
			found = true
			m.items[i].intVal = 3
		}
	}
	assert.True(t, found, "supervisor.max_goals must be in TUI items")

	result := m.ToSettings()
	assert.Equal(t, 3, result.Supervisor.MaxGoals, "edited supervisor.max_goals must overlay into ToSettings")
}

func TestModel_ToSettings_PreservesMaxGoals(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_workers: 4
  max_goals: 4
  max_cycles: 5
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.Equal(t, 4, result.Supervisor.MaxGoals, "max_goals must be preserved")
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
}

// TestNewModel_IncludesMaxGoalsItem proves the supervisor.max_goals knob surfaces
// in the TUI items list with kind "int" and the default intVal of 1 (the MaxGoals=1
// semantics from DefaultSettings — parallel independent-goal dispatch defaults off).
func TestNewModel_IncludesMaxGoalsItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "supervisor.max_goals" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 1, item.intVal, "default supervisor.max_goals should be 1")
		}
	}
	assert.True(t, found, "supervisor.max_goals must be in TUI items")
}

// TestToSettings_OverlaysMaxGoals proves an edited supervisor.max_goals overlays
// onto Supervisor.MaxGoals while sibling/undisplayed Settings fields survive
// (AGENTS.md TUI INVARIANT — overlay onto baseSettings, not DefaultSettings).
func TestToSettings_OverlaysMaxGoals(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  max_goals: 1
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  circuit_breaker_k: 4
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "supervisor.max_goals" {
			m.items[i].intVal = 2
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 2, result.Supervisor.MaxGoals, "edited supervisor.max_goals must overlay onto settings")
	// Sibling/undisplayed fields preserved.
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

// TestToSettings_PreservesMaxGoalsFromBase proves a base setting.yaml max_goals
// value survives ToSettings() when the item is not edited (overlay-not-reset).
func TestToSettings_PreservesMaxGoalsFromBase(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_workers: 4
  max_goals: 3
  max_cycles: 5
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)

	m := NewModel(dir, settings)
	result := m.ToSettings()

	assert.Equal(t, 3, result.Supervisor.MaxGoals, "max_goals must overlay from base, not reset")
	assert.Equal(t, 5, result.Supervisor.MaxCycles)
}

// TestSettingsTUI_MaxWallClockSec_RoundTrip proves the P3 wall-clock ceiling is
// TUI-editable: NewModel surfaces the taskvisor.max_wall_clock_sec item, an edit
// overlays onto Settings via ToSettings(), and sibling/undisplayed base fields are
// preserved (AGENTS.md TUI overlay invariant).
func TestSettingsTUI_MaxWallClockSec_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  max_goals: 1
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  circuit_breaker_k: 4
  max_wall_clock_sec: 14400
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	var found bool
	for i, item := range m.items {
		if item.key == "taskvisor.max_wall_clock_sec" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 14400, item.intVal, "item should seed from loaded settings")
			m.items[i].intVal = 7200
		}
	}
	assert.True(t, found, "taskvisor.max_wall_clock_sec must be in TUI items")

	result := m.ToSettings()
	assert.Equal(t, 7200, result.Taskvisor.MaxWallClockSec, "edited max_wall_clock_sec must overlay into ToSettings")
	// Sibling/undisplayed fields preserved (overlay onto base, not DefaultSettings).
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

func TestSettingsTUI_ValidateScriptTimeoutSec_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  max_goals: 1
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  validate_script_timeout_sec: 120
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	var found bool
	for i, item := range m.items {
		if item.key == "taskvisor.validate_script_timeout_sec" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 120, item.intVal, "item should seed from loaded settings")
			m.items[i].intVal = 600
		}
	}
	assert.True(t, found, "taskvisor.validate_script_timeout_sec must be in TUI items")

	result := m.ToSettings()
	assert.Equal(t, 600, result.Taskvisor.ValidateScriptTimeoutSec, "edited validate_script_timeout_sec must overlay into ToSettings")
	// Sibling/undisplayed fields preserved (overlay onto base, not DefaultSettings).
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
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

	assert.Len(t, m.items, 32)

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
	assert.Contains(t, keys, "taskvisor.require_plan_approval")

	for _, item := range m.items {
		switch item.key {
		case "taskvisor.dispatch_timeout":
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 3600, item.intVal)
		case "taskvisor.validate_timeout":
			assert.Equal(t, "int", item.kind)
			// Default seeded via DeriveValidateTimeout(600,4,4) = 1260 (C4).
			assert.Equal(t, 1260, item.intVal)
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

	assert.Len(t, m.items, 32)

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

// TestSettings_ProgressTimeoutSec_TUIEditable proves the P2 progress_timeout_sec
// setting surfaces in the items list and that an edited value overlays onto
// Taskvisor.ProgressTimeoutSec while sibling/undisplayed Settings fields survive
// (AGENTS.md TUI INVARIANT — overlay onto loaded settings, not DefaultSettings).
func TestSettings_ProgressTimeoutSec_TUIEditable(t *testing.T) {
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
  progress_timeout_sec: 300
  transient_retry_max_attempts: 3
  transient_retry_backoff_ms: 500
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	// The item is present, int-kind, seeded from loaded settings.
	var found bool
	for _, item := range m.items {
		if item.key == "taskvisor.progress_timeout_sec" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 300, item.intVal, "seeded from loaded settings")
		}
	}
	require.True(t, found, "taskvisor.progress_timeout_sec must be TUI-editable")

	for i, item := range m.items {
		if item.key == "taskvisor.progress_timeout_sec" {
			m.items[i].intVal = 120
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 120, result.Taskvisor.ProgressTimeoutSec, "edited value overlays onto loaded settings")
	// Sibling/undisplayed taskvisor + supervisor fields preserved through the overlay.
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 500, result.Taskvisor.TransientRetryBackoffMs)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
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

// integrationCmdIdx finds the taskvisor.integration_cmd item index (fails the
// test if absent).
func integrationCmdIdx(t *testing.T, m Model) int {
	t.Helper()
	for i, item := range m.items {
		if item.key == "taskvisor.integration_cmd" {
			return i
		}
	}
	t.Fatal("taskvisor.integration_cmd must be in TUI items")
	return -1
}

// TestNewModel_IncludesIntegrationCmdItem proves the P4 integration-gate command
// surfaces in the TUI items list as a "string" kind seeded from loaded settings.
func TestNewModel_IncludesIntegrationCmdItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	settings.Taskvisor.IntegrationCmd = "make test"
	m := NewModel(dir, settings)

	idx := integrationCmdIdx(t, m)
	assert.Equal(t, "string", m.items[idx].kind, "integration_cmd must be a string-kind item")
	assert.Equal(t, "make test", m.items[idx].strVal, "strVal must seed from loaded settings")
}

// TestModel_StringItem_TypingAppendsRunes proves printable runes append to a
// focused string item's strVal.
func TestModel_StringItem_TypingAppendsRunes(t *testing.T) {
	dir := t.TempDir()
	m := NewModel(dir, setup.DefaultSettings())
	m.cursor = integrationCmdIdx(t, m)

	for _, r := range []rune{'m', 'a', 'k', 'e'} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}

	assert.Equal(t, "make", m.items[m.cursor].strVal)
}

// TestModel_StringItem_Backspace proves backspace removes the last rune and is
// safe on an empty string.
func TestModel_StringItem_Backspace(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	settings.Taskvisor.IntegrationCmd = "ab"
	m := NewModel(dir, settings)
	m.cursor = integrationCmdIdx(t, m)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	assert.Equal(t, "a", m.items[m.cursor].strVal)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	assert.Equal(t, "", m.items[m.cursor].strVal)

	// Backspace on empty is a no-op (must not panic / underflow).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	assert.Equal(t, "", m.items[m.cursor].strVal)
}

// TestModel_StringItem_SpaceTypesSpace proves space is a literal char on a string
// item (does NOT toggle, since the focused item is a string).
func TestModel_StringItem_SpaceTypesSpace(t *testing.T) {
	dir := t.TempDir()
	m := NewModel(dir, setup.DefaultSettings())
	m.cursor = integrationCmdIdx(t, m)

	for _, r := range []rune{'g', 'o'} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	// bubbletea delivers a lone space as KeySpace with Runes==[' '].
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = updated.(Model)

	assert.Equal(t, "go t", m.items[m.cursor].strVal, "space must type a literal space")
}

// TestModel_ToSettings_IntegrationCmd proves an edited strVal maps to
// Taskvisor.IntegrationCmd.
func TestModel_ToSettings_IntegrationCmd(t *testing.T) {
	dir := t.TempDir()
	m := NewModel(dir, setup.DefaultSettings())
	idx := integrationCmdIdx(t, m)
	m.items[idx].strVal = "make integration"

	result := m.ToSettings()
	assert.Equal(t, "make integration", result.Taskvisor.IntegrationCmd)
}

// TestModel_ToSettings_OverlaysNotReset_WithStringItem proves an edited string
// item overlays onto Taskvisor.IntegrationCmd while sibling/undisplayed Settings
// fields survive (AGENTS.md TUI overlay-not-reset invariant).
func TestModel_ToSettings_OverlaysNotReset_WithStringItem(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `hooks:
  session_notify: false
  block_interactive: true
commands:
  enabled: true
supervisor:
  max_cycles: 7
  max_goals: 2
taskvisor:
  dispatch_timeout: 1234
  validate_timeout: 5678
  circuit_breaker_k: 4
  integration_cmd: make test
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	idx := integrationCmdIdx(t, m)
	assert.Equal(t, "make test", m.items[idx].strVal, "item seeded from base setting.yaml")
	m.items[idx].strVal = "go test ./..."

	result := m.ToSettings()
	assert.Equal(t, "go test ./...", result.Taskvisor.IntegrationCmd, "edited integration_cmd overlays into ToSettings")
	// Sibling/undisplayed fields preserved through the overlay.
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
	assert.Equal(t, 2, result.Supervisor.MaxGoals)
}

func TestNewModel_IncludesMaxStuckRetriesItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	var found bool
	for _, item := range m.items {
		if item.key == "supervisor.max_stuck_retries" {
			found = true
			assert.Equal(t, "int", item.kind)
			assert.Equal(t, 3, item.intVal, "default max_stuck_retries should be 3")
		}
	}
	assert.True(t, found, "supervisor.max_stuck_retries must be in TUI items")
}

func TestToSettings_OverlaysMaxStuckRetries(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "supervisor.max_stuck_retries" {
			m.items[i].intVal = 5
		}
	}

	result := m.ToSettings()
	assert.Equal(t, 5, result.Supervisor.MaxStuckRetries, "edited max_stuck_retries must overlay into ToSettings")
}

func TestToSettings_RequirePlanApproval(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	found := false
	for i, item := range m.items {
		if item.key == "taskvisor.require_plan_approval" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.False(t, item.value, "default should be false")
			m.items[i].value = true
		}
	}
	assert.True(t, found, "taskvisor.require_plan_approval must be in TUI items")

	result := m.ToSettings()
	assert.True(t, result.Taskvisor.RequirePlanApproval, "toggled require_plan_approval must overlay into ToSettings")
}

func TestToSettings_HaltOnStaleBinary(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	found := false
	for i, item := range m.items {
		if item.key == "taskvisor.halt_on_stale_binary" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.False(t, item.value, "default should be false")
			m.items[i].value = true
		}
	}
	assert.True(t, found, "taskvisor.halt_on_stale_binary must be in TUI items")

	result := m.ToSettings()
	assert.True(t, result.Taskvisor.HaltOnStaleBinary, "toggled halt_on_stale_binary must overlay into ToSettings")
}

func TestToSettings_RestartOnStaleBinary(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	found := false
	for i, item := range m.items {
		if item.key == "taskvisor.restart_on_stale_binary" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.True(t, item.value, "default should be true")
			m.items[i].value = false
		}
	}
	assert.True(t, found, "taskvisor.restart_on_stale_binary must be in TUI items")

	result := m.ToSettings()
	assert.False(t, result.Taskvisor.RestartOnStaleBinary, "toggled restart_on_stale_binary must overlay into ToSettings")
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

// TestSettingsTUI_AutoCommit_RoundTrip proves the 29th item taskvisor.auto_commit
// surfaces as a bool seeded from AutoCommitEnabled(), that a toggle writes the
// *bool pointer via ToSettings(), and that sibling/undisplayed base fields are
// preserved (AGENTS.md TUI overlay invariant).
func TestSettingsTUI_AutoCommit_RoundTrip(t *testing.T) {
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
  circuit_breaker_k: 4
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	var found bool
	for i, item := range m.items {
		if item.key == "taskvisor.auto_commit" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.True(t, item.value, "item must seed true (default ON via the LoadSettings backfill)")
			m.items[i].value = false
		}
	}
	require.True(t, found, "taskvisor.auto_commit must be in TUI items")

	result := m.ToSettings()
	require.NotNil(t, result.Taskvisor.AutoCommit, "ToSettings must write the *bool pointer")
	assert.False(t, *result.Taskvisor.AutoCommit, "toggled-off value must overlay into ToSettings")
	assert.False(t, result.Taskvisor.AutoCommitEnabled())
	// Sibling/undisplayed fields preserved (overlay onto base, not DefaultSettings).
	assert.Equal(t, 1234, result.Taskvisor.DispatchTimeout)
	assert.Equal(t, 5678, result.Taskvisor.ValidateTimeout)
	assert.Equal(t, 4, result.Taskvisor.CircuitBreakerK)
	assert.Equal(t, 7, result.Supervisor.MaxCycles)
}

// TestNewModel_IncludesGitFreshnessItem proves the git-freshness setting is
// surfaced as a bool item seeded from GitFreshnessEnabled() and that ToSettings()
// round-trips a toggle via the *bool pointer write-back idiom (AGENTS.md TUI
// mirror invariant — items + ToSettings + count stay in lockstep).
func TestNewModel_IncludesGitFreshnessItem(t *testing.T) {
	dir := t.TempDir()
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	var found bool
	for i, item := range m.items {
		if item.key == "taskvisor.git_freshness" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.True(t, item.value, "item must seed true (default ON via GitFreshnessEnabled)")
			m.items[i].value = false
		}
	}
	require.True(t, found, "taskvisor.git_freshness must be in TUI items")

	result := m.ToSettings()
	require.NotNil(t, result.Taskvisor.GitFreshness, "ToSettings must write the *bool pointer")
	assert.False(t, *result.Taskvisor.GitFreshness, "toggled-off value must overlay into ToSettings")
	assert.False(t, result.Taskvisor.GitFreshnessEnabled())
}

// TestNewModel_IncludesValidationItem proves the goal-validation toggle is
// surfaced as a bool item seeded from ValidationEnabled() and that ToSettings()
// round-trips a toggle via the *bool pointer write-back idiom (AGENTS.md TUI
// mirror invariant — items + ToSettings + count stay in lockstep).
func TestNewModel_IncludesValidationItem(t *testing.T) {
	dir := t.TempDir()
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	var found bool
	for i, item := range m.items {
		if item.key == "taskvisor.validation" {
			found = true
			assert.Equal(t, "bool", item.kind)
			assert.True(t, item.value, "item must seed true (default ON via ValidationEnabled)")
			m.items[i].value = false
		}
	}
	require.True(t, found, "taskvisor.validation must be in TUI items")

	result := m.ToSettings()
	require.NotNil(t, result.Taskvisor.Validation, "ToSettings must write the *bool pointer")
	assert.False(t, *result.Taskvisor.Validation, "toggled-off value must overlay into ToSettings")
	assert.False(t, result.Taskvisor.ValidationEnabled())
}

// TestSettingsTUI_AutoCommit_ExplicitFalsePreserved proves an opt-out base
// setting.yaml seeds the item false and survives an unedited ToSettings()
// round-trip — the TUI must never silently re-enable auto-commit.
func TestSettingsTUI_AutoCommit_ExplicitFalsePreserved(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `commands:
  enabled: true
taskvisor:
  auto_commit: false
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for _, item := range m.items {
		if item.key == "taskvisor.auto_commit" {
			assert.False(t, item.value, "item must seed false from the explicit opt-out")
		}
	}

	result := m.ToSettings()
	require.NotNil(t, result.Taskvisor.AutoCommit)
	assert.False(t, *result.Taskvisor.AutoCommit, "explicit false must survive the TUI round-trip")
}

// TestNewModel_IncludesPlanningModeItem proves taskvisor.planning_mode surfaces
// in the TUI items list (AGENTS.md TUI INVARIANT) as an enum toggling between
// roadmap and incremental, seeded from the loaded settings.
func TestNewModel_IncludesPlanningModeItem(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	found := false
	for _, item := range m.items {
		if item.key == "taskvisor.planning_mode" {
			found = true
			assert.Equal(t, "enum", item.kind)
			assert.Equal(t, "roadmap", item.strVal, "item must seed the default roadmap")
			assert.Equal(t, []string{"roadmap", "incremental"}, item.options)
		}
	}
	assert.True(t, found, "taskvisor.planning_mode must be in TUI items")
}

// TestUpdate_PlanningModeCyclesOnSpaceEnter proves space/enter toggles the enum
// between roadmap and incremental instead of the bool [x]/[ ] behavior.
func TestUpdate_PlanningModeCyclesOnSpaceEnter(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.planning_mode" {
			m.cursor = i
		}
	}
	require.Equal(t, "taskvisor.planning_mode", m.items[m.cursor].key)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	assert.Equal(t, "incremental", m.items[m.cursor].strVal, "space must cycle roadmap → incremental")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assert.Equal(t, "roadmap", m.items[m.cursor].strVal, "enter must cycle incremental → roadmap")
}

// TestToSettings_PlanningModeRoundTrips proves an edited planning_mode overlays
// onto Taskvisor.PlanningMode while sibling/undisplayed Settings fields survive
// (AGENTS.md TUI INVARIANT: overlay onto loaded settings, not DefaultSettings).
func TestToSettings_PlanningModeRoundTrips(t *testing.T) {
	dir := t.TempDir()
	writeSettingsYAML(t, dir, `commands:
  enabled: true
supervisor:
  max_cycles: 10
  cycle_delay: 15
taskvisor:
  poll_interval: 7
`)
	settings, err := setup.LoadSettings(dir)
	require.NoError(t, err)
	m := NewModel(dir, settings)

	for i, item := range m.items {
		if item.key == "taskvisor.planning_mode" {
			assert.Equal(t, "roadmap", item.strVal, "coerced load must seed roadmap")
			m.items[i].strVal = "incremental"
		}
	}

	result := m.ToSettings()
	assert.Equal(t, "incremental", result.Taskvisor.PlanningMode, "edited value must overlay into ToSettings")
	assert.Equal(t, 10, result.Supervisor.MaxCycles, "sibling fields must be preserved")
	assert.Equal(t, 15, result.Supervisor.CycleDelay, "sibling fields must be preserved")
	assert.Equal(t, 7, result.Taskvisor.PollInterval, "sibling taskvisor fields must be preserved")
	assert.True(t, result.API.Enabled, "undisplayed api block must be preserved")
}

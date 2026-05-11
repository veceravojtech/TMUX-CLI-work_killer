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

	assert.Len(t, m.items, 3)
	assert.Equal(t, "hooks.session_notify", m.items[0].key)
	assert.True(t, m.items[0].value)
	assert.Equal(t, "hooks.block_interactive", m.items[1].key)
	assert.False(t, m.items[1].value)
	assert.Equal(t, "commands.enabled", m.items[2].key)
	assert.True(t, m.items[2].value)
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

	// Can't go past last item
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 2, m.cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 1, m.cursor)

	// Can't go above first item
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 0, m.cursor)
}

func TestModel_ToSettings(t *testing.T) {
	dir := t.TempDir()
	settings := setup.DefaultSettings()
	m := NewModel(dir, settings)

	// Toggle first item off
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)

	result := m.ToSettings()
	assert.True(t, result.Hooks.SessionNotify)
	assert.True(t, result.Hooks.BlockInteractive)
	assert.True(t, result.Commands.Enabled)
}

func TestModel_SaveOnQuit(t *testing.T) {
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

	// Press q to save & quit - this returns a save command
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	require.NotNil(t, cmd)

	// Execute the save command
	msg := cmd()
	result, ok := msg.(saveResultMsg)
	require.True(t, ok)
	assert.NoError(t, result.err)

	// Verify saved
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

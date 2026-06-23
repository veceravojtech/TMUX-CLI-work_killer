package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteHookScripts_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{
		"tmux-session-notify.sh": "#!/bin/bash\necho notify",
		"tmux-session-start.sh":  "#!/bin/bash\necho start",
		"tmux-session-stop.sh":   "#!/bin/bash\necho stop",
	}

	err := WriteHookScripts(root, scripts)
	require.NoError(t, err)

	for name := range scripts {
		path := filepath.Join(root, ".tmux-cli", "hooks", name)
		assert.FileExists(t, path)
	}
}

func TestWriteHookScripts_ExecutablePermissions(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{
		"hook-a.sh": "#!/bin/bash\necho a",
		"hook-b.sh": "#!/bin/bash\necho b",
	}

	err := WriteHookScripts(root, scripts)
	require.NoError(t, err)

	for name := range scripts {
		path := filepath.Join(root, ".tmux-cli", "hooks", name)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "script %s should be 0755", name)
	}
}

func TestWriteHookScripts_WritesWindowWatchdog(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{
		"tmux-window-watchdog.sh": "#!/usr/bin/env bash\n",
	}

	err := WriteHookScripts(root, scripts)
	require.NoError(t, err)

	path := filepath.Join(root, ".tmux-cli", "hooks", "tmux-window-watchdog.sh")
	assert.FileExists(t, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "watchdog hook should be 0755")
}

func TestWriteHookScripts_CreatesDirectories(t *testing.T) {
	root := t.TempDir()

	err := WriteHookScripts(root, map[string]string{"x.sh": "#!/bin/bash"})
	require.NoError(t, err)

	hooksDir := filepath.Join(root, ".tmux-cli", "hooks")
	logsDir := filepath.Join(root, ".tmux-cli", "logs")

	assert.DirExists(t, hooksDir)
	assert.DirExists(t, logsDir)
}

func TestWriteHookScripts_Idempotent(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{
		"hook.sh": "#!/bin/bash\necho hello",
	}

	err := WriteHookScripts(root, scripts)
	require.NoError(t, err)

	err = WriteHookScripts(root, scripts)
	require.NoError(t, err)

	path := filepath.Join(root, ".tmux-cli", "hooks", "hook.sh")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/bash\necho hello", string(content))
}

func TestWriteHookScripts_OverwritesExisting(t *testing.T) {
	root := t.TempDir()

	err := WriteHookScripts(root, map[string]string{"hook.sh": "old content"})
	require.NoError(t, err)

	err = WriteHookScripts(root, map[string]string{"hook.sh": "new content"})
	require.NoError(t, err)

	path := filepath.Join(root, ".tmux-cli", "hooks", "hook.sh")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
}

func TestWriteHookScripts_EmptyMap(t *testing.T) {
	root := t.TempDir()

	err := WriteHookScripts(root, map[string]string{})
	require.NoError(t, err)

	hooksDir := filepath.Join(root, ".tmux-cli", "hooks")
	logsDir := filepath.Join(root, ".tmux-cli", "logs")
	assert.DirExists(t, hooksDir)
	assert.DirExists(t, logsDir)

	entries, err := os.ReadDir(hooksDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

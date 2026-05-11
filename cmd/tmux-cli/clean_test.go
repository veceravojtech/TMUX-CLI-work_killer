package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartCmd_HasCleanFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("clean")
	assert.NotNil(t, flag, "--clean flag should exist on start command")
}

func TestStartAttachCmd_HasCleanFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"start-attach"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	flag := cmd.Flags().Lookup("clean")
	assert.NotNil(t, flag, "--clean flag should exist on start-attach command")
}

func TestCleanProjectDir_RemovesTmuxCliFolder(t *testing.T) {
	dir := t.TempDir()
	tmuxCliDir := filepath.Join(dir, ".tmux-cli")

	require.NoError(t, os.MkdirAll(filepath.Join(tmuxCliDir, "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxCliDir, "setting.yaml"), []byte("hooks:\n  session_notify: true\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxCliDir, "hooks", "notify.sh"), []byte("#!/bin/sh\n"), 0o755))

	err := cleanProjectDir(dir)
	assert.NoError(t, err)

	_, statErr := os.Stat(tmuxCliDir)
	assert.True(t, os.IsNotExist(statErr), ".tmux-cli/ directory should be removed after clean")
}

func TestCleanProjectDir_NoErrorWhenDirMissing(t *testing.T) {
	dir := t.TempDir()

	err := cleanProjectDir(dir)
	assert.NoError(t, err, "cleanProjectDir should not error when .tmux-cli/ does not exist")
}

func TestCleanProjectDir_PreservesOtherFiles(t *testing.T) {
	dir := t.TempDir()

	otherFile := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(otherFile, []byte("module test\n"), 0o644))

	tmuxCliDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxCliDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxCliDir, "setting.yaml"), []byte("test"), 0o644))

	err := cleanProjectDir(dir)
	assert.NoError(t, err)

	assert.FileExists(t, otherFile, "files outside .tmux-cli/ should be preserved")
}

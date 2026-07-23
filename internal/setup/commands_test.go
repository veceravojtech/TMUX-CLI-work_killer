package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCommands_CreatesFiles(t *testing.T) {
	root := t.TempDir()
	templates := map[string]string{
		"supervisor.md": "# Supervisor\ncontent",
		"execute.md":    "# Execute\ncontent",
		"workflow.xml":  "<task>workflow</task>",
	}

	err := WriteCommands(root, templates)
	require.NoError(t, err)

	for name := range templates {
		path := filepath.Join(root, ".claude", "commands", "tmux", name)
		assert.FileExists(t, path)
	}
}

func TestWriteCommands_CreatesSubdirectories(t *testing.T) {
	root := t.TempDir()
	templates := map[string]string{
		"worker/task/workflow.xml": "<task>deep nested</task>",
	}

	err := WriteCommands(root, templates)
	require.NoError(t, err)

	path := filepath.Join(root, ".claude", "commands", "tmux", "worker", "task", "workflow.xml")
	assert.FileExists(t, path)

	subDir := filepath.Join(root, ".claude", "commands", "tmux", "worker", "task")
	assert.DirExists(t, subDir)
}

func TestWriteCommands_CleanBeforeWrite(t *testing.T) {
	root := t.TempDir()
	tmuxDir := filepath.Join(root, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(tmuxDir, 0755))
	staleFile := filepath.Join(tmuxDir, "stale-command.md")
	require.NoError(t, os.WriteFile(staleFile, []byte("stale"), 0644))

	err := WriteCommands(root, map[string]string{
		"fresh.md": "fresh content",
	})
	require.NoError(t, err)

	assert.NoFileExists(t, staleFile)
	assert.FileExists(t, filepath.Join(tmuxDir, "fresh.md"))
}

func TestWriteCommands_PreservesOtherCommands(t *testing.T) {
	root := t.TempDir()
	commandsDir := filepath.Join(root, ".claude", "commands")
	require.NoError(t, os.MkdirAll(commandsDir, 0755))
	otherFile := filepath.Join(commandsDir, "mycommand.md")
	require.NoError(t, os.WriteFile(otherFile, []byte("my custom command"), 0644))

	err := WriteCommands(root, map[string]string{
		"supervisor.md": "# Supervisor",
	})
	require.NoError(t, err)

	content, err := os.ReadFile(otherFile)
	require.NoError(t, err)
	assert.Equal(t, "my custom command", string(content))
}

func TestWriteCommands_EmptyMap(t *testing.T) {
	root := t.TempDir()
	tmuxDir := filepath.Join(root, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(tmuxDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "old.md"), []byte("old"), 0644))

	err := WriteCommands(root, map[string]string{})
	require.NoError(t, err)

	entries, err := os.ReadDir(tmuxDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPurgeUserCommandShadow_RemovesSeededTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shadowDir := filepath.Join(home, ".claude", "commands", "tmux")
	require.NoError(t, os.MkdirAll(filepath.Join(shadowDir, "worker"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(shadowDir, "stale.md"), []byte("stale"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(shadowDir, "worker", "deep.xml"), []byte("<task/>"), 0644))

	err := PurgeUserCommandShadow()
	require.NoError(t, err)

	assert.NoDirExists(t, shadowDir)
}

func TestPurgeUserCommandShadow_NoOpWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := PurgeUserCommandShadow()
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(home, ".claude", "commands", "tmux"))
}

func TestPurgeUserCommandShadowAt_PreservesSiblingCommands(t *testing.T) {
	home := t.TempDir()
	commandsDir := filepath.Join(home, ".claude", "commands")
	require.NoError(t, os.MkdirAll(filepath.Join(commandsDir, "tmux"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(commandsDir, "tmux", "stale.md"), []byte("stale"), 0644))
	otherFile := filepath.Join(commandsDir, "mycommand.md")
	require.NoError(t, os.WriteFile(otherFile, []byte("my custom command"), 0644))

	err := purgeUserCommandShadowAt(home)
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(commandsDir, "tmux"))
	content, err := os.ReadFile(otherFile)
	require.NoError(t, err)
	assert.Equal(t, "my custom command", string(content))
}

func TestWriteCommands_CorrectContent(t *testing.T) {
	root := t.TempDir()
	expected := "# Header\n\nParagraph with special chars: <>&\"\nLine 2"
	templates := map[string]string{
		"test.md": expected,
	}

	err := WriteCommands(root, templates)
	require.NoError(t, err)

	path := filepath.Join(root, ".claude", "commands", "tmux", "test.md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, expected, string(content))
}

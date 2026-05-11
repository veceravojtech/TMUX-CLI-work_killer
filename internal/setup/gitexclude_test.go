package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var managedEntries = []string{
	"/.tmux-cli/",
	"/.claude/settings.json",
	"/.claude/commands/tmux/",
}

func setupGitInfo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755))
	return root
}

func readExclude(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	require.NoError(t, err)
	return string(data)
}

func TestEnsureGitExclude_AddsEntries(t *testing.T) {
	root := setupGitInfo(t)

	err := EnsureGitExclude(root)
	require.NoError(t, err)

	content := readExclude(t, root)
	assert.Contains(t, content, "# tmux-cli managed")
	for _, entry := range managedEntries {
		assert.Contains(t, content, entry)
	}
}

func TestEnsureGitExclude_Idempotent(t *testing.T) {
	root := setupGitInfo(t)

	require.NoError(t, EnsureGitExclude(root))
	first := readExclude(t, root)

	require.NoError(t, EnsureGitExclude(root))
	second := readExclude(t, root)

	assert.Equal(t, first, second)
}

func TestEnsureGitExclude_PreservesExisting(t *testing.T) {
	root := setupGitInfo(t)
	existing := "# existing comment\n*.log\n/build/\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".git", "info", "exclude"),
		[]byte(existing), 0o644,
	))

	require.NoError(t, EnsureGitExclude(root))

	content := readExclude(t, root)
	assert.True(t, strings.HasPrefix(content, existing),
		"existing content must be preserved at the top")
	for _, entry := range managedEntries {
		assert.Contains(t, content, entry)
	}
}

func TestEnsureGitExclude_NoGitDir(t *testing.T) {
	root := t.TempDir()

	err := EnsureGitExclude(root)
	assert.NoError(t, err)
}

func TestEnsureGitExclude_PartialEntries(t *testing.T) {
	root := setupGitInfo(t)
	partial := "# tmux-cli managed\n/.tmux-cli/\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".git", "info", "exclude"),
		[]byte(partial), 0o644,
	))

	require.NoError(t, EnsureGitExclude(root))

	content := readExclude(t, root)
	assert.Equal(t, 1, strings.Count(content, "/.tmux-cli/"),
		"should not duplicate existing entry")
	assert.Contains(t, content, "/.claude/settings.json")
	assert.Contains(t, content, "/.claude/commands/tmux/")
}

func TestEnsureGitExclude_CreatesExcludeFile(t *testing.T) {
	root := setupGitInfo(t)
	// .git/info/ exists but exclude file does not
	_, err := os.Stat(filepath.Join(root, ".git", "info", "exclude"))
	require.True(t, os.IsNotExist(err), "precondition: exclude must not exist yet")

	require.NoError(t, EnsureGitExclude(root))

	content := readExclude(t, root)
	assert.Contains(t, content, "# tmux-cli managed")
	for _, entry := range managedEntries {
		assert.Contains(t, content, entry)
	}
}

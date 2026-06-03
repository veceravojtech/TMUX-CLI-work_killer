package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTemplates_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	templates := map[string]string{
		"_base/adr.md":             "# ADR template",
		"php-symfony/discovery.md": "# Discovery template",
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	for relPath, content := range templates {
		absPath := filepath.Join(dir, ".tmux-cli", "templates", relPath)
		assert.FileExists(t, absPath)
		data, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
	}
}

func TestWriteTemplates_PreservesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	templates := map[string]string{
		"_base/adr.md":             "# ADR",
		"php-symfony/discovery.md": "# Discovery",
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	assert.DirExists(t, filepath.Join(dir, ".tmux-cli", "templates", "_base"))
	assert.DirExists(t, filepath.Join(dir, ".tmux-cli", "templates", "php-symfony"))
}

func TestWriteTemplates_CleanSlate(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, ".tmux-cli", "templates")
	require.NoError(t, os.MkdirAll(tplDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tplDir, "stale.md"), []byte("old"), 0644))

	templates := map[string]string{
		"_base/fresh.md": "# Fresh",
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(tplDir, "stale.md"))
	assert.FileExists(t, filepath.Join(tplDir, "_base", "fresh.md"))
}

func TestWriteTemplates_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	templates := map[string]string{
		"_base/adr.md":             "# ADR",
		"php-symfony/discovery.md": "# Discovery",
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	tplDir := filepath.Join(dir, ".tmux-cli", "templates")
	var tmpFiles []string
	filepath.Walk(tplDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".tmp" {
			tmpFiles = append(tmpFiles, path)
		}
		return nil
	})
	assert.Empty(t, tmpFiles, "no .tmp files should remain after WriteTemplates")
}

func TestWriteTemplates_EmptyMap(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, ".tmux-cli", "templates")
	require.NoError(t, os.MkdirAll(tplDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tplDir, "old.md"), []byte("old"), 0644))

	err := WriteTemplates(dir, map[string]string{})
	require.NoError(t, err)

	entries, err := os.ReadDir(tplDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "templates dir should be empty after writing empty map")
}

func TestWriteTemplates_CorrectContent(t *testing.T) {
	dir := t.TempDir()
	special := "# Template\n\nSpecial chars: čřžšůúýáíé €£¥ «»\n\ttabs\ntrailing newline\n"
	templates := map[string]string{
		"_base/special.md": special,
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "templates", "_base", "special.md"))
	require.NoError(t, err)
	assert.Equal(t, special, string(data))
}

func TestWriteTemplates_DeepNesting(t *testing.T) {
	dir := t.TempDir()
	templates := map[string]string{
		"deep/nested/file.md": "# Deep",
	}

	err := WriteTemplates(dir, templates)
	require.NoError(t, err)

	assert.DirExists(t, filepath.Join(dir, ".tmux-cli", "templates", "deep", "nested"))
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "templates", "deep", "nested", "file.md"))
}

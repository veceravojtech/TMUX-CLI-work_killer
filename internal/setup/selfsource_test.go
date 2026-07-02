package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkoutDir builds a candidate source dir: module names the go.mod module
// line ("" ⇒ no go.mod at all), makefile controls the Makefile's presence.
func checkoutDir(t *testing.T, module string, makefile bool) string {
	t.Helper()
	dir := t.TempDir()
	if module != "" {
		gomod := "module " + module + "\n\ngo 1.25.5\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644))
	}
	if makefile {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("install:\n\ttrue\n"), 0o644))
	}
	return dir
}

func TestIsCliSourceCheckout_MatchesModuleAndMakefile(t *testing.T) {
	t.Run("cli module and Makefile -> true", func(t *testing.T) {
		dir := checkoutDir(t, "github.com/console/tmux-cli", true)
		assert.True(t, IsCliSourceCheckout(dir))
	})

	t.Run("wrong module -> false", func(t *testing.T) {
		dir := checkoutDir(t, "github.com/other/project", true)
		assert.False(t, IsCliSourceCheckout(dir))
	})

	t.Run("missing Makefile -> false", func(t *testing.T) {
		dir := checkoutDir(t, "github.com/console/tmux-cli", false)
		assert.False(t, IsCliSourceCheckout(dir))
	})

	t.Run("missing go.mod -> false", func(t *testing.T) {
		dir := checkoutDir(t, "", true)
		assert.False(t, IsCliSourceCheckout(dir))
	})
}

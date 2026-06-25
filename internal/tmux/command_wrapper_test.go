package tmux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// createFakeShell writes a fake, executable shell file named `name` into `dir`
// so that isValidShellPath (which requires an absolute path to an existing,
// regular, executable file) accepts it WITHOUT depending on which shells happen
// to be installed on the host. Returns the absolute path to the fake shell.
func createFakeShell(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to create fake shell %s: %v", path, err)
	}
	return path
}

func TestWrapCommandForPersistence(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		shell    string
		expected string
	}{
		{
			name:     "simple command with zsh",
			command:  "ch",
			shell:    "/bin/zsh",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`,
		},
		{
			name:     "simple command with bash",
			command:  "exec ch",
			shell:    "/bin/bash",
			expected: `bash -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; exec ch"`,
		},
		{
			name:     "command with quotes needs escaping",
			command:  `echo "hello"`,
			shell:    "/bin/bash",
			expected: `bash -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; echo \"hello\""`,
		},
		{
			name:     "command with multiple quotes",
			command:  `echo "hello" && echo "world"`,
			shell:    "/bin/zsh",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; echo \"hello\" && echo \"world\""`,
		},
		{
			name:     "empty command returns empty",
			command:  "",
			shell:    "/bin/zsh",
			expected: "",
		},
		{
			name:     "fish shell (falls back if not installed)",
			command:  "ch",
			shell:    "/usr/bin/fish",                                                                                          // May not exist, will fall back to sh
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`, // Expected fallback
		},
		{
			name:     "sh shell",
			command:  "sleep 10",
			shell:    "/bin/sh",
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; sleep 10"`,
		},
		{
			name:     "already wrapped command not double-wrapped",
			command:  `zsh -ic "ch"`,
			shell:    "/bin/zsh",
			expected: `zsh -ic "ch"`, // Should remain unchanged
		},
		{
			name:     "already wrapped with bash",
			command:  `bash -ic "exec ch"`,
			shell:    "/bin/bash",
			expected: `bash -ic "exec ch"`, // Should remain unchanged
		},
		{
			name:     "complex command with pipes",
			command:  "cat file.txt | grep pattern",
			shell:    "/bin/bash",
			expected: `bash -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; cat file.txt | grep pattern"`,
		},
		{
			name:     "command with -ic flag but not shell wrapper",
			command:  "myapp -ic config.yaml",
			shell:    "/bin/zsh",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; myapp -ic config.yaml"`, // Should be wrapped, not detected as already wrapped
		},
		{
			name:     "command with dollar sign (variable expansion)",
			command:  "echo $HOME",
			shell:    "/bin/bash",
			expected: `bash -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; echo \$HOME"`, // Dollar should be escaped
		},
		{
			name:     "command with backticks (command substitution)",
			command:  "echo `date`",
			shell:    "/bin/bash",
			expected: "bash -ic \"export TMUX_WINDOW_UUID=\\\"\\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\\\"; echo \\`date\\`\"", // Backticks should be escaped
		},
		{
			name:     "command with backslash",
			command:  `echo "test\nline"`,
			shell:    "/bin/bash",
			expected: `bash -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; echo \"test\\nline\""`, // Backslash and quotes escaped
		},
	}

	// Hermeticity: rather than pointing SHELL at host paths (/bin/zsh may be
	// absent), create fake executable shells in a temp dir and redirect SHELL at
	// them by basename. The shells the cases expect to be USED (zsh/bash/sh) are
	// created; the fish case's basename is deliberately NOT created, so it resolves
	// to a missing path and exercises the real isValidShellPath -> /bin/sh fallback.
	shellDir := t.TempDir()
	for _, name := range []string{"zsh", "bash", "sh"} {
		createFakeShell(t, shellDir, name)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Redirect SHELL at the hermetic fake shell of the same name, so the
			// test does not depend on which shells are installed on the host.
			t.Setenv("SHELL", filepath.Join(shellDir, filepath.Base(tt.shell)))

			result := WrapCommandForPersistence(tt.command)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWrapCommandForPersistence_NoShellEnv(t *testing.T) {
	// Test fallback to /bin/sh when $SHELL not set
	originalShell := os.Getenv("SHELL")
	os.Unsetenv("SHELL")
	defer os.Setenv("SHELL", originalShell)

	result := WrapCommandForPersistence("ch")
	expected := `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`
	assert.Equal(t, expected, result)
}

func TestWrapCommandForPersistence_InvalidShellPath(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		expected string // Expected to fall back to sh
	}{
		{
			name:     "non-existent shell",
			shell:    "/nonexistent/shell",
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`,
		},
		{
			name:     "relative path",
			shell:    "bin/zsh",
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`,
		},
		{
			name:     "directory instead of executable",
			shell:    "/tmp",
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`,
		},
		{
			name:     "malformed path",
			shell:    ";;;invalid;;;",
			expected: `sh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ch"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalShell := os.Getenv("SHELL")
			os.Setenv("SHELL", tt.shell)
			defer os.Setenv("SHELL", originalShell)

			result := WrapCommandForPersistence("ch")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWrapCommandForPersistence_MultiWordCommands(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected string
	}{
		{
			name:     "command with arguments",
			command:  "ls -la /tmp",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ls -la /tmp"`,
		},
		{
			name:     "command with options and pipes",
			command:  "ps aux | grep tmux",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; ps aux | grep tmux"`,
		},
		{
			name:     "command with environment variables",
			command:  "PATH=/custom/path mycommand",
			expected: `zsh -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @window-uuid 2>/dev/null || echo '')\"; PATH=/custom/path mycommand"`,
		},
	}

	// Use a hermetic fake zsh for these tests (host may have no /bin/zsh).
	shellDir := t.TempDir()
	t.Setenv("SHELL", createFakeShell(t, shellDir, "zsh"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapCommandForPersistence(tt.command)
			assert.Equal(t, tt.expected, result)
		})
	}
}

package tmux

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// isValidShellPath validates that a shell path is safe to use.
// It checks that the path is absolute and the file exists and is executable.
func isValidShellPath(path string) bool {
	// Must be absolute path
	if !filepath.IsAbs(path) {
		return false
	}

	// Check if file exists and is executable
	info, err := os.Stat(path)
	if err != nil {
		return false // File doesn't exist or can't be accessed
	}

	// Must be a regular file (not directory or device)
	if !info.Mode().IsRegular() {
		return false
	}

	// Check if executable (Unix permission check)
	// Mode & 0111 checks if any execute bit is set (user/group/other)
	if info.Mode()&0111 == 0 {
		return false
	}

	return true
}

// Regex to detect already-wrapped shell commands
// Matches: bash -ic "...", zsh -ic "...", sh -ic "...", etc.
// Also handles variations: -i -c, extra spaces, different shells
var alreadyWrappedPattern = regexp.MustCompile(`^(bash|zsh|sh|fish|ksh|tcsh|csh)\s+-i\s*c\s+`)

// WrapCommandForPersistence wraps a command in an interactive shell to ensure window persistence.
// This prevents windows from dying when short-lived commands (like `ch`, `exec ch`) complete.
//
// The function:
// 1. Detects the user's shell from $SHELL environment variable
// 2. Wraps the command as: shell -ic "command"
//   - `-i` = interactive mode (keeps shell alive)
//   - `-c` = execute command string
//
// 3. Falls back to /bin/sh if $SHELL is not set
// 4. Returns empty string for empty commands
//
// Examples:
//   - WrapCommandForPersistence("ch") -> "zsh -ic \"ch\""
//   - WrapCommandForPersistence("exec ch") -> "bash -ic \"exec ch\""
//   - WrapCommandForPersistence("") -> ""
func WrapCommandForPersistence(command string) string {
	// Empty command needs no wrapping
	if command == "" {
		return ""
	}

	// Don't double-wrap if already wrapped
	// Use regex to detect shell -ic pattern more robustly
	if alreadyWrappedPattern.MatchString(command) {
		return command
	}

	// Get user's shell with validation
	shell := os.Getenv("SHELL")

	// Validate shell path and fall back to /bin/sh if invalid
	if shell == "" || !isValidShellPath(shell) {
		shell = "/bin/sh" // Fallback to POSIX shell
	}

	// Extract shell name from path (e.g., /bin/zsh -> zsh)
	shellName := filepath.Base(shell)

	// Final safety check: ensure we got a valid shell name
	if shellName == "" || shellName == "." || shellName == "/" {
		shellName = "sh"
	}

	// Escape shell metacharacters for safe execution inside double quotes
	// Characters that need escaping: \ " $ `
	escapedCommand := command
	escapedCommand = strings.ReplaceAll(escapedCommand, `\`, `\\`)  // Backslash first!
	escapedCommand = strings.ReplaceAll(escapedCommand, `"`, `\"`)  // Double quotes
	escapedCommand = strings.ReplaceAll(escapedCommand, `$`, `\$`)  // Dollar (variable expansion)
	escapedCommand = strings.ReplaceAll(escapedCommand, "`", "\\`") // Backticks (command substitution)

	// Wrap command with UUID export and interactive shell
	// 1. Export TMUX_WINDOW_UUID from tmux user-option
	// 2. Run the user's command
	// 3. Keep shell alive with -i flag
	// The tmux show-options command reads the UUID at runtime (2>/dev/null suppresses errors)
	return shellName + ` -ic "export TMUX_WINDOW_UUID=\"\$(tmux show-options -wv @` + WindowUUIDOption + ` 2>/dev/null || echo '')\"; ` + escapedCommand + `"`
}

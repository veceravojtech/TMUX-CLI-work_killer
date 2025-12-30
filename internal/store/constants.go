// Package store provides session state persistence functionality.
package store

import "os"

const (
	// SessionsDir is the directory path for active session files relative to home directory
	SessionsDir = ".tmux-cli/sessions"

	// EndedDir is the subdirectory path for archived session files relative to home directory
	EndedDir = ".tmux-cli/sessions/ended"

	// DirPerms defines directory permissions (rwxr-xr-x)
	DirPerms = 0755

	// FilePerms defines file permissions (rw-r--r--)
	FilePerms = 0644
)

// ensureDirectories creates the required session storage directories if they don't exist.
// This function is called lazily on first use to avoid requiring manual setup.
func ensureDirectories(homeDir string) error {
	sessionsPath := homeDir + "/" + SessionsDir
	endedPath := homeDir + "/" + EndedDir

	// Create sessions directory
	if err := os.MkdirAll(sessionsPath, DirPerms); err != nil {
		return err
	}

	// Create ended subdirectory
	if err := os.MkdirAll(endedPath, DirPerms); err != nil {
		return err
	}

	return nil
}

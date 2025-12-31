// Package store provides session state persistence functionality.
package store

const (
	// SessionFileName is the filename for the session file in project directories
	SessionFileName = ".tmux-session"

	// DirPerms defines directory permissions (rwxr-xr-x)
	DirPerms = 0755

	// FilePerms defines file permissions (rw-r--r--)
	FilePerms = 0644
)

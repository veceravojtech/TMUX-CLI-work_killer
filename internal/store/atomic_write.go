// Package store provides session state persistence functionality.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWrite writes session data to a file using the atomic write pattern.
// This prevents data corruption by writing to a temporary file first,
// then atomically renaming it to the final path.
//
// The atomic write pattern ensures:
// - Process crashes after temp write but before rename → old file intact
// - Process crashes during rename → POSIX guarantees file is either old or new, never partial
// - File is NEVER in corrupted state
func atomicWrite(path string, session *Session) error {
	dir := filepath.Dir(path)

	// CRITICAL: Temp file MUST be in same directory for atomic rename
	tmpFile, err := os.CreateTemp(dir, "session-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// CRITICAL: Always cleanup temp file on all error paths
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	// Write JSON with 2-space indentation for human readability
	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(session); err != nil {
		return fmt.Errorf("encode session: %w", err)
	}

	// Close before rename
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Set file permissions before rename
	if err := os.Chmod(tmpPath, FilePerms); err != nil {
		return fmt.Errorf("set file permissions: %w", err)
	}

	// CRITICAL: Atomic rename (POSIX guarantees this is atomic)
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}

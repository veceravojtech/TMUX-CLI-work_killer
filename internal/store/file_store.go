// Package store provides session state persistence functionality.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileSessionStore implements SessionStore using JSON files on disk.
// It provides atomic file operations to prevent data corruption.
type FileSessionStore struct {
	homeDir string
}

// NewFileSessionStore creates a new file-based session store.
// It uses the user's home directory for session storage.
func NewFileSessionStore() (*FileSessionStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	return &FileSessionStore{
		homeDir: homeDir,
	}, nil
}

// Save persists a session to disk using atomic file operations.
// It creates the required directories lazily on first use.
func (s *FileSessionStore) Save(session *Session) error {
	if session == nil {
		return fmt.Errorf("save session: %w", ErrInvalidSession)
	}

	if session.SessionID == "" {
		return fmt.Errorf("save session: session ID is required: %w", ErrInvalidSession)
	}

	// Ensure directories exist
	if err := ensureDirectories(s.homeDir); err != nil {
		return fmt.Errorf("ensure directories: %w", err)
	}

	// Build file path
	sessionsPath := filepath.Join(s.homeDir, SessionsDir)
	filePath := filepath.Join(sessionsPath, session.SessionID+".json")

	// Use atomic write to prevent corruption
	if err := atomicWrite(filePath, session); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	return nil
}

// Load retrieves a session from disk by its ID.
// Returns ErrSessionNotFound if the session file doesn't exist.
func (s *FileSessionStore) Load(id string) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("load session: session ID is required: %w", ErrInvalidSession)
	}

	// Build file path
	sessionsPath := filepath.Join(s.homeDir, SessionsDir)
	filePath := filepath.Join(sessionsPath, id+".json")

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load session %s: %w", id, ErrSessionNotFound)
		}
		return nil, fmt.Errorf("read session file: %w", err)
	}

	// Parse JSON
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse session JSON: %w", ErrInvalidSession)
	}

	return &session, nil
}

// Delete removes a session file from disk.
// Returns ErrSessionNotFound if the session doesn't exist.
func (s *FileSessionStore) Delete(id string) error {
	if id == "" {
		return fmt.Errorf("delete session: session ID is required: %w", ErrInvalidSession)
	}

	// Build file path
	sessionsPath := filepath.Join(s.homeDir, SessionsDir)
	filePath := filepath.Join(sessionsPath, id+".json")

	// Delete file
	if err := os.Remove(filePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete session %s: %w", id, ErrSessionNotFound)
		}
		return fmt.Errorf("remove session file: %w", err)
	}

	return nil
}

// List returns all active sessions from the sessions directory.
// Returns an empty slice if no sessions exist.
func (s *FileSessionStore) List() ([]*Session, error) {
	sessionsPath := filepath.Join(s.homeDir, SessionsDir)

	// Ensure directories exist
	if err := ensureDirectories(s.homeDir); err != nil {
		return nil, fmt.Errorf("ensure directories: %w", err)
	}

	// Read directory
	entries, err := os.ReadDir(sessionsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*Session{}, nil
		}
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	// Load all session files
	var sessions []*Session
	for _, entry := range entries {
		// Skip non-JSON files and temp files
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Extract session ID from filename
		id := strings.TrimSuffix(entry.Name(), ".json")

		// Load session
		session, err := s.Load(id)
		if err != nil {
			// Skip sessions that fail to load but continue processing others
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// Move relocates a session file to a destination directory.
// This is primarily used for archiving sessions to the "ended" directory.
// The move operation is atomic and preserves all data integrity.
func (s *FileSessionStore) Move(id string, destination string) error {
	if id == "" {
		return fmt.Errorf("move session: session ID is required: %w", ErrInvalidSession)
	}

	if destination == "" {
		return fmt.Errorf("move session: destination is required: %w", ErrInvalidSession)
	}

	// Ensure base directories exist
	if err := ensureDirectories(s.homeDir); err != nil {
		return fmt.Errorf("ensure directories: %w", err)
	}

	// Build source and destination paths
	sessionsPath := filepath.Join(s.homeDir, SessionsDir)
	srcPath := filepath.Join(sessionsPath, id+".json")
	destDir := filepath.Join(sessionsPath, destination)
	dstPath := filepath.Join(destDir, id+".json")

	// Verify source file exists
	if _, err := os.Stat(srcPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move session %s: %w", id, ErrSessionNotFound)
		}
		return fmt.Errorf("stat source file: %w", err)
	}

	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	// Read source file data
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source file: %w", err)
	}

	// Unmarshal to validate JSON before move
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return fmt.Errorf("validate source file JSON: %w", err)
	}

	// Use atomic write to destination (same pattern as Save)
	if err := atomicWrite(dstPath, &session); err != nil {
		return fmt.Errorf("write destination file: %w", err)
	}

	// Delete source file only after successful write
	if err := os.Remove(srcPath); err != nil {
		return fmt.Errorf("remove source file: %w", err)
	}

	return nil
}

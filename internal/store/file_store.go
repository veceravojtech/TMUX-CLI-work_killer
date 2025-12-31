// Package store provides session state persistence functionality.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileSessionStore implements SessionStore using JSON files on disk.
// It provides atomic file operations to prevent data corruption.
type FileSessionStore struct{}

// NewFileSessionStore creates a new file-based session store.
func NewFileSessionStore() (*FileSessionStore, error) {
	return &FileSessionStore{}, nil
}

// Save persists a session to disk using atomic file operations.
// Writes full session JSON to {session.ProjectPath}/.tmux-session
func (s *FileSessionStore) Save(session *Session) error {
	if session == nil {
		return fmt.Errorf("save session: %w", ErrInvalidSession)
	}

	if session.SessionID == "" {
		return fmt.Errorf("save session: session ID is required: %w", ErrInvalidSession)
	}

	if session.ProjectPath == "" {
		return fmt.Errorf("save session: project path is required: %w", ErrInvalidSession)
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(session.ProjectPath)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}

	// Build session file path
	sessionFilePath := filepath.Join(absPath, SessionFileName)

	// Use atomic write to prevent corruption
	if err := atomicWrite(sessionFilePath, session); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	return nil
}

// Load retrieves a session from disk by its project path.
// Returns ErrSessionNotFound if the session file doesn't exist.
func (s *FileSessionStore) Load(projectPath string) (*Session, error) {
	if projectPath == "" {
		return nil, fmt.Errorf("load session: project path is required: %w", ErrInvalidSession)
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path: %w", err)
	}

	// Build session file path
	sessionFilePath := filepath.Join(absPath, SessionFileName)

	// Read file
	data, err := os.ReadFile(sessionFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load session from %s: %w", projectPath, ErrSessionNotFound)
		}
		return nil, fmt.Errorf("read session file: %w", err)
	}

	// Parse JSON
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse session JSON: %w", ErrInvalidSession)
	}

	// Validate required fields
	if session.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required: %w", ErrInvalidSession)
	}
	if session.ProjectPath == "" {
		return nil, fmt.Errorf("projectPath is required: %w", ErrInvalidSession)
	}
	if session.Windows == nil {
		return nil, fmt.Errorf("windows array is required: %w", ErrInvalidSession)
	}

	// Backward compatibility: populate CreatedAt if missing
	if session.CreatedAt == "" {
		session.CreatedAt = time.Now().Format(time.RFC3339)
	}

	// LastRecoveryAt remains empty if not present (never recovered)

	return &session, nil
}

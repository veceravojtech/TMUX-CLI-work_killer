// Package store provides session state persistence functionality.
package store

import "errors"

var (
	// ErrSessionNotFound is returned when a session file does not exist
	ErrSessionNotFound = errors.New("session not found")

	// ErrInvalidSession is returned when JSON parsing fails
	ErrInvalidSession = errors.New("invalid session data")

	// ErrStorageError is returned for filesystem failures
	ErrStorageError = errors.New("storage operation failed")
)

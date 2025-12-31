// Package store provides session state persistence functionality.
package store

import "errors"

var (
	// ErrSessionNotFound is returned when a session file does not exist
	ErrSessionNotFound = errors.New("session not found")

	// ErrInvalidSession is returned when JSON parsing fails or validation fails
	ErrInvalidSession = errors.New("invalid session data")
)

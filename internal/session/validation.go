package session

import (
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrInvalidUUID is returned when a UUID string cannot be parsed
	ErrInvalidUUID = errors.New("invalid UUID format")
)

// ValidateUUID checks if the provided string is a valid UUID
func ValidateUUID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrInvalidUUID
	}
	return nil
}

// GenerateUUID generates a new UUID v4 string
func GenerateUUID() string {
	return uuid.New().String()
}

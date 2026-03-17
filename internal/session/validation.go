package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

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

// GenerateSessionID generates a tmux session ID from a project path.
// Format: tmux-cli-{sanitized-path}-{timestamp}
// The path is sanitized: /, ., : replaced with -, leading - stripped, lowercased, truncated to 50 chars.
func GenerateSessionID(projectPath string) string {
	// Sanitize path: replace problematic chars with -, lowercase
	sanitized := strings.ToLower(projectPath)
	sanitized = strings.NewReplacer("/", "-", ".", "-", ":", "-").Replace(sanitized)
	sanitized = strings.TrimLeft(sanitized, "-")

	// Truncate to keep session name reasonable
	if len(sanitized) > 50 {
		sanitized = sanitized[len(sanitized)-50:]
	}

	// Compact timestamp
	timestamp := time.Now().Format("20060102T150405")

	return fmt.Sprintf("tmux-cli-%s-%s", sanitized, timestamp)
}

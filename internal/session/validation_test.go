package session

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateUUID_ValidUUIDs tests valid UUID formats
func TestValidateUUID_ValidUUIDs(t *testing.T) {
	tests := []struct {
		name string
		uuid string
	}{
		{
			name: "standard UUID v4",
			uuid: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name: "generated UUID",
			uuid: uuid.New().String(),
		},
		{
			name: "another valid UUID",
			uuid: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUUID(tt.uuid)
			assert.NoError(t, err, "valid UUID should not return error")
		})
	}
}

// TestValidateUUID_InvalidUUIDs tests invalid UUID formats
func TestValidateUUID_InvalidUUIDs(t *testing.T) {
	tests := []struct {
		name    string
		uuid    string
		wantErr error
	}{
		{
			name:    "empty string",
			uuid:    "",
			wantErr: ErrInvalidUUID,
		},
		{
			name:    "invalid format",
			uuid:    "not-a-uuid",
			wantErr: ErrInvalidUUID,
		},
		{
			name:    "too short",
			uuid:    "550e8400-e29b",
			wantErr: ErrInvalidUUID,
		},
		{
			name:    "invalid characters",
			uuid:    "550e8400-e29b-41d4-a716-44665544000g",
			wantErr: ErrInvalidUUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUUID(tt.uuid)
			assert.Error(t, err, "invalid UUID should return error")
			assert.ErrorIs(t, err, tt.wantErr, "should return ErrInvalidUUID")
		})
	}
}

// TestGenerateUUID_ReturnsValidUUID tests UUID generation
func TestGenerateUUID_ReturnsValidUUID(t *testing.T) {
	generatedUUID := GenerateUUID()

	// Verify it's a valid UUID string
	_, err := uuid.Parse(generatedUUID)
	require.NoError(t, err, "generated UUID should be valid")

	// Verify it's not empty
	assert.NotEmpty(t, generatedUUID, "generated UUID should not be empty")

	// Verify it has the standard format (36 characters with dashes)
	assert.Len(t, generatedUUID, 36, "UUID should be 36 characters")
}

// TestGenerateUUID_UniqueValues tests that generated UUIDs are unique
func TestGenerateUUID_UniqueValues(t *testing.T) {
	seen := make(map[string]bool)
	iterations := 1000

	for i := 0; i < iterations; i++ {
		generatedUUID := GenerateUUID()
		assert.False(t, seen[generatedUUID], "UUID should be unique")
		seen[generatedUUID] = true
	}

	assert.Len(t, seen, iterations, "all UUIDs should be unique")
}

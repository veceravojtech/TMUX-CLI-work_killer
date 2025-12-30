package tmux

import (
	"testing"
)

// TestSession_Structure demonstrates TDD approach
// Following Red-Green-Refactor cycle
func TestSession_ValidStructure_HasRequiredFields(t *testing.T) {
	// Arrange
	expectedName := "test-session"
	expectedID := "session-1"

	// Act
	session := Session{
		Name: expectedName,
		ID:   expectedID,
	}

	// Assert
	if session.Name != expectedName {
		t.Errorf("Session.Name = %v, want %v", session.Name, expectedName)
	}
	if session.ID != expectedID {
		t.Errorf("Session.ID = %v, want %v", session.ID, expectedID)
	}
}

// Example of table-driven test pattern
func TestSession_Creation_VariousInputs(t *testing.T) {
	tests := []struct {
		name        string
		sessionName string
		sessionID   string
		wantName    string
		wantID      string
	}{
		{
			name:        "simple session",
			sessionName: "dev",
			sessionID:   "1",
			wantName:    "dev",
			wantID:      "1",
		},
		{
			name:        "session with hyphen",
			sessionName: "my-project",
			sessionID:   "2",
			wantName:    "my-project",
			wantID:      "2",
		},
		{
			name:        "empty session",
			sessionName: "",
			sessionID:   "",
			wantName:    "",
			wantID:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := Session{
				Name: tt.sessionName,
				ID:   tt.sessionID,
			}

			if session.Name != tt.wantName {
				t.Errorf("Session.Name = %v, want %v", session.Name, tt.wantName)
			}
			if session.ID != tt.wantID {
				t.Errorf("Session.ID = %v, want %v", session.ID, tt.wantID)
			}
		})
	}
}

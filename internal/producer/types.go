package producer

import "github.com/console/tmux-cli/internal/identity"

// TaskRequest is the JSON payload POSTed to the backend's /api/v1/tasks
// endpoint. Field names carry explicit json tags so the wire shape is stable
// regardless of Go field naming; SystemInfo reuses identity.SystemInfo verbatim
// (the leaf identity package owns that type and is never edited here).
type TaskRequest struct {
	Category           string              `json:"category"`
	Severity           string              `json:"severity"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	ProposedFix        string              `json:"proposed_fix"`
	ExpectedGreenState string              `json:"expected_green_state"`
	SystemInfo         identity.SystemInfo `json:"system_info"`
	Payload            map[string]any      `json:"payload,omitempty"`
}

// TaskResponse is the minimal decoded form of a successful backend reply. The
// backend shape (goal-003) is not yet frozen, so only the fields the producer
// needs are modelled; unknown fields are tolerated by encoding/json's default
// decoder, keeping this forward-compatible.
type TaskResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

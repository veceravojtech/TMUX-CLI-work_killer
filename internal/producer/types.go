package producer

import (
	"encoding/json"
	"fmt"

	"github.com/console/tmux-cli/internal/identity"
)

// TaskRequest is the JSON payload POSTed to the backend's /api/v1/tasks
// endpoint. The json tags are camelCase to match the backend's CreateTaskRequest
// DTO (Symfony #[MapRequestPayload] with no name converter maps keys verbatim to
// the PHP property names: proposedFix, expectedGreenState, systemInfo). SystemInfo
// reuses identity.SystemInfo verbatim, which carries its own camelCase tags.
// fingerprint and lastSeenAt are intentionally NOT sent: the backend derives the
// fingerprint from the signed request's auth token and stamps lastSeenAt from its
// own clock when resolving the TmuxInstance.
type TaskRequest struct {
	Category           string              `json:"category"`
	Severity           string              `json:"severity"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	ProposedFix        string              `json:"proposedFix"`
	ExpectedGreenState string              `json:"expectedGreenState"`
	SystemInfo         identity.SystemInfo `json:"systemInfo"`
	Payload            map[string]any      `json:"payload,omitempty"`
}

// TaskResponse is the minimal decoded form of a successful backend reply. The
// backend shape (goal-003) is not yet frozen, so only the fields the producer
// needs are modelled; unknown fields are tolerated by encoding/json's default
// decoder, keeping this forward-compatible.
type TaskResponse struct {
	ID     FlexibleID `json:"id"`
	Status string     `json:"status"`
}

// FlexibleID is a backend task id that may arrive as either a JSON number (the
// current backend shape — /api/v1/tasks returns a numeric id) or a JSON string,
// normalized to its textual form. The reply shape is not frozen, so accepting
// both keeps decoding forward-compatible: a synchronous caller (the task-report
// MCP tool) would otherwise fail to decode a numeric id. Its string form is the
// canonical value callers read.
type FlexibleID string

// String returns the id's textual form.
func (f FlexibleID) String() string { return string(f) }

// UnmarshalJSON accepts a JSON string, number, or null (→ ""), normalizing each
// to FlexibleID's string form.
func (f *FlexibleID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexibleID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexibleID(n.String())
		return nil
	}
	return fmt.Errorf("producer: task id is neither a JSON string nor number: %s", string(b))
}

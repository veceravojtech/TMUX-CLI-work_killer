package producer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlexibleID_DecodesNumberAndString locks in the fix for the backend
// returning a NUMERIC task id: TaskResponse.ID must decode from a JSON number
// (the live /api/v1/tasks shape) as well as a JSON string and null, normalizing
// each to its textual form. A synchronous caller (the task-report MCP tool)
// previously failed with "cannot unmarshal number into ... id of type string".
func TestFlexibleID_DecodesNumberAndString(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"numeric id", `{"id":12345,"status":"queued"}`, "12345"},
		{"string id", `{"id":"task-123","status":"queued"}`, "task-123"},
		{"null id", `{"id":null,"status":"queued"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp TaskResponse
			require.NoError(t, json.Unmarshal([]byte(tc.body), &resp))
			assert.Equal(t, tc.want, resp.ID.String())
			assert.Equal(t, "queued", resp.Status)
		})
	}
}

func TestFlexibleID_RejectsNonScalar(t *testing.T) {
	var resp TaskResponse
	err := json.Unmarshal([]byte(`{"id":{"nested":true}}`), &resp)
	require.Error(t, err)
}

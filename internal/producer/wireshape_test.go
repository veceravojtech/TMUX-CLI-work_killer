package producer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/identity"
)

// TestTaskRequest_WireShape locks the JSON keys the producer emits to the exact
// camelCase shape the backend's CreateTaskRequest / SystemInfo DTOs expect
// (Symfony #[MapRequestPayload], no name converter → keys map verbatim to PHP
// property names). A drift here is invisible in Go but silently lands NULL /
// 422-rejected rows on the backend, which is the regression this guards.
func TestTaskRequest_WireShape(t *testing.T) {
	req := TaskRequest{
		Category:           "execute",
		Severity:           "info",
		Title:              "t",
		Description:        "d",
		ProposedFix:        "f",
		ExpectedGreenState: "g",
		SystemInfo: identity.SystemInfo{
			Fingerprint: "fp",
			Hostname:    "host",
			OS:          "linux/amd64",
			TmuxVersion: "3.4",
			CLIVersion:  "0.1.0 (abc1234)",
			GoVersion:   "go1.22",
			Shell:       "/bin/zsh",
			Username:    "console",
		},
		Payload: map[string]any{"goal": "goal-003"},
	}

	b, err := json.Marshal(req)
	require.NoError(t, err)

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &got))

	// Top-level keys must match CreateTaskRequest property names.
	for _, k := range []string{
		"category", "severity", "title", "description",
		"proposedFix", "expectedGreenState", "systemInfo", "payload",
	} {
		_, ok := got[k]
		assert.True(t, ok, "top-level key %q must be present on the wire", k)
	}

	var si map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got["systemInfo"], &si))

	// SystemInfo keys: the five the backend NotBlank-validates must be present,
	// with osInfo (not OS) carrying the OS string.
	for _, k := range []string{"hostname", "osInfo", "tmuxVersion", "cliVersion", "goVersion"} {
		_, ok := si[k]
		assert.True(t, ok, "systemInfo key %q must be present on the wire", k)
	}
	assert.JSONEq(t, `"linux/amd64"`, string(si["osInfo"]))
	assert.JSONEq(t, `"0.1.0 (abc1234)"`, string(si["cliVersion"]))

	// Guard against the old PascalCase shape that landed NULL backend rows.
	for _, bad := range []string{"OS", "Hostname", "CLIVersion", "proposed_fix", "system_info"} {
		_, badTop := got[bad]
		_, badSI := si[bad]
		assert.False(t, badTop || badSI, "stale key %q must not appear on the wire", bad)
	}

	// A report carrying no dependsOn must be byte-identical (omitempty): the key
	// is absent from the wire entirely.
	_, hasDeps := got["dependsOn"]
	assert.False(t, hasDeps, "dependsOn must be absent when DependsOn is nil (omitempty)")

	// A report with DependsOn set serializes as a `dependsOn` string array.
	withDeps := req
	withDeps.DependsOn = []string{"12"}
	b2, err := json.Marshal(withDeps)
	require.NoError(t, err)
	var got2 map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b2, &got2))
	raw, ok := got2["dependsOn"]
	require.True(t, ok, "dependsOn must be present when DependsOn is set")
	assert.JSONEq(t, `["12"]`, string(raw))
}

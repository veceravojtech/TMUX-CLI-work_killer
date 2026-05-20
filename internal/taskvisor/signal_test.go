package taskvisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSignalDir(t *testing.T, root, goalID string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-cli", "goals", goalID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
}

func TestLoadSignal_Missing(t *testing.T) {
	root := t.TempDir()
	sig, err := LoadSignal(root, "goal-001")
	assert.Nil(t, sig)
	assert.NoError(t, err)
}

func TestLoadSignal_Supervisor(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"supervisor","status":"done","timestamp":"2026-05-20T14:30:00Z"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	ss, ok := sig.(*SupervisorSignal)
	require.True(t, ok, "expected *SupervisorSignal, got %T", sig)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, "done", ss.Status)
	assert.Equal(t, "2026-05-20T14:30:00Z", ss.Timestamp)
}

func TestLoadSignal_Validator(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data, err := json.Marshal(map[string]any{
		"source":      "validator",
		"verdict":     "fail",
		"findings":    []map[string]string{{"rule": "price check", "status": "fail", "detail": "mismatch"}},
		"next_action": "fix prices",
		"timestamp":   "2026-05-20T14:35:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), data, 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "expected *ValidatorSignal, got %T", sig)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, "fail", vs.Verdict)
	assert.Equal(t, "fix prices", vs.NextAction)
	assert.Equal(t, "2026-05-20T14:35:00Z", vs.Timestamp)
	require.Len(t, vs.Findings, 1)
	assert.Equal(t, "price check", vs.Findings[0].Rule)
	assert.Equal(t, "fail", vs.Findings[0].Status)
	assert.Equal(t, "mismatch", vs.Findings[0].Detail)
}

func TestLoadSignal_UnknownSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"foo","status":"done"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	assert.Nil(t, sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "foo")
}

func TestSaveSupervisorSignal(t *testing.T) {
	root := t.TempDir()
	sig := &SupervisorSignal{
		Status:    "done",
		Timestamp: "2026-05-20T14:30:00Z",
	}
	err := SaveSupervisorSignal(root, "goal-001", sig)
	require.NoError(t, err)

	assert.Equal(t, "supervisor", sig.Source)

	data, err := os.ReadFile(SignalPath(root, "goal-001"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "supervisor", raw["source"])
	assert.Equal(t, "done", raw["status"])

	tmpPath := SignalPath(root, "goal-001") + ".tmp"
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain")
}

func TestSaveValidatorSignal(t *testing.T) {
	root := t.TempDir()
	sig := &ValidatorSignal{
		Verdict: "pass",
		Findings: []ValidationFinding{
			{Rule: "price check", Status: "pass", Detail: "matched"},
		},
		NextAction: "",
		Timestamp:  "2026-05-20T14:35:00Z",
	}
	err := SaveValidatorSignal(root, "goal-001", sig)
	require.NoError(t, err)

	assert.Equal(t, "validator", sig.Source)

	data, err := os.ReadFile(SignalPath(root, "goal-001"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "validator", raw["source"])
	assert.Equal(t, "pass", raw["verdict"])

	findings, ok := raw["findings"].([]any)
	require.True(t, ok)
	require.Len(t, findings, 1)
}

func TestSignal_Roundtrip(t *testing.T) {
	root := t.TempDir()
	original := &SupervisorSignal{
		Status:    "stopped",
		Timestamp: "2026-05-20T15:00:00Z",
	}
	require.NoError(t, SaveSupervisorSignal(root, "goal-002", original))

	loaded, err := LoadSignal(root, "goal-002")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	ss, ok := loaded.(*SupervisorSignal)
	require.True(t, ok)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, original.Status, ss.Status)
	assert.Equal(t, original.Timestamp, ss.Timestamp)
}

func TestReadSignal_SupervisorSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data := `{"source":"supervisor","status":"done","timestamp":"2026-05-20T14:30:00Z"}`
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), []byte(data), 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	ss, ok := sig.(*SupervisorSignal)
	require.True(t, ok)
	assert.Equal(t, "supervisor", ss.Source)
	assert.Equal(t, "done", ss.Status)
}

func TestReadSignal_ValidatorSource(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	data, err := json.Marshal(map[string]any{
		"source":      "validator",
		"verdict":     "fail",
		"next_action": "fix X",
		"findings":    []map[string]string{},
		"timestamp":   "2026-05-20T14:35:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(SignalPath(root, "goal-001"), data, 0o644))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, "fail", vs.Verdict)
	assert.Equal(t, "fix X", vs.NextAction)
}

func TestReadSignal_FileNotFound(t *testing.T) {
	root := t.TempDir()
	sig, err := LoadSignal(root, "goal-999")
	assert.Nil(t, sig)
	assert.NoError(t, err)
}

func TestDeleteSignal(t *testing.T) {
	root := t.TempDir()
	setupSignalDir(t, root, "goal-001")
	require.NoError(t, SaveSupervisorSignal(root, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))

	sig, err := LoadSignal(root, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	err = DeleteSignal(root, "goal-001")
	require.NoError(t, err)

	sig, err = LoadSignal(root, "goal-001")
	assert.NoError(t, err)
	assert.Nil(t, sig)
}

func TestSignal_RoundtripValidator(t *testing.T) {
	root := t.TempDir()
	original := &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{Rule: "api test", Status: "fail", Detail: "404 error", Correction: "fix endpoint"},
			{Rule: "ui check", Status: "pass", Detail: "looks good"},
		},
		NextAction: "fix the API endpoint",
		Timestamp:  "2026-05-20T15:05:00Z",
	}
	require.NoError(t, SaveValidatorSignal(root, "goal-003", original))

	loaded, err := LoadSignal(root, "goal-003")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	vs, ok := loaded.(*ValidatorSignal)
	require.True(t, ok)
	assert.Equal(t, "validator", vs.Source)
	assert.Equal(t, original.Verdict, vs.Verdict)
	assert.Equal(t, original.NextAction, vs.NextAction)
	assert.Equal(t, original.Timestamp, vs.Timestamp)
	require.Len(t, vs.Findings, 2)
	assert.Equal(t, original.Findings[0], vs.Findings[0])
	assert.Equal(t, original.Findings[1], vs.Findings[1])
}

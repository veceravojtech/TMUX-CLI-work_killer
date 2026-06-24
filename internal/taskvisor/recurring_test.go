package taskvisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecurringFileRoundTrip is the TDD field-drop guard: a fully-populated
// *RecurringFile — both CurrentCycle and a History entry set ALL SIX
// RecurringCycle fields (incl. Outcome) to distinct non-zero values — must
// survive Save→Load byte-for-byte (reflect.DeepEqual).
func TestRecurringFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rf := &RecurringFile{
		Task: &RecurringTask{
			ID:              "rec-1",
			Prompt:          "do the thing",
			TargetWindow:    "worker-1",
			TotalCycles:     5,
			CompletedCycles: 2,
			Status:          RecurringActive,
			ClearBetween:    true,
			IdleGraceSec:    30,
			BootMinSec:      10,
			CooldownSec:     15,
			MaxCycleWallSec: 600,
			CreatedAt:       "2026-06-24T12:00:00Z",
			CurrentCycle: RecurringCycle{
				Index:            3,
				Phase:            "supervising",
				DispatchedAt:     "2026-06-24T12:01:00Z",
				LastActivityAt:   "2026-06-24T12:02:00Z",
				LastProgressHash: "abc123",
				Outcome:          "pending",
			},
			History: []RecurringCycle{
				{
					Index:            1,
					Phase:            "done",
					DispatchedAt:     "2026-06-24T11:00:00Z",
					LastActivityAt:   "2026-06-24T11:30:00Z",
					LastProgressHash: "def456",
					Outcome:          "settled",
				},
			},
		},
	}

	require.NoError(t, SaveRecurring(dir, rf))

	loaded, err := LoadRecurring(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.True(t, reflect.DeepEqual(rf, loaded), "round-trip drift:\nwant %+v\ngot  %+v", rf, loaded)
}

// TestLoadRecurringAbsentReturnsNilNil — absence is not an error.
func TestLoadRecurringAbsentReturnsNilNil(t *testing.T) {
	dir := t.TempDir()
	loaded, err := LoadRecurring(dir)
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

// TestRecurringFilePathJoins — pure path resolution.
func TestRecurringFilePathJoins(t *testing.T) {
	root := "/some/root"
	assert.Equal(t, filepath.Join(root, ".tmux-cli", "recurring.yaml"), RecurringFilePath(root))
}

// TestSaveRecurringCreatesDir — SaveRecurring MkdirAll's a missing .tmux-cli/.
func TestSaveRecurringCreatesDir(t *testing.T) {
	dir := t.TempDir()
	rf := &RecurringFile{Task: &RecurringTask{ID: "rec-1", Prompt: "p", TotalCycles: 1, Status: RecurringActive}}
	require.NoError(t, SaveRecurring(dir, rf))
	_, err := os.Stat(RecurringFilePath(dir))
	require.NoError(t, err)
}

// TestRecurringStatusConsts — pin the persisted status strings.
func TestRecurringStatusConsts(t *testing.T) {
	assert.Equal(t, "active", RecurringActive)
	assert.Equal(t, "stopped", RecurringStopped)
	assert.Equal(t, "done", RecurringDone)
}

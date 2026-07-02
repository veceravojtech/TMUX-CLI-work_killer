package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ValidateState: strict ledger validation (status enum, cycle/max bounds,
//    scenario non-empty) — called from ParseState so every disk read is gated ──

func TestValidateState_ValidPasses(t *testing.T) {
	for _, status := range []string{StatusInProgress, StatusPassed, StatusExhausted, StatusEscalated} {
		s := State{Scenario: "scn", Cycle: 1, MaxCycles: 10, Status: status}
		assert.NoError(t, ValidateState(s), "status %q must validate", status)
	}
}

func TestValidateState_RejectsBadStatus(t *testing.T) {
	s := State{Scenario: "scn", Cycle: 1, MaxCycles: 10, Status: "wedged"}
	assert.Error(t, ValidateState(s))
}

func TestValidateState_RejectsCycleBelowOne(t *testing.T) {
	s := State{Scenario: "scn", Cycle: 0, MaxCycles: 10, Status: StatusInProgress}
	assert.Error(t, ValidateState(s))
}

func TestValidateState_RejectsMaxCyclesBelowOne(t *testing.T) {
	s := State{Scenario: "scn", Cycle: 1, MaxCycles: 0, Status: StatusInProgress}
	assert.Error(t, ValidateState(s))
}

func TestValidateState_RejectsEmptyScenario(t *testing.T) {
	s := State{Scenario: "  ", Cycle: 1, MaxCycles: 10, Status: StatusInProgress}
	assert.Error(t, ValidateState(s))
}

func TestParseState_RejectsInvalidLedger(t *testing.T) {
	// Structurally valid JSON, semantically corrupt ledger (bad status enum).
	_, err := ParseState([]byte(`{"scenario":"scn","cycle":1,"max_cycles":10,"status":"nope","history":[]}`))
	assert.Error(t, err, "ParseState must reject a ledger that fails ValidateState")
}

func TestParseState_AcceptsValidLedger(t *testing.T) {
	b, err := NewState("scn", 10).Marshal()
	require.NoError(t, err)
	st, err := ParseState(b)
	require.NoError(t, err)
	assert.Equal(t, "scn", st.Scenario)
}

// ── RecordCycleOutcome: the deterministic step-8 transition (e2e-state record) ──

func mustEntryMap(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func TestRecordCycleOutcome_FailedBumpsCycle(t *testing.T) {
	st := NewState("scn", 10)
	st.Cycle = 2
	got, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed, DefectSignature: "dispatch/hang/goal"}, nil)
	require.NoError(t, err)

	assert.Equal(t, 3, got.Cycle, "failed outcome bumps cycle")
	assert.Equal(t, StatusInProgress, got.Status, "failed outcome keeps in-progress")
	require.Len(t, got.History, 1)
	m := mustEntryMap(t, got.History[0])
	assert.Equal(t, "2", string(m["cycle"]), "history entry carries the just-finished cycle, not the bumped one")
	assert.Equal(t, `"failed"`, string(m["outcome"]))
}

func TestRecordCycleOutcome_PassedSetsStatusNoBump(t *testing.T) {
	st := NewState("scn", 10)
	st.Cycle = 4
	got, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomePassed, AppUp: true}, nil)
	require.NoError(t, err)

	assert.Equal(t, StatusPassed, got.Status)
	assert.Equal(t, 4, got.Cycle, "passed outcome must NOT bump cycle")
	require.Len(t, got.History, 1)
	m := mustEntryMap(t, got.History[0])
	assert.Equal(t, "true", string(m["app_up"]))
}

func TestRecordCycleOutcome_ExhaustsAtMaxCycles(t *testing.T) {
	st := NewState("scn", 3)
	st.Cycle = 3 // last budgeted cycle just failed — bumping would exceed MaxCycles
	got, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed}, nil)
	require.NoError(t, err)

	assert.Equal(t, StatusExhausted, got.Status, "bump past MaxCycles flips to exhausted instead")
	assert.Equal(t, 3, got.Cycle, "cycle never bumps past MaxCycles")
	require.Len(t, got.History, 1, "the exhausting failure is still recorded")
}

func TestRecordCycleOutcome_RejectsUnknownOutcome(t *testing.T) {
	st := NewState("scn", 10)
	_, err := RecordCycleOutcome(st, HistoryEntry{Outcome: "maybe"}, nil)
	assert.Error(t, err)
}

func TestRecordCycleOutcome_RejectsTerminalLedger(t *testing.T) {
	st := NewState("scn", 10)
	st.Status = StatusPassed
	_, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed}, nil)
	assert.Error(t, err, "record only applies to an in-progress run")
}

func TestRecordCycleOutcome_RejectsInvalidLedger(t *testing.T) {
	_, err := RecordCycleOutcome(State{}, HistoryEntry{Outcome: OutcomeFailed}, nil)
	assert.Error(t, err, "a zero/corrupt ledger must be refused, never written back")
}

func TestRecordCycleOutcome_EntryUsesSchemaFieldNames(t *testing.T) {
	st := NewState("scn", 10)
	got, err := RecordCycleOutcome(st, HistoryEntry{
		Outcome:         OutcomeFailed,
		DefectSignature: "roadmap/over-serialized/planner",
		TaskReported:    "task-281",
		TaskStatus:      "new",
		GitAfter:        "abc1234",
		AppUp:           false,
		Durations:       json.RawMessage(`{"implement_sec":120}`),
	}, nil)
	require.NoError(t, err)
	require.Len(t, got.History, 1)

	m := mustEntryMap(t, got.History[0])
	for _, key := range []string{
		"cycle", "outcome", "defect_signature", "task_reported",
		"task_status", "git_after", "app_up", "durations",
	} {
		assert.Contains(t, m, key, "history entry must carry the XML schema field %q", key)
	}
	assert.JSONEq(t, `{"implement_sec":120}`, string(m["durations"]))
}

func TestRecordCycleOutcome_DoesNotMutateInput(t *testing.T) {
	st := NewState("scn", 10)
	_, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Cycle, "input state must stay untouched (value semantics)")
	assert.Empty(t, st.History)
}

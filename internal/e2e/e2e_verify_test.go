package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Verify field + LastSelfUpdate stamp: ledger schema round-trip (task 8a/8b) ──

func TestState_VerifyRoundTrip(t *testing.T) {
	st := NewState("scn", 10)
	st.Verify = &VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	st.LastSelfUpdate = &SelfUpdateStamp{TaskID: "task-317", At: "2026-07-02T09:00:00Z"}

	b, err := st.Marshal()
	require.NoError(t, err)
	// Pinned JSON field names — the XML/bootstrap contract reads these.
	assert.Contains(t, string(b), `"verify"`)
	assert.Contains(t, string(b), `"last_self_update"`)
	assert.Contains(t, string(b), `"signature"`)

	got, err := ParseState(b)
	require.NoError(t, err)
	require.NotNil(t, got.Verify)
	assert.Equal(t, "dispatch/hang/goal", got.Verify.Signature)
	assert.Equal(t, "task-317", got.Verify.TaskID)
	require.NotNil(t, got.LastSelfUpdate)
	assert.Equal(t, "task-317", got.LastSelfUpdate.TaskID)
	assert.Equal(t, "2026-07-02T09:00:00Z", got.LastSelfUpdate.At)
}

func TestState_VerifyOmittedWhenNil(t *testing.T) {
	b, err := NewState("scn", 10).Marshal()
	require.NoError(t, err)
	assert.NotContains(t, string(b), "verify", "nil Verify must be omitted (omitempty)")
	assert.NotContains(t, string(b), "last_self_update", "nil LastSelfUpdate must be omitted (omitempty)")
}

// ── RecordCycleOutcome verify semantics: set on verify-record, clear without ──

func TestRecordCycleOutcome_VerifySetsPendingVerification(t *testing.T) {
	st := NewState("scn", 10)
	st.Cycle = 2
	v := &VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	got, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed, DefectSignature: "dispatch/hang/goal", TaskStatus: "resolved"}, v)
	require.NoError(t, err)

	require.NotNil(t, got.Verify)
	assert.Equal(t, "dispatch/hang/goal", got.Verify.Signature)
	assert.Equal(t, "task-317", got.Verify.TaskID)
	assert.Equal(t, 3, got.Cycle, "the verify record still bumps — the verification runs as the next cycle")
	assert.Equal(t, StatusInProgress, got.Status, "the verify record keeps the run in-progress")
}

func TestRecordCycleOutcome_NoVerifyClearsField(t *testing.T) {
	st := NewState("scn", 10)
	st.Verify = &VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	got, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomePassed, AppUp: true}, nil)
	require.NoError(t, err)
	assert.Nil(t, got.Verify, "a record without verify flags clears the pending verification (fix confirmed or superseded)")
}

func TestRecordCycleOutcome_RejectsVerifyWithPassed(t *testing.T) {
	st := NewState("scn", 10)
	v := &VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	_, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomePassed, AppUp: true}, v)
	assert.Error(t, err, "a passed outcome cannot flag a pending fix-verification")
}

func TestRecordCycleOutcome_RejectsIncompleteVerify(t *testing.T) {
	st := NewState("scn", 10)
	for _, v := range []*VerifyState{
		{Signature: "", TaskID: "task-317"},
		{Signature: "dispatch/hang/goal", TaskID: ""},
	} {
		_, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed}, v)
		assert.Error(t, err, "verify requires BOTH signature and task id, got %+v", v)
	}
}

// ── MarkSelfUpdate: the one-restart-per-resolved-task loop guard (task 8b) ──

func TestMarkSelfUpdate_StampsTask(t *testing.T) {
	st := NewState("scn", 10)
	got, err := MarkSelfUpdate(st, "task-317", "2026-07-02T09:00:00Z")
	require.NoError(t, err)
	require.NotNil(t, got.LastSelfUpdate)
	assert.Equal(t, "task-317", got.LastSelfUpdate.TaskID)
	assert.Equal(t, "2026-07-02T09:00:00Z", got.LastSelfUpdate.At)
	assert.Nil(t, st.LastSelfUpdate, "input state must stay untouched (value semantics)")
}

func TestMarkSelfUpdate_RefusesRepeatTaskID(t *testing.T) {
	st := NewState("scn", 10)
	st.LastSelfUpdate = &SelfUpdateStamp{TaskID: "task-317", At: "2026-07-02T09:00:00Z"}
	_, err := MarkSelfUpdate(st, "task-317", "2026-07-02T10:00:00Z")
	require.Error(t, err, "one session restart per resolved task — a repeat task-id is the restart loop")
	assert.Contains(t, err.Error(), "task-317")
}

func TestMarkSelfUpdate_AllowsNewTaskID(t *testing.T) {
	st := NewState("scn", 10)
	st.LastSelfUpdate = &SelfUpdateStamp{TaskID: "task-317", At: "2026-07-02T09:00:00Z"}
	got, err := MarkSelfUpdate(st, "task-401", "2026-07-02T11:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "task-401", got.LastSelfUpdate.TaskID, "a NEW resolved task earns its own restart")
}

func TestMarkSelfUpdate_RejectsEmptyInputs(t *testing.T) {
	st := NewState("scn", 10)
	_, err := MarkSelfUpdate(st, "", "2026-07-02T09:00:00Z")
	assert.Error(t, err, "task id is required")
	_, err = MarkSelfUpdate(st, "task-317", "")
	assert.Error(t, err, "timestamp is required (injected by the CLI layer — internal/e2e stays clock-free)")
}

func TestMarkSelfUpdate_RefusesTerminalLedger(t *testing.T) {
	st := NewState("scn", 10)
	st.Status = StatusPassed
	_, err := MarkSelfUpdate(st, "task-317", "2026-07-02T09:00:00Z")
	assert.Error(t, err, "restarting onto a terminal run is meaningless — refuse so the XML skips the restart")
}

func TestMarkSelfUpdate_RefusesInvalidLedger(t *testing.T) {
	_, err := MarkSelfUpdate(State{}, "task-317", "2026-07-02T09:00:00Z")
	assert.Error(t, err)
}

// ── RenderStateMD: the human/kickoff-readable handoff rendering (task 8a) ──

func TestRenderStateMD_CoreFieldsAndNextAction(t *testing.T) {
	st := NewState("symfony-dashboard-login", 10)
	st.Cycle = 3
	md := RenderStateMD(st)

	assert.Contains(t, md, "symfony-dashboard-login")
	assert.Contains(t, md, "cycle: 3")
	assert.Contains(t, md, "max_cycles: 10")
	assert.Contains(t, md, "in-progress")
	assert.Contains(t, md, "Next action", "the kickoff reader needs an explicit next-action line")
	assert.Contains(t, md, "/tmux:e2e-evaluator resume",
		"an in-progress ledger's next action is to resume the conductor")
}

func TestRenderStateMD_PendingVerification(t *testing.T) {
	st := NewState("scn", 10)
	st.Cycle = 4
	st.Verify = &VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	st.LastSelfUpdate = &SelfUpdateStamp{TaskID: "task-317", At: "2026-07-02T09:00:00Z"}
	md := RenderStateMD(st)

	assert.Contains(t, md, "dispatch/hang/goal")
	assert.Contains(t, md, "task-317")
	assert.Contains(t, md, "verification", "a set Verify must render as a pending fix-verification")
	assert.Contains(t, md, "signature cleared",
		"the verify next action tells JUDGE what to check")
	assert.Contains(t, md, "2026-07-02T09:00:00Z", "the last self-update stamp is surfaced")
}

func TestRenderStateMD_NoVerifyRendersNone(t *testing.T) {
	md := RenderStateMD(NewState("scn", 10))
	assert.Contains(t, md, "pending verification: none")
}

func TestRenderStateMD_LastOutcomeFromHistory(t *testing.T) {
	st := NewState("scn", 10)
	rec, err := RecordCycleOutcome(st, HistoryEntry{Outcome: OutcomeFailed, DefectSignature: "dispatch/hang/goal"}, nil)
	require.NoError(t, err)
	md := RenderStateMD(rec)
	assert.Contains(t, md, "last outcome: cycle 1 failed")
}

func TestRenderStateMD_TerminalDoesNotResume(t *testing.T) {
	st := NewState("scn", 10)
	st.Status = StatusPassed
	md := RenderStateMD(st)
	assert.NotContains(t, md, "/tmux:e2e-evaluator resume",
		"a terminal run must not instruct a resume")
	assert.Contains(t, md, "nothing to resume")
}

// ── StateMDPath: the .state.md sits alongside the .state.json ──

func TestStateMDPath(t *testing.T) {
	p := StateMDPath("/repo", "scn")
	assert.Equal(t, "/repo/.tmux-cli/e2e-evaluator/scn.state.md", p)
	assert.Equal(t, strings.TrimSuffix(StateFilePath("/repo", "scn"), ".json")+".md", p)
}

// ── BootstrapResult surfaces the pending verification on resume (task 8c) ──

func TestBootstrapResultJSON_CarriesVerify(t *testing.T) {
	r := BootstrapResult{Ok: true, Scenario: "scn", VerifySignature: "dispatch/hang/goal", VerifyTaskID: "task-317"}
	s := r.JSON()
	assert.Contains(t, s, `"verify_signature":"dispatch/hang/goal"`)
	assert.Contains(t, s, `"verify_task_id":"task-317"`)
}

func TestBootstrapResultJSON_VerifyOmittedWhenAbsent(t *testing.T) {
	s := BootstrapResult{Ok: true, Scenario: "scn"}.JSON()
	assert.NotContains(t, s, "verify_signature")
	assert.NotContains(t, s, "verify_task_id")
}

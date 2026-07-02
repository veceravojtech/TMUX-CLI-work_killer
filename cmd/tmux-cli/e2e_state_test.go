package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLedger writes a fresh in-progress ledger for scenario under repoRoot and
// returns its path (the e2e-bootstrap init that `e2e-state record` requires).
func seedLedger(t *testing.T, repoRoot, scenario string, cycle, maxCycles int) string {
	t.Helper()
	stateFile := e2e.StateFilePath(repoRoot, scenario)
	require.NoError(t, os.MkdirAll(filepath.Dir(stateFile), 0o755))
	st := e2e.NewState(scenario, maxCycles)
	st.Cycle = cycle
	require.NoError(t, writeStateAtomic(stateFile, st))
	return stateFile
}

func TestE2EStateRecord_FailedBumpsCycle(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 2, 10)

	got, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomeFailed, DefectSignature: "dispatch/hang/goal"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Cycle)
	assert.Equal(t, e2e.StatusInProgress, got.Status)

	// The bump is durable: the on-disk ledger re-parses to the same state.
	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	onDisk, err := e2e.ParseState(raw)
	require.NoError(t, err)
	assert.Equal(t, 3, onDisk.Cycle)
	require.Len(t, onDisk.History, 1)

	// Atomic write leaves no temp file behind.
	_, statErr := os.Stat(stateFile + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "no .tmp leftover after the atomic rename")
}

func TestE2EStateRecord_PassedSetsStatus(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 4, 10)

	got, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomePassed, AppUp: true}, nil)
	require.NoError(t, err)
	assert.Equal(t, e2e.StatusPassed, got.Status)
	assert.Equal(t, 4, got.Cycle, "passed must not bump cycle")

	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	onDisk, err := e2e.ParseState(raw)
	require.NoError(t, err)
	assert.Equal(t, e2e.StatusPassed, onDisk.Status)
}

func TestE2EStateRecord_ExhaustsAtMaxCycles(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 3, 3)

	got, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomeFailed}, nil)
	require.NoError(t, err)
	assert.Equal(t, e2e.StatusExhausted, got.Status, "bump past MaxCycles flips to exhausted")
	assert.Equal(t, 3, got.Cycle)
}

func TestE2EStateRecord_RefusesMissingLedger(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomeFailed}, nil)
	require.Error(t, err, "record never initializes a ledger — that's e2e-bootstrap's job")
	assert.Contains(t, err.Error(), "e2e-bootstrap")
}

func TestE2EStateRecord_RefusesCorruptLedger(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := e2e.StateFilePath(repoRoot, "scn")
	require.NoError(t, os.MkdirAll(filepath.Dir(stateFile), 0o755))
	require.NoError(t, os.WriteFile(stateFile, []byte(`{"scenario":"","cycle":0`), 0o644))

	_, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomeFailed}, nil)
	assert.Error(t, err)

	// The corrupt ledger must be left untouched, never overwritten.
	raw, readErr := os.ReadFile(stateFile)
	require.NoError(t, readErr)
	assert.Equal(t, `{"scenario":"","cycle":0`, string(raw))
}

// ── flag surface: strict parsing mirrors the spec's required/enum contract ──

func TestParseE2EStateFlags_Valid(t *testing.T) {
	entry, err := parseE2EStateFlags("failed", "dispatch/hang/goal", "true", "task-281", "new", "abc1234", `{"implement_sec":120}`)
	require.NoError(t, err)
	assert.Equal(t, e2e.OutcomeFailed, entry.Outcome)
	assert.True(t, entry.AppUp)
	assert.Equal(t, "task-281", entry.TaskReported)
	assert.Equal(t, "new", entry.TaskStatus)
	assert.Equal(t, "abc1234", entry.GitAfter)
	assert.JSONEq(t, `{"implement_sec":120}`, string(entry.Durations))
}

func TestParseE2EStateFlags_RejectsBadOutcome(t *testing.T) {
	_, err := parseE2EStateFlags("wedged", "none", "true", "", "", "", "")
	assert.Error(t, err)
}

func TestParseE2EStateFlags_RejectsBadAppUp(t *testing.T) {
	_, err := parseE2EStateFlags("failed", "none", "yes", "", "", "", "")
	assert.Error(t, err)
}

func TestParseE2EStateFlags_RejectsEmptySignature(t *testing.T) {
	_, err := parseE2EStateFlags("failed", "  ", "false", "", "", "", "")
	assert.Error(t, err, "signature is required (use the literal 'none' when there is no defect)")
}

func TestParseE2EStateFlags_RejectsNonObjectDurations(t *testing.T) {
	_, err := parseE2EStateFlags("failed", "none", "false", "", "", "", `[1,2]`)
	assert.Error(t, err, "--durations-json must be a JSON object")
}

func TestParseE2EStateFlags_EmptyDurationsDefaultsToObject(t *testing.T) {
	entry, err := parseE2EStateFlags("passed", "none", "true", "", "", "", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(entry.Durations))
}

// ── verify flags: pending-verification record + state.md rendering (task 8a) ──

func TestParseE2EVerifyFlags_BothOrNeither(t *testing.T) {
	v, err := parseE2EVerifyFlags("", "")
	require.NoError(t, err)
	assert.Nil(t, v, "no verify flags = no pending verification (and a clear on record)")

	v, err = parseE2EVerifyFlags("dispatch/hang/goal", "task-317")
	require.NoError(t, err)
	require.NotNil(t, v)
	assert.Equal(t, "dispatch/hang/goal", v.Signature)
	assert.Equal(t, "task-317", v.TaskID)

	_, err = parseE2EVerifyFlags("dispatch/hang/goal", "")
	assert.Error(t, err, "--verify-signature without --verify-task-id is refused")
	_, err = parseE2EVerifyFlags("", "task-317")
	assert.Error(t, err, "--verify-task-id without --verify-signature is refused")
}

func TestE2EStateRecord_VerifySetsLedgerDurably(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 2, 10)

	v := &e2e.VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	got, err := e2eStateRecord(repoRoot, "scn",
		e2e.HistoryEntry{Outcome: e2e.OutcomeFailed, DefectSignature: "dispatch/hang/goal", TaskStatus: "resolved"}, v)
	require.NoError(t, err)
	require.NotNil(t, got.Verify)

	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	onDisk, err := e2e.ParseState(raw)
	require.NoError(t, err)
	require.NotNil(t, onDisk.Verify)
	assert.Equal(t, "task-317", onDisk.Verify.TaskID)
	assert.Equal(t, 3, onDisk.Cycle, "the verify record still bumps — the verification is the next cycle")
}

func TestE2EStateRecord_NoVerifyFlagsClearsField(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := e2e.StateFilePath(repoRoot, "scn")
	require.NoError(t, os.MkdirAll(filepath.Dir(stateFile), 0o755))
	st := e2e.NewState("scn", 10)
	st.Verify = &e2e.VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	require.NoError(t, writeStateAtomic(stateFile, st))

	_, err := e2eStateRecord(repoRoot, "scn", e2e.HistoryEntry{Outcome: e2e.OutcomePassed, AppUp: true}, nil)
	require.NoError(t, err)

	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	onDisk, err := e2e.ParseState(raw)
	require.NoError(t, err)
	assert.Nil(t, onDisk.Verify, "a record without verify flags clears the pending verification")
}

func TestE2EStateRecord_WritesStateMDAlongside(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 2, 10)

	v := &e2e.VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	_, err := e2eStateRecord(repoRoot, "scn",
		e2e.HistoryEntry{Outcome: e2e.OutcomeFailed, DefectSignature: "dispatch/hang/goal"}, v)
	require.NoError(t, err)

	mdPath := e2e.StateMDPath(repoRoot, "scn")
	b, err := os.ReadFile(mdPath)
	require.NoError(t, err, "record must write <scenario>.state.md alongside the JSON on every invocation")
	assert.Contains(t, string(b), "dispatch/hang/goal")
	assert.Contains(t, string(b), "/tmux:e2e-evaluator resume")

	_, statErr := os.Stat(mdPath + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "no .tmp leftover after the atomic md rename")
}

// ── mark-self-update: the restart-loop guard (task 8b) ──

func TestE2EStateMarkSelfUpdate_StampsAndWritesMD(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 2, 10)

	got, err := e2eStateMarkSelfUpdate(repoRoot, "scn", "task-317", "2026-07-02T09:00:00Z")
	require.NoError(t, err)
	require.NotNil(t, got.LastSelfUpdate)
	assert.Equal(t, "task-317", got.LastSelfUpdate.TaskID)

	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	onDisk, err := e2e.ParseState(raw)
	require.NoError(t, err)
	require.NotNil(t, onDisk.LastSelfUpdate)
	assert.Equal(t, "2026-07-02T09:00:00Z", onDisk.LastSelfUpdate.At)

	_, err = os.Stat(e2e.StateMDPath(repoRoot, "scn"))
	require.NoError(t, err, "mark-self-update keeps the .state.md rendering in sync")
}

func TestE2EStateMarkSelfUpdate_RefusesSameTask(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 2, 10)

	_, err := e2eStateMarkSelfUpdate(repoRoot, "scn", "task-317", "2026-07-02T09:00:00Z")
	require.NoError(t, err)
	_, err = e2eStateMarkSelfUpdate(repoRoot, "scn", "task-317", "2026-07-02T10:00:00Z")
	require.Error(t, err, "one session restart per resolved task — the repeat is refused ({ok:false})")
}

func TestE2EStateMarkSelfUpdate_RefusesMissingLedger(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := e2eStateMarkSelfUpdate(repoRoot, "scn", "task-317", "2026-07-02T09:00:00Z")
	require.Error(t, err, "mark-self-update never initializes a ledger")
	assert.Contains(t, err.Error(), "e2e-bootstrap")
}

func TestResolveMarkAt_ValidatesRFC3339(t *testing.T) {
	got, err := resolveMarkAt("2026-07-02T09:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, "2026-07-02T09:00:00Z", got)

	_, err = resolveMarkAt("yesterday")
	assert.Error(t, err, "--at must be RFC3339")

	now, err := resolveMarkAt("")
	require.NoError(t, err, "empty --at defaults to the CLI-layer clock")
	_, perr := time.Parse(time.RFC3339, now)
	assert.NoError(t, perr, "the defaulted stamp must itself be RFC3339")
}

// ── bootstrap --resume surfaces the pending verification (task 8c) ──

func TestReadLedgerVerify_SurfacesPendingVerification(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := e2e.StateFilePath(repoRoot, "scn")
	require.NoError(t, os.MkdirAll(filepath.Dir(stateFile), 0o755))
	st := e2e.NewState("scn", 10)
	st.Verify = &e2e.VerifyState{Signature: "dispatch/hang/goal", TaskID: "task-317"}
	require.NoError(t, writeStateAtomic(stateFile, st))

	v := readLedgerVerify(stateFile)
	require.NotNil(t, v)
	assert.Equal(t, "dispatch/hang/goal", v.Signature)
	assert.Equal(t, "task-317", v.TaskID)
}

func TestReadLedgerVerify_NilWithoutVerifyOrFile(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 1, 10)
	assert.Nil(t, readLedgerVerify(stateFile), "no pending verification = nil")
	assert.Nil(t, readLedgerVerify(filepath.Join(repoRoot, "missing.json")), "a missing/corrupt ledger yields nil, never a panic")
}

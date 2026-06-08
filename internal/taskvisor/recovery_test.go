package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Worker-crash reporting tests (execute-009-4) ---
//
// These exercise the genuine-crash branch of crashRecovery: a GoalRunning goal
// whose worker window vanished, re-dispatched (action="re-dispatch") or failed
// (action="fail"). The report MUST fire on that branch ONLY — never on the
// missing-guard idle path, the Pass-1 signal-resume path, or the
// resumed/validator-spawn continue paths.
//
// Observation seam: reportWorkerCrashFn (a package var defaulting to
// (*Daemon).reportWorkerCrash). producer.Client is a concrete type with an
// unexported constructor and no daemon-level injection seam — and no signing key
// is embedded in tests, so producer.New yields nil — making the swappable
// function the only way to count/inspect submissions deterministically.

// capturedCrash records one reportWorkerCrashFn invocation for assertion.
type capturedCrash struct {
	goalID    string
	mg        int
	action    string
	allDone   bool
	surviving []string
}

// swapCrashReporter replaces reportWorkerCrashFn with a recorder for the duration
// of the test and restores the original in cleanup. The returned slice pointer
// accumulates one entry per crash-branch report. Tests using this MUST NOT call
// t.Parallel — the seam is a shared package var.
func swapCrashReporter(t *testing.T) *[]capturedCrash {
	t.Helper()
	orig := reportWorkerCrashFn
	captured := &[]capturedCrash{}
	reportWorkerCrashFn = func(d *Daemon, g *Goal, mg int, action string, allDone bool, surviving []tmux.WindowInfo) {
		names := make([]string, 0, len(surviving))
		for _, w := range surviving {
			names = append(names, w.Name)
		}
		*captured = append(*captured, capturedCrash{
			goalID:    g.ID,
			mg:        mg,
			action:    action,
			allDone:   allDone,
			surviving: names,
		})
	}
	t.Cleanup(func() { reportWorkerCrashFn = orig })
	return captured
}

// TestCrashRecovery_ReportsWorkerCrash_OnReDispatch: a GoalRunning goal with
// retries left whose window is missing is re-pended AND triggers exactly one
// execute/warning report carrying action="re-dispatch" and the surviving (but
// non-matching) window in its window list.
func TestCrashRecovery_ReportsWorkerCrash_OnReDispatch(t *testing.T) {
	captured := swapCrashReporter(t)
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	// A bare "supervisor" window survives but does NOT match supervisorWindow("goal-001") =
	// "supervisor-001", so the goal still falls into the crash branch.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.crashRecovery())

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g.Status, "retries left → re-pended")

	require.Len(t, *captured, 1, "exactly one crash report on the re-dispatch branch")
	c := (*captured)[0]
	assert.Equal(t, "goal-001", c.goalID)
	assert.Equal(t, "re-dispatch", c.action)
	assert.Equal(t, 1, c.mg)
	assert.Contains(t, c.surviving, "supervisor", "surviving window list is reused, not re-fetched")
}

// TestCrashRecovery_ReportsWorkerCrash_OnFail: budget spent → goal failed AND one
// report with action="fail".
func TestCrashRecovery_ReportsWorkerCrash_OnFail(t *testing.T) {
	captured := swapCrashReporter(t)
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 3, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.crashRecovery())

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalFailed, g.Status, "budget spent → failed")

	require.Len(t, *captured, 1)
	assert.Equal(t, "fail", (*captured)[0].action)
	assert.Equal(t, "goal-001", (*captured)[0].goalID)
}

// TestCrashRecovery_NoReport_WhenWindowSurvives: the goal's own supervisor window
// is alive (resume path) → ZERO reports and the goal stays GoalRunning.
func TestCrashRecovery_NoReport_WhenWindowSurvives(t *testing.T) {
	captured := swapCrashReporter(t)
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: supervisorWindow("goal-001", 1)},
	}, nil)

	require.NoError(t, d.crashRecovery())

	assert.Empty(t, *captured, "supervisor-alive resume must NOT report a crash")

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status, "resume must not re-pend")
}

// TestCrashRecovery_NoReport_OnMissingGuard: no taskvisor-active guard → early
// return, ZERO reports.
func TestCrashRecovery_NoReport_OnMissingGuard(t *testing.T) {
	captured := swapCrashReporter(t)
	d, _, _ := setupDaemon(t)

	require.NoError(t, d.crashRecovery())
	assert.Empty(t, *captured, "missing-guard idle path must NOT report")
}

// TestCrashRecovery_NoReport_OnSignalResume: a SupervisorSignal resumes the goal
// in Pass-1 (never reaching the window check) → ZERO reports, goal stays running.
func TestCrashRecovery_NoReport_OnSignalResume(t *testing.T) {
	captured := swapCrashReporter(t)
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, MaxRetries: 3}},
	})
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-06-08T10:00:00Z",
	}))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.crashRecovery())

	assert.Empty(t, *captured, "Pass-1 signal-resume must NOT report")
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)
}

// TestCrashRecovery_MultipleCrashedGoals_OneReportEach: MaxGoals>1 with two
// running goals and all windows gone → exactly two reports, one per goal ID.
func TestCrashRecovery_MultipleCrashedGoals_OneReportEach(t *testing.T) {
	captured := swapCrashReporter(t)
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeSettingsMaxGoals(t, dir, 2)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-045",
		Goals: []Goal{
			{ID: "goal-045", Description: "pricing", Status: GoalRunning, Retries: 1, MaxRetries: 3},
			{ID: "goal-046", Description: "identity", Status: GoalRunning, Retries: 0, MaxRetries: 3},
		},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.crashRecovery())

	require.Len(t, *captured, 2, "exactly one report per crashed goal")
	ids := []string{(*captured)[0].goalID, (*captured)[1].goalID}
	assert.Contains(t, ids, "goal-045")
	assert.Contains(t, ids, "goal-046")
	for _, c := range *captured {
		assert.Equal(t, 2, c.mg, "maxGoals threaded into every report")
	}
}

// TestCrashRecovery_NilProducer_NoPanic: with NO seam swap (the real
// reportWorkerCrash → reportFailure path) and a nil producer (default), the crash
// branch is reached, recovery returns nil without panic, and the goal is re-pended.
func TestCrashRecovery_NilProducer_NoPanic(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	require.Nil(t, d.producer, "default daemon has reporting disabled")
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3}},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NotPanics(t, func() {
		require.NoError(t, d.crashRecovery())
	})

	goals, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := goals.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalPending, g.Status, "recovery proceeds with reporting disabled")
}

// --- Pure payload / log-tail unit tests ---

// TestCrashReportPayload_Fields pins the deterministic, network-free payload
// assembly: every documented key with the expected value.
func TestCrashReportPayload_Fields(t *testing.T) {
	g := &Goal{ID: "goal-001", Status: GoalRunning}
	surviving := []tmux.WindowInfo{
		{Name: "supervisor"},
		{Name: "validator-002"},
	}

	payload := crashReportPayload(g, 2, "re-dispatch", true, surviving, "tail text")

	assert.Equal(t, "goal-001", payload["goal_id"])
	assert.Equal(t, GoalRunning, payload["status_before"])
	assert.Equal(t, "re-dispatch", payload["recovery_action"])
	assert.Equal(t, supervisorWindow("goal-001", 2), payload["expected_window"])
	assert.Equal(t, []string{"supervisor", "validator-002"}, payload["surviving_windows"])
	assert.Equal(t, true, payload["tasks_all_done"])
	assert.Equal(t, "tail text", payload["log_tail"])
	assert.Contains(t, payload["log_tail_source"], "shared daemon log",
		"tail is labelled as the shared daemon log so consumers do not over-attribute it")
}

// TestCrashReportPayload_NoSurvivingWindows: an empty surviving slice yields an
// empty (non-nil) name list.
func TestCrashReportPayload_NoSurvivingWindows(t *testing.T) {
	g := &Goal{ID: "goal-001", Status: GoalRunning}
	payload := crashReportPayload(g, 1, "fail", false, nil, "")
	names, ok := payload["surviving_windows"].([]string)
	require.True(t, ok)
	assert.Empty(t, names)
	assert.Equal(t, "fail", payload["recovery_action"])
}

// TestReadLogTail_MissingFile: an unreadable/missing path yields "" (the report
// is still sent without a tail).
func TestReadLogTail_MissingFile(t *testing.T) {
	assert.Equal(t, "", readLogTail(filepath.Join(t.TempDir(), "does-not-exist.log")))
}

// TestReadLogTail_BoundsLines: a file with more than 50 lines is trimmed to the
// last 50, preserving the final line.
func TestReadLogTail_BoundsLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "taskvisor.log")
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o644))

	tail := readLogTail(p)
	lines := strings.Split(tail, "\n")
	assert.LessOrEqual(t, len(lines), 50, "tail bounded to at most 50 lines")
	assert.Equal(t, "line9", lines[len(lines)-1], "last log line preserved")
}

// TestReadLogTail_BoundsBytes: a file larger than 4096 bytes keeps only the tail
// slice, then at most 50 lines of it — never the whole file.
func TestReadLogTail_BoundsBytes(t *testing.T) {
	big := strings.Repeat("x\n", 5000) // 10000 bytes, 5000 lines
	p := filepath.Join(t.TempDir(), "taskvisor.log")
	require.NoError(t, os.WriteFile(p, []byte(big), 0o644))

	tail := readLogTail(p)
	assert.LessOrEqual(t, len(tail), 4096, "byte-bounded to the last 4096 bytes")
	lines := strings.Split(tail, "\n")
	assert.LessOrEqual(t, len(lines), 50, "then line-bounded to 50")
	assert.Equal(t, "x", lines[len(lines)-1])
}

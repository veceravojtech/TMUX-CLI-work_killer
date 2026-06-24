package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recurring_driver_test.go — RED-phase spec for the daemon recurring cycle
// machine (dispatch → settle → advance → finish) and its two daemon-integration
// seams (tick step-5 teardown guard, poll modeIdle pickup). Every TestRecurring*
// here pins a behavior the NO-OP stubs in recurring_driver.go do NOT yet perform,
// so each fails on an ASSERTION (a real `--- FAIL:` banner), never a compile
// error. The green goal turns these green. All time is injected via
// d.clock = clk.now; nothing calls time.Now().

// writeRecurring stages .tmux-cli/recurring.yaml with a single active task via the
// shared goal-001 SaveRecurring (no redefinition of the persisted types — ADR-1).
func writeRecurring(t *testing.T, dir string, task *RecurringTask) {
	t.Helper()
	require.NoError(t, SaveRecurring(dir, &RecurringFile{Task: task}))
}

// loadRecurringTask reads recurring.yaml back through the daemon's own
// LoadRecurring so the tests observe exactly the persisted shape green will write.
func loadRecurringTask(t *testing.T, dir string) *RecurringTask {
	t.Helper()
	rf, err := LoadRecurring(dir)
	require.NoError(t, err)
	require.NotNil(t, rf, "recurring.yaml must be present")
	require.NotNil(t, rf.Task, "recurring.yaml must carry a task")
	return rf.Task
}

func recurringMarkerPath(dir string) string {
	return filepath.Join(dir, ".tmux-cli", "recurring-active")
}

func writeRecurringMarker(t *testing.T, dir string) {
	t.Helper()
	p := recurringMarkerPath(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, nil, 0o644))
}

func rfc3339(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }

// --- 1: dispatch ------------------------------------------------------------

func TestRecurringDispatchSendsClearThenSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "do the thing", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0, ClearBetween: true,
		IdleGraceSec: 5, BootMinSec: 5, CooldownSec: 5, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{Index: 1, Phase: cyclePhaseName(cyclePhaseDispatching)},
	})

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("ready ❯ ", nil).Maybe()
	var sent []string
	rec := func(args mock.Arguments) { sent = append(sent, args.Get(2).(string)) }
	exec.On("SendMessage", testSession, "@0", mock.Anything).Run(rec).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Run(rec).Return(nil).Maybe()

	goals := &GoalsFile{}
	writeGoals(t, dir, goals)

	d.driveRecurring(goals)

	require.Len(t, sent, 2, "dispatch must send /clear then /tmux:supervisor")
	assert.Equal(t, "/clear", sent[0])
	assert.Equal(t, "/tmux:supervisor do the thing", sent[1])

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettling), got.CurrentCycle.Phase, "phase → settling after dispatch")
	assert.NotEmpty(t, got.CurrentCycle.DispatchedAt, "dispatched_at must be stamped")
}

// --- 2: drain unmet — live worker ------------------------------------------

func TestRecurringSettlingStaysOnLiveWorkerWindow(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		IdleGraceSec: 30, BootMinSec: 30, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettling),
			DispatchedAt: rfc3339(clk.t),
		},
	})

	// A live execute-* worker (classifyWindow == "execute") means the cycle is
	// still in flight: the drain is unmet, so the driver must stay settling.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		{TmuxWindowID: "@1", Name: "execute-rec-1-1", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("working ❯ ", nil).Maybe()

	clk.advance(2 * time.Second)
	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettling), got.CurrentCycle.Phase, "stays settling on a live worker")
	assert.Equal(t, 0, got.CompletedCycles, "no advance while a worker is live")
	assert.NotEmpty(t, got.CurrentCycle.LastActivityAt, "live worker is activity — last_activity_at refreshed")
}

// --- 3: drain unmet — pending task -----------------------------------------

func TestRecurringSettlingStaysOnPendingTask(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		IdleGraceSec: 30, BootMinSec: 30, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettling),
			DispatchedAt: rfc3339(clk.t),
		},
	})
	writeTasksYaml(t, dir, "status: ready\ncycle: 1\ntasks:\n  - name: \"still working\"\n    wid: execute-9\n    status: in_progress\n    context: x.md\n")

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("idle ❯ ", nil).Maybe()

	clk.advance(2 * time.Second)
	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettling), got.CurrentCycle.Phase, "stays settling while a task is pending")
	assert.Equal(t, 0, got.CompletedCycles, "no advance while a task is pending")
	assert.NotEmpty(t, got.CurrentCycle.LastActivityAt, "pending task is activity — last_activity_at refreshed")
}

// --- 4: drain unmet — changing pane hash -----------------------------------

func TestRecurringSettlingStaysOnChangingPaneHash(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	const oldPane = "old pane output"
	const newPane = "new pane output — moved"
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		IdleGraceSec: 30, BootMinSec: 30, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettling),
			DispatchedAt: rfc3339(clk.t), LastProgressHash: hashPane(oldPane),
		},
	})

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return(newPane, nil).Maybe()

	clk.advance(2 * time.Second)
	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettling), got.CurrentCycle.Phase, "stays settling while the pane is changing")
	assert.Equal(t, hashPane(newPane), got.CurrentCycle.LastProgressHash, "last_progress_hash tracks the moved pane")
	assert.NotEmpty(t, got.CurrentCycle.LastActivityAt, "a changed pane updates last_activity_at")
}

// --- 5: settle past grace AND boot -----------------------------------------

func TestRecurringSettlesPastGraceAndBoot(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	const pane = "quiet ❯ "
	dispatched := clk.t
	task := &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		IdleGraceSec: 5, BootMinSec: 5, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettling),
			DispatchedAt: rfc3339(dispatched), LastProgressHash: hashPane(pane),
		},
	}
	writeRecurring(t, dir, task)

	// All drain clauses hold: only the bare supervisor window, no pending task,
	// pane hash stable.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return(pane, nil).Maybe()

	clk.advance(10 * time.Second) // past both idle_grace_sec and boot_min_sec

	// recurringSettled is the green drain+age predicate; the stub returns false.
	rt := &recurringRuntime{
		phase: cyclePhaseSettling, dispatchedAt: dispatched, lastProgressHash: hashPane(pane),
	}
	assert.True(t, d.recurringSettled(rt, task), "all clauses drained and aged past grace+boot ⇒ settled")

	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettled), got.CurrentCycle.Phase, "phase → settled")
	assert.Equal(t, 1, got.CompletedCycles, "completed_cycles incremented on settle")
	require.NotEmpty(t, got.History, "settle appends a history record")
	assert.Equal(t, "settled", got.History[len(got.History)-1].Outcome, "history outcome=settled")
}

// --- 6: force-settle on wall-clock cap -------------------------------------

func TestRecurringForceSettleOnWallClock(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	dispatched := clk.t
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		IdleGraceSec: 5, BootMinSec: 5, MaxCycleWallSec: 10,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettling),
			DispatchedAt: rfc3339(dispatched),
		},
	})

	// A live worker keeps the normal drain UNMET, proving the wall-clock cap force
	// settles regardless.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
		{TmuxWindowID: "@1", Name: "execute-rec-1-1", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("busy ❯ ", nil).Maybe()

	clk.advance(20 * time.Second) // past max_cycle_wall_sec
	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, cyclePhaseName(cyclePhaseSettled), got.CurrentCycle.Phase, "wall-clock cap forces settled")
	require.NotEmpty(t, got.History, "force-settle appends a history record")
	assert.Equal(t, "timeout", got.History[len(got.History)-1].Outcome, "history outcome=timeout")
}

// --- 7: advance to the next cycle ------------------------------------------

func TestRecurringAdvancesToNextCycle(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	settledAt := clk.t
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 1, ClearBetween: true,
		IdleGraceSec: 5, BootMinSec: 5, CooldownSec: 5, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 1, Phase: cyclePhaseName(cyclePhaseSettled),
			LastActivityAt: rfc3339(settledAt),
		},
	})

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("ready ❯ ", nil).Maybe()
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Return(nil).Maybe()

	clk.advance(10 * time.Second) // cooldown elapsed
	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, 2, got.CurrentCycle.Index, "current_cycle.index++ on advance")
	assert.Equal(t, cyclePhaseName(cyclePhaseDispatching), got.CurrentCycle.Phase, "advance re-enters dispatching")
}

// --- 8: finish goes idle (not via deactivateOnCompletion) ------------------

func TestRecurringFinishGoesIdle(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	clk := &fakeClock{t: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	d.clock = clk.now

	writeRecurringMarker(t, dir)
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 3,
		IdleGraceSec: 5, BootMinSec: 5, MaxCycleWallSec: 600,
		CurrentCycle: RecurringCycle{
			Index: 3, Phase: cyclePhaseName(cyclePhaseSettled),
			LastActivityAt: rfc3339(clk.t),
		},
	})

	var sent []string
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Run(func(args mock.Arguments) {
		sent = append(sent, args.Get(2).(string))
	}).Return(nil).Maybe()
	exec.On("SendMessage", testSession, "@0", mock.Anything).Run(func(args mock.Arguments) {
		sent = append(sent, args.Get(2).(string))
	}).Return(nil).Maybe()

	d.driveRecurring(&GoalsFile{})

	got := loadRecurringTask(t, dir)
	assert.Equal(t, RecurringDone, got.Status, "finish marks the task done")
	assert.Equal(t, modeIdle, d.mode, "finish returns the daemon to modeIdle")
	_, err := os.Stat(recurringMarkerPath(dir))
	assert.True(t, os.IsNotExist(err), "recurring-active marker is removed on finish")
	for _, m := range sent {
		assert.NotContains(t, m, "ALL-COMPLETE", "finish must NOT route through deactivateOnCompletion's ALL-COMPLETE")
	}
}

// --- 9: tick step-5 guard keeps mode active --------------------------------

func TestRecurringTickStep5KeepsModeActive(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	// No goal is in flight and none is runnable (a single GoalDone goal), so the
	// step-5 teardown predicate is reached. With a recurring task active, the
	// `&& !d.recurringActive()` guard must keep the daemon modeActive.
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "done", Status: GoalDone}},
	}
	writeGoals(t, dir, gf)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		CurrentCycle: RecurringCycle{Index: 1, Phase: cyclePhaseName(cyclePhaseSettling)},
	})

	setupDeactivateOnCompletionMocks(exec, testSession)

	ctx := context.Background()
	require.NoError(t, d.tick(ctx, gf))

	assert.Equal(t, modeActive, d.mode, "an active recurring task keeps the daemon modeActive at step-5")
}

// --- 10: poll modeIdle pickup activates ------------------------------------

func TestRecurringPollPickupActivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeIdle

	// No taskvisor-start signal, no goals — only an active recurring.yaml. The
	// poll modeIdle pickup seam must activate the daemon from the recurring file.
	writeRecurring(t, dir, &RecurringTask{
		ID: "rec-1", Prompt: "p", Status: RecurringActive,
		TotalCycles: 3, CompletedCycles: 0,
		CurrentCycle: RecurringCycle{Index: 1, Phase: cyclePhaseName(cyclePhaseDispatching)},
	})
	writeSettings(t, dir, true, true)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Return(nil).Maybe()
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil).Maybe()
	exec.On("CaptureWindowOutput", testSession, "@0").Return("ready ❯ ", nil).Maybe()

	ctx := context.Background()
	require.NoError(t, d.poll(ctx))

	assert.Equal(t, modeActive, d.mode, "an active recurring.yaml activates the idle daemon")
	assert.FileExists(t, recurringMarkerPath(dir), "pickup writes the recurring-active marker")
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "taskvisor-active"), "pickup writes the taskvisor-active marker")
}

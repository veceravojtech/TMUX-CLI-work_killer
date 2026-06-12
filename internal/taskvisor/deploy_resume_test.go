package taskvisor

// deploy_resume_test.go — goal-005 post-mortem (2026-06-12): a binary deploy
// (exec-replace restart) must not cascade into killing a goal's live worker
// pool.
//
// Incident shape: RestartOnStaleBinary exec-replaced the daemon mid-goal; crash
// recovery correctly resumed the supervising phase in place, but the supervisor
// Claude was interrupted shortly after and its pane went static while two
// execute-005-* workers were still mid-task. The lead-only progress heartbeat
// declared the goal stuck after 5m and handleStuckSupervisor swept the WHOLE
// namespace — destroying the workers' in-flight work — then re-dispatched.
//
// Covered here:
//  1. Pool output counts as goal progress: a static lead pane with a changing
//     worker pane never fires the heartbeat; only a fully-quiet namespace does.
//  2. Pool membership changes count as progress (a worker spawning/vanishing).
//  3. A planned exec-replace restart announces STATE exec-replace-restart, not
//     CRASH-RECOVERY, while the resume behavior itself stays identical.

import (
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHeartbeat_PoolProgressPreventsStuck — the supervisor pane is static the
// whole time; as long as a worker pane keeps changing the heartbeat refreshes,
// and it fires only once the entire namespace has been quiet past the timeout.
func TestHeartbeat_PoolProgressPreventsStuck(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@90", Name: "supervisor-001", CurrentCommand: "claude"},
		{TmuxWindowID: "@91", Name: "execute-001-1", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@90").Return("IDLE PROMPT", nil)
	exec.On("CaptureWindowOutput", testSession, "@91").Return("WORK-A", nil).Once()
	exec.On("CaptureWindowOutput", testSession, "@91").Return("WORK-B", nil)

	rt := &goalRuntime{}

	// Tick 1: first observation seeds, never fires.
	stuck, err := d.checkProgressHeartbeat(rt, "supervisor-001", "execute-001-")
	require.NoError(t, err)
	assert.False(t, stuck, "first observation seeds")

	// Tick 2, past the timeout: lead static but the worker pane changed
	// (WORK-A → WORK-B) — pool progress refreshes the heartbeat.
	clk.advance(6 * time.Minute)
	stuck, err = d.checkProgressHeartbeat(rt, "supervisor-001", "execute-001-")
	require.NoError(t, err)
	assert.False(t, stuck, "worker pane progress must keep an idle supervisor alive")

	// Tick 3, past the timeout again: NOTHING changed anywhere — now it fires.
	clk.advance(6 * time.Minute)
	stuck, err = d.checkProgressHeartbeat(rt, "supervisor-001", "execute-001-")
	require.NoError(t, err)
	assert.True(t, stuck, "a fully-quiet namespace past the timeout is stuck")
}

// TestHeartbeat_PoolMembershipChangeIsProgress — a worker window appearing
// between ticks changes the digest even when every captured pane is static.
func TestHeartbeat_PoolMembershipChangeIsProgress(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession
	clk := &fakeClock{t: time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC)}
	d.clock = clk.now
	d.progressTimeout = 5 * time.Minute

	leadOnly := []tmux.WindowInfo{
		{TmuxWindowID: "@90", Name: "supervisor-001", CurrentCommand: "claude"},
	}
	withWorker := append(leadOnly, tmux.WindowInfo{
		TmuxWindowID: "@91", Name: "execute-001-1", CurrentCommand: "claude",
	})
	exec.On("ListWindows", testSession).Return(leadOnly, nil).Once()
	exec.On("ListWindows", testSession).Return(withWorker, nil)
	exec.On("CaptureWindowOutput", testSession, "@90").Return("IDLE PROMPT", nil)
	exec.On("CaptureWindowOutput", testSession, "@91").Return("BOOTING", nil)

	rt := &goalRuntime{}

	stuck, err := d.checkProgressHeartbeat(rt, "supervisor-001", "execute-001-")
	require.NoError(t, err)
	assert.False(t, stuck)

	clk.advance(6 * time.Minute)
	stuck, err = d.checkProgressHeartbeat(rt, "supervisor-001", "execute-001-")
	require.NoError(t, err)
	assert.False(t, stuck, "a worker joining the pool is progress")
}

// TestCrashRecovery_PlannedRestart_NotifiesStateNotCrash — with the
// taskvisor-restart marker semantics (plannedRestart=true) the in-flight resume
// announces a planned exec-replace restart; the alarming CRASH-RECOVERY message
// is reserved for genuine crashes (plannedRestart=false, covered by
// TestCrashRecovery_NotifiesCrashRecovery).
func TestCrashRecovery_PlannedRestart_NotifiesStateNotCrash(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "g1", Status: GoalRunning, MaxRetries: 3},
		},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.crashRecovery(true))
	assert.Equal(t, modeActive, d.mode)

	var sawPlanned, sawCrash bool
	for _, call := range exec.Calls {
		if call.Method != "SendMessageWithDelay" && call.Method != "SendMessage" {
			continue
		}
		msg := call.Arguments.String(2)
		if strings.Contains(msg, "[TASKVISOR:STATE exec-replace-restart resumed=1 live-windows=none]") {
			sawPlanned = true
		}
		if strings.Contains(msg, "CRASH-RECOVERY") {
			sawCrash = true
		}
	}
	assert.True(t, sawPlanned, "planned restart must announce STATE exec-replace-restart resumed=1")
	assert.False(t, sawCrash, "planned restart must NOT announce CRASH-RECOVERY")
}

// TestCrashRecovery_AnnouncesLiveWindowsWithIDs — the recovery announcement
// surveys the running goals' namespace windows and embeds them as name(@id)
// pairs. The window ID is the recreation discriminator: a window's NAME is
// reused when the stuck/dispatch paths recreate it, but its tmux ID is not, so
// comparing the announced IDs against the post-recovery session shows whether
// the detected workers survived or were recreated.
func TestCrashRecovery_AnnouncesLiveWindowsWithIDs(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)
	writeGoals(t, dir, &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "g1", Status: GoalRunning, MaxRetries: 3},
		},
	})

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"}, // human window-0: NOT part of the goal namespace
		{TmuxWindowID: "@5", Name: "supervisor-001", CurrentCommand: "claude"},
		{TmuxWindowID: "@6", Name: "execute-001-1", CurrentCommand: "claude"},
		{TmuxWindowID: "@7", Name: "execute-001-2", CurrentCommand: "claude"},
		{TmuxWindowID: "@8", Name: "supervisor-002"}, // other goal's namespace: not running, excluded
	}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.crashRecovery(true))

	var found bool
	for _, call := range exec.Calls {
		if call.Method != "SendMessageWithDelay" {
			continue
		}
		if strings.Contains(call.Arguments.String(2),
			"live-windows=supervisor-001(@5),execute-001-1(@6),execute-001-2(@7)") {
			found = true
		}
	}
	assert.True(t, found, "announcement must list the goal's live windows as name(@id) pairs")
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase, "live supervisor window resumes in place")
}

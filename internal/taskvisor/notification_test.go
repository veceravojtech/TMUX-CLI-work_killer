package taskvisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNotifySupervisor_Success(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", "test msg").Return(nil)

	d.notifySupervisor("test msg")

	exec.AssertCalled(t, "SendMessage", testSession, "@0", "test msg")
}

func TestNotifySupervisor_MissingWindow(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001"},
	}, nil)

	d.notifySupervisor("test msg")

	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestNotifySupervisor_SendError(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", "test msg").Return(fmt.Errorf("send failed"))

	d.notifySupervisor("test msg")

	exec.AssertCalled(t, "SendMessage", testSession, "@0", "test msg")
}

func TestNotifySupervisor_EmptySession(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = ""

	d.notifySupervisor("test msg")

	exec.AssertNotCalled(t, "ListWindows", mock.Anything)
	exec.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything)
}

func TestCountCascaded(t *testing.T) {
	goals := &GoalsFile{Goals: []Goal{
		{ID: "A", Status: GoalFailed, BlockedBy: ""},
		{ID: "B", Status: GoalBlocked, BlockedBy: "A"},
		{ID: "C", Status: GoalBlocked, BlockedBy: "A"},
	}}
	assert.Equal(t, 2, countCascaded(goals, "A"))
}

func TestCountCascaded_NoCascade(t *testing.T) {
	goals := &GoalsFile{Goals: []Goal{
		{ID: "A", Status: GoalFailed},
	}}
	assert.Equal(t, 0, countCascaded(goals, "A"))
}

func TestDeactivateOnCompletion_GoalDoneNotifications(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	fixedNow := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixedNow }
	d.activatedAt = fixedNow.Add(-30 * time.Minute)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Description: "first goal", Status: GoalDone,
			StartedAt: "2026-06-07T11:00:00Z", FinishedAt: "2026-06-07T11:15:00Z"},
		{ID: "goal-002", Description: "second goal", Status: GoalDone,
			StartedAt: "2026-06-07T11:20:00Z", FinishedAt: "2026-06-07T11:45:00Z"},
		{ID: "goal-003", Description: "failed goal", Status: GoalFailed,
			StartedAt: "2026-06-07T11:50:00Z", FinishedAt: "2026-06-07T11:55:00Z"},
	}}
	writeGoals(t, dir, gf)

	// notifySupervisor calls: ListWindows for each notification (2 GOAL-DONE + 1 ALL-COMPLETE = 3 calls)
	// plus deactivation mocks
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	require.NoError(t, d.deactivateOnCompletion(gf))

	var sentMsgs []string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			sentMsgs = append(sentMsgs, call.Arguments.String(2))
		}
	}

	doneMsgs := 0
	for _, msg := range sentMsgs {
		if strings.Contains(msg, "[TASKVISOR:GOAL-DONE") {
			doneMsgs++
		}
	}
	assert.Equal(t, 2, doneMsgs, "should send GOAL-DONE for each done goal, not for failed")

	var hasDone1, hasDone2 bool
	for _, msg := range sentMsgs {
		if strings.Contains(msg, "id=goal-001") && strings.Contains(msg, "GOAL-DONE") {
			hasDone1 = true
		}
		if strings.Contains(msg, "id=goal-002") && strings.Contains(msg, "GOAL-DONE") {
			hasDone2 = true
		}
	}
	assert.True(t, hasDone1, "should send GOAL-DONE for goal-001")
	assert.True(t, hasDone2, "should send GOAL-DONE for goal-002")
}

func TestDeactivateOnCompletion_AllCompleteNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	fixedNow := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixedNow }
	d.activatedAt = fixedNow.Add(-30 * time.Minute)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Description: "done goal", Status: GoalDone,
			StartedAt: "2026-06-07T11:00:00Z", FinishedAt: "2026-06-07T11:15:00Z"},
		{ID: "goal-002", Description: "done goal 2", Status: GoalDone,
			StartedAt: "2026-06-07T11:20:00Z", FinishedAt: "2026-06-07T11:45:00Z"},
		{ID: "goal-003", Description: "failed goal", Status: GoalFailed,
			StartedAt: "2026-06-07T11:50:00Z", FinishedAt: "2026-06-07T11:55:00Z"},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	require.NoError(t, d.deactivateOnCompletion(gf))

	var allCompleteMsg string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:ALL-COMPLETE") {
				allCompleteMsg = msg
			}
		}
	}
	require.NotEmpty(t, allCompleteMsg, "must send ALL-COMPLETE")
	assert.Contains(t, allCompleteMsg, "done=2")
	assert.Contains(t, allCompleteMsg, "failed=1")
	assert.Contains(t, allCompleteMsg, "blocked=0")
	assert.Contains(t, allCompleteMsg, "wall=30m0s")
}

func TestDeactivateOnCompletion_AllCompleteAfterGoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	fixedNow := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixedNow }
	d.activatedAt = fixedNow.Add(-10 * time.Minute)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Description: "done", Status: GoalDone,
			StartedAt: "2026-06-07T11:00:00Z", FinishedAt: "2026-06-07T11:15:00Z"},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	require.NoError(t, d.deactivateOnCompletion(gf))

	var msgOrder []string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "GOAL-DONE") {
				msgOrder = append(msgOrder, "GOAL-DONE")
			} else if strings.Contains(msg, "ALL-COMPLETE") {
				msgOrder = append(msgOrder, "ALL-COMPLETE")
			}
		}
	}
	require.Equal(t, []string{"GOAL-DONE", "ALL-COMPLETE"}, msgOrder, "ALL-COMPLETE must come after all GOAL-DONE messages")
}

func TestHandleStuckSupervisor_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "stuck goal", Status: GoalRunning,
			StartedAt:    "2026-06-07T10:00:00Z",
			StuckRetries: 1, MaxStuckRetries: 3,
			CodeRetries: 5, MaxCodeRetries: 5,
			SpecRetries: 3, MaxSpecRetries: 3,
			ValidationRetries: 2, MaxValidationRetries: 2,
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckSupervisor(goal, gf))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=stuck-supervisor") &&
				strings.Contains(msg, "cascade=1") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=stuck-supervisor and cascade=1")
}

func TestRerunValidationOnly_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "validation goal", Status: GoalRunning,
			StartedAt:            "2026-06-07T10:00:00Z",
			ValidationRetries:    1,
			MaxValidationRetries: 2,
			CodeRetries:          5, MaxCodeRetries: 5,
			SpecRetries: 3, MaxSpecRetries: 3,
			StuckRetries: 3, MaxStuckRetries: 3,
		},
		{ID: "goal-002", Description: "dep A", Status: GoalPending, DependsOn: []string{"goal-001"}},
		{ID: "goal-003", Description: "dep B", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, nil))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=validation-exhausted") &&
				strings.Contains(msg, "cascade=2") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=validation-exhausted and cascade=2")
}

func TestHandleFailedCycle_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "code goal", Status: GoalRunning,
			StartedAt:      "2026-06-07T10:00:00Z",
			CodeRetries:    1,
			MaxCodeRetries: 5,
			SpecRetries:    3, MaxSpecRetries: 3,
			ValidationRetries: 2, MaxValidationRetries: 2,
			StuckRetries: 3, MaxStuckRetries: 3,
		},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "test reason", "code-defect"))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=code-exhausted") &&
				strings.Contains(msg, "cascade=0") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=code-exhausted and cascade=0")
}

func TestBounceToGeneration_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "spec goal", Status: GoalRunning,
			StartedAt:      "2026-06-07T10:00:00Z",
			SpecRetries:    1,
			MaxSpecRetries: 3,
			CodeRetries:    5, MaxCodeRetries: 5,
			ValidationRetries: 2, MaxValidationRetries: 2,
			StuckRetries: 3, MaxStuckRetries: 3,
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	valSig := &ValidatorSignal{Verdict: VerdictBlocked, Owner: "planner"}
	require.NoError(t, d.bounceToGeneration(goal, gf, valSig))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=spec-exhausted") &&
				strings.Contains(msg, "cascade=1") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=spec-exhausted and cascade=1")
}

func TestHaltRetryCeiling_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "ceiling goal", Status: GoalRunning,
			StartedAt:  "2026-06-07T10:00:00Z",
			Retries:    100,
			MaxRetries: 10,
		},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.haltRetryCeiling(goal, gf))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=retry-ceiling") &&
				strings.Contains(msg, "cascade=0") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=retry-ceiling and cascade=0")
}

func TestHaltBlockedEnv_NoGoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "env goal", Status: GoalRunning,
			StartedAt: "2026-06-07T10:00:00Z",
		},
	}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	goal := &gf.Goals[0]
	valSig := &ValidatorSignal{
		Verdict: VerdictBlocked,
		Owner:   "ops",
		Class:   "env-config",
	}
	require.NoError(t, d.haltBlockedEnv(goal, gf, valSig))

	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			msg := call.Arguments.String(2)
			assert.NotContains(t, msg, "GOAL-FAILED", "haltBlockedEnv must NOT send GOAL-FAILED (soft cascade)")
		}
	}
}

func TestDeactivateOnCompletion_MissingSupervisor_NoError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	fixedNow := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixedNow }
	d.activatedAt = fixedNow.Add(-5 * time.Minute)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{ID: "goal-001", Description: "done", Status: GoalDone,
			StartedAt: "2026-06-07T11:00:00Z", FinishedAt: "2026-06-07T11:15:00Z"},
	}}
	writeGoals(t, dir, gf)

	d.SetWindowCreateFunc(mockCreateWindowFn("@2"))

	// First several ListWindows calls: no bare "supervisor" (namespaced only).
	// Notifications log a warning. KillWindow for teardown. Last ListWindows
	// call from ensureWindow0Supervisor — return the freshly created bare supervisor.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001"},
	}, nil).Times(10)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@2", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)
	exec.On("CaptureWindowOutput", testSession, "@2").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	require.NoError(t, d.deactivateOnCompletion(gf), "must complete without error even when supervisor window is missing")
	assert.Equal(t, modeIdle, d.mode)
}

func TestHandleStuckValidator_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "stuck validator goal", Status: GoalRunning,
			StartedAt:       "2026-06-07T10:00:00Z",
			StuckRetries:    1,
			MaxStuckRetries: 3,
			CodeRetries:     5, MaxCodeRetries: 5,
			SpecRetries: 3, MaxSpecRetries: 3,
			ValidationRetries: 2, MaxValidationRetries: 2,
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@3", Name: "validator-001"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)
	exec.On("ClosePipePane", testSession, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckValidator(goal, gf))

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=stuck-validator") &&
				strings.Contains(msg, "cascade=1") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=stuck-validator and cascade=1")
}

func TestFinalizeWorktreeOnDone_IntegrationFailed_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "integration goal", Status: GoalDone,
			StartedAt:  "2026-06-07T10:00:00Z",
			FinishedAt: "2026-06-07T10:30:00Z",
		},
		{ID: "goal-002", Description: "dependent", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	wtDir := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wtDir, 0o755))
	rt := d.runtime("goal-001")
	rt.WorktreeDir = wtDir
	rt.Branch = "taskvisor/goal-001"

	callN := 0
	d.SetGitRunnerFunc(func(ctx context.Context, args ...string) (string, string, int, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "add -A"):
			return "", "", 0, nil
		case strings.Contains(joined, "status --porcelain"):
			return "", "", 0, nil
		case strings.Contains(joined, "rev-parse --abbrev-ref HEAD"):
			return "main\n", "", 0, nil
		case strings.Contains(joined, "rev-list --count"):
			return "1\n", "", 0, nil
		case strings.Contains(joined, "rebase"):
			return "", "", 0, nil
		case strings.Contains(joined, "merge --ff-only"):
			return "", "", 0, nil
		case strings.Contains(joined, "worktree remove"):
			return "", "", 0, nil
		case strings.Contains(joined, "branch -D"):
			return "", "", 0, nil
		}
		callN++
		return "", "", 0, nil
	})

	// Write settings with integration_cmd so the gate fires
	settingsContent := `taskvisor:
  integration_cmd: "make test"
`
	p := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	require.NoError(t, os.WriteFile(p, []byte(settingsContent), 0o644))

	d.SetScriptRunnerFunc(func(ctx context.Context, scriptPath, runDir string, env []string) (string, string, int, error) {
		return "", "tests failed", 1, fmt.Errorf("exit 1")
	})

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	failed, err := d.finalizeWorktreeOnDone(gf, goal)
	require.NoError(t, err)
	assert.True(t, failed, "should report failed=true for integration-gate failure")

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=integration-gate-failed") &&
				strings.Contains(msg, "cascade=1") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=integration-gate-failed and cascade=1")
}

func TestFinalizeWorktreeOnDone_MergeConflict_GoalFailedNotification(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
		{
			ID: "goal-001", Description: "merge conflict goal", Status: GoalDone,
			StartedAt:  "2026-06-07T10:00:00Z",
			FinishedAt: "2026-06-07T10:30:00Z",
		},
		{ID: "goal-002", Description: "dep A", Status: GoalPending, DependsOn: []string{"goal-001"}},
		{ID: "goal-003", Description: "dep B", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	writeGoals(t, dir, gf)

	wtDir := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
	require.NoError(t, os.MkdirAll(wtDir, 0o755))
	rt := d.runtime("goal-001")
	rt.WorktreeDir = wtDir
	rt.Branch = "taskvisor/goal-001"

	d.SetGitRunnerFunc(func(ctx context.Context, args ...string) (string, string, int, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "add -A"):
			return "", "", 0, nil
		case strings.Contains(joined, "status --porcelain"):
			return "", "", 0, nil
		case strings.Contains(joined, "rev-parse --abbrev-ref HEAD"):
			return "main\n", "", 0, nil
		case strings.Contains(joined, "rev-list --count"):
			return "1\n", "", 0, nil
		case strings.Contains(joined, "rebase") && !strings.Contains(joined, "--abort"):
			return "", "", 1, nil
		case strings.Contains(joined, "ls-files --unmerged"):
			return "internal/foo.go\n", "", 0, nil
		case strings.Contains(joined, "rebase --abort"):
			return "", "", 0, nil
		case strings.Contains(joined, "worktree remove"):
			return "", "", 0, nil
		case strings.Contains(joined, "branch -D"):
			return "", "", 0, nil
		}
		return "", "", 0, nil
	})

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("SendMessage", testSession, "@0", mock.Anything).Return(nil)

	goal := &gf.Goals[0]
	failed, err := d.finalizeWorktreeOnDone(gf, goal)
	require.NoError(t, err)
	assert.True(t, failed, "should report failed=true for merge conflict")

	var foundNotif bool
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" && call.Arguments.Get(1) == "@0" {
			msg := call.Arguments.String(2)
			if strings.Contains(msg, "[TASKVISOR:GOAL-FAILED") &&
				strings.Contains(msg, "reason=merge-conflict") &&
				strings.Contains(msg, "cascade=2") {
				foundNotif = true
			}
		}
	}
	assert.True(t, foundNotif, "must send GOAL-FAILED with reason=merge-conflict and cascade=2")
}

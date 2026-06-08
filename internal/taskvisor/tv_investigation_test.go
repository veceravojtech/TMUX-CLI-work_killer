package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestInvestigationLifecycle_ValidatorSpawnsSendsInvestigateCommand(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession

	goal := &Goal{
		ID:          "goal-001",
		Description: "test goal",
		Acceptance:  []string{"it works"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdPath := filepath.Join(goalDir, "goal.md")
	require.NoError(t, os.WriteFile(goalMdPath, []byte("# Test Goal\n"), 0o644))

	var capturedCmd string
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@5").Return("❯ ", nil)
	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		capturedCmd = cmd
		return strings.HasPrefix(cmd, "/tmux:investigate ")
	})).Return(nil)

	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	err = d.createValidatorAndSendPayload(goal)
	require.NoError(t, err)

	expectedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md")
	assert.Equal(t, "/tmux:investigate "+expectedPath, capturedCmd)
}

func TestInvestigationLifecycle_FailedValidation_WritesCorrectionAndRetries(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix pricing bug", Timestamp: "2026-05-20T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	goal := &gf.Goals[0]
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, phaseSupervising, d.runtime("goal-001").phase)

	corrPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	data, readErr := os.ReadFile(corrPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "fix pricing bug")
	assert.Contains(t, string(data), "Implementation completed but failed acceptance criteria.")

	sig, sigErr := LoadSignal(dir, "goal-001")
	assert.NoError(t, sigErr)
	assert.Nil(t, sig)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)
	dispatchData, _ := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	assert.Contains(t, string(dispatchData), "fix pricing bug")
	assert.NotContains(t, string(dispatchData), "None (first attempt)")
}

func TestInvestigationLifecycle_RedispatchIncludesCorrectionsInDispatchMd(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Fix pricing display", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"Price matches API"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	err = d.handleFailedCycle(goal, gf, "Fix pricing bug — API returns cents not dollars", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Fix pricing bug")
	assert.Contains(t, content, "API returns cents not dollars")
	assert.NotContains(t, content, "None (first attempt)")
	assert.Contains(t, content, "Prior Corrections")
}

func TestInvestigationLifecycle_RedispatchInjectsCorrectionsIntoTaskContext(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
  - name: "task two"
    wid: execute-2
    status: done
    context: .tmux-cli/research/ctx2.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1 context")
	writeTaskContext(t, dir, ".tmux-cli/research/ctx2.md", "# Task 2 context")

	goal := &gf.Goals[0]
	err = d.handleFailedCycle(goal, gf, "Fix the broken test", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err = d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	ctx1Data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ctx1Data), "# Task 1 context")
	assert.Contains(t, string(ctx1Data), "## Prior Corrections (Cycle 1)")
	assert.Contains(t, string(ctx1Data), "Fix the broken test")

	ctx2Data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx2.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ctx2Data), "# Task 2 context")
	assert.Contains(t, string(ctx2Data), "## Prior Corrections (Cycle 1)")
	assert.Contains(t, string(ctx2Data), "Fix the broken test")
}

func TestInvestigationLifecycle_MultipleCycles_CorrectionsAccumulate(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.runtime("goal-001").lastSupervisorStatus = "done"

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, Retries: 0, MaxRetries: 5, Acceptance: []string{"it works"}, CodeRetries: 5, MaxCodeRetries: 5},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]

	err = d.handleFailedCycle(goal, gf, "Fix pricing calculation", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 4, goal.CodeRetries, "code budget 5->4")

	goal.Status = GoalRunning
	err = d.handleFailedCycle(goal, gf, "Also fix currency formatting", "code-defect")
	require.NoError(t, err)
	assert.Equal(t, 3, goal.CodeRetries, "code budget 5->4->3")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	_, statErr1 := os.Stat(filepath.Join(corrDir, "cycle-1.md"))
	assert.NoError(t, statErr1)
	_, statErr2 := os.Stat(filepath.Join(corrDir, "cycle-2.md"))
	assert.NoError(t, statErr2)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Fix pricing calculation")
	assert.Contains(t, content, "Also fix currency formatting")
	assert.NotContains(t, content, "None (first attempt)")

	idx1 := strings.Index(content, "Fix pricing calculation")
	idx2 := strings.Index(content, "Also fix currency formatting")
	assert.True(t, idx1 < idx2, "cycle-1 correction should appear before cycle-2")
}

func TestInvestigationLifecycle_FullChain_FailThenPassOnRetry(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising
	d.validatorSendDelay = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test goal", Status: GoalRunning, Retries: 0, MaxRetries: 3, Acceptance: []string{"it works"}, CodeRetries: 3, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	writeGuardFile(t, dir)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Context")

	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		switch name {
		case "validator-001":
			return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
		case "supervisor-001":
			return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
		}
		return nil, fmt.Errorf("unexpected window: %s", name)
	})

	validatorClaude := []tmux.WindowInfo{{TmuxWindowID: "@5", Name: "validator-001", CurrentCommand: "claude"}}
	validatorPlain := []tmux.WindowInfo{{TmuxWindowID: "@5", Name: "validator-001"}}
	supervisorClaude := []tmux.WindowInfo{{TmuxWindowID: "@9", Name: "supervisor-001", CurrentCommand: "claude"}}
	empty := []tmux.WindowInfo{}

	// Stage 1: checkSupervisingPhase — supervisor done, create validator
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	// Stage 2: checkValidatingPhase — validator fail
	exec.On("ListWindows", testSession).Return(validatorPlain, nil).Once()
	// Stage 3: dispatchRetry — kill all (5 kills incl. plan-audit) + collect + gone, create supervisor
	exec.On("ListWindows", testSession).Return(empty, nil).Times(7)
	exec.On("ListWindows", testSession).Return(supervisorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(supervisorClaude, nil).Once()
	// Stage 3b: dispatchRetry's notifySupervisor lookup for [TASKVISOR:GOAL-DISPATCHED
	// ...retry=true] — the window is namespaced ("supervisor-001"), so findWindowByName
	// ("supervisor") finds no match and the notification silently skips (no SendMessage).
	exec.On("ListWindows", testSession).Return(supervisorClaude, nil).Once()
	// Stage 4: checkSupervisingPhase — supervisor done again, create validator
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	exec.On("ListWindows", testSession).Return(validatorClaude, nil).Once()
	// Stage 5: checkValidatingPhase pass + deactivateOnCompletion
	// 1 for notifyCompletion,
	// 5 for teardown kill lookups (killGoalWindows), 1 for collectManagedNames, 1 for waitWindowsGone
	exec.On("ListWindows", testSession).Return(validatorPlain, nil).Once()
	exec.On("ListWindows", testSession).Return(empty, nil).Times(8)

	exec.On("CaptureWindowOutput", testSession, "@5").Return("", fmt.Errorf("no prompt")).Times(2)
	exec.On("CaptureWindowOutput", testSession, "@9").Return("", fmt.Errorf("no prompt")).Once()

	exec.On("SendMessage", testSession, "@5", mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "/tmux:investigate")
	})).Return(nil).Times(2)
	exec.On("SendMessage", testSession, "@9", "/tmux:supervisor goal-001").Return(nil).Once()

	exec.On("KillWindow", testSession, "@5").Return(nil).Times(2)

	goal := &gf.Goals[0]

	// Stage 1: Supervisor done → validator spawned
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T14:30:00Z",
	}))
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	// Stage 2: Validator fail → correction written, retries incremented
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "fail", NextAction: "fix pricing", Timestamp: "2026-05-20T14:35:00Z",
	}))
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, 2, goal.CodeRetries, "code budget 3->2")
	assert.Equal(t, GoalPending, goal.Status)

	corrPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections", "cycle-1.md")
	_, corrStatErr := os.Stat(corrPath)
	require.NoError(t, corrStatErr, "correction file should exist after failed cycle")

	// Stage 3: Re-dispatch with corrections
	err = d.dispatchRetry(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, GoalRunning, goal.Status)

	// Stage 4: Supervisor done again → validator spawned
	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-05-20T15:30:00Z",
	}))
	err = d.checkSupervisingPhase(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)

	// Stage 5: Validator pass → goal done
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: "pass", Timestamp: "2026-05-20T15:35:00Z",
	}))
	err = d.checkValidatingPhase(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalDone, goal.Status)
	assert.NotEmpty(t, goal.FinishedAt)
	assert.Equal(t, modeIdle, d.mode)
}

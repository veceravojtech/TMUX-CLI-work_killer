package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDispatch_WritesDispatchMd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Fix pricing",
			Acceptance:  []string{"Price matches API"},
			Validate:    []string{"run pricing test"},
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	goal := &gf.Goals[0]
	err = d.dispatch(goal, gf)
	require.NoError(t, err)

	dispatchPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	data, err := os.ReadFile(dispatchPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Price matches API")
	assert.NotContains(t, string(data), "run pricing test")
}

func TestDispatch_WritesCurrentGoalFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "taskvisor-current-goal"))
	require.NoError(t, err)
	assert.Equal(t, "goal-001", string(data))
}

func TestDispatch_KillWaitCreateBootSend(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	callOrder := make([]string, 0, 10)

	// Call 1: killWindowByName("supervisor") — finds supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@0").Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "kill")
	})
	// Calls 2-5: killWindowsByPrefix("execute-"), killWindowByName("validator"),
	// killWindowsByPrefix("inv-"), killWindowByName("plan-audit") — empty
	// (killGoalWindows runs all 5 kills, including plan-audit).
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(4)
	// Call 6: collectManagedNames — empty
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 7: waitWindowsGone — still has supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
	}, nil).Once()
	// Call 8: waitWindowsGone — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// Call 9: waitClaudeBoot — zsh
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "zsh"},
	}, nil).Once()
	// Call 10+: waitClaudeBoot — claude
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	// waitForPrompt — prompt detected
	exec.On("CaptureWindowOutput", testSession, "@1").Return("❯ ", nil)

	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		callOrder = append(callOrder, "create")
		return &CreatedWindow{TmuxWindowID: "@1", Name: name}, nil
	})

	exec.On("SendMessage", testSession, "@1", mock.MatchedBy(func(msg string) bool {
		return strings.HasPrefix(msg, "/tmux:plan")
	})).Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "send")
	})

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	killIdx := indexOf(callOrder, "kill")
	createIdx := indexOf(callOrder, "create")
	sendIdx := indexOf(callOrder, "send")
	require.NotEqual(t, -1, killIdx, "kill should have been called")
	require.NotEqual(t, -1, createIdx, "create should have been called")
	require.NotEqual(t, -1, sendIdx, "send should have been called")
	assert.Greater(t, createIdx, killIdx, "create must come after kill")
	assert.Greater(t, sendIdx, createIdx, "send must come after create")
}

func TestDispatch_SetsRunningStatus(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	goal := &gf.Goals[0]
	err = d.dispatch(goal, gf)
	require.NoError(t, err)

	assert.Equal(t, GoalRunning, goal.Status)

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalRunning, g.Status)
}

func TestDispatch_RecordsDispatchTime(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	before := time.Now()
	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	assert.WithinDuration(t, time.Now(), d.runtime("goal-001").dispatchTime, time.Second)
	assert.True(t, d.runtime("goal-001").dispatchTime.After(before) || d.runtime("goal-001").dispatchTime.Equal(before))
}

func TestWriteDispatchMd_FirstAttempt(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
	}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "None (first attempt)")
	assert.Contains(t, content, "Price matches API")
}

func TestWriteDispatchMd_WithCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	correctionsDir := filepath.Join(goalDir, "corrections")
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-1.md"), []byte("First correction"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-2.md"), []byte("Second correction"), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "First correction")
	assert.Contains(t, content, "Second correction")
	assert.NotContains(t, content, "None (first attempt)")
}

func TestWriteDispatchMd_ExcludesValidateRules(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"Price matches API"},
		Validate:    []string{"run pricing e2e test", "check price format"},
	}
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "run pricing e2e test")
	assert.NotContains(t, content, "check price format")
	assert.NotContains(t, content, "validate")
	assert.NotContains(t, content, "Validate")
}

func TestWriteDispatchMd_GoalMdPresent(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"inline criterion"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- Price matches API response\n- Currency symbol shown\n\n## Context\n\nPricing page redesign"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Price matches API response")
	assert.Contains(t, content, "Currency symbol shown")
	assert.Contains(t, content, "Pricing page redesign")
	assert.NotContains(t, content, "- inline criterion")
}

func TestWriteDispatchMd_GoalMdEmpty_FallsBackToInline(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"criterion A"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(""), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "- criterion A")
}

func TestWriteDispatchMd_GoalMdTakesPrecedence(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
		Acceptance:  []string{"inline criterion"},
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- goal.md criterion\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "goal.md criterion")
	assert.NotContains(t, content, "inline criterion")
}

func TestWriteDispatchMd_GoalMdPreservesCorrections(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix pricing display",
	}
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMdContent := "# Fix pricing display\n\n## Acceptance Criteria\n\n- goal.md criterion\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0o644))

	correctionsDir := filepath.Join(goalDir, "corrections")
	require.NoError(t, os.WriteFile(filepath.Join(correctionsDir, "cycle-1.md"), []byte("First correction"), 0o644))

	err = d.writeDispatchMd(goal)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "goal.md criterion")
	assert.Contains(t, content, "First correction")
	assert.NotContains(t, content, "None (first attempt)")
}

// --- validate.sh gate tests ---

func TestDispatch_LogsStateTransition(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	output := captureLog(t, func() {
		err = d.dispatch(&gf.Goals[0], gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: pending -> running")
}

func TestDispatch_LogsPhaseTransition(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseNone

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	output := captureLog(t, func() {
		err = d.dispatch(&gf.Goals[0], gf)
	})
	require.NoError(t, err)
	assert.Contains(t, output, "goal-001: phase idle -> supervising")
}

func TestDispatchRetry_ResetsTaskStatuses(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

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
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")
	writeTaskContext(t, dir, ".tmux-cli/research/ctx2.md", "# Task 2")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Fix task two"), 0o644))

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	goal := &gf.Goals[0]
	err := d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	// per-goal tasks.yaml should have all tasks reset to pending
	data, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "status: done", "all task statuses should be reset from done")
	assert.Contains(t, content, "status: pending", "tasks should be reset to pending")
}

func TestDispatchRetry_InjectsCorrections(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3, CodeRetries: 2, MaxCodeRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Original context")

	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Remove Doctrine from quality-gates.md"), 0o644))

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	goal := &gf.Goals[0]
	err := d.dispatchRetry(goal, gf)
	require.NoError(t, err)

	// Context file should have corrections appended
	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)
	assert.Contains(t, ctxContent, "# Original context", "original context preserved")
	assert.Contains(t, ctxContent, "Prior Corrections", "corrections section appended")
	assert.Contains(t, ctxContent, "Remove Doctrine from quality-gates.md", "correction content present")
}

func TestDispatchRetry_FallsBackToDispatch_WhenNoTasksYaml(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	// No tasks.yaml written — should fallback

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	err := d.tick(context.Background(), gf)
	require.NoError(t, err)

	// Without tasks.yaml, should fall back to /tmux:plan
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:plan", "without tasks.yaml should fallback to /tmux:plan")
}

func TestDispatch_DispatchMdWellFormedForPlan(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Implement pricing module",
			Acceptance:  []string{"Price matches API"},
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMd := "# Implement pricing module\n\n## Acceptance Criteria\n\n- Price matches API response exactly\n- Currency formatting follows locale\n\n## Context\n\nThe pricing page was redesigned.\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0o644))

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Dispatch:")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "Price matches API response exactly")
	assert.Contains(t, content, "Currency formatting follows locale")
	assert.Contains(t, content, "## Prior Corrections")
}

func TestDispatch_PlanCommandSendsCorrectPath(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	expectedPath := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md")
	// dispatch() ships goal.ID as a trailing token so plan.xml gets an explicit
	// goal binding (see TestDispatch_PlanCommandCarriesGoalID).
	expectedCmd := "/tmux:plan " + expectedPath + " goal-001"
	exec.AssertCalled(t, "SendMessage", testSession, "@0", expectedCmd)
}

func TestDispatch_ResultingTasksYamlValid(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: implement pricing
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "valid single-task tasks.yaml should produce no validation errors")
}

// --- 6.28: Multi-task fan-out 3 BCs → 3 parallel tasks ---

func TestDispatch_FanOutHintsInGoalMd_PropagatedToDispatchMd(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "Fan-out test",
			Status:      GoalPending,
		}},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goalMd := "# Fan-out test\n\n## Acceptance Criteria\n\n- All three BCs implemented\n\n## Fan-Out\n\nParallel tasks for three bounded contexts:\n- BC-Pricing: implement price calculation\n- BC-Display: implement UI rendering\n- BC-Logging: implement audit trail\n"
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0o644))

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	err = d.dispatch(&gf.Goals[0], gf)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "dispatch.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Fan-Out")
	assert.Contains(t, content, "BC-Pricing: implement price calculation")
	assert.Contains(t, content, "BC-Display: implement UI rendering")
	assert.Contains(t, content, "BC-Logging: implement audit trail")
}

func TestDispatchRetry_UsesPerGoalPath(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending, Retries: 1, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	// Sentinel top-level planning-queue: dispatchRetry must not touch it.
	writeTasksYaml(t, dir, `status: ready
cycle: 1
tasks:
  - name: "top task"
    wid: execute-9
    status: done
    context: top.md
`)

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# context")

	d.createWindowFn = mockCreateWindowFn("@99")
	setupDispatchMocks(exec, testSession, "@99")

	// Drives the tick retry-branch: tasksYamlExists("goal-001") gates re-dispatch.
	require.NoError(t, d.tick(context.Background(), gf))

	// reset operated on the GOAL-SCOPED file.
	goalData, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	assert.NotContains(t, string(goalData), "status: done", "per-goal tasks reset to pending")
	assert.Contains(t, string(goalData), "status: pending")

	// top-level planning-queue untouched.
	topData, err := os.ReadFile(tasks.TasksFilePath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(topData), "status: done", "top-level planning-queue must be left untouched")

	// retry sends /tmux:supervisor (skip planning), confirming the retry branch.
	var sentCmd string
	for _, call := range exec.Calls {
		if call.Method == "SendMessage" {
			sentCmd = call.Arguments.Get(2).(string)
		}
	}
	assert.Contains(t, sentCmd, "/tmux:supervisor")
	assert.NotContains(t, sentCmd, "/tmux:plan")
}

// --- E1-0e: ready-set scheduler tests ---

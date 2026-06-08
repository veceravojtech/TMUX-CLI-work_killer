package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeactivateOnCompletion_KillsWindowsNoSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "done", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	// notifyCompletion (1 ListWindows for supervisor lookup)
	// + killWindowByName("supervisor-001"), killWindowsByPrefix("execute-001-"),
	// killWindowByName("validator-001"), killWindowsByPrefix("investigator-001-"),
	// collectManagedNames, waitWindowsGone — all need ListWindows.
	// First: notifyCompletion finds no bare "supervisor" → logs and skips.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	// killWindowByName("supervisor-001") — finds the goal's namespaced supervisor
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor-001"},
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@0").Return(nil)
	// killWindowsByPrefix("execute-001-") — finds execute-001-1
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-001-1"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@1").Return(nil)
	// killWindowByName("validator-001") — none
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// killWindowsByPrefix("investigator-") — none
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// killWindowByName("plan-audit-001") — none
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// collectManagedNames — gone
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
	// waitWindowsGone — immediate success
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()

	var createCalled bool
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createCalled = true
		return &CreatedWindow{TmuxWindowID: "@9", Name: name}, nil
	})

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.False(t, createCalled, "supervisor window should NOT be created on completion")
	exec.AssertCalled(t, "KillWindow", testSession, "@0")
	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	assert.Equal(t, modeIdle, d.mode)
}

func TestDeactivateOnCompletion_RemovesAllRuntimeMarkers(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeAllRuntimeMarkers(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assertAllRuntimeMarkersAbsent(t, dir)
}

func TestDeactivateOnCompletion_ArchivesTopLevelTasksYaml(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeAllRuntimeMarkers(t, dir)

	tasksContent := "cycle: 3\ntasks:\n  - name: do-thing\n    status: done\n"
	tasksPath := filepath.Join(dir, ".tmux-cli", "tasks.yaml")
	require.NoError(t, os.WriteFile(tasksPath, []byte(tasksContent), 0o644))

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	_, statErr := os.Stat(tasksPath)
	assert.True(t, os.IsNotExist(statErr), "tasks.yaml should be archived (removed from original location)")

	archiveBaseDir := filepath.Join(dir, ".tmux-cli", "tasks")
	hourDirs, err := os.ReadDir(archiveBaseDir)
	require.NoError(t, err)
	require.NotEmpty(t, hourDirs, "archive hour directory should exist")

	archivedFiles, err := os.ReadDir(filepath.Join(archiveBaseDir, hourDirs[0].Name()))
	require.NoError(t, err)
	require.NotEmpty(t, archivedFiles, "archived tasks file should exist")

	archivedData, err := os.ReadFile(filepath.Join(archiveBaseDir, hourDirs[0].Name(), archivedFiles[0].Name()))
	require.NoError(t, err)
	assert.Equal(t, tasksContent, string(archivedData))

	assertAllRuntimeMarkersAbsent(t, dir)
}

func TestDeactivateOnCompletion_NoTasksYaml_NoError(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeAllRuntimeMarkers(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assertAllRuntimeMarkersAbsent(t, dir)
}

func TestDeactivateOnCompletion_SalvageGrace_NoArchive(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeAllRuntimeMarkers(t, dir)

	tasksPath := filepath.Join(dir, ".tmux-cli", "tasks.yaml")
	require.NoError(t, os.WriteFile(tasksPath, []byte("cycle: 1\n"), 0o644))

	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Status:     GoalFailed,
				FailedBy:   "validation-timeout",
				FinishedAt: freshRFC3339(),
			},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	for _, name := range []string{"taskvisor-current-goal", "taskvisor-current-cycle", "taskvisor-current-worktree", "taskvisor-active"} {
		_, err := os.Stat(filepath.Join(dir, ".tmux-cli", name))
		assert.False(t, os.IsNotExist(err), "%s should still be present (salvage grace)", name)
	}

	_, statErr := os.Stat(tasksPath)
	assert.False(t, os.IsNotExist(statErr), "tasks.yaml should still be present (salvage grace)")

	archiveDir := filepath.Join(dir, ".tmux-cli", "tasks")
	_, statErr = os.Stat(archiveDir)
	assert.True(t, os.IsNotExist(statErr), "no archive directory should be created during salvage grace")
}

func TestDeactivateOnCompletion_BlocksDepsBeforeShutdown(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalFailed},
			{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.Equal(t, GoalBlocked, gf.Goals[1].Status)
	assert.Equal(t, "deps_unsatisfied", gf.Goals[1].BlockedBy)
}

func TestDeactivateOnCompletion_AllResolved(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone},
			{ID: "goal-002", Status: GoalFailed},
			{ID: "goal-003", Status: GoalDone},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode)
	assert.Equal(t, GoalDone, gf.Goals[0].Status)
	assert.Equal(t, GoalFailed, gf.Goals[1].Status)
	assert.Equal(t, GoalDone, gf.Goals[2].Status)
}

func TestDeactivateOnCompletion_GeneratesCompletionReport(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.mode = modeActive
	d.session = testSession
	writeGuardFile(t, dir)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "first task", Status: GoalDone, Retries: 1, MaxRetries: 3},
			{ID: "goal-002", Description: "second task", Status: GoalFailed, Retries: 3, MaxRetries: 3},
			{ID: "goal-003", Description: "third task", Status: GoalBlocked, BlockedBy: "deps_unsatisfied", Retries: 0, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	err := d.deactivateOnCompletion(gf)
	require.NoError(t, err)

	reportPath := filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md")
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err, "completion report file should exist")

	report := string(data)
	assert.Contains(t, report, "# Taskvisor Completion Report")
	assert.Contains(t, report, "| Done   | 1     |")
	assert.Contains(t, report, "| Failed | 1     |")
	assert.Contains(t, report, "| Blocked| 1     |")
	assert.Contains(t, report, "| Total  | 3     |")

	assert.Contains(t, report, "### goal-001: first task")
	assert.Contains(t, report, "### goal-002: second task")
	assert.Contains(t, report, "### goal-003: third task")

	assert.Contains(t, report, "- **Status:** done")
	assert.Contains(t, report, "- **Status:** failed")
	assert.Contains(t, report, "- **Status:** blocked")

	assert.Contains(t, report, "- **Retries:** 1/3")
	assert.Contains(t, report, "- **Retries:** 3/3")
	assert.Contains(t, report, "- **Retries:** 0/3")
}

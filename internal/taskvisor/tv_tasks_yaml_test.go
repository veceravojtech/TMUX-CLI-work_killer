package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTasksYamlExists_TrueForPerGoalFile(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeGoalTasksYaml(t, dir, "g1", "status: ready\ncycle: 1\ntasks: []\n")
	assert.True(t, d.tasksYamlExists("g1"))
}

func TestTasksYamlExists_FalseWhenOnlyTopLevelExists(t *testing.T) {
	d, _, dir := setupDaemon(t)
	// Only the top-level planning-queue exists — the per-goal probe must NOT
	// fall back to it, so a missing per-goal file routes to full dispatch.
	writeTasksYaml(t, dir, "status: ready\ncycle: 1\ntasks: []\n")
	assert.False(t, d.tasksYamlExists("g1"), "must not cross-read the top-level planning-queue")
}

func TestResetTaskStatuses_RependsPerGoalFile(t *testing.T) {
	d, _, dir := setupDaemon(t)

	// Sentinel top-level planning-queue that must be left untouched.
	writeTasksYaml(t, dir, `status: ready
cycle: 1
tasks:
  - name: "top task"
    wid: execute-9
    status: done
    context: top.md
`)

	writeGoalTasksYaml(t, dir, "g1", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: ctx1.md
  - name: "task two"
    wid: execute-2
    status: in_progress
    context: ctx2.md
`)

	require.NoError(t, d.resetTaskStatuses("g1"))

	data, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "g1"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "status: done", "all task statuses reset from done")
	assert.NotContains(t, content, "status: in_progress", "all task statuses reset from in_progress")
	assert.Contains(t, content, "status: pending", "tasks reset to pending")
	assert.Contains(t, content, "status: ready", "file-level status set to ready")

	// Top-level planning-queue must NOT be mutated.
	topData, err := os.ReadFile(tasks.TasksFilePath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(topData), "status: done", "top-level planning-queue must be left untouched")
}

func TestInjectCorrections_ReadsPerGoalTasksFile(t *testing.T) {
	d, _, dir := setupDaemon(t)

	goal := &Goal{ID: "g1", Description: "test", Status: GoalRunning, CodeRetries: 2, MaxCodeRetries: 3}
	_, err := EnsureGoalDir(dir, "g1")
	require.NoError(t, err)

	// CurrentCycle(goal)=2 -> injectCorrections reads corrections/cycle-1.md.
	corrDir := filepath.Join(dir, ".tmux-cli", "goals", "g1", "corrections")
	require.NoError(t, os.MkdirAll(corrDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(corrDir, "cycle-1.md"), []byte("Remove Doctrine from quality-gates.md"), 0o644))

	// The per-goal tasks file names the context .md to amend.
	writeGoalTasksYaml(t, dir, "g1", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Original context")

	require.NoError(t, d.injectCorrections(goal))

	ctxData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "research", "ctx1.md"))
	require.NoError(t, err)
	ctxContent := string(ctxData)
	assert.Contains(t, ctxContent, "# Original context", "original context preserved")
	assert.Contains(t, ctxContent, "## Prior Corrections", "corrections section appended from per-goal tasks file")
	assert.Contains(t, ctxContent, "Remove Doctrine from quality-gates.md", "correction content present")
}

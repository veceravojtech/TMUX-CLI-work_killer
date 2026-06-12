package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTasksFilePath(t *testing.T) {
	p := TasksFilePath("/project")
	assert.Equal(t, filepath.Join("/project", ".tmux-cli", "tasks.yaml"), p)
}

func TestArchiveTasks(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	yamlBody := `status: ready
cycle: 3
tasks:
  - name: "archived task"
    wid: "execute-1"
    status: done
    context: "done.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlBody), 0o644))

	err := ArchiveTasks(root)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(root, ".tmux-cli", "tasks.yaml"))
	assert.True(t, os.IsNotExist(err), "tasks.yaml should be removed after archive")

	entries, err := os.ReadDir(filepath.Join(root, ".tmux-cli", "tasks"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "should have exactly one archive directory")

	files, err := os.ReadDir(filepath.Join(root, ".tmux-cli", "tasks", entries[0].Name()))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Regexp(t, `^tasks-\d{2}\.yaml$`, files[0].Name(), "archive filename should include minutes")

	archivedPath := filepath.Join(root, ".tmux-cli", "tasks", entries[0].Name(), files[0].Name())
	data, err := os.ReadFile(archivedPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "archived task")
}

func TestArchiveTasks_NoSameMinuteCollision(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for i := 0; i < 2; i++ {
		yamlBody := fmt.Sprintf(`status: ready
cycle: %d
tasks:
  - name: "task-%d"
    status: done
`, i+1, i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlBody), 0o644))
		require.NoError(t, ArchiveTasks(root))
	}

	entries, err := os.ReadDir(filepath.Join(root, ".tmux-cli", "tasks"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "same-minute archives go in same hour dir")

	files, err := os.ReadDir(filepath.Join(root, ".tmux-cli", "tasks", entries[0].Name()))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 1, "at least one archive file should exist")
}

func TestArchiveTasks_NoFile(t *testing.T) {
	root := t.TempDir()

	err := ArchiveTasks(root)
	assert.Error(t, err)
}

func TestTask_MarshalUnmarshal_AllFields(t *testing.T) {
	task := Task{
		Name:    "implement feature",
		Wid:     "execute-3",
		Status:  StatusInProgress,
		Context: ".tmux-cli/research/2026-05-11-20/task-feature.md",
	}

	data, err := yaml.Marshal(task)
	require.NoError(t, err)

	var loaded Task
	require.NoError(t, yaml.Unmarshal(data, &loaded))

	assert.Equal(t, task.Name, loaded.Name)
	assert.Equal(t, task.Wid, loaded.Wid)
	assert.Equal(t, task.Status, loaded.Status)
	assert.Equal(t, task.Context, loaded.Context)

	yamlStr := string(data)
	assert.Contains(t, yamlStr, "name:")
	assert.Contains(t, yamlStr, "wid:")
	assert.Contains(t, yamlStr, "status:")
	assert.Contains(t, yamlStr, "context:")
	assert.NotContains(t, yamlStr, "context_file:")
}

func TestTask_MarshalUnmarshal_EmptyWid(t *testing.T) {
	task := Task{
		Name:    "task without wid",
		Status:  StatusPending,
		Context: "some/path.md",
	}

	data, err := yaml.Marshal(task)
	require.NoError(t, err)

	var loaded Task
	require.NoError(t, yaml.Unmarshal(data, &loaded))

	assert.Equal(t, "", loaded.Wid)
	assert.Equal(t, task.Name, loaded.Name)
}

func TestTask_DependsOnField(t *testing.T) {
	task := Task{
		Name:      "implement feature",
		Wid:       "execute-1",
		Status:    StatusPending,
		Context:   "ctx.md",
		DependsOn: []string{"execute-2", "execute-3"},
	}

	data, err := yaml.Marshal(task)
	require.NoError(t, err)

	yamlStr := string(data)
	assert.Contains(t, yamlStr, "depends_on:")
	assert.Contains(t, yamlStr, "execute-2")
	assert.Contains(t, yamlStr, "execute-3")

	var loaded Task
	require.NoError(t, yaml.Unmarshal(data, &loaded))
	assert.Equal(t, task.DependsOn, loaded.DependsOn)
}

func TestTask_DependsOnOmitempty(t *testing.T) {
	taskNil := Task{Name: "nil deps", Status: StatusPending}
	dataNil, err := yaml.Marshal(taskNil)
	require.NoError(t, err)
	assert.NotContains(t, string(dataNil), "depends_on")

	taskEmpty := Task{Name: "empty deps", Status: StatusPending, DependsOn: []string{}}
	dataEmpty, err := yaml.Marshal(taskEmpty)
	require.NoError(t, err)
	assert.NotContains(t, string(dataEmpty), "depends_on")
}

func TestValidateTasks_Clean(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "short task name"
    wid: "execute-1"
    status: pending
    context: ".tmux-cli/research/2026-05-12-00/task-auth.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	assert.Empty(t, errs)
}

func TestValidateTasks_ExtraFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "task with scope"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    scope: |
      This is inline scope that should be in the context file.
  - name: "task with supporting_context"
    wid: "execute-2"
    status: pending
    context: "ctx2.md"
    supporting_context: |
      This is inline context that should be in the context file.
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 2)
	assert.Contains(t, errs[0], "execute-1")
	assert.Contains(t, errs[0], "scope")
	assert.Contains(t, errs[0], "context .md file")
	assert.Contains(t, errs[1], "execute-2")
	assert.Contains(t, errs[1], "supporting_context")
}

func TestValidateTasks_NameTooLong(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	longName := ""
	for i := 0; i < 101; i++ {
		longName += "x"
	}

	yamlData := fmt.Sprintf(`status: ready
cycle: 1
tasks:
  - name: "%s"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`, longName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "name exceeds 100 chars")
}

func TestValidateTasks_MissingContext(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "no context"
    wid: "execute-1"
    status: pending
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "missing context")
}

func TestValidateTasks_MultipleErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	longName := ""
	for i := 0; i < 120; i++ {
		longName += "a"
	}

	yamlData := fmt.Sprintf(`status: ready
cycle: 1
tasks:
  - name: "%s"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    scope: "inline scope"
    supporting_context: "inline context"
`, longName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	assert.Len(t, errs, 3)
}

func TestValidateTasks_FileNotFound(t *testing.T) {
	errs := ValidateTasksFile("/nonexistent/tasks.yaml")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "cannot read")
}

func TestValidateTasks_UnknownExtraField(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    description: "long description that should be in the context file"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "description")
	assert.Contains(t, errs[0], "context .md file")
}

func TestValidateTasksFile_DependsOnUnknownWid(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "task a"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    depends_on:
      - execute-99
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "depends_on references unknown wid")
	assert.Contains(t, errs[0], "execute-99")
}

func TestValidateTasksFile_DependsOnValidRefs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "task a"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
    depends_on:
      - execute-2
  - name: "task b"
    wid: "execute-2"
    status: pending
    context: "ctx2.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	assert.Empty(t, errs)
}

func TestValidateTasksFile_InvalidTaskStatus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "task a"
    wid: "execute-1"
    status: running
    context: "ctx.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "invalid status")
	assert.Contains(t, errs[0], "running")
}

func TestValidateTasksFile_ValidTaskStatuses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "pending task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
  - name: "in progress task"
    wid: "execute-2"
    status: in_progress
    context: "ctx2.md"
  - name: "done task"
    wid: "execute-3"
    status: done
    context: "ctx3.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	assert.Empty(t, errs)
}

func TestValidateTasksFile_InvalidWidFormat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 1
tasks:
  - name: "bad wid"
    wid: "worker-1"
    status: pending
    context: "ctx.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "invalid wid format")
	assert.Contains(t, errs[0], "worker-1")
}

func TestValidateTasksFile_InvalidWidFormatVariants(t *testing.T) {
	tests := []struct {
		name  string
		wid   string
		valid bool
	}{
		{"valid execute-1", "execute-1", true},
		{"valid execute-99", "execute-99", true},
		{"missing number", "execute-", false},
		{"no dash", "execute1", false},
		{"uppercase", "Execute-1", false},
		{"extra suffix", "execute-1-abc", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".tmux-cli")
			require.NoError(t, os.MkdirAll(dir, 0o755))

			yamlData := fmt.Sprintf(`status: ready
cycle: 1
tasks:
  - name: "task"
    wid: "%s"
    status: pending
    context: "ctx.md"
`, tt.wid)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

			errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
			if tt.valid {
				assert.Empty(t, errs, "wid %q should be valid", tt.wid)
			} else {
				require.NotEmpty(t, errs, "wid %q should be invalid", tt.wid)
				assert.Contains(t, errs[0], "invalid wid format")
			}
		})
	}
}

func TestValidateTasksFile_InvalidFileStatus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: active
cycle: 1
tasks:
  - name: "task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "invalid file status")
	assert.Contains(t, errs[0], "active")
}

func TestValidateTasksFile_ValidFileStatuses(t *testing.T) {
	for _, status := range []string{"planning", "ready"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".tmux-cli")
			require.NoError(t, os.MkdirAll(dir, 0o755))

			yamlData := fmt.Sprintf(`status: %s
cycle: 1
tasks:
  - name: "task"
    wid: "execute-1"
    status: pending
    context: "ctx.md"
`, status)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

			errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
			assert.Empty(t, errs)
		})
	}
}

func TestValidateTasksFile_MultipleNewErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: bogus
cycle: 1
tasks:
  - name: "task"
    wid: "worker-1"
    status: running
    context: "ctx.md"
    depends_on:
      - execute-999
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	errs := ValidateTasksFile(filepath.Join(dir, "tasks.yaml"))
	assert.GreaterOrEqual(t, len(errs), 4)
}

// --- E1-0d: per-goal fan-out tasks.yaml relocation ---

func TestGoalTasksFilePath_ReturnsGoalScopedPath(t *testing.T) {
	p := GoalTasksFilePath("/project", "goal-020")
	assert.Equal(t, filepath.Join("/project", ".tmux-cli", "goals", "goal-020", "tasks.yaml"), p)
}

func TestTasksFilePath_TopLevelUnchanged(t *testing.T) {
	// The planning-queue path must remain byte-for-byte stable.
	p := TasksFilePath("/project")
	assert.Equal(t, filepath.Join("/project", ".tmux-cli", "tasks.yaml"), p)
}

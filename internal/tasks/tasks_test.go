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

func TestLoadTasks_FileMissing_ReturnsNil(t *testing.T) {
	root := t.TempDir()

	tf, err := LoadTasks(root)
	require.NoError(t, err)
	assert.Nil(t, tf)
}

func TestLoadTasks_ValidFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	yamlData := `status: ready
cycle: 2
tasks:
  - name: "implement auth"
    wid: "execute-1"
    status: pending
    context: ".tmux-cli/research/auth.md"
  - name: "add logging"
    wid: "execute-2"
    status: done
    context: ".tmux-cli/research/logging.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yamlData), 0o644))

	tf, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, tf)

	assert.Equal(t, FileStatusReady, tf.Status)
	assert.Equal(t, 2, tf.Cycle)
	require.Len(t, tf.Tasks, 2)
	assert.Equal(t, "implement auth", tf.Tasks[0].Name)
	assert.Equal(t, "execute-1", tf.Tasks[0].Wid)
	assert.Equal(t, StatusPending, tf.Tasks[0].Status)
	assert.Equal(t, ".tmux-cli/research/auth.md", tf.Tasks[0].Context)
	assert.Equal(t, "add logging", tf.Tasks[1].Name)
	assert.Equal(t, "execute-2", tf.Tasks[1].Wid)
	assert.Equal(t, StatusDone, tf.Tasks[1].Status)
	assert.Equal(t, ".tmux-cli/research/logging.md", tf.Tasks[1].Context)
}

func TestLoadTasks_InvalidYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte("{{{{bad yaml"), 0o644))

	_, err := LoadTasks(root)
	assert.Error(t, err)
}

func TestSaveTasks_RoundTrip(t *testing.T) {
	root := t.TempDir()

	original := &TasksFile{
		Status: FileStatusReady,
		Cycle:  1,
		Tasks: []Task{
			{Name: "task one", Wid: "execute-1", Status: StatusPending, Context: "path/one.md"},
			{Name: "task two", Wid: "execute-2", Status: StatusInProgress, Context: "path/two.md"},
		},
	}

	err := SaveTasks(root, original)
	require.NoError(t, err)

	loaded, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, FileStatusReady, loaded.Status)
	assert.Equal(t, original.Cycle, loaded.Cycle)
	require.Len(t, loaded.Tasks, 2)
	assert.Equal(t, "task one", loaded.Tasks[0].Name)
	assert.Equal(t, "execute-1", loaded.Tasks[0].Wid)
	assert.Equal(t, StatusPending, loaded.Tasks[0].Status)
	assert.Equal(t, "path/one.md", loaded.Tasks[0].Context)
	assert.Equal(t, "task two", loaded.Tasks[1].Name)
	assert.Equal(t, "execute-2", loaded.Tasks[1].Wid)
	assert.Equal(t, StatusInProgress, loaded.Tasks[1].Status)
	assert.Equal(t, "path/two.md", loaded.Tasks[1].Context)
}

func TestSaveTasks_CreatesDirectories(t *testing.T) {
	root := t.TempDir()

	err := SaveTasks(root, &TasksFile{Cycle: 1})
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(root, ".tmux-cli", "tasks.yaml"))
	assert.NoError(t, err)
}

func TestArchiveTasks(t *testing.T) {
	root := t.TempDir()

	tf := &TasksFile{
		Cycle: 3,
		Tasks: []Task{
			{Name: "archived task", Wid: "execute-1", Status: StatusDone, Context: "done.md"},
		},
	}
	require.NoError(t, SaveTasks(root, tf))

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

	for i := 0; i < 2; i++ {
		tf := &TasksFile{
			Cycle: i + 1,
			Tasks: []Task{
				{Name: fmt.Sprintf("task-%d", i), Status: StatusDone},
			},
		}
		require.NoError(t, SaveTasks(root, tf))
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

func TestIsPlanning(t *testing.T) {
	tests := []struct {
		name   string
		status string
		expect bool
	}{
		{"planning", FileStatusPlanning, true},
		{"ready", FileStatusReady, false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := &TasksFile{Status: tt.status}
			assert.Equal(t, tt.expect, tf.IsPlanning())
		})
	}
}

func TestPendingTasks(t *testing.T) {
	tf := &TasksFile{
		Tasks: []Task{
			{Name: "a", Status: StatusPending},
			{Name: "b", Status: StatusInProgress},
			{Name: "c", Status: StatusDone},
			{Name: "d", Status: StatusPending},
		},
	}

	pending := tf.PendingTasks()
	require.Len(t, pending, 2)
	assert.Equal(t, "a", pending[0].Name)
	assert.Equal(t, "d", pending[1].Name)
}

func TestPendingTasks_Empty(t *testing.T) {
	tf := &TasksFile{
		Tasks: []Task{
			{Name: "a", Status: StatusDone},
		},
	}

	pending := tf.PendingTasks()
	assert.Empty(t, pending)
}

func TestHasUnfinished(t *testing.T) {
	tests := []struct {
		name   string
		tasks  []Task
		expect bool
	}{
		{"all done", []Task{{Status: StatusDone}}, false},
		{"has pending", []Task{{Status: StatusPending}, {Status: StatusDone}}, true},
		{"has in_progress", []Task{{Status: StatusInProgress}, {Status: StatusDone}}, true},
		{"empty", []Task{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := &TasksFile{Tasks: tt.tasks}
			assert.Equal(t, tt.expect, tf.HasUnfinished())
		})
	}
}

func TestMarkDone_Found(t *testing.T) {
	tf := &TasksFile{
		Tasks: []Task{
			{Name: "first", Status: StatusPending},
			{Name: "second", Status: StatusInProgress},
		},
	}

	ok := tf.MarkDone("second")
	assert.True(t, ok)
	assert.Equal(t, StatusDone, tf.Tasks[1].Status)
	assert.Equal(t, StatusPending, tf.Tasks[0].Status)
}

func TestMarkDone_NotFound(t *testing.T) {
	tf := &TasksFile{
		Tasks: []Task{
			{Name: "first", Status: StatusPending},
		},
	}

	ok := tf.MarkDone("nonexistent")
	assert.False(t, ok)
	assert.Equal(t, StatusPending, tf.Tasks[0].Status)
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

func TestTasksFile_RoundTrip_LeanFormat(t *testing.T) {
	root := t.TempDir()

	original := &TasksFile{
		Status: FileStatusReady,
		Cycle:  3,
		Tasks: []Task{
			{
				Name:    "full task",
				Wid:     "execute-1",
				Status:  StatusPending,
				Context: "path/ctx.md",
			},
			{
				Name:    "minimal task",
				Wid:     "execute-2",
				Status:  StatusDone,
				Context: "path/other.md",
			},
		},
	}

	require.NoError(t, SaveTasks(root, original))

	loaded, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Tasks, 2)

	assert.Equal(t, "full task", loaded.Tasks[0].Name)
	assert.Equal(t, "path/ctx.md", loaded.Tasks[0].Context)

	data, err := os.ReadFile(TasksFilePath(root))
	require.NoError(t, err)
	yamlStr := string(data)
	assert.NotContains(t, yamlStr, "scope:")
	assert.NotContains(t, yamlStr, "supporting_context:")
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

func TestTasksFile_RoundTrip_WithDependsOn(t *testing.T) {
	root := t.TempDir()

	original := &TasksFile{
		Status: FileStatusReady,
		Cycle:  1,
		Tasks: []Task{
			{
				Name:      "task with deps",
				Wid:       "execute-1",
				Status:    StatusPending,
				Context:   "ctx.md",
				DependsOn: []string{"execute-2", "execute-3"},
			},
			{
				Name:    "task without deps",
				Wid:     "execute-2",
				Status:  StatusPending,
				Context: "ctx2.md",
			},
		},
	}

	require.NoError(t, SaveTasks(root, original))

	loaded, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Tasks, 2)

	assert.Equal(t, []string{"execute-2", "execute-3"}, loaded.Tasks[0].DependsOn)
	assert.Nil(t, loaded.Tasks[1].DependsOn)

	data, err := os.ReadFile(TasksFilePath(root))
	require.NoError(t, err)
	yamlStr := string(data)
	assert.Contains(t, yamlStr, "depends_on:")
	assert.Contains(t, yamlStr, "execute-2")
}

func TestTasksFile_BackwardsCompatibility_NoDependsOn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	oldYAML := `status: ready
cycle: 1
tasks:
  - name: "old task"
    wid: "execute-1"
    status: pending
    context: "old/path.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(oldYAML), 0o644))

	tf, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, tf)
	require.Len(t, tf.Tasks, 1)

	assert.Equal(t, "old task", tf.Tasks[0].Name)
	assert.Nil(t, tf.Tasks[0].DependsOn)
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

func TestCreateContextFile(t *testing.T) {
	root := t.TempDir()
	researchDir := "2026-05-11-20"
	slug := "task-auth-flow"
	taskName := "Implement auth flow"

	relPath, err := CreateContextFile(root, researchDir, slug, taskName)
	require.NoError(t, err)

	expectedRel := filepath.Join(".tmux-cli", "research", researchDir, slug+".md")
	assert.Equal(t, expectedRel, relPath)

	absPath := filepath.Join(root, relPath)
	data, err := os.ReadFile(absPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "# Task: Implement auth flow")
	assert.Contains(t, content, "## Problem")
	assert.Contains(t, content, "## Solution")
	assert.Contains(t, content, "## Files to touch")
}

func TestCreateContextFile_CreatesDirectories(t *testing.T) {
	root := t.TempDir()

	relPath, err := CreateContextFile(root, "2026-01-01-00", "task-test", "Test task")
	require.NoError(t, err)

	absPath := filepath.Join(root, relPath)
	_, err = os.Stat(absPath)
	assert.NoError(t, err)
}

func TestCreateContextFile_DoesNotOverwriteExisting(t *testing.T) {
	root := t.TempDir()
	researchDir := "2026-05-11-20"
	slug := "task-existing"

	dir := filepath.Join(root, ".tmux-cli", "research", researchDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	existing := filepath.Join(dir, slug+".md")
	require.NoError(t, os.WriteFile(existing, []byte("existing content"), 0o644))

	relPath, err := CreateContextFile(root, researchDir, slug, "New task")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(".tmux-cli", "research", researchDir, slug+".md"), relPath)

	data, err := os.ReadFile(filepath.Join(root, relPath))
	require.NoError(t, err)
	assert.Equal(t, "existing content", string(data))
}

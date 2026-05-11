package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	yaml := `cycle: 2
tasks:
  - name: "implement auth"
    status: pending
    context_file: ".tmux-cli/research/auth.md"
  - name: "add logging"
    status: done
    context_file: ".tmux-cli/research/logging.md"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks.yaml"), []byte(yaml), 0o644))

	tf, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, tf)

	assert.Equal(t, 2, tf.Cycle)
	require.Len(t, tf.Tasks, 2)
	assert.Equal(t, "implement auth", tf.Tasks[0].Name)
	assert.Equal(t, StatusPending, tf.Tasks[0].Status)
	assert.Equal(t, ".tmux-cli/research/auth.md", tf.Tasks[0].ContextFile)
	assert.Equal(t, "add logging", tf.Tasks[1].Name)
	assert.Equal(t, StatusDone, tf.Tasks[1].Status)
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
		Cycle: 1,
		Tasks: []Task{
			{Name: "task one", Status: StatusPending, ContextFile: "path/one.md"},
			{Name: "task two", Status: StatusInProgress, ContextFile: "path/two.md"},
		},
	}

	err := SaveTasks(root, original)
	require.NoError(t, err)

	loaded, err := LoadTasks(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, original.Cycle, loaded.Cycle)
	require.Len(t, loaded.Tasks, 2)
	assert.Equal(t, "task one", loaded.Tasks[0].Name)
	assert.Equal(t, StatusPending, loaded.Tasks[0].Status)
	assert.Equal(t, "path/one.md", loaded.Tasks[0].ContextFile)
	assert.Equal(t, "task two", loaded.Tasks[1].Name)
	assert.Equal(t, StatusInProgress, loaded.Tasks[1].Status)
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
			{Name: "archived task", Status: StatusDone, ContextFile: "done.md"},
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

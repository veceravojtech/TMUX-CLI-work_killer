package tasks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
)

type Task struct {
	Name        string `yaml:"name"`
	Status      string `yaml:"status"`
	ContextFile string `yaml:"context_file"`
}

type TasksFile struct {
	Cycle int    `yaml:"cycle"`
	Tasks []Task `yaml:"tasks"`
}

func TasksFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "tasks.yaml")
}

func LoadTasks(projectRoot string) (*TasksFile, error) {
	data, err := os.ReadFile(TasksFilePath(projectRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var tf TasksFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

func SaveTasks(projectRoot string, tf *TasksFile) error {
	p := TasksFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(tf)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func ArchiveTasks(projectRoot string) error {
	src := TasksFilePath(projectRoot)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("no tasks file to archive: %w", err)
	}

	now := time.Now()
	hourDir := now.Format("2006-01-02-15")
	archiveDir := filepath.Join(projectRoot, ".tmux-cli", "tasks", hourDir)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return err
	}

	filename := fmt.Sprintf("tasks-%s.yaml", now.Format("04"))
	dst := filepath.Join(archiveDir, filename)
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}

func (tf *TasksFile) PendingTasks() []Task {
	var out []Task
	for _, t := range tf.Tasks {
		if t.Status == StatusPending {
			out = append(out, t)
		}
	}
	return out
}

func (tf *TasksFile) HasUnfinished() bool {
	for _, t := range tf.Tasks {
		if t.Status == StatusPending || t.Status == StatusInProgress {
			return true
		}
	}
	return false
}

func (tf *TasksFile) MarkDone(name string) bool {
	for i := range tf.Tasks {
		if tf.Tasks[i].Name == name {
			tf.Tasks[i].Status = StatusDone
			return true
		}
	}
	return false
}

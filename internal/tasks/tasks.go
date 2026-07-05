package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"

	FileStatusPlanning = "planning"
	FileStatusReady    = "ready"
)

type Task struct {
	Name      string   `yaml:"name"`
	Wid       string   `yaml:"wid"`
	Status    string   `yaml:"status"`
	Context   string   `yaml:"context"`
	DependsOn []string `yaml:"depends_on,omitempty"`
}

type TasksFile struct {
	Status string `yaml:"status"`
	Cycle  int    `yaml:"cycle"`
	Tasks  []Task `yaml:"tasks"`
}

// TasksFilePath is the TOP-LEVEL planning-queue path produced by /tmux:plan and
// consumed by the standalone supervisor (PRE_PLANNED). It is NEVER read by the
// taskvisor daemon in goal mode — keep it byte-for-byte stable.
func TasksFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "tasks.yaml")
}

// GoalTasksFilePath is the per-goal supervisor fan-out path used in GOAL_MODE.
// It isolates each goal's fan-out plan so concurrent goals (MaxGoals>1) never
// clobber each other's tasks/retry state. Distinct from the top-level
// planning-queue (TasksFilePath).
func GoalTasksFilePath(projectRoot, goalID string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals", goalID, "tasks.yaml")
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

var allowedTaskKeys = map[string]bool{
	"name":       true,
	"wid":        true,
	"status":     true,
	"context":    true,
	"depends_on": true,
}

var widPattern = regexp.MustCompile(`^execute-\d+(?:-\d+)?$`)

var validTaskStatuses = map[string]bool{
	StatusPending:    true,
	StatusInProgress: true,
	StatusDone:       true,
}

var validFileStatuses = map[string]bool{
	FileStatusPlanning: true,
	FileStatusReady:    true,
}

func ValidateTasksFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read %s: %v", path, err)}
	}

	var raw struct {
		Status string      `yaml:"status"`
		Tasks  []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return []string{fmt.Sprintf("invalid YAML: %v", err)}
	}

	var errs []string

	if !validFileStatuses[raw.Status] {
		errs = append(errs, fmt.Sprintf("invalid file status %q (must be planning or ready)", raw.Status))
	}

	wids := make(map[string]bool)
	for _, node := range raw.Tasks {
		if node.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "wid" {
				wids[node.Content[i+1].Value] = true
			}
		}
	}

	for _, node := range raw.Tasks {
		if node.Kind != yaml.MappingNode {
			continue
		}
		var name, wid, status, context string
		var dependsOn []string
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			valNode := node.Content[i+1]
			switch key {
			case "name":
				name = valNode.Value
			case "wid":
				wid = valNode.Value
			case "status":
				status = valNode.Value
			case "context":
				context = valNode.Value
			case "depends_on":
				if valNode.Kind == yaml.SequenceNode {
					for _, item := range valNode.Content {
						dependsOn = append(dependsOn, item.Value)
					}
				}
			}
		}
		id := wid
		if id == "" {
			id = name
		}

		if len(name) > 100 {
			errs = append(errs, fmt.Sprintf("task %s: name exceeds 100 chars (%d) — shorten it, put detail in the context .md file", id, len(name)))
		}
		if context == "" {
			errs = append(errs, fmt.Sprintf("task %s: missing context field — every task must point to a context .md file", id))
		}
		if !validTaskStatuses[status] {
			errs = append(errs, fmt.Sprintf("task %q: invalid status %q (must be pending, in_progress, or done)", id, status))
		}
		if !widPattern.MatchString(wid) {
			errs = append(errs, fmt.Sprintf("task %q: invalid wid format %q (must be execute-N or execute-<ns>-N)", id, wid))
		}
		for _, dep := range dependsOn {
			if !wids[dep] {
				errs = append(errs, fmt.Sprintf("task %q: depends_on references unknown wid %q", id, dep))
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if !allowedTaskKeys[key] {
				errs = append(errs, fmt.Sprintf("task %s: extra field '%s' — remove it and move content into the context .md file", id, key))
			}
		}
	}
	return errs
}

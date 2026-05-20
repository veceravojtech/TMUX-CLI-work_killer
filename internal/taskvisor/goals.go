package taskvisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	GoalPending = "pending"
	GoalRunning = "running"
	GoalDone    = "done"
	GoalFailed  = "failed"
)

type Goal struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Acceptance  []string `yaml:"acceptance,omitempty"`
	Validate    []string `yaml:"validate,omitempty"`
	Status      string   `yaml:"status"`
	Retries     int      `yaml:"retries"`
	MaxRetries  int      `yaml:"max_retries"`
	StartedAt   string   `yaml:"started_at,omitempty"`
	FinishedAt  string   `yaml:"finished_at,omitempty"`
}

type GoalsFile struct {
	CurrentGoal string `yaml:"current_goal"`
	Goals       []Goal `yaml:"goals"`
}

func GoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals.yaml")
}

func LoadGoals(projectRoot string) (*GoalsFile, error) {
	data, err := os.ReadFile(GoalsFilePath(projectRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var gf GoalsFile
	if err := yaml.Unmarshal(data, &gf); err != nil {
		return nil, err
	}
	return &gf, nil
}

func SaveGoals(projectRoot string, gf *GoalsFile) error {
	p := GoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(gf)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func parseGoalIDNumber(id string) (int, error) {
	if !strings.HasPrefix(id, "goal-") {
		return 0, fmt.Errorf("invalid goal ID format: %s", id)
	}
	return strconv.Atoi(strings.TrimPrefix(id, "goal-"))
}

func NextGoalID(goals []Goal) string {
	max := 0
	for _, g := range goals {
		n, err := parseGoalIDNumber(g.ID)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("goal-%03d", max+1)
}

func EnsureGoalDir(projectRoot, goalID string) (string, error) {
	dir := filepath.Join(projectRoot, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(filepath.Join(dir, "corrections"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteGoalMD(goalDir, description string, acceptance, validate []string, context, notInScope string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", description)

	b.WriteString("\n## Acceptance Criteria\n\n")
	for _, a := range acceptance {
		fmt.Fprintf(&b, "- %s\n", a)
	}

	if len(validate) > 0 {
		b.WriteString("\n## Validation Rules\n\n")
		for _, v := range validate {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	}

	if context != "" {
		fmt.Fprintf(&b, "\n## Context\n\n%s\n", context)
	}

	if notInScope != "" {
		fmt.Fprintf(&b, "\n## Not In Scope\n\n%s\n", notInScope)
	}

	return atomicWrite(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0o644)
}

func (gf *GoalsFile) GoalByID(id string) (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			return &gf.Goals[i], true
		}
	}
	return nil, false
}

func (gf *GoalsFile) NextPendingGoal() (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalPending {
			return &gf.Goals[i], true
		}
	}
	return nil, false
}

func (gf *GoalsFile) SetStatus(id, status string) bool {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			gf.Goals[i].Status = status
			return true
		}
	}
	return false
}

func (gf *GoalsFile) DeleteGoal(id string) (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			removed := gf.Goals[i]
			gf.Goals = append(gf.Goals[:i], gf.Goals[i+1:]...)
			if gf.CurrentGoal == id {
				gf.CurrentGoal = ""
			}
			return &removed, true
		}
	}
	return nil, false
}

func (gf *GoalsFile) ResetGoal(id string) bool {
	g, ok := gf.GoalByID(id)
	if !ok || g.Status != GoalFailed {
		return false
	}
	g.Status = GoalPending
	g.Retries = 0
	g.FinishedAt = ""
	return true
}

func (gf *GoalsFile) SkipGoal(id string) bool {
	g, ok := gf.GoalByID(id)
	if !ok || g.Status != GoalRunning {
		return false
	}
	g.Status = GoalDone
	g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return true
}

func (g *Goal) IncrementRetries() int {
	g.Retries++
	return g.Retries
}

package taskvisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

func (g *Goal) IncrementRetries() int {
	g.Retries++
	return g.Retries
}

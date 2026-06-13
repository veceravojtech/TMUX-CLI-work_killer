package taskvisor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// taskgoals.go — the first Go reader/writer of .tmux-cli/task-goals.yaml, the
// local task↔goal ledger that /tmux:task-list consume appends to and reconcile
// rewrites. Schema is byte-compatible with the XML command's ledger:
// `mappings: [{task_id, goal_id, title, claimed_at}]`.
//
// LOCK DISCIPLINE (the trap): LoadTaskGoals/SaveTaskGoals are deliberately
// LOCK-FREE, mirroring SaveGoals/LoadGoals (goals.go). The daemon `tick` — and
// therefore every terminal transition that calls the resolver — already runs
// inside `poll`'s withGoalsLock (daemon.go). Re-acquiring the same flock from a
// second fd in-process deadlocks. The atomicWrite rename still gives crash-safe
// single-writer durability under that held lock.

// TaskGoalMapping is one task↔goal ledger entry. The yaml tags match the schema
// the /tmux:task-list XML command writes, so reconcile reads daemon-written
// files byte-compatibly.
type TaskGoalMapping struct {
	TaskID    string `yaml:"task_id"`
	GoalID    string `yaml:"goal_id"`
	Title     string `yaml:"title"`
	ClaimedAt string `yaml:"claimed_at"`
}

// TaskGoalsFile is the root of task-goals.yaml.
type TaskGoalsFile struct {
	Mappings []TaskGoalMapping `yaml:"mappings"`
}

// TaskGoalsFilePath is the ledger path, mirroring GoalsFilePath.
func TaskGoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "task-goals.yaml")
}

// LoadTaskGoals reads the ledger. An absent file is not an error: it returns
// (nil, nil), matching LoadGoals — absence means nothing to resolve. No lock.
func LoadTaskGoals(projectRoot string) (*TaskGoalsFile, error) {
	data, err := os.ReadFile(TaskGoalsFilePath(projectRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var tgf TaskGoalsFile
	if err := yaml.Unmarshal(data, &tgf); err != nil {
		return nil, err
	}
	return &tgf, nil
}

// SaveTaskGoals atomically rewrites the ledger, mirroring SaveGoals. No lock.
func SaveTaskGoals(projectRoot string, tgf *TaskGoalsFile) error {
	p := TaskGoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(tgf)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

// indexOf returns the index of the mapping for goalID, or -1 when none exists
// (including a nil receiver).
func (tgf *TaskGoalsFile) indexOf(goalID string) int {
	if tgf == nil {
		return -1
	}
	for i := range tgf.Mappings {
		if tgf.Mappings[i].GoalID == goalID {
			return i
		}
	}
	return -1
}

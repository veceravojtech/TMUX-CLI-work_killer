package taskvisor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// recurring.go — the reader/writer of .tmux-cli/recurring.yaml, the persisted
// state model for the daemon-driven recurring supervisor task. ONE shared
// RecurringFile/RecurringTask/RecurringCycle type is imported by BOTH the
// daemon driver and the MCP recurring-* tools (ADR-1 Option 1), so there is no
// tv* mirror struct to drift — a deliberate departure from the goals.go↔tvGoal
// dual-struct hazard.
//
// LOCK DISCIPLINE: LoadRecurring/SaveRecurring are deliberately LOCK-FREE,
// mirroring LoadTaskGoals/SaveTaskGoals (taskgoals.go) and LoadGoals/SaveGoals
// (goals.go). The daemon tick already holds the goals flock; a second in-process
// acquire deadlocks. atomicWrite's temp+rename still gives crash-safe durability
// under that held lock.

// Recurring task lifecycle statuses (mirror GoalPending-style consts in goals.go).
const (
	RecurringActive  = "active"
	RecurringStopped = "stopped"
	RecurringDone    = "done"
)

// RecurringCycle is one per-cycle record. Timestamps are RFC3339 strings,
// mirroring Goal.StartedAt/FinishedAt — no time import needed.
type RecurringCycle struct {
	Index            int    `yaml:"index,omitempty"`
	Phase            string `yaml:"phase,omitempty"`
	DispatchedAt     string `yaml:"dispatched_at,omitempty"`
	LastActivityAt   string `yaml:"last_activity_at,omitempty"`
	LastProgressHash string `yaml:"last_progress_hash,omitempty"`
	Outcome          string `yaml:"outcome,omitempty"`
}

// RecurringTask is the persisted recurring-task model. Required scalars
// (id/prompt/total_cycles/completed_cycles/status) OMIT omitempty so a zeroed
// completed_cycles still serializes visibly (mirror Goal.Retries, goals.go).
type RecurringTask struct {
	ID              string           `yaml:"id"`
	Prompt          string           `yaml:"prompt"`
	TargetWindow    string           `yaml:"target_window,omitempty"`
	TotalCycles     int              `yaml:"total_cycles"`
	CompletedCycles int              `yaml:"completed_cycles"`
	Status          string           `yaml:"status"`
	ClearBetween    bool             `yaml:"clear_between,omitempty"`
	IdleGraceSec    int              `yaml:"idle_grace_sec,omitempty"`
	BootMinSec      int              `yaml:"boot_min_sec,omitempty"`
	CooldownSec     int              `yaml:"cooldown_sec,omitempty"`
	MaxCycleWallSec int              `yaml:"max_cycle_wall_sec,omitempty"`
	CreatedAt       string           `yaml:"created_at,omitempty"`
	CurrentCycle    RecurringCycle   `yaml:"current_cycle,omitempty"`
	History         []RecurringCycle `yaml:"history,omitempty"`
}

// RecurringFile is the root of recurring.yaml — the SINGLE shared type. A nil
// Task pointer encodes "no active recurring task", complementing the (nil,nil)
// absent-file load.
type RecurringFile struct {
	Task *RecurringTask `yaml:"task,omitempty"`
}

// RecurringFilePath is the recurring-state path, mirroring TaskGoalsFilePath.
func RecurringFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "recurring.yaml")
}

// LoadRecurring reads recurring.yaml. An absent file is not an error: it returns
// (nil, nil), matching LoadTaskGoals. No lock.
func LoadRecurring(projectRoot string) (*RecurringFile, error) {
	data, err := os.ReadFile(RecurringFilePath(projectRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var rf RecurringFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, err
	}
	return &rf, nil
}

// SaveRecurring atomically rewrites recurring.yaml, mirroring SaveTaskGoals. No lock.
func SaveRecurring(projectRoot string, rf *RecurringFile) error {
	p := RecurringFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(rf)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

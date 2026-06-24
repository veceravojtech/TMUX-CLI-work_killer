package mcp

// tools_recurring.go — core methods for the recurring-supervisor MCP tool trio
// (recurring-create / recurring-status / recurring-stop). Each method validates,
// then atomically persists via the shared taskvisor.RecurringFile layer
// (goal-001), and returns immediately. These methods MUST NOT call the tmux
// executor, SendMessage, or any tmux send — the daemon driver (goal-004+) owns
// dispatching; here we only read/write recurring.yaml and the
// .tmux-cli/recurring-active marker.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/console/tmux-cli/internal/taskvisor"
)

// recurringID is the fixed id for the single-active recurring task (v1 supports
// one active task at a time, so a constant id is sufficient).
const recurringID = "recurring-001"

// recurringMarkerPath returns the .tmux-cli/recurring-active marker path.
func recurringMarkerPath(workingDir string) string {
	return filepath.Join(workingDir, ".tmux-cli", "recurring-active")
}

// RecurringCreate validates and persists a new active recurring task. It rejects
// an empty prompt or Cycles<1 (ErrInvalidInput), and refuses to overwrite an
// already-active task. On success it writes recurring.yaml and the
// .tmux-cli/recurring-active marker, then returns {Created:true}.
func (s *Server) RecurringCreate(input RecurringCreateInput) (*RecurringCreateOutput, error) {
	if input.Prompt == "" {
		return nil, fmt.Errorf("%w: prompt must not be empty", ErrInvalidInput)
	}
	if input.Cycles < 1 {
		return nil, fmt.Errorf("%w: cycles must be >= 1", ErrInvalidInput)
	}

	rf, err := taskvisor.LoadRecurring(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load recurring state: %v", ErrInvalidInput, err)
	}
	if rf != nil && rf.Task != nil && rf.Task.Status == taskvisor.RecurringActive {
		return nil, fmt.Errorf("%w: a recurring task is already active — stop it first", ErrInvalidInput)
	}

	rf = &taskvisor.RecurringFile{Task: &taskvisor.RecurringTask{
		ID:              recurringID,
		Prompt:          input.Prompt,
		TotalCycles:     input.Cycles,
		CompletedCycles: 0,
		Status:          taskvisor.RecurringActive,
		CurrentCycle:    taskvisor.RecurringCycle{Index: 1, Phase: "dispatching"},
	}}
	if err := taskvisor.SaveRecurring(s.workingDir, rf); err != nil {
		return nil, fmt.Errorf("%w: failed to persist recurring state: %v", ErrInvalidInput, err)
	}

	marker := recurringMarkerPath(s.workingDir)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create .tmux-cli dir: %v", ErrInvalidInput, err)
	}
	if err := os.WriteFile(marker, []byte("active"), 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write recurring-active marker: %v", ErrInvalidInput, err)
	}

	return &RecurringCreateOutput{Created: true, ID: recurringID}, nil
}

// RecurringStatus reads recurring.yaml read-only. An absent file or nil Task
// encodes "no active task" → {Active:false}.
func (s *Server) RecurringStatus() (*RecurringStatusOutput, error) {
	rf, err := taskvisor.LoadRecurring(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load recurring state: %v", ErrInvalidInput, err)
	}
	if rf == nil || rf.Task == nil {
		return &RecurringStatusOutput{Active: false}, nil
	}
	return &RecurringStatusOutput{Active: true, Task: rf.Task}, nil
}

// RecurringStop idempotently stops the active recurring task: it removes the
// marker (tolerating absence) and, if a task is present, flips its status to
// stopped and persists. With no active task it returns {Stopped:false}, no error.
func (s *Server) RecurringStop() (*RecurringStopOutput, error) {
	rf, err := taskvisor.LoadRecurring(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load recurring state: %v", ErrInvalidInput, err)
	}

	marker := recurringMarkerPath(s.workingDir)
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: failed to remove recurring-active marker: %v", ErrInvalidInput, err)
	}

	if rf == nil || rf.Task == nil {
		return &RecurringStopOutput{Stopped: false}, nil
	}

	rf.Task.Status = taskvisor.RecurringStopped
	if err := taskvisor.SaveRecurring(s.workingDir, rf); err != nil {
		return nil, fmt.Errorf("%w: failed to persist recurring state: %v", ErrInvalidInput, err)
	}
	return &RecurringStopOutput{Stopped: true}, nil
}

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
	"gopkg.in/yaml.v3"
)

type tvGoal struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Acceptance  []string `yaml:"acceptance,omitempty"`
	Validate    []string `yaml:"validate,omitempty"`
	Status      string   `yaml:"status"`
	Retries     int      `yaml:"retries"`
	MaxRetries  int      `yaml:"max_retries"`
}

type tvGoalsFile struct {
	CurrentGoal string   `yaml:"current_goal"`
	Goals       []tvGoal `yaml:"goals"`
}

func tvGoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals.yaml")
}

func tvLoadGoals(projectRoot string) (*tvGoalsFile, error) {
	data, err := os.ReadFile(tvGoalsFilePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gf tvGoalsFile
	if err := yaml.Unmarshal(data, &gf); err != nil {
		return nil, err
	}
	return &gf, nil
}

func tvSaveGoals(projectRoot string, gf *tvGoalsFile) error {
	p := tvGoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(gf)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func tvNextGoalID(goals []tvGoal) string {
	max := 0
	for _, g := range goals {
		if !strings.HasPrefix(g.ID, "goal-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(g.ID, "goal-"))
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("goal-%03d", max+1)
}

// TaskvisorStart checks for pending goals and writes the taskvisor-start signal file.
func (s *Server) TaskvisorStart() (*TaskvisorStartOutput, error) {
	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goals.yaml not found", ErrInvalidInput)
	}

	hasPending := false
	for _, g := range gf.Goals {
		if g.Status == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return nil, fmt.Errorf("%w: no pending goals in goals.yaml", ErrInvalidInput)
	}

	signalPath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-start")
	if err := os.MkdirAll(filepath.Dir(signalPath), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create directory: %w", ErrInvalidInput, err)
	}
	if err := os.WriteFile(signalPath, []byte("start"), 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}

	return &TaskvisorStartOutput{Started: true}, nil
}

const MaxGoalDescriptionLength = 120

// GoalCreate generates a sequential goal ID, appends the goal to goals.yaml, creates the goal directory, and writes goal.md.
func (s *Server) GoalCreate(description string, acceptance, validate []string, context, notInScope string, maxRetries int) (*GoalCreateOutput, error) {
	if description == "" {
		return nil, fmt.Errorf("%w: description cannot be empty", ErrInvalidInput)
	}
	if len(description) > MaxGoalDescriptionLength {
		return nil, fmt.Errorf("%w: description exceeds %d characters (got %d); use --acceptance for detailed criteria", ErrInvalidInput, MaxGoalDescriptionLength, len(description))
	}

	if maxRetries == 0 {
		maxRetries = 3
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		gf = &tvGoalsFile{}
	}

	goalID := tvNextGoalID(gf.Goals)

	goal := tvGoal{
		ID:          goalID,
		Description: description,
		Status:      "pending",
		Retries:     0,
		MaxRetries:  maxRetries,
	}
	gf.Goals = append(gf.Goals, goal)

	if err := tvSaveGoals(s.workingDir, gf); err != nil {
		return nil, fmt.Errorf("%w: failed to save goals.yaml: %w", ErrInvalidInput, err)
	}

	goalDir := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create goal directory: %w", ErrInvalidInput, err)
	}

	if err := taskvisor.WriteGoalMD(goalDir, description, acceptance, validate, context, notInScope); err != nil {
		return nil, fmt.Errorf("%w: failed to write goal.md: %w", ErrInvalidInput, err)
	}

	return &GoalCreateOutput{ID: goalID}, nil
}

// GoalValidationDone validates caller authorization and writes signal.json for the goal.
func (s *Server) GoalValidationDone(goalID, verdict string, findings []ValidationFinding, nextAction string) (*GoalValidationDoneOutput, error) {
	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	found := false
	for _, g := range gf.Goals {
		if g.ID == goalID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	var validatorWindow *tmux.WindowInfo
	for i := range windows {
		if windows[i].Name == "validator" {
			validatorWindow = &windows[i]
			break
		}
	}
	if validatorWindow == nil {
		return nil, fmt.Errorf("%w: no validator window found", ErrInvalidInput)
	}

	validatorUUID, err := s.executor.GetWindowOption(sessionID, validatorWindow.TmuxWindowID, tmux.WindowUUIDOption)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read validator window UUID: %w", ErrTmuxCommandFailed, err)
	}

	callerUUID := os.Getenv("TMUX_WINDOW_UUID")
	if callerUUID != validatorUUID {
		return nil, fmt.Errorf("%w: caller is not the validator window (caller=%s, validator=%s)", ErrInvalidInput, callerUUID, validatorUUID)
	}

	sig := validatorSignalJSON{
		Source:     "validator",
		Verdict:    verdict,
		Findings:   findings,
		NextAction: nextAction,
		Timestamp:  time.Now().Format(time.RFC3339),
	}

	signalData, err := json.Marshal(sig)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal signal: %w", ErrInvalidInput, err)
	}

	signalDir := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(signalDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create goal directory: %w", ErrInvalidInput, err)
	}

	signalPath := filepath.Join(signalDir, "signal.json")
	tmpPath := signalPath + ".tmp"
	if err := os.WriteFile(tmpPath, signalData, 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}
	if err := os.Rename(tmpPath, signalPath); err != nil {
		return nil, fmt.Errorf("%w: failed to rename signal file: %w", ErrInvalidInput, err)
	}

	return &GoalValidationDoneOutput{Written: true}, nil
}

// GoalPrune atomically removes all taskvisor goal state.
func (s *Server) GoalPrune() (*GoalPruneOutput, error) {
	activePath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(activePath); err == nil {
		return nil, fmt.Errorf("%w: taskvisor daemon is active — stop it first", ErrInvalidInput)
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	count := 0
	if gf != nil {
		count = len(gf.Goals)
	}

	goalsFile := tvGoalsFilePath(s.workingDir)
	if err := os.Remove(goalsFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: failed to remove goals.yaml: %w", ErrInvalidInput, err)
	}

	goalsDir := filepath.Join(s.workingDir, ".tmux-cli", "goals")
	if err := os.RemoveAll(goalsDir); err != nil {
		return nil, fmt.Errorf("%w: failed to remove goals directory: %w", ErrInvalidInput, err)
	}

	for _, name := range []string{"taskvisor-current-goal", "taskvisor-start"} {
		p := filepath.Join(s.workingDir, ".tmux-cli", name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: failed to remove %s: %w", ErrInvalidInput, name, err)
		}
	}

	return &GoalPruneOutput{Pruned: true, GoalsRemoved: count}, nil
}

type validatorSignalJSON struct {
	Source     string              `json:"source"`
	Verdict    string              `json:"verdict"`
	Findings   []ValidationFinding `json:"findings"`
	NextAction string              `json:"next_action"`
	Timestamp  string              `json:"timestamp"`
}

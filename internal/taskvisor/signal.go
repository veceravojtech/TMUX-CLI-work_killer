package taskvisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type ValidationFinding struct {
	Rule       string `json:"rule"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Correction string `json:"correction,omitempty"`
}

type SupervisorSignal struct {
	Source    string `json:"source"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type ValidatorSignal struct {
	Source     string              `json:"source"`
	Verdict    string              `json:"verdict"`
	Findings   []ValidationFinding `json:"findings"`
	NextAction string              `json:"next_action"`
	Timestamp  string              `json:"timestamp"`
}

func SignalPath(projectRoot, goalID string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals", goalID, "signal.json")
}

func LoadSignal(projectRoot, goalID string) (any, error) {
	data, err := os.ReadFile(SignalPath(projectRoot, goalID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	source, _ := raw["source"].(string)
	switch source {
	case "supervisor":
		var sig SupervisorSignal
		if err := json.Unmarshal(data, &sig); err != nil {
			return nil, err
		}
		return &sig, nil
	case "validator":
		var sig ValidatorSignal
		if err := json.Unmarshal(data, &sig); err != nil {
			return nil, err
		}
		return &sig, nil
	default:
		return nil, fmt.Errorf("unknown signal source: %q", source)
	}
}

func SaveSupervisorSignal(projectRoot, goalID string, sig *SupervisorSignal) error {
	sig.Source = "supervisor"
	return saveSignal(projectRoot, goalID, sig)
}

func SaveValidatorSignal(projectRoot, goalID string, sig *ValidatorSignal) error {
	sig.Source = "validator"
	return saveSignal(projectRoot, goalID, sig)
}

func DeleteSignal(projectRoot, goalID string) error {
	p := SignalPath(projectRoot, goalID)
	err := os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func saveSignal(projectRoot, goalID string, sig any) error {
	p := SignalPath(projectRoot, goalID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(sig)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type CustomHook struct {
	Event   string `yaml:"event"`
	Matcher string `yaml:"matcher,omitempty"`
	Command string `yaml:"command"`
	Timeout int    `yaml:"timeout"`
}

type HooksSettings struct {
	SessionNotify    bool         `yaml:"session_notify"`
	BlockInteractive bool         `yaml:"block_interactive"`
	Custom           []CustomHook `yaml:"custom,omitempty"`
}

type CommandsSettings struct {
	Enabled bool `yaml:"enabled"`
}

type SupervisorSettings struct {
	MaxCycles      int  `yaml:"max_cycles"`
	MaxWorkers     int  `yaml:"max_workers"`
	CycleDelay     int  `yaml:"cycle_delay"`
	UnplannedAudit bool `yaml:"unplanned_audit"`
}

type PlanSettings struct {
	AutoApprove bool `yaml:"auto_approve"`
	AutoExecute bool `yaml:"auto_execute"`
}

type SudoSettings struct {
	Timeout int `yaml:"timeout"`
}

type TaskvisorSettings struct {
	DispatchTimeout int `yaml:"dispatch_timeout"`
	ValidateTimeout int `yaml:"validate_timeout"`
	PollInterval    int `yaml:"poll_interval"`
}

type Settings struct {
	Hooks      HooksSettings      `yaml:"hooks"`
	Commands   CommandsSettings   `yaml:"commands"`
	Supervisor SupervisorSettings `yaml:"supervisor"`
	Plan       PlanSettings       `yaml:"plan"`
	Sudo       SudoSettings       `yaml:"sudo"`
	Taskvisor  TaskvisorSettings  `yaml:"taskvisor"`
}

func DefaultSettings() *Settings {
	return &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
		},
		Commands: CommandsSettings{
			Enabled: true,
		},
		Supervisor: SupervisorSettings{
			MaxCycles:      0,
			MaxWorkers:     4,
			CycleDelay:     5,
			UnplannedAudit: true,
		},
		Plan: PlanSettings{
			AutoApprove: true,
			AutoExecute: true,
		},
		Sudo: SudoSettings{
			Timeout: 30,
		},
		Taskvisor: TaskvisorSettings{
			DispatchTimeout: 3600,
			ValidateTimeout: 300,
			PollInterval:    5,
		},
	}
}

func settingPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "setting.yaml")
}

func LoadSettings(projectRoot string) (*Settings, error) {
	p := settingPath(projectRoot)

	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		s := DefaultSettings()
		if err := SaveSettings(projectRoot, s); err != nil {
			return nil, err
		}
		return s, nil
	}

	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if err := SaveSettings(projectRoot, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveSettings(projectRoot string, s *Settings) error {
	p := settingPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

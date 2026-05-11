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
	MaxCycles  int `yaml:"max_cycles"`
	CycleDelay int `yaml:"cycle_delay"`
}

type Settings struct {
	Hooks      HooksSettings      `yaml:"hooks"`
	Commands   CommandsSettings   `yaml:"commands"`
	Supervisor SupervisorSettings `yaml:"supervisor"`
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
			MaxCycles:  0,
			CycleDelay: 5,
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

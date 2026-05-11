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

type Settings struct {
	Hooks    HooksSettings    `yaml:"hooks"`
	Commands CommandsSettings `yaml:"commands"`
}

func DefaultSettings() *Settings {
	return &Settings{
		Hooks: HooksSettings{
			SessionNotify:    true,
			BlockInteractive: true,
		},
		Commands: CommandsSettings{
			Enabled: true,
		},
	}
}

func settingsPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "settings.yaml")
}

func LoadSettings(projectRoot string) (*Settings, error) {
	p := settingsPath(projectRoot)

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
	return &s, nil
}

func SaveSettings(projectRoot string, s *Settings) error {
	p := settingsPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

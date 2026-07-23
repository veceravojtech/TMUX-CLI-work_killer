package telemetry

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Enabled reports whether structured-event emit is on for the project rooted at
// projectDir. It reads only the `telemetry.enabled` key from
// <projectDir>/.tmux-cli/setting.yaml with its OWN minimal view — deliberately
// decoupled from internal/setup.Settings so this producer never fights the
// settings-block owner over a shared struct. Default is ON: a missing file, an
// unparseable file, or an absent key all yield true (contract default).
func Enabled(projectDir string) bool {
	path := filepath.Join(projectDir, ".tmux-cli", "setting.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var f struct {
		Telemetry struct {
			Enabled *bool `yaml:"enabled"`
		} `yaml:"telemetry"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return true
	}
	if f.Telemetry.Enabled == nil {
		return true
	}
	return *f.Telemetry.Enabled
}

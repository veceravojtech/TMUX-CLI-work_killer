package producer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the producer-local configuration consumed by New. It is deliberately
// independent of setup.Settings: the producer reads .tmux-cli/setting.yaml on its
// own so that adding API knobs never disturbs the Settings struct or the TUI
// settings cascade (AGENTS.md TUI invariant).
type Config struct {
	APIURL     string
	APIEnabled bool
}

// apiFile mirrors the two accepted shapes of api configuration in
// .tmux-cli/setting.yaml. The flat top-level keys (apiUrl/apiEnabled) are
// preferred; the nested api.{url,enabled} block is the fallback. Flat fields are
// pointers so an absent key is distinguishable from an explicit false/empty
// value, letting a present flat key always win over the nested form.
type apiFile struct {
	APIURL     *string `yaml:"apiUrl"`
	APIEnabled *bool   `yaml:"apiEnabled"`
	API        struct {
		URL     string `yaml:"url"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"api"`
}

// LoadConfig reads .tmux-cli/setting.yaml under projectRoot and resolves the
// producer Config. It accepts BOTH the flat top-level form (apiUrl/apiEnabled,
// preferred) and the nested api.{url,enabled} form (fallback), with the flat
// form winning when present. A missing file degrades to a zero-value Config with
// no error, matching the opt-in, fire-and-forget posture. setup.Settings is
// never read or modified.
func LoadConfig(projectRoot string) (Config, error) {
	path := filepath.Join(projectRoot, ".tmux-cli", "setting.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}

	var f apiFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Config{}, err
	}

	cfg := Config{}
	if f.APIURL != nil {
		cfg.APIURL = *f.APIURL
	} else {
		cfg.APIURL = f.API.URL
	}
	if f.APIEnabled != nil {
		cfg.APIEnabled = *f.APIEnabled
	} else {
		cfg.APIEnabled = f.API.Enabled
	}
	return cfg, nil
}

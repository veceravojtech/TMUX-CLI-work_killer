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
	// Project is the task "lane" the client reports to and claims from: the
	// absolute working-folder path. Resolved by LoadConfig as a `project:`
	// override from setting.yaml, else auto-derived as the abs projectRoot. The
	// path is the matching key, so the SAME path on different machines pairs
	// (e.g. a laptop reports work that the remote box, sharing the path, claims);
	// the reporting machine (origin) is tracked separately via the task's
	// instance/fingerprint in the backend. Empty only when no setting.yaml exists
	// (API disabled anyway).
	Project string
}

// apiFile mirrors the two accepted shapes of api configuration in
// .tmux-cli/setting.yaml. The flat top-level keys (apiUrl/apiEnabled) are
// preferred; the nested api.{url,enabled} block is the fallback. Flat fields are
// pointers so an absent key is distinguishable from an explicit false/empty
// value, letting a present flat key always win over the nested form.
type apiFile struct {
	APIURL     *string `yaml:"apiUrl"`
	APIEnabled *bool   `yaml:"apiEnabled"`
	Project    *string `yaml:"project"`
	API        struct {
		URL     string `yaml:"url"`
		Enabled bool   `yaml:"enabled"`
		Project string `yaml:"project"`
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

	// Project lane: a `project:` override (flat preferred, then nested) wins;
	// otherwise auto-derive the absolute working-folder path. The path is the
	// matching key, so the same path on different machines pairs (laptop reports,
	// remote claims). The reporting machine (origin) lives on the task's instance
	// in the backend, not in this key. Resolved only when a setting.yaml exists —
	// a missing file already returned a zero-value Config above.
	override := ""
	if f.Project != nil {
		override = *f.Project
	} else {
		override = f.API.Project
	}
	if override != "" {
		cfg.Project = override
	} else {
		absRoot, absErr := filepath.Abs(projectRoot)
		if absErr != nil {
			absRoot = projectRoot
		}
		cfg.Project = absRoot
	}

	return cfg, nil
}

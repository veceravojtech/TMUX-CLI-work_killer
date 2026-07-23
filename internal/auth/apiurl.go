package auth

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaultAPIURL is the fallback backend base URL, identical to the producer's
// default (internal/producer/client.go). Kept in sync deliberately without
// importing producer — see the package doc.
const defaultAPIURL = "https://tmux.vojta.ai"

// apiFile mirrors the accepted shapes of api configuration in setting.yaml,
// matching internal/producer.apiFile: a flat top-level apiUrl (preferred) or the
// nested api.url block. The flat field is a pointer so a present-but-empty key
// still wins over the nested form.
type apiFile struct {
	APIURL *string `yaml:"apiUrl"`
	API    struct {
		URL string `yaml:"url"`
	} `yaml:"api"`
}

// LoadAPIURL resolves the backend base URL the same way the producer does:
// setting.yaml (flat apiUrl, else nested api.url) → TMUX_CLI_API_URL env →
// defaultAPIURL. A missing or unparseable setting.yaml degrades to the env/default
// chain rather than erroring.
func LoadAPIURL(projectRoot string) string {
	cfgURL := ""
	path := filepath.Join(projectRoot, ".tmux-cli", "setting.yaml")
	if data, err := os.ReadFile(path); err == nil {
		var f apiFile
		if yaml.Unmarshal(data, &f) == nil {
			if f.APIURL != nil {
				cfgURL = *f.APIURL
			} else {
				cfgURL = f.API.URL
			}
		}
	}
	return resolveBaseURL(cfgURL)
}

// resolveBaseURL applies the URL precedence: explicit cfg URL → TMUX_CLI_API_URL
// env → defaultAPIURL. Mirrors producer.resolveBaseURL.
func resolveBaseURL(cfgURL string) string {
	if cfgURL != "" {
		return cfgURL
	}
	if env := os.Getenv("TMUX_CLI_API_URL"); env != "" {
		return env
	}
	return defaultAPIURL
}

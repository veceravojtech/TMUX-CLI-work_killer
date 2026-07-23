package transcript

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/shipper"
	"gopkg.in/yaml.v3"
)

// settingsAllow reads only the telemetry.{enabled,transcripts} keys from
// <projectDir>/.tmux-cli/setting.yaml with its own minimal view (mirroring
// internal/telemetry.Enabled — deliberately decoupled from internal/setup so
// this reader never fights the settings-block owner). Contract defaults:
// enabled is ON when absent, transcripts is OFF when absent — capture is
// strictly OPT-IN, so a missing/unparseable file yields false.
func settingsAllow(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, ".tmux-cli", "setting.yaml"))
	if err != nil {
		return false
	}
	var f struct {
		Telemetry struct {
			Enabled     *bool `yaml:"enabled"`
			Transcripts *bool `yaml:"transcripts"`
		} `yaml:"telemetry"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return false
	}
	enabled := f.Telemetry.Enabled == nil || *f.Telemetry.Enabled
	transcripts := f.Telemetry.Transcripts != nil && *f.Telemetry.Transcripts
	return enabled && transcripts
}

// Armed reports whether transcript capture is armed for projectDir per the
// contract's privacy gate: telemetry.enabled AND telemetry.transcripts AND a
// logged-in auth store. When false, callers must wire NO capture pipe and
// write NO segment — opted-out sessions leave zero pane content on disk.
func Armed(projectDir string) bool {
	if !settingsAllow(projectDir) {
		return false
	}
	store, err := auth.NewStore()
	if err != nil {
		return false
	}
	return shipper.LoggedIn(store)
}

// CapturePipeCommand returns the shell command `tmux pipe-pane` should run for
// a managed window, or "" when capture is not armed (the caller falls back to
// its existing plain pane-log pipe, if any). tmux allows ONE pipe per pane, so
// windows that already stream a pane log (paneLogPath != "") get a tee through
// the capture process — the pane log keeps its exact byte stream and the
// capture process consumes the same feed. The binary path is absolute
// (os.Executable) because pipe-pane runs under the tmux server's own /bin/sh
// environment, not the caller's PATH.
func CapturePipeCommand(projectDir, sessionID, window, paneLogPath string) string {
	if !Armed(projectDir) {
		return ""
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	capture := fmt.Sprintf("%q logs capture --dir %q --session %q --window %q",
		self, projectDir, sessionID, window)
	if paneLogPath == "" {
		return capture
	}
	return fmt.Sprintf("tee -a %q | %s", paneLogPath, capture)
}

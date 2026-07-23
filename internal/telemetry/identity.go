package telemetry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/console/tmux-cli/internal/identity"
)

// Identity is the per-session stamp applied to every emitted event.
type Identity struct {
	SessionID   string // tmux session name
	Project     string // lane name
	Fingerprint string // 64-hex machine identity
}

// ResolveIdentity gathers the event identity from the ambient environment,
// degrading gracefully (empty strings) rather than erroring. It never touches the
// network. Resolution order:
//   - SessionID: $TMUX_CLI_SESSION_ID, else `tmux display-message -p '#S'`, else "".
//   - Project:   $TMUX_CLI_PROJECT, else basename of the absolute projectDir.
//   - Fingerprint: identity.Fingerprint() (cached, reboot-stable).
func ResolveIdentity(projectDir string) Identity {
	return Identity{
		SessionID:   resolveSessionID(),
		Project:     resolveProject(projectDir),
		Fingerprint: identity.Fingerprint(),
	}
}

func resolveSessionID() string {
	if v := strings.TrimSpace(os.Getenv("TMUX_CLI_SESSION_ID")); v != "" {
		return v
	}
	// Only attempt tmux when inside a tmux client — avoids a pointless exec (and a
	// hang risk) outside tmux, and keeps unit tests server-free.
	if os.Getenv("TMUX") == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func resolveProject(projectDir string) string {
	if v := strings.TrimSpace(os.Getenv("TMUX_CLI_PROJECT")); v != "" {
		return v
	}
	if projectDir == "" {
		return ""
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	return filepath.Base(abs)
}

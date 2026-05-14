package sudo

import "github.com/console/tmux-cli/internal/setup"

const DefaultTimeout = 30

// ResolveTimeout returns the effective timeout in seconds.
// nil → not provided (fall through to config/default); 0 → unlimited; >0 → that value.
func ResolveTimeout(inputTimeout *int, workingDir string) int {
	if inputTimeout != nil {
		return *inputTimeout
	}
	settings, _ := setup.LoadSettings(workingDir)
	if settings != nil {
		return settings.Sudo.Timeout
	}
	return DefaultTimeout
}

//go:build darwin

package identity

import (
	"os/exec"
	"strings"
)

// machineID resolves the macOS machine id from the IOPlatformUUID reported by
// ioreg. On any exec or parse failure it returns the hashed fallback.
func machineID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").CombinedOutput()
	if err != nil {
		return fallbackMachineID()
	}
	if uuid := parseIOPlatformUUID(string(out)); uuid != "" {
		return uuid
	}
	return fallbackMachineID()
}

// parseIOPlatformUUID extracts the quoted UUID value from an ioreg dump. The
// relevant line looks like: `    "IOPlatformUUID" = "AABBCCDD-..."`. It is a
// pure string-in/string-out function and returns "" when the key is absent or
// the value is unterminated.
func parseIOPlatformUUID(raw string) string {
	const key = "\"IOPlatformUUID\""
	idx := strings.Index(raw, key)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(key):]
	// Find the opening quote of the value.
	open := strings.Index(rest, "\"")
	if open < 0 {
		return ""
	}
	rest = rest[open+1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

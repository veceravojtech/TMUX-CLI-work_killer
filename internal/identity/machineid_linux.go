//go:build linux

package identity

import (
	"os"
	"strings"
)

// machineID resolves the Linux machine id, preferring /etc/machine-id, then the
// dbus machine-id, and finally a hashed fallback. Each source is best-effort: a
// read error or an empty-after-trim value falls through to the next.
func machineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	return fallbackMachineID()
}

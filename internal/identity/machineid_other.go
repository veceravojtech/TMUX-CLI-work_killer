//go:build !linux && !darwin

package identity

// machineID falls back to a hashed identity on platforms without a supported
// native machine-id source (e.g. Windows).
func machineID() string {
	return fallbackMachineID()
}

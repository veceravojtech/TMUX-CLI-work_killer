// Package identity computes a stable, reboot-survivable machine fingerprint and
// a graceful snapshot of the host environment (tmux/Go/shell/OS/user).
//
// It is a leaf package: it depends only on the standard library and must not
// import any other internal/* package. All host lookups degrade gracefully —
// a missing tmux binary, hostname, or user lookup yields an empty string or a
// hashed fallback, never an error and never a panic.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"sync"
)

var (
	fpOnce  sync.Once
	fpCache string
)

// Fingerprint returns a deterministic, reboot-stable, lowercase 64-character
// SHA256 hex identity for the current machine. The value is computed once per
// process and cached via sync.Once, so repeated calls are cheap and identical.
func Fingerprint() string {
	fpOnce.Do(func() {
		fpCache = computeFingerprint()
	})
	return fpCache
}

// computeFingerprint assembles the stable host inputs and hashes them. The
// component order is fixed so the result is reproducible across calls.
func computeFingerprint() string {
	data := machineID() + "|" + hostname() + "|" + runtime.GOOS + "|" + runtime.GOARCH + "|" + currentUsername()
	return hashString(data)
}

// hashString returns the lowercase 64-character SHA256 hex digest of s.
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fallbackMachineID derives a stable machine id from the hostname, username and
// home directory when no OS-native machine id is available. It is build-tag-free
// so every per-GOOS machineID() implementation can share it without duplication.
func fallbackMachineID() string {
	return hashString(hostname() + "|" + currentUsername() + "|" + homeDir())
}

// hostname returns the host name, or "" if the lookup fails.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// homeDir returns the current user's home directory, or "" if unavailable.
func homeDir() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return d
}

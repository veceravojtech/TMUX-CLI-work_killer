package identity

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// SystemInfo is a graceful snapshot of the host environment. Any field whose
// source is unavailable is left as an empty string rather than causing an error.
//
// The json tags are camelCase to match the backend's CreateTaskRequest/SystemInfo
// DTOs (Symfony #[MapRequestPayload] with no name converter maps keys verbatim to
// the PHP property names). In particular OS marshals as "osInfo" — its backend
// column. Without these tags Go would emit PascalCase keys the backend cannot map,
// leaving every system_info field NULL (and now, post release 8, 422-rejected).
type SystemInfo struct {
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
	OS          string `json:"osInfo"`
	TmuxVersion string `json:"tmuxVersion"`
	CLIVersion  string `json:"cliVersion"`
	GoVersion   string `json:"goVersion"`
	Shell       string `json:"shell"`
	Username    string `json:"username"`
}

// CollectSystemInfo gathers the host environment snapshot. cliVersion is passed
// in because the version constant lives in package main and is not importable.
func CollectSystemInfo(cliVersion string) SystemInfo {
	return SystemInfo{
		Fingerprint: Fingerprint(),
		Hostname:    hostname(),
		OS:          runtime.GOOS + "/" + runtime.GOARCH,
		TmuxVersion: tmuxVersion(),
		CLIVersion:  cliVersion,
		GoVersion:   runtime.Version(),
		Shell:       os.Getenv("SHELL"),
		Username:    currentUsername(),
	}
}

// parseTmuxVersion extracts the version token from `tmux -V` output. It returns
// "" for empty, malformed, or unprefixed input. Examples: "tmux 3.4\n" → "3.4",
// "tmux next-3.5\n" → "next-3.5".
func parseTmuxVersion(raw string) string {
	f := strings.TrimSpace(raw)
	if !strings.HasPrefix(f, "tmux ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(f, "tmux "))
}

// tmuxVersion shells out to `tmux -V` and parses the result. A missing tmux
// binary (or any exec error) yields "" — never an error.
func tmuxVersion() string {
	out, err := exec.Command("tmux", "-V").CombinedOutput()
	if err != nil {
		return ""
	}
	return parseTmuxVersion(string(out))
}

// currentUsername returns the current user name, preferring user.Current() and
// falling back to $USER, then $LOGNAME, then "".
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return os.Getenv("LOGNAME")
}

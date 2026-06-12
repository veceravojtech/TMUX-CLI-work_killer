package main

import (
	"runtime/debug"
)

// version information. `version` is the default for local/dev builds; release
// builds override it via -ldflags "-X main.version=<semver>" (set from the git
// tag by the release workflow). The commit suffix comes from vcsRevision().
var version = "0.1.0"

const appName = "tmux-cli"

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}

func versionString() string {
	return version + " (" + vcsRevision() + ")"
}

func main() {
	if err := Execute(); err != nil {
		exitWithError(err)
	}
}

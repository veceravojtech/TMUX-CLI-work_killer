package main

import (
	"runtime/debug"
)

// version information
const (
	version = "0.1.0"
	appName = "tmux-cli"
)

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

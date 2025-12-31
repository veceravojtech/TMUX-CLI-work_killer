package main

// version information
const (
	version = "0.1.0"
	appName = "tmux-cli"
)

func main() {
	if err := Execute(); err != nil {
		exitWithError(err)
	}
}

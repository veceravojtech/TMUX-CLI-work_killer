package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Exit codes following AR8 standard
const (
	ExitSuccess         = 0
	ExitGeneralError    = 1
	ExitUsageError      = 2
	ExitCommandNotFound = 126
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tmux-cli",
	Short: "A tmux session manager CLI",
	Long: `tmux-cli is a command-line interface for managing tmux sessions and windows.
It provides structured session management with JSON-based persistence and crash recovery.`,
	Version: version,
	// Root command does nothing by itself - all functionality is in subcommands
	Run: func(cmd *cobra.Command, args []string) {
		// When no subcommand is provided, show help
		cmd.Help()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags can be added here
	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.tmux-cli.yaml)")
}

// determineExitCode maps errors to appropriate exit codes following AR8
// Enhanced version is in session.go
func determineExitCode(err error) int {
	// Use enhanced version if available
	return determineExitCodeEnhanced(err)
}

// exitWithError prints error and exits with appropriate code
func exitWithError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(determineExitCode(err))
	}
}

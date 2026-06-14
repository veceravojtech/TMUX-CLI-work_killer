package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/console/tmux-cli/internal/producer"
)

// apiProjectCmd prints the resolved backend "project lane" for the current
// directory — the machine-qualified working-folder identity the producer stamps
// on submissions and scopes claims to (`<fingerprint>:<abs-path>`, or a
// `project:` override from setting.yaml). The worker dispatcher reads this to
// query/act on only this lane's tasks instead of re-deriving the fingerprint in
// shell/Node. Prints an empty line when no .tmux-cli/setting.yaml exists.
var apiProjectCmd = &cobra.Command{
	Use:   "api-project",
	Short: "Print the resolved backend project lane (<fingerprint>:<abs-path>) for this directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		cfg, err := producer.LoadConfig(cwd)
		if err != nil {
			return fmt.Errorf("load producer config: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), cfg.Project)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(apiProjectCmd)
}

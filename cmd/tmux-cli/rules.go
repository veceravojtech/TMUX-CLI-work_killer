package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/console/tmux-cli/internal/rules"
	"github.com/spf13/cobra"
)

var (
	rulesResolveKind      string
	rulesResolveLang      string
	rulesResolveFramework string
	rulesResolveJSON      bool
	rulesResolveProject   string
)

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Per-project rule pack resolution",
	Long: `Resolve which rule packs (.tmux-cli/rules/) apply to a project.
Conventions bind the planner; code-rules guide spec/implementation/review
agents. See .tmux-cli/rules/SCHEMA.md.`,
}

var rulesResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Print the rule files applicable to this project, one path per line",
	Long: `Detects the project's stack and capability signals (project manifests +
docs/architecture/ discovery docs), evaluates the pack manifest, and prints
the applicable rule-file paths relative to the project root.

--lang/--framework pass the discovery session state (LANG/FRAMEWORK); they
override filesystem detection — required for greenfield projects that have no
dependency manifest yet. Unknown capability signals include their packs
conservatively (warning on stderr); unknown stack loads no stack pack.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot := rulesResolveProject
		if projectRoot == "" {
			var err error
			projectRoot, err = os.Getwd()
			if err != nil {
				return err
			}
		}

		manifest, err := rules.LoadManifest(projectRoot)
		if err != nil {
			return err
		}

		sig := rules.Detect(projectRoot)
		if rulesResolveLang != "" {
			sig.Lang = rulesResolveLang
		}
		if rulesResolveFramework != "" {
			sig.Framework = rulesResolveFramework
		}

		files, warnings, err := rules.Resolve(projectRoot, manifest, sig)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
		}

		if rulesResolveKind != "" {
			filtered := files[:0]
			for _, f := range files {
				if f.Kind == rulesResolveKind {
					filtered = append(filtered, f)
				}
			}
			files = filtered
		}

		if rulesResolveJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(files)
		}
		for _, f := range files {
			fmt.Fprintln(cmd.OutOrStdout(), f.Path)
		}
		return nil
	},
}

func init() {
	rulesResolveCmd.Flags().StringVar(&rulesResolveKind, "kind", "",
		"filter by file kind: convention | code-rules (default: both)")
	rulesResolveCmd.Flags().StringVar(&rulesResolveLang, "lang", "",
		"language slug from discovery state (overrides detection)")
	rulesResolveCmd.Flags().StringVar(&rulesResolveFramework, "framework", "",
		"framework slug from discovery state (overrides detection)")
	rulesResolveCmd.Flags().BoolVar(&rulesResolveJSON, "json", false,
		"emit JSON objects with pack/kind/path instead of bare paths")
	rulesResolveCmd.Flags().StringVar(&rulesResolveProject, "project", "",
		"project root (default: current directory)")
	rulesCmd.AddCommand(rulesResolveCmd)
	rootCmd.AddCommand(rulesCmd)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/console/tmux-cli/internal/rules"
	"github.com/spf13/cobra"
)

var (
	rulesResolveKind      string
	rulesResolveLang      string
	rulesResolveFramework string
	rulesResolveJSON      bool
	rulesResolveProject   string
	rulesResolveSignals   bool

	rulesMatchFiles   []string
	rulesMatchPhase   string
	rulesMatchJSON    bool
	rulesMatchProject string
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

		// --signals short-circuits BEFORE the manifest load: it is the single
		// authority JSON of detected stack/capability signals and must work on
		// greenfield projects that have no rules tree yet (exit 0, unknowns).
		if rulesResolveSignals {
			sig := rules.Detect(projectRoot)
			if rulesResolveLang != "" {
				sig.Lang = rulesResolveLang
			}
			if rulesResolveFramework != "" {
				sig.Framework = rulesResolveFramework
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(sig)
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

var rulesMatchCmd = &cobra.Command{
	Use:   "match",
	Short: "Match code-rules to changed files and emit pre-rendered injection payloads",
	Long: `Detects the project's signals, resolves the applicable code-rules catalogue,
glob-matches each rule's applies_to against --files, and emits one pre-rendered
payload per matched rule. Go owns ALL glob/severity/signal routing here; the
consuming planner copies payloads verbatim (determinism boundary).

Each payload carries id, severity, validate_kind, phase, the source code-rules
path(s), a CR-<id> acceptance_line (must rules only), and a fail-closed
validate_cmd (negated grep over the matched footprint) when the rule has a
runnable signal. A rule whose signal cannot run is dropped with a warning.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot := rulesMatchProject
		if projectRoot == "" {
			var err error
			projectRoot, err = os.Getwd()
			if err != nil {
				return err
			}
		}

		sig := rules.Detect(projectRoot)

		// No rules tree (greenfield): emit an empty result, never error.
		manifest, err := rules.LoadManifest(projectRoot)
		if err != nil {
			return printMatchResult(cmd, rules.MatchResult{
				Rules:    []rules.RulePayload{},
				Warnings: []string{fmt.Sprintf("rules manifest unavailable: %v", err)},
			}, rulesMatchJSON)
		}

		resolved, resolveWarnings, err := rules.Resolve(projectRoot, manifest, sig)
		if err != nil {
			return err
		}

		codeRules, err := rules.LoadCodeRules(projectRoot, resolved)
		if err != nil {
			return err
		}

		result, _ := rules.Match(codeRules, rulesMatchFiles, rulesMatchPhase)
		// Surface conservative-inclusion warnings from resolution alongside the
		// match-time warnings (dropped rules, bad globs).
		result.Warnings = append(append([]string{}, resolveWarnings...), result.Warnings...)
		return printMatchResult(cmd, result, rulesMatchJSON)
	},
}

// printMatchResult renders a MatchResult as indented JSON (--json) or a
// human-readable summary (warnings to stderr, one block per matched rule).
func printMatchResult(cmd *cobra.Command, result rules.MatchResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	if len(result.Rules) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no code rules match the given files")
		return nil
	}
	for _, r := range result.Rules {
		fmt.Fprintf(cmd.OutOrStdout(), "%s [%s/%s] phase=%s\n", r.ID, r.Severity, r.ValidateKind, r.Phase)
		if r.AcceptanceLine != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  acceptance: %s\n", r.AcceptanceLine)
		}
		if r.ValidateCmd != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  validate:   %s\n", r.ValidateCmd)
		}
		if len(r.Paths) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  source:     %s\n", strings.Join(r.Paths, ", "))
		}
	}
	return nil
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
	rulesResolveCmd.Flags().BoolVar(&rulesResolveSignals, "signals", false,
		"dump detected stack/capability signals as JSON instead of resolving paths")
	rulesCmd.AddCommand(rulesResolveCmd)

	rulesMatchCmd.Flags().StringSliceVar(&rulesMatchFiles, "files", nil,
		"changed file paths to match rules against (required, repeatable/comma-separated)")
	rulesMatchCmd.Flags().StringVar(&rulesMatchPhase, "phase", "",
		"only match rules whose phase equals this value")
	rulesMatchCmd.Flags().BoolVar(&rulesMatchJSON, "json", false,
		"emit the MatchResult as JSON instead of a human-readable summary")
	rulesMatchCmd.Flags().StringVar(&rulesMatchProject, "project", "",
		"project root (default: current directory)")
	_ = rulesMatchCmd.MarkFlagRequired("files")
	rulesCmd.AddCommand(rulesMatchCmd)

	rootCmd.AddCommand(rulesCmd)
}

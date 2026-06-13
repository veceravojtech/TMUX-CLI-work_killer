package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

	rulesCheckFiles   []string
	rulesCheckDiff    string
	rulesCheckPhase   string
	rulesCheckJSON    bool
	rulesCheckProject string

	rulesLintProject  string
	rulesLintJSON     bool
	rulesLintEmbedded bool
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

var rulesCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Gate a diff: report which resolved code-rules apply and which are violated",
	Long: `The brownfield diff gate (the previo:code-rules:goals analog). Derives the
changed files (working tree vs HEAD by default; --diff <range> or an explicit
--files list override), resolves the applicable code-rules catalogue, and runs
each rule's automated signal against the changed footprint.

A rule whose applies_to matches no changed file is omitted. For a matched rule:
if it carries a runnable signal, VIOLATED means the anti-pattern is FOUND in the
diff (grep exit 0 — the inverse of the fail-closed 'match' validate_cmd; the
signal is never negated). A non-runnable signal is dropped with a warning.
A signal-less or review/mixed rule carries agent_review:true — Go never
auto-fails it; the agent judges it from the JSON (determinism boundary §6.4).
Greenfield (no rules tree) yields an empty result, exit 0.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot := rulesCheckProject
		if projectRoot == "" {
			var err error
			projectRoot, err = os.Getwd()
			if err != nil {
				return err
			}
		}

		files, err := changedFiles(projectRoot, rulesCheckDiff, rulesCheckFiles)
		if err != nil {
			return err
		}

		sig := rules.Detect(projectRoot)

		// No rules tree (greenfield): emit an empty result, never error.
		manifest, err := rules.LoadManifest(projectRoot)
		if err != nil {
			return printCheckResult(cmd, rules.CheckResult{
				Rules:    []rules.CheckRulePayload{},
				Warnings: []string{fmt.Sprintf("rules manifest unavailable: %v", err)},
			}, rulesCheckJSON)
		}

		resolved, resolveWarnings, err := rules.Resolve(projectRoot, manifest, sig)
		if err != nil {
			return err
		}

		codeRules, err := rules.LoadCodeRules(projectRoot, resolved)
		if err != nil {
			return err
		}

		result := rules.Check(codeRules, files, rulesCheckPhase, projectRoot)
		// Surface conservative-inclusion warnings from resolution alongside the
		// check-time warnings (dropped rules, bad globs, grep errors).
		result.Warnings = append(append([]string{}, resolveWarnings...), result.Warnings...)
		return printCheckResult(cmd, result, rulesCheckJSON)
	},
}

var rulesLintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Lint project-local code-rules against the falsifiability contract",
	Long: `Lints .tmux-cli/rules/local/code-rules/*.yaml against the same
falsifiability contract the embedded catalogue selftest enforces (schema
completeness, global id-uniqueness, validate_kind dispatch, and — for rules
carrying a signal — that the signal compiles, matches its own examples.bad, and
does NOT match examples.good). A check that cannot go red on its own bad example
manufactures false confidence and is rejected.

Exits 0 when clean (including an absent local tree) and NON-ZERO on any finding,
so /tmux:rules:add can use it as a hard gate. --embedded also lints the
materialized pack rules (excluding manifest.yaml and the local/ subtree).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot := rulesLintProject
		if projectRoot == "" {
			var err error
			projectRoot, err = os.Getwd()
			if err != nil {
				return err
			}
		}

		sets, loadFindings := rules.LoadLocalRuleSets(projectRoot, rulesLintEmbedded)
		findings := append(loadFindings, rules.LintRuleSets(sets)...)

		ruleCount := 0
		for _, s := range sets {
			ruleCount += len(s.Rules)
		}

		if rulesLintJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if findings == nil {
				findings = []rules.LintFinding{}
			}
			if err := enc.Encode(struct {
				Findings []rules.LintFinding `json:"findings"`
				Clean    bool                `json:"clean"`
			}{Findings: findings, Clean: len(findings) == 0}); err != nil {
				return err
			}
		} else {
			for _, f := range findings {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", f.Source, f.RuleID, f.Message)
			}
		}

		if len(findings) > 0 {
			return fmt.Errorf("rules lint: %d finding(s)", len(findings))
		}
		if !rulesLintJSON {
			fmt.Fprintf(cmd.OutOrStdout(), "ok: %d rule(s) lint clean\n", ruleCount)
		}
		return nil
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

// printCheckResult renders a CheckResult as indented JSON (--json) or a
// human-readable summary (warnings to stderr, one block per applicable rule
// tagged APPLICABLE or VIOLATED, with the matched files and an agent-review
// note). Parallel to printMatchResult.
func printCheckResult(cmd *cobra.Command, result rules.CheckResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	if len(result.Rules) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no code rules apply to the changed files")
		return nil
	}
	for _, r := range result.Rules {
		status := "APPLICABLE"
		if r.Violated {
			status = "VIOLATED"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s [%s/%s] phase=%s %s\n", r.ID, r.Severity, r.ValidateKind, r.Phase, status)
		if len(r.Matched) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  matched:      %s\n", strings.Join(r.Matched, ", "))
		}
		if r.AgentReview {
			fmt.Fprintln(cmd.OutOrStdout(), "  agent-review: yes (judge from the rule, not auto-failed)")
		}
		if len(r.Paths) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  source:       %s\n", strings.Join(r.Paths, ", "))
		}
	}
	return nil
}

// changedFiles derives the changed-file list the diff gate runs against. An
// explicit list (--files) short-circuits git entirely (deterministic, scripting
// parity with `match`). Otherwise it unions `git diff --name-only <range>`
// (range defaults to HEAD = working tree vs HEAD) with untracked files
// (`git ls-files --others --exclude-standard`); untracked are included only for
// the default working-tree gate, not for an explicit --diff range. Results are
// de-duplicated, preserving first-seen order. A git failure (e.g. not a repo) is
// surfaced as an actionable error, never a silent empty diff.
func changedFiles(projectRoot, diffRange string, explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}

	rangeArg := diffRange
	if rangeArg == "" {
		rangeArg = "HEAD"
	}

	seen := map[string]bool{}
	var files []string
	add := func(line string) {
		s := strings.TrimSpace(line)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		files = append(files, s)
	}

	out, err := runGit(projectRoot, "diff", "--name-only", rangeArg)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		add(line)
	}

	// Untracked files only when no explicit range was given (the working-tree
	// gate); a commit range has no working-tree-only files to add.
	if diffRange == "" {
		un, err := runGit(projectRoot, "ls-files", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(un, "\n") {
			add(line)
		}
	}

	return files, nil
}

// runGit runs a git subcommand in projectRoot and returns its stdout, or an
// actionable error that includes git's stderr (e.g. "not a git repository").
func runGit(projectRoot string, args ...string) (string, error) {
	full := append([]string{"-C", projectRoot}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
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

	rulesCheckCmd.Flags().StringVar(&rulesCheckDiff, "diff", "",
		"git range to derive changed files from (default: working tree vs HEAD)")
	rulesCheckCmd.Flags().StringSliceVar(&rulesCheckFiles, "files", nil,
		"explicit changed file paths (skips git derivation; repeatable/comma-separated)")
	rulesCheckCmd.Flags().StringVar(&rulesCheckPhase, "phase", "",
		"only check rules whose phase equals this value")
	rulesCheckCmd.Flags().BoolVar(&rulesCheckJSON, "json", false,
		"emit the CheckResult as JSON instead of a human-readable summary")
	rulesCheckCmd.Flags().StringVar(&rulesCheckProject, "project", "",
		"project root (default: current directory)")
	rulesCmd.AddCommand(rulesCheckCmd)

	rulesLintCmd.Flags().StringVar(&rulesLintProject, "project", "",
		"project root (default: current directory)")
	rulesLintCmd.Flags().BoolVar(&rulesLintJSON, "json", false,
		"emit {findings, clean} as JSON instead of one line per finding")
	rulesLintCmd.Flags().BoolVar(&rulesLintEmbedded, "embedded", false,
		"also lint materialized pack rules (excludes manifest.yaml and the local/ subtree)")
	rulesCmd.AddCommand(rulesLintCmd)

	rootCmd.AddCommand(rulesCmd)
}

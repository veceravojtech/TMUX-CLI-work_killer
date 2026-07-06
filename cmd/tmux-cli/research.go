package main

import (
	"encoding/json"
	"fmt"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/spf13/cobra"
)

var (
	researchModeNamedFiles   []string
	researchModeNamedSymbols []string
	researchModeConcreteEdit bool
	researchModeImpliedLines int
	researchModeImpliedFiles int
	researchModeCandidateLOC int
	researchModeJSON         bool
)

// researchModeCmd sizes /tmux:feature Stage-1 research by the IMPLIED CHANGE a
// brief describes (not the LOC of the candidate files), returning the inline vs
// spawn branch. The model supplies extracted brief facts as flags; Go owns the
// branch decision (determinism boundary) — the XML consumes the printed `mode`
// and never re-judges it in prose.
var researchModeCmd = &cobra.Command{
	Use:   "research-mode",
	Short: "Decide /tmux:feature Stage-1 research mode (inline vs spawn) from a brief's implied change",
	Long: `Sizes Stage-1 context research by the IMPLIED CHANGE a brief describes,
NOT by the LOC of the candidate files that change lives in.

The caller EXTRACTS facts from the brief and passes them as flags; this command
OWNS the branch decision so the consuming XML reads a computed 'mode' instead of
judging prose. Logic, in order:
  1. Precise brief (--named-file AND --named-symbol AND --concrete-edit) -> inline,
     regardless of --candidate-loc.
  2. Unmeasurable (--implied-lines omitted / negative) -> spawn (fail-safe).
  3. Otherwise inline when implied change is within the thresholds, else spawn.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decision := tasks.ComputeResearchMode(tasks.ResearchModeInput{
			NamedFiles:          researchModeNamedFiles,
			NamedSymbols:        researchModeNamedSymbols,
			HasConcreteEdit:     researchModeConcreteEdit,
			Measurable:          researchModeImpliedLines >= 0,
			ImpliedChangedLines: researchModeImpliedLines,
			ImpliedTouchedFiles: researchModeImpliedFiles,
			CandidateFileLOC:    researchModeCandidateLOC,
		})
		return printResearchModeDecision(cmd, decision, researchModeJSON)
	},
}

// printResearchModeDecision renders the decision as indented JSON (--json) or a
// single human-readable line via cmd.OutOrStdout() (mirrors printMatchResult).
func printResearchModeDecision(cmd *cobra.Command, decision tasks.ResearchModeDecision, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Mode    string `json:"mode"`
			Precise bool   `json:"precise"`
			Reason  string `json:"reason"`
		}{
			Mode:    string(decision.Mode),
			Precise: decision.Precise,
			Reason:  decision.Reason,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "research-mode: %s (%s)\n", decision.Mode, decision.Reason)
	return nil
}

func init() {
	researchModeCmd.Flags().StringSliceVar(&researchModeNamedFiles, "named-file", nil,
		"a concrete file the brief names (repeatable/comma-separated)")
	researchModeCmd.Flags().StringSliceVar(&researchModeNamedSymbols, "named-symbol", nil,
		"a concrete symbol the brief names (repeatable/comma-separated)")
	researchModeCmd.Flags().BoolVar(&researchModeConcreteEdit, "concrete-edit", false,
		"the brief describes a specific edit to make")
	researchModeCmd.Flags().IntVar(&researchModeImpliedLines, "implied-lines", -1,
		"estimated implied changed lines (default -1 = unmeasurable sentinel -> spawn)")
	researchModeCmd.Flags().IntVar(&researchModeImpliedFiles, "implied-files", 0,
		"estimated implied touched files")
	researchModeCmd.Flags().IntVar(&researchModeCandidateLOC, "candidate-loc", 0,
		"total LOC of the candidate files (ignored on the precise branch; carried for logging)")
	researchModeCmd.Flags().BoolVar(&researchModeJSON, "json", false,
		"emit the decision as JSON instead of a human-readable line")
	rootCmd.AddCommand(researchModeCmd)
}

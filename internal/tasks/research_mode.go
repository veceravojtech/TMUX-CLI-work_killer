package tasks

import "fmt"

// ResearchMode is the branch /tmux:feature Stage-1 substep 1.2b takes when
// sizing its context research: run it inline in-window, or spawn the parallel
// investigator fan-out.
type ResearchMode string

const (
	// ResearchModeInline runs the research in-window with zero spawned workers.
	ResearchModeInline ResearchMode = "inline"
	// ResearchModeSpawn fans out read-only investigator workers.
	ResearchModeSpawn ResearchMode = "spawn"
)

// Inline thresholds are sized against the IMPLIED CHANGE a brief describes, not
// the LOC of the candidate files that change lives in (task 474): a tiny edit
// inside a huge file must still inline.
const (
	// ResearchModeInlineMaxChangedLines is the inclusive upper bound on a
	// brief's implied changed lines for the inline branch.
	ResearchModeInlineMaxChangedLines = 500
	// ResearchModeInlineMaxFiles is the inclusive upper bound on a brief's
	// implied touched files for the inline branch.
	ResearchModeInlineMaxFiles = 5
)

// ResearchModeInput is the set of facts the caller (the model, from the brief)
// EXTRACTS; ComputeResearchMode owns which branch they produce. The model never
// judges the branch in prose — it supplies these facts and reads the decision.
type ResearchModeInput struct {
	// NamedFiles are the concrete files the brief names (e.g. "auth.go").
	NamedFiles []string
	// NamedSymbols are the concrete symbols the brief names (e.g. "Login").
	NamedSymbols []string
	// HasConcreteEdit is true when the brief describes a specific edit to make.
	HasConcreteEdit bool
	// Measurable is false when the implied change cannot be estimated (the CLI
	// sentinel --implied-lines < 0) — the deterministic "couldn't measure"
	// signal that fails safe to spawn.
	Measurable bool
	// ImpliedChangedLines estimates the lines the change adds/modifies.
	ImpliedChangedLines int
	// ImpliedTouchedFiles estimates the number of files the change touches.
	ImpliedTouchedFiles int
	// CandidateFileLOC is the total LOC of the files the change lives in. It is
	// carried ONLY to prove it is IGNORED on the precise branch (task 474).
	CandidateFileLOC int
}

// ResearchModeDecision is the computed branch plus the facts that drove it.
type ResearchModeDecision struct {
	// Mode is the chosen branch (inline or spawn).
	Mode ResearchMode
	// Precise is true when the precise short-circuit fired (named files +
	// symbols + a concrete edit) — candidate-file LOC was ignored.
	Precise bool
	// Reason is a one-line human-readable rationale for the XML LOG line.
	Reason string
}

// ComputeResearchMode decides inline vs spawn from the extracted brief facts.
//
// The ORDER is load-bearing:
//  1. Precise short-circuit: a brief naming concrete file(s) + symbol(s) + a
//     concrete edit inlines regardless of CandidateFileLOC (a tiny edit in a
//     huge file must not spawn).
//  2. Fail-safe: an unmeasurable brief spawns — never under-provision. This is
//     checked AFTER the short-circuit so a precise brief with no numeric
//     estimate still inlines.
//  3. Size by implied change: inline when the implied change is within BOTH
//     thresholds, else spawn.
func ComputeResearchMode(in ResearchModeInput) ResearchModeDecision {
	// (1) Precise short-circuit — candidate-file LOC is deliberately ignored.
	if len(in.NamedFiles) > 0 && len(in.NamedSymbols) > 0 && in.HasConcreteEdit {
		return ResearchModeDecision{
			Mode:    ResearchModeInline,
			Precise: true,
			Reason:  "precise brief (named files + symbols + concrete edit) — candidate-file LOC ignored",
		}
	}

	// (2) Fail-safe — an unmeasurable brief must spawn.
	if !in.Measurable {
		return ResearchModeDecision{
			Mode:    ResearchModeSpawn,
			Precise: false,
			Reason:  "unmeasurable brief (no implied-change estimate) — fail-safe to spawn",
		}
	}

	// (3) Size by implied change (both bounds inclusive).
	if in.ImpliedChangedLines <= ResearchModeInlineMaxChangedLines && in.ImpliedTouchedFiles <= ResearchModeInlineMaxFiles {
		return ResearchModeDecision{
			Mode:    ResearchModeInline,
			Precise: false,
			Reason: fmt.Sprintf("implied change %d lines / %d files within inline threshold (%d lines / %d files)",
				in.ImpliedChangedLines, in.ImpliedTouchedFiles, ResearchModeInlineMaxChangedLines, ResearchModeInlineMaxFiles),
		}
	}

	return ResearchModeDecision{
		Mode:    ResearchModeSpawn,
		Precise: false,
		Reason: fmt.Sprintf("implied change %d lines / %d files exceeds inline threshold (%d lines / %d files)",
			in.ImpliedChangedLines, in.ImpliedTouchedFiles, ResearchModeInlineMaxChangedLines, ResearchModeInlineMaxFiles),
	}
}

package taskvisor

import (
	"fmt"
	"sort"
)

// Inline-plan modes returned by InlinePlan.
const (
	InlineModeInline = "inline"
	InlineModeFanout = "fanout"
)

// InlinePlan is the read-only decision seam of the B9b inline validation
// fast-path. It mirrors C10's PlanRevalidation pattern: a deterministic,
// unit-testable Go function so investigate.xml only branches on its JSON output.
//
// It first derives the RERUN set via PlanRevalidation (so the C10 reuse gate is
// honoured, never bypassed), then returns mode="inline" iff ALL of:
//   - the RERUN set is non-empty (an all-REUSE set spawns nothing already), and
//   - every RERUN investigator satisfies IsPureCommand (B9a) — all-or-nothing;
//     a single reasoning investigator fans the whole set out.
//
// Otherwise it returns mode="fanout" with a human-readable reason. The rerun
// slice is the sorted set of RERUN finding ids in every branch, so the output is
// byte-stable for identical input. REUSE investigators (C10) are intentionally
// absent from rerun — they carry forward as pass without execution.
func InlinePlan(investigators []Investigator, prev *Results, findings []ValidationFinding, changedFiles []string, cycleN int, forceFull, finalCycle bool) (mode string, rerun []string, reason string) {
	plans := PlanRevalidation(prev, findings, changedFiles, forceFull, finalCycle)

	rerun = make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Action == ActionRerun {
			rerun = append(rerun, p.FindingID)
		}
	}
	sort.Strings(rerun)

	// Empty RERUN set (all REUSE): the existing flow spawns nothing and the
	// aggregation carries REUSE forward as pass. Nothing to inline.
	if len(rerun) == 0 {
		return InlineModeFanout, rerun, "no RERUN investigators"
	}

	// No cycle gate: inline runs AFTER C10 partitioning, so on retry cycles the
	// RERUN set is the already-minimized remainder — when it is all pure-command,
	// in-window execution is exactly as safe as on cycle 1 and avoids the worker
	// spawn overhead that can blow the daemon's validate envelope (goal-061
	// post-mortem: a 2-command retry validation forced through fan-out took ~17 min
	// vs <1 min inline).

	// All-or-nothing pure-command gate. A missing investigator config for a RERUN
	// finding (e.g. a rule-based finding seeded from the prior ledger) cannot be
	// proven pure-command, so it conservatively forces fan-out.
	byName := make(map[string]Investigator, len(investigators))
	for _, inv := range investigators {
		byName[inv.Name] = inv
	}
	for _, id := range rerun {
		inv, ok := byName[id]
		if !ok {
			return InlineModeFanout, rerun, fmt.Sprintf("RERUN finding %q has no investigator config — cannot prove pure-command", id)
		}
		if !IsPureCommand(inv) {
			return InlineModeFanout, rerun, fmt.Sprintf("RERUN investigator %q is not pure-command (type=%q) — needs a reasoning worker", id, inv.Type)
		}
	}

	return InlineModeInline, rerun, fmt.Sprintf("all %d RERUN investigators are pure-command (cycle %d)", len(rerun), cycleN)
}

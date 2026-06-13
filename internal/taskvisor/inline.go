package taskvisor

import (
	"fmt"
	"sort"
)

// InlinePlan is the read-only decision seam of the inline/spawn validation
// SPLIT. It mirrors C10's PlanRevalidation pattern: a deterministic,
// unit-testable Go function so the `tmux-cli taskvisor inline-plan` CLI (and
// investigate.xml, which applies the same type-based split per-investigator) can
// branch on its JSON output without re-deriving the rule.
//
// It first derives the RERUN set via PlanRevalidation (so the C10 reuse gate is
// honoured, never bypassed), then PARTITIONS that RERUN set per-investigator:
//   - inline = the RERUN investigators that satisfy IsPureCommand — static
//     analysis / pure exit-code checks (build/lint/test/grep/vet/deptrac). These
//     run in-window, with NO worker spawn.
//   - spawn  = every other RERUN investigator — a reasoning/advanced check
//     (code-review, e2e-test/Chrome, integration-test), an investigator with a
//     semantic Pass, an unknown/missing config (cannot be proven pure-command),
//     or a deterministic type with no command. These need a reasoning worker.
//
// This is a SPLIT, NOT all-or-nothing: a goal mixing static analysis with a
// code-review/e2e investigator returns the static checks in `inline` AND the
// advanced ones in `spawn` — they run concurrently. REUSE investigators (C10)
// are in NEITHER set; they carry forward as pass without execution. Both slices
// are sorted, so the output is byte-stable for identical input.
//
// No cycle gate: the partition runs AFTER C10 partitioning, so on retry cycles
// the RERUN set is the already-minimized remainder — inlining its pure-command
// members is exactly as safe as on cycle 1 and avoids the worker-spawn overhead
// that can blow the daemon's validate envelope (goal-061 post-mortem: a
// 2-command retry validation forced through fan-out took ~17 min vs <1 min
// inline). cycleN is informational only (it annotates the reason string).
func InlinePlan(investigators []Investigator, prev *Results, findings []ValidationFinding, changedFiles []string, cycleN int, forceFull, finalCycle bool) (inline []string, spawn []string, reason string) {
	plans := PlanRevalidation(prev, findings, changedFiles, forceFull, finalCycle)

	byName := make(map[string]Investigator, len(investigators))
	for _, inv := range investigators {
		byName[inv.Name] = inv
	}

	inline = make([]string, 0, len(plans))
	spawn = make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Action != ActionRerun {
			continue // REUSE investigators are in neither set (carry forward as pass).
		}
		// A RERUN finding whose investigator config is present AND pure-command
		// runs inline. Everything else spawns: a reasoning/advanced type, a
		// semantic Pass, OR a finding with no investigator config (e.g. seeded
		// from the prior ledger) which cannot be proven pure-command —
		// false-inlining a check that needs reasoning is the only unsafe
		// direction, so the partition fails toward spawn.
		if inv, ok := byName[p.FindingID]; ok && IsPureCommand(inv) {
			inline = append(inline, p.FindingID)
		} else {
			spawn = append(spawn, p.FindingID)
		}
	}
	sort.Strings(inline)
	sort.Strings(spawn)

	switch {
	case len(inline) == 0 && len(spawn) == 0:
		reason = "no RERUN investigators"
	case len(spawn) == 0:
		reason = fmt.Sprintf("all %d RERUN investigator(s) pure-command — run inline (cycle %d)", len(inline), cycleN)
	case len(inline) == 0:
		reason = fmt.Sprintf("all %d RERUN investigator(s) need a reasoning worker — spawn (cycle %d)", len(spawn), cycleN)
	default:
		reason = fmt.Sprintf("split: %d pure-command inline, %d reasoning/advanced spawned (cycle %d)", len(inline), len(spawn), cycleN)
	}
	return inline, spawn, reason
}

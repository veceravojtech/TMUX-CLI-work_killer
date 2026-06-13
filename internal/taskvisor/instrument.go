package taskvisor

import (
	"fmt"
	"log"
	"time"
)

// instrument.go — per-goal/per-cycle cost instrumentation (B7).
//
// Every token-saving claim in the hardening plan (C10 reuse gate, bounded
// retries, cycle skipping) needs a greppable per-cycle cost record. This file
// owns ONE stable structured line — prefixed "COUNTERS" — emitted at two
// daemon-owned seams already in scope: cycle-start (dispatch/dispatchRetry) and
// goal-transition (checkValidatingPhase, after the verdict is resolved).
//
// Design: formatCounterLine is the single source of truth for the format; every
// emit goes through it. No metrics deps — just the stdlib log. Always-on and
// cheap (one log.Printf per transition); emits zeros rather than blocking when a
// count is unavailable.

// formatCounterLine is the single source of truth for the COUNTERS line format.
// The leading "COUNTERS " prefix plus space-separated key=value tokens keep it
// greppable (`grep 'COUNTERS '`) and trivially parseable (split on spaces, then
// on '='). cycleWallS/goalWallS are emitted as integer seconds (%.0f) — sub-
// second precision is noise for cost analysis and would only destabilise greps.
func formatCounterLine(goalID string, cycle int, phase, event string,
	consumedCode, consumedSpec, consumedVal, invSpawned, invReused, invInlined int,
	cycleWallS, goalWallS float64) string {
	return fmt.Sprintf("COUNTERS goal=%s cycle=%d phase=%s event=%s "+
		"retries_code=%d retries_spec=%d retries_val=%d "+
		"inv_spawned=%d inv_reused=%d inv_inlined=%d cycle_wall_s=%.0f goal_wall_s=%.0f",
		goalID, cycle, phase, event, consumedCode, consumedSpec, consumedVal,
		invSpawned, invReused, invInlined, cycleWallS, goalWallS)
}

// countInvFindings partitions a validator signal's findings three ways:
// spawned, reused, inlined. A finding carrying ReusedFromCycle != 0 was served
// by C10's reuse gate (no investigator re-spawned), so it counts as reused —
// the reuse check runs FIRST so a malformed double tag (reuse marker + inline
// marker) still counts as reused, keeping existing reuse cycles byte-identical.
// A finding tagged ValidationMode == ValidationModeInline was produced
// in-window by the B9b inline route (zero spawns), so it counts as inlined.
// Everything else (untagged) is counted as a fresh spawn. This reflects ACTUAL
// spawns rather than the investigator-config count — a reused or inlined
// investigator is never launched, so it must never be counted under inv_spawned.
func countInvFindings(fs []ValidationFinding) (spawned, reused, inlined int) {
	for _, f := range fs {
		switch {
		case f.ReusedFromCycle != 0:
			reused++
		case f.ValidationMode == ValidationModeInline:
			inlined++
		default:
			spawned++
		}
	}
	return
}

// goalWallSeconds returns the goal's wall-clock duration in seconds: StartedAt →
// (FinishedAt if set, else now). It returns 0 when StartedAt is empty (the goal
// never dispatched) or unparseable, so the line is always emittable. Mirrors
// goalDuration (diagnostics.go) but yields a numeric seconds value for grepping.
func goalWallSeconds(g *Goal) float64 {
	if g.StartedAt == "" {
		return 0
	}
	start, err := time.Parse(time.RFC3339, g.StartedAt)
	if err != nil {
		return 0
	}
	end := time.Now()
	if g.FinishedAt != "" {
		if parsed, perr := time.Parse(time.RFC3339, g.FinishedAt); perr == nil {
			end = parsed
		}
	}
	return end.Sub(start).Seconds()
}

// logCounters emits exactly one COUNTERS line for the given goal/event. It is
// side-effect-only: it reads goal budgets, the daemon phase, and the cycle clock
// but mutates nothing, so wiring it into dispatch/checkValidatingPhase changes no
// scheduling behaviour. Consumed retries-by-class are Max−remaining (the live
// per-class counters hold the REMAINING budget). cycle_wall_s is guarded against
// a zero dispatch clock (recovery edge) so it never reports a bogus huge value.
func (d *Daemon) logCounters(g *Goal, event string, spawned, reused, inlined int) {
	rt := d.runtime(g.ID)
	cycleWall := 0.0
	if !rt.dispatchTime.IsZero() {
		cycleWall = time.Since(rt.dispatchTime).Seconds()
	}
	log.Print(formatCounterLine(
		g.ID, CurrentCycle(g), phaseName(rt.phase), event,
		g.MaxCodeRetries-g.CodeRetries,
		g.MaxSpecRetries-g.SpecRetries,
		g.MaxValidationRetries-g.ValidationRetries,
		spawned, reused, inlined, cycleWall, goalWallSeconds(g)))
}

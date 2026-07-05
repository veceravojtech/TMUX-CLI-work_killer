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
//
// Scope of the reuse counter is REVALIDATION-ONLY: C10 reuse (PlanRevalidation,
// signal.go) serves a PRIOR CYCLE of the SAME goal — a REUSE entry carries that
// goal's earlier ReusedFromCycle, and a fresh goal / first cycle (prev == nil)
// yields all RERUN. Reuse is NEVER drawn from a sibling or cross-goal candidate
// set. Consequently a first-cycle inv_reused=0, or an inv_reused=0 that persists
// across consecutive same-shaped sibling goals, is BY-DESIGN — not a broken
// reuse gate. See goalmd.go C10 ("incremental re-validation reuses a PRIOR
// CYCLE's pass") and logReuseDecision below, which makes this zero legible in
// the daemon log.
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

// logReuseDecision makes a by-design inv_reused=0 legible. Like logCounters it is
// side-effect-only (reads g.ID + the already-counted spawned/reused, mutates
// nothing). It emits ONE line only for the spawn-only case (spawned > 0 &&
// reused == 0): investigators ran but none reused, which an operator could
// mistake for a broken reuse gate. The line names the reuse counter's
// revalidation-only scope — C10 reuse serves a PRIOR CYCLE of the SAME goal
// (ReusedFromCycle), never a sibling/cross-goal candidate — so the zero reads as
// by-design. It is intentionally NOT prefixed with "COUNTERS ": that token is
// reserved for the single counter line and greps key on it, so this reason line
// must never collide with it. Silent when reuse engaged (reused > 0 — the
// interesting non-zero case needs no note) or when nothing spawned.
func (d *Daemon) logReuseDecision(g *Goal, spawned, reused int) {
	if spawned == 0 || reused > 0 {
		return // reuse engaged, or nothing spawned — no reason to explain
	}
	log.Printf("%s: inv reuse scope=revalidation-only — %d spawned, 0 reused; "+
		"C10 reuse serves a PRIOR CYCLE of the SAME goal (ReusedFromCycle), never a "+
		"sibling/cross-goal candidate (no-live-candidate), so inv_reused=0 here is by-design",
		g.ID, spawned)
}

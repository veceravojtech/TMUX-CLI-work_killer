package taskvisor

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// stallWatchdogTicks is the number of consecutive idle-but-runnable ticks the
// stall watchdog tolerates before logging a single STUCK: line (~15-30s at the
// 5-10s poll cadence). A package constant by design — this is a diagnostics
// signal that should never fire in healthy operation, so it gets no setting.yaml
// surface (a config key would only invite tuning a never-fire alarm).
const stallWatchdogTicks = 3

func goalDuration(goal *Goal) string {
	if goal.StartedAt == "" || goal.FinishedAt == "" {
		return ""
	}
	start, err := time.Parse(time.RFC3339, goal.StartedAt)
	if err != nil {
		return ""
	}
	end, err := time.Parse(time.RFC3339, goal.FinishedAt)
	if err != nil {
		return ""
	}
	return end.Sub(start).Round(time.Second).String()
}

func phaseName(p phase) string {
	switch p {
	case phaseSupervising:
		return "supervising"
	case phaseValidating:
		return "validating"
	default:
		return "idle"
	}
}

// checkInvariant logs the literal Bug-A incident signature: a non-terminal goal
// still BlockedBy an id whose goal is GoalDone, post-reconcile. After M1's
// ReconcileBlocks runs this should be unreachable, so a hit is a reconcile
// regression. Diagnostics only — it NEVER mutates Status/BlockedBy/budgets and
// never touches dispatch. Excludes legitimate holds (precondition park, the
// convergence-circuit-breaker sentinel) and only flags BlockedBy values that
// name a real goal whose Status==GoalDone.
func (d *Daemon) checkInvariant(goals *GoalsFile) {
	var ids []string
	for i := range goals.Goals {
		g := &goals.Goals[i]
		switch g.Status {
		case GoalDone, GoalFailed, GoalRunning:
			continue
		}
		if g.BlockedByPrecondition {
			continue
		}
		if g.BlockedBy == "convergence-circuit-breaker" {
			continue
		}
		if g.BlockedBy == "" {
			continue
		}
		if goals.statusOf(g.BlockedBy) == GoalDone {
			ids = append(ids, g.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	const maxShown = 10
	n := len(ids)
	shown := ids
	suffix := ""
	if n > maxShown {
		shown = ids[:maxShown]
		suffix = fmt.Sprintf(" (+%d more)", n-maxShown)
	}
	log.Printf("INVARIANT VIOLATION: %d goal(s) blocked by a done goal post-reconcile: %s%s",
		n, strings.Join(shown, ", "), suffix)
}

// checkStall is the stall watchdog. It has two independent detection branches,
// each emitting at most one STUCK: line per episode:
//
//   - Terminal final-gate deadlock: empty RunnableCandidates AND a
//     phase=final_gate goal blocked behind a GoalFailed dep. A failed blocker is
//     unrecoverable (no retry, no in-flight worker clears it — only
//     `taskvisor goal reset <id>`), so this branch fires even while AnyRunning,
//     unlike the idle-tick path. Debounced by d.finalGateStuckReported, which
//     self-clears here whenever the signature is absent (a candidate appears or
//     the blocker leaves GoalFailed) — no edits at the dispatch/deactivate reset
//     sites needed.
//   - Idle-tick stall (preserved): the daemon stays idle for stallWatchdogTicks
//     consecutive ticks while a runnable candidate exists — the silent-deadlock
//     signature. A worker mid-flight (AnyRunning) or no runnable candidate at all
//     is legitimate, so d.idleTicks/d.stallReported reset and it never fires in
//     those cases. dispatch/dispatchRetry/deactivate also reset them, so a
//     normally-dispatching tick increments then resets within the same tick
//     (net 0). One STUCK: per episode (gated by stallReported); a later
//     dispatch/deactivate clears the flag, allowing a fresh episode.
//
// Both branches are read-only — they NEVER mutate goal Status/BlockedBy.
func (d *Daemon) checkStall(goals *GoalsFile) {
	candidates := goals.RunnableCandidates()

	// Terminal final-gate deadlock branch (AnyRunning-agnostic, read-only).
	if len(candidates) == 0 {
		if blocker, n := goals.FinalGateBlockedByFailed(); n > 0 {
			if !d.finalGateStuckReported {
				log.Printf("STUCK: %d final-gate(s) blocked by failed %s — run 'taskvisor goal reset %s'", n, blocker, blocker)
				d.finalGateStuckReported = true
			}
		} else {
			d.finalGateStuckReported = false
		}
	} else {
		d.finalGateStuckReported = false
	}

	if goals.AnyRunning() || len(candidates) == 0 {
		d.idleTicks = 0
		d.stallReported = false
		return
	}
	d.idleTicks++
	if d.idleTicks >= stallWatchdogTicks && !d.stallReported {
		ids := make([]string, len(candidates))
		for i, g := range candidates {
			ids[i] = g.ID
		}
		log.Printf("STUCK: daemon idle %d ticks with %d runnable goal(s): %s",
			d.idleTicks, len(candidates), strings.Join(ids, ", "))
		d.stallReported = true
	}
}

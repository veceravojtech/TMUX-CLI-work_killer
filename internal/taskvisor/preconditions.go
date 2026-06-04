package taskvisor

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// preconditionDialTimeout bounds the TCP reachability probe for service
// preconditions. Kept short so a real outage does not stall the poll loop;
// transient-retry/backoff is a separate concern (C4), not handled here.
const preconditionDialTimeout = 2 * time.Second

// evaluatePreconditions checks a goal's declared preconditions before dispatch.
// An empty slice is all-pass. It short-circuits on the first failure, returning
// the failure class (env-config|infra-flake|spec-defect) and the failing
// precondition's remedy. env: unset OR empty fails. service: a TCP dial to
// host:port that errors within the timeout fails. Any unknown kind fails as a
// spec defect.
func (d *Daemon) evaluatePreconditions(goal *Goal) (success bool, class, remedy string) {
	for _, p := range goal.Preconditions {
		switch p.Kind {
		case "env":
			if v, ok := os.LookupEnv(p.Spec); !ok || v == "" {
				return false, "env-config", p.Remedy
			}
		case "service":
			conn, err := net.DialTimeout("tcp", p.Spec, preconditionDialTimeout)
			if err != nil {
				return false, "infra-flake", p.Remedy
			}
			_ = conn.Close()
		default:
			return false, "spec-defect", p.Remedy
		}
	}
	return true, "", ""
}

// preconditionClass maps a precondition kind to its failure class.
func preconditionClass(kind string) string {
	switch kind {
	case "env":
		return "env-config"
	case "service":
		return "infra-flake"
	default:
		return "spec-defect"
	}
}

// ownerFor maps a failure class to the party responsible for remediation.
func ownerFor(class string) string {
	if class == "spec-defect" {
		return "planner"
	}
	return "ops"
}

// failingPreconditionSpec re-identifies the spec of the first precondition that
// produced the given (class, remedy) pair, so the block signal/log can name it.
// evaluatePreconditions short-circuits on the first failure, so this matches
// that same precondition without re-running the (possibly networked) check.
func failingPreconditionSpec(goal *Goal, class, remedy string) string {
	for _, p := range goal.Preconditions {
		if preconditionClass(p.Kind) == class && p.Remedy == remedy {
			return p.Spec
		}
	}
	return ""
}

// scanPreconditionBlocked re-evaluates every goal flagged BlockedByPrecondition,
// cross-checked against its latest signal.json class (env-config / infra-flake) so
// it never blindly re-probes unrelated goals. For a matching goal it re-runs C3's
// evaluatePreconditions; when ALL preconditions pass it clears the block, sets the
// goal GoalPending and lets the dispatch loop re-validate it (no retry budget is
// consumed). A goal whose preconditions still fail is left flagged for the next
// tick. All mutations run under WithGoalsLock to serialize against the dispatch
// loop and operator edits; this loop is a SEPARATE goroutine from poll, so the
// flock provides mutual exclusion (no re-entrancy, no deadlock).
func (d *Daemon) scanPreconditionBlocked() {
	err := d.withGoalsLock(func() error {
		goals, err := LoadGoals(d.workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if goals == nil {
			return nil
		}
		changed := false
		for i := range goals.Goals {
			g := &goals.Goals[i]
			if !g.BlockedByPrecondition {
				continue
			}
			if !d.preconditionParkEligible(g) {
				continue
			}
			ok, _, _ := d.evaluatePreconditions(g)
			if !ok {
				// Still failing — leave flagged, re-poll next tick.
				continue
			}
			d.clearBlock(g)
			g.Status = GoalPending
			changed = true
			log.Printf("%s: precondition cleared — resuming (blocked -> pending) for re-validation", g.ID)
		}
		if !changed {
			return nil
		}
		return SaveGoals(d.workDir, goals)
	})
	if err != nil {
		log.Printf("scanPreconditionBlocked: %v", err)
	}
}

// latestSignalIsPreconditionClass reports whether the goal's latest signal.json
// classifies as an env/infra precondition hold (env-config / infra-flake) — the
// cross-check that gates auto-resume. It accepts either the signal's top-level
// Class (set by the preflight gate) or a non-pass finding's FailureClass (set by
// the validator), so both block sources are recognized. A missing/unreadable or
// non-validator signal returns false (never auto-resume on ambiguous state).
func (d *Daemon) latestSignalIsPreconditionClass(goalID string) bool {
	loaded, err := LoadSignal(d.workDir, goalID)
	if err != nil || loaded == nil {
		return false
	}
	valSig, ok := loaded.(*ValidatorSignal)
	if !ok {
		return false
	}
	isPrecond := func(c string) bool { return c == "env-config" || c == "infra-flake" }
	if isPrecond(valSig.Class) {
		return true
	}
	for _, f := range valSig.Findings {
		if f.Status != VerdictPass && isPrecond(f.FailureClass) {
			return true
		}
	}
	return false
}

// preconditionParkEligible reports whether a BlockedByPrecondition goal should be
// re-evaluated by the §5 resume loop. It resumes on EITHER (a) a readable
// precondition-class signal, OR (b) an absent/unreadable signal when the daemon's
// own BlockedBy=="env_precondition" discriminator is set (the validation-route
// park that pre-fix never wrote a signal — recovers already-stranded goals). A
// readable NON-precondition signal returns false. LoadSignal is called directly
// here because latestSignalIsPreconditionClass conflates absent/unreadable with
// non-precondition (both false), so it cannot be negated to tell them apart.
func (d *Daemon) preconditionParkEligible(g *Goal) bool {
	loaded, err := LoadSignal(d.workDir, g.ID)
	if err != nil || loaded == nil {
		// (b) absent/unreadable — trust the daemon's own flag.
		return g.BlockedBy == "env_precondition"
	}
	// (a) signal present — resume only on a precondition class.
	return d.latestSignalIsPreconditionClass(g.ID)
}

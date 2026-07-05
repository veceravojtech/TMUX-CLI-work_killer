package taskvisor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/console/tmux-cli/internal/setup"
)

// plannext.go is the incremental planning loop (taskvisor.planning_mode ==
// "incremental"): instead of executing a pre-populated full roadmap, the daemon
// drives one-goal-at-a-time — dispatch the goal generator
// (/tmux:task-plan-generate incremental) to author exactly ONE concrete goal
// appended to goals.yaml, run that goal through the unchanged goal state
// machine, then re-invoke the generator (review + next) once it is terminal.
// The generator alternatively writes the product-complete marker instead of a
// goal, which ends the run through the same terminal path as "all goals done".
// Every entry point below is gated on incrementalPlanning(), so roadmap mode is
// byte-identical to the pre-incremental build.

const (
	// planNextWindow is the generator's tmux window name. It is daemon-global
	// (not goal-namespaced) because the generator is not tied to a goal — it
	// AUTHORS the next one — and at most one episode is ever open (the
	// incremental loop is strictly one-at-a-time).
	planNextWindow = "plan-next"

	// incrementalMaxGoals caps how many goals one incremental run may author —
	// the runaway guard against a generator that never concludes the product is
	// complete. On the cap the daemon deactivates loudly instead of spinning.
	incrementalMaxGoals = 40

	// incrementalFailureLimit is K: an unbroken tail of K consecutively-authored
	// failed goals means no forward progress (failure does NOT cascade here —
	// there is no pre-planned downstream — but an all-failing streak must not
	// loop), so the daemon deactivates for operator review.
	incrementalFailureLimit = 3

	// planNextTimeout bounds one generator episode, mirroring elaborationTimeout:
	// a generator that has neither authored a goal nor written the product-
	// complete marker within this window is wedged and its episode is failed.
	planNextTimeout = 20 * time.Minute

	// planNextAttemptLimit bounds consecutive FAILED generator episodes
	// (crash/timeout without output). Under the limit the next tick re-dispatches;
	// at the limit the daemon deactivates with a loud halt reason.
	planNextAttemptLimit = 3
)

// planNextState is the daemon-global in-flight generator episode. A struct (not
// a goalRuntime entry) because the episode has no goal ID yet — the goal is its
// OUTPUT. attempts survives across episodes (it counts consecutive failures and
// resets on any successful episode); the rest describes the open episode only.
type planNextState struct {
	inFlight     bool
	dispatchedAt time.Time
	// baselineGoals snapshots len(goals.Goals) at dispatch; the episode is
	// complete when the ledger grows past it (the generator's goal-create landed).
	baselineGoals int
	attempts      int
}

// incrementalPlanning reports whether the daemon runs the incremental planning
// loop. planningMode is seeded from Settings.Taskvisor.PlanningMode in Run()
// (already load-coerced to roadmap|incremental by setup.LoadSettings — no
// re-validation here); the zero value ("", literal-constructed daemons and
// every pre-existing test) is roadmap, keeping roadmap byte-identical.
func (d *Daemon) incrementalPlanning() bool {
	return d.planningMode == setup.PlanningModeIncremental
}

// productCompletePath is the generator-written product-done marker. Presence =
// the product is complete; the daemon only ever reads it (the marker is KEPT on
// deactivation — it is the durable product-done signal the generator's own
// no-op guard keys on; an operator removes it to resume product development).
func (d *Daemon) productCompletePath() string {
	return filepath.Join(d.workDir, ".tmux-cli", "taskvisor-product-complete")
}

func (d *Daemon) productComplete() bool {
	_, err := os.Stat(d.productCompletePath())
	return err == nil
}

// hasDiscoveryEvidence reports whether a prior product-discovery plan exists —
// any of docs/architecture/{product-brief.md,bounded-contexts.md,api-endpoints.md}
// under d.workDir. Option A (task 412) gates incremental generation on this
// discovery evidence so an all-terminal ledger with NO product-brief / product
// spec idles instead of dispatching a generator with nothing to ground on.
// Mirrors productComplete()'s os.Stat/err==nil idiom (resolved via d.workDir the
// same way productCompletePath does): an absent or unreadable artifact is simply
// "not present" — the stat error is swallowed, never surfaced.
func (d *Daemon) hasDiscoveryEvidence() bool {
	for _, name := range []string{"product-brief.md", "bounded-contexts.md", "api-endpoints.md"} {
		if _, err := os.Stat(filepath.Join(d.workDir, "docs", "architecture", name)); err == nil {
			return true
		}
	}
	return false
}

// trailingConsecutiveFailures counts the UNBROKEN GoalFailed tail of the ledger
// (goals.yaml is append-only under incremental authoring, so file order is
// authoring order). Any non-failed goal breaks the streak.
func trailingConsecutiveFailures(gf *GoalsFile) int {
	n := 0
	for i := len(gf.Goals) - 1; i >= 0; i-- {
		if gf.Goals[i].Status != GoalFailed {
			break
		}
		n++
	}
	return n
}

// goalsAllTerminal reports whether every authored goal is terminal (done or
// failed) — the state in which the next goal may be generated. An empty ledger
// is vacuously terminal (generate goal-001). A GoalBlocked goal is deliberately
// NOT terminal: it is human-owned (circuit breaker) or auto-resumable
// (precondition park), and generating past it would churn goals against an
// unresolved halt.
func goalsAllTerminal(gf *GoalsFile) bool {
	for i := range gf.Goals {
		if s := gf.Goals[i].Status; s != GoalDone && s != GoalFailed {
			return false
		}
	}
	return true
}

// incrementalShouldGenerate is the pure next-goal decision: generate only when
// the product-complete marker is absent, the runaway guards hold, every authored
// goal is terminal, AND discovery evidence exists (Option A, task 412 — a
// terminal ledger with no product spec has nothing to generate against and must
// idle). Shared by the tick's no-work arm (planNextOrComplete) and
// advanceToNextGoal's stay-active gate so the two can never disagree.
func (d *Daemon) incrementalShouldGenerate(gf *GoalsFile) bool {
	return !d.productComplete() &&
		len(gf.Goals) < incrementalMaxGoals &&
		trailingConsecutiveFailures(gf) < incrementalFailureLimit &&
		goalsAllTerminal(gf) &&
		d.hasDiscoveryEvidence()
}

// planNextOrComplete is the incremental replacement for the tick's teardown arm
// (roadmap: deactivateOnCompletion): with nothing running and nothing runnable,
// either dispatch the generator for the next goal or end the run. Terminal
// arms route through deactivateOnCompletion — the SAME path "all goals done"
// takes today (completion report, failed-goal reporting, ALL-COMPLETE
// milestone) — with a loud haltReason on the runaway guards.
func (d *Daemon) planNextOrComplete(gf *GoalsFile) error {
	switch {
	case d.productComplete():
		log.Printf("incremental: product-complete marker present — deactivating as complete (marker kept)")
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:PRODUCT-COMPLETE goals=%d]", len(gf.Goals)))
		return d.deactivateOnCompletion(gf)
	case len(gf.Goals) >= incrementalMaxGoals:
		d.haltReason = fmt.Sprintf("HALTED: incremental cap reached (%d goals authored, cap %d) — review the run before restarting", len(gf.Goals), incrementalMaxGoals)
		log.Printf("incremental cap reached (%d goals) — deactivating", len(gf.Goals))
		return d.deactivateOnCompletion(gf)
	case trailingConsecutiveFailures(gf) >= incrementalFailureLimit:
		n := trailingConsecutiveFailures(gf)
		d.haltReason = fmt.Sprintf("HALTED: %d consecutive incremental goals failed — no forward progress; operator review required", n)
		log.Printf("incremental: %d consecutive authored goals failed — deactivating", n)
		return d.deactivateOnCompletion(gf)
	case !goalsAllTerminal(gf):
		// A non-terminal, non-runnable goal (e.g. circuit-breaker GoalBlocked) —
		// the same terminal path roadmap mode takes; deactivateOnCompletion's own
		// guards (resumable park / recoverable block) keep the daemon active when
		// the block is recoverable.
		return d.deactivateOnCompletion(gf)
	case !d.hasDiscoveryEvidence():
		// Option A (task 412): the ledger is genuinely terminal, under cap, not
		// failure-halted, and no product-complete marker (all earlier cases
		// excluded) — but there is NO product-discovery spec (docs/architecture)
		// to ground the generator on. Dispatching it would author nothing and
		// re-fail every idle tick, so idle via the same terminal path instead.
		log.Printf("incremental: all goals terminal but no discovery evidence (docs/architecture product spec) — idling instead of dispatching the generator")
		return d.deactivateOnCompletion(gf)
	default:
		return d.dispatchPlanNext(gf)
	}
}

// dispatchPlanNext spawns the generator window and sends
// /tmux:task-plan-generate incremental (dispatchcmd.go DispatchPlanNext — the
// literal `incremental` argument is the seam's primary mode signal). It mirrors
// dispatchElaborate's window plumbing minus every goal-scoped surface (no goal
// dir, no dispatch.md, no markers, no worktree — the generator runs against the
// base tree and reads goals.yaml/docs itself). The pre-kill makes a leftover
// generator window from a crashed/restarted daemon self-heal on re-dispatch.
func (d *Daemon) dispatchPlanNext(gf *GoalsFile) error {
	if err := d.killWindowByName(planNextWindow); err != nil {
		return err
	}
	if err := d.waitWindowsGone([]string{planNextWindow}, 5*time.Second); err != nil {
		return fmt.Errorf("plan-next: waitWindowsGone: %w", err)
	}
	winInfo, err := d.createWindow(planNextWindow, "", d.workDir)
	if err != nil {
		return fmt.Errorf("create plan-next window: %w", err)
	}
	if err := d.waitClaudeBoot(planNextWindow, 30*time.Second); err != nil {
		return fmt.Errorf("plan-next: waitClaudeBoot: %w", err)
	}
	if err := d.waitForPromptOrFail(planNextWindow, 30*time.Second); err != nil {
		return fmt.Errorf("plan-next: wait for prompt: %w", err)
	}
	cmd := dispatchCommand(DispatchPlanNext, DispatchArgs{})
	log.Printf("dispatchPlanNext: sending to session=%s window=%s cmd=%s", d.session, winInfo.TmuxWindowID, cmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, cmd); err != nil {
		return fmt.Errorf("send plan-next command: %w", err)
	}
	d.planNext.inFlight = true
	d.planNext.dispatchedAt = d.now()
	d.planNext.baselineGoals = len(gf.Goals)
	d.idleTicks = 0
	d.stallReported = false
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GENERATING goals=%d cap=%d]", len(gf.Goals), incrementalMaxGoals))
	return nil
}

// drivePlanNext advances the open generator episode. Exactly one signal ends it:
//
//   - product-complete marker written → deactivate as complete (planNextOrComplete);
//   - the ledger grew past the dispatch baseline (goal-create landed) → close the
//     episode; the NEW goal dispatches through the normal path on the NEXT tick
//     (a generation and the goal dispatch never share a tick, mirroring the
//     completion/dispatch tick separation);
//   - the window vanished or fell back to the shell with no output → failed
//     episode (failPlanNextEpisode);
//   - planNextTimeout elapsed with the window still grinding → kill it, failed
//     episode.
//
// Otherwise the episode is still in flight and the tick idles. While an episode
// is open the tick returns HERE, before the dispatch loop — the "never dispatch
// while a generator window is in flight" guard.
func (d *Daemon) drivePlanNext(gf *GoalsFile) error {
	if d.productComplete() {
		if err := d.killWindowByName(planNextWindow); err != nil {
			return err
		}
		d.planNext.inFlight = false
		d.planNext.attempts = 0
		log.Printf("incremental: generator wrote the product-complete marker")
		return d.planNextOrComplete(gf)
	}
	if len(gf.Goals) > d.planNext.baselineGoals {
		if err := d.killWindowByName(planNextWindow); err != nil {
			return err
		}
		d.planNext.inFlight = false
		d.planNext.attempts = 0
		log.Printf("incremental: generator authored goal %d/%d — dispatching next tick", len(gf.Goals), incrementalMaxGoals)
		return nil
	}
	// Crash detect: the generator finished only via the two signals above, so a
	// gone/at-shell window here produced nothing. A listing error is swallowed
	// (the timeout below still bounds the episode), mirroring the heartbeat.
	if windows, err := d.listWindows(); err == nil {
		alive := false
		for i := range windows {
			if windows[i].Name == planNextWindow {
				alive = windows[i].CurrentCommand != "zsh" && windows[i].CurrentCommand != ""
				break
			}
		}
		if !alive {
			return d.failPlanNextEpisode("generator window crashed or exited without authoring a goal or the product-complete marker")
		}
	}
	if !d.planNext.dispatchedAt.IsZero() && d.now().Sub(d.planNext.dispatchedAt) >= planNextTimeout {
		if err := d.killWindowByName(planNextWindow); err != nil {
			return err
		}
		return d.failPlanNextEpisode(fmt.Sprintf("generator produced nothing within %s", planNextTimeout))
	}
	return nil
}

// failPlanNextEpisode closes a failed generator episode. Under the attempt
// limit the daemon stays active — the next tick's no-work arm re-dispatches a
// fresh episode; at the limit it deactivates with a loud halt reason (the
// generator itself is broken and re-dispatching would spin).
func (d *Daemon) failPlanNextEpisode(reason string) error {
	d.planNext.inFlight = false
	d.planNext.attempts++
	if d.planNext.attempts >= planNextAttemptLimit {
		d.haltReason = fmt.Sprintf("HALTED: incremental generator failed %d consecutive episodes (%s) — fix /tmux:task-plan-generate and restart", d.planNext.attempts, reason)
		log.Printf("incremental: generator failed %d consecutive episodes (%s) — deactivating", d.planNext.attempts, reason)
		return d.deactivate()
	}
	log.Printf("incremental: generator episode failed (%s) — retrying (attempt %d/%d)", reason, d.planNext.attempts, planNextAttemptLimit)
	return nil
}

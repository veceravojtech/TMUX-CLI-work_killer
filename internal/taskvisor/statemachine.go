package taskvisor

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/setup"
)

// tick is the ready-set scheduler. Each invocation (under the poll flock):
//  1. ReconcileBlocks + checkInvariant + checkStall — whole-GoalsFile, unchanged.
//  2. Drive EVERY in-flight (GoalRunning) goal via its own per-goal runtime by
//     calling checkProgress(goal, goals) per running goal.
//  3. Size the free dispatch budget: free = maxGoals - (goals running AT TICK
//     START). Snapshotting the running set before step 2 means a goal that
//     completes during this tick does NOT free its slot until the next tick, so
//     a completion and the next dispatch never share a tick — the property that
//     makes MaxGoals=1 byte-identical to the old scalar-CurrentGoal switch.
//  4. Fill free slots from DisjointReadySet(maxGoals) — RunnableCandidates()
//     scope-gated so MaxGoals>1 never co-schedules two goals editing overlapping
//     files (byte-identical to RunnableCandidates' head at MaxGoals=1) — via the
//     shared per-candidate decision helper (dispatchCandidate: retry-ceiling halt
//     / dispatchRetry when code budget was consumed and a per-goal tasks.yaml
//     exists / else dispatch).
//  5. Teardown: when nothing is running and nothing is runnable, attempt
//     deactivateOnCompletion (which self-guards on resumable parks / recoverable
//     cascade blocks / AllResolved).
//
// At MaxGoals=1 the running set holds <=1 goal and free<=1, so the loop visits
// the same single goal through the same dispatch/checkProgress/advanceToNextGoal
// calls in the same order, and the scalar CurrentGoal still head-tracks via
// dispatchCandidate + advanceToNextGoal.
func (d *Daemon) tick(ctx context.Context, goals *GoalsFile) error {
	// Salvage late validator verdicts FIRST, before ReconcileBlocks: a late pass
	// flips a timeout-failed goal to done, and the reconcile right after then
	// un-sticks its cascade-blocked dependents in the SAME tick.
	if err := d.salvageLateVerdicts(goals); err != nil {
		return err
	}

	// Safety net (preserved from the scalar switch): a CurrentGoal naming a goal
	// that no longer exists means the active goal vanished out from under us —
	// tear down the idle supervisor surface and go idle. ReconcileBlocks does not
	// add/remove goals, so the existence check is stable across the heal below.
	if _, ok := goals.GoalByID(goals.CurrentGoal); !ok {
		return d.deactivate()
	}

	// Heal stale block-state before acting on it (Bug A + self-recovery on load).
	// This MUST self-persist here at the tick top: a GoalRunning->checkProgress
	// path can return nil WITHOUT a SaveGoals, so the reconcile mutation would be
	// discarded when the flock releases. Reconcile mutates &goals.Goals[i] in
	// place, so a re-pended goal is visible to RunnableCandidates below.
	if goals.ReconcileBlocks() {
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}

	// Diagnostics-only guardrails, run strictly AFTER reconcile (the only point
	// where the invariant should hold), BEFORE any dispatch. Neither mutates goal
	// state nor alters the dispatch decision below — checkInvariant only logs,
	// checkStall only advances its own counter.
	d.checkInvariant(goals)
	d.checkStall(goals)

	if err := d.checkStaleBinary(goals); err != nil {
		return err
	}
	if d.mode != modeActive {
		return nil
	}

	// (2) Drive every in-flight goal. Snapshot the running set FIRST so the free
	// budget in (3) reflects the tick-start in-flight count, not mid-tick
	// completions.
	runningAtStart := goals.RunningGoalIDs()
	for _, id := range runningAtStart {
		g, ok := goals.GoalByID(id)
		if !ok {
			continue
		}
		if err := d.checkProgress(g, goals); err != nil {
			return err
		}
	}

	// A checkProgress completion may have driven the goals to all-resolved and
	// already torn down via advanceToNextGoal -> deactivateOnCompletion (the
	// byte-identical MaxGoals=1 path). If so, the tick is done — never re-enter
	// teardown or dispatch against an idle daemon.
	if d.mode != modeActive {
		return nil
	}

	// (3) Free dispatch budget for this tick.
	free := d.maxGoals() - len(runningAtStart)

	// (3b) Per-goal wall-clock cost ceiling (P3). Evaluated ONCE per tick at the
	// dispatch boundary, immediately BEFORE the free-slot gate so a fully-saturated
	// daemon (free==0, every slot busy) still halts rather than waiting for a free
	// slot. The budget epoch is now PER-GOAL (goalRuntime.activatedAt, stamped at
	// dispatch), so each in-flight goal is measured against maxWallClock from ITS own
	// dispatch — goals running sequentially no longer share one daemon timer. We
	// iterate the running set fresh (post-checkProgress, so a goal that completed
	// earlier this tick is not halted) and halt the FIRST offender; any further
	// offenders are caught on subsequent ticks (keeps the tick simple, byte-identical
	// at MaxGoals=1). The IsZero() guard skips a never-stamped epoch (never halts).
	// Math routes through the P2 clock seam (d.now()) so it is injectable in tests.
	// Zero maxWallClock ⇒ DISABLED (no-op).
	if d.maxWallClock > 0 {
		for _, id := range goals.RunningGoalIDs() {
			rt := d.runtime(id)
			if rt.activatedAt.IsZero() {
				continue
			}
			if elapsed := d.now().Sub(rt.activatedAt); elapsed >= d.maxWallClock {
				return d.haltGoalWallClock(goals, id, elapsed)
			}
		}
	}

	// (4) Fill free slots from the SCOPE-GATED ready set, in goal-file order.
	// DisjointReadySet composes on top of RunnableCandidates: it returns the same
	// candidates but only those whose declared file Scope is disjoint from every
	// in-flight and already-admitted goal, capped at the dispatch budget. At
	// MaxGoals=1 it returns ≤1 goal (the same head RunnableCandidates would), so
	// the single-goal dispatch cadence is byte-identical; at MaxGoals>1 it is the
	// conservative gate that prevents co-scheduling two goals editing overlapping
	// files before per-goal worktree isolation (execute-33) exists. `free` remains
	// the authoritative budget bound (tick-start running count).
	if free > 0 {
		for _, cand := range goals.DisjointReadySet(d.maxGoals()) {
			if free <= 0 {
				break
			}
			// Global retry ceiling is checked per candidate (it is a whole-file
			// query); a hit halts that goal and advances, matching the old
			// per-branch ceiling guard. Halting returns from the tick.
			if goals.RetryCeilingReached() {
				return d.haltRetryCeiling(cand, goals)
			}
			if err := d.dispatchCandidate(cand, goals); err != nil {
				return err
			}
			free--
		}
	}

	// Stack-gate skip counter: after the dispatch loop, count runnable
	// stack-consuming candidates that were NOT admitted due to an in-flight
	// stack-consumer. Keeps DisjointReadySet pure (no side-channel metadata).
	d.stackGateSkips = 0
	if d.maxGoals() > 1 {
		admitted := make(map[string]bool, len(goals.DisjointReadySet(d.maxGoals())))
		for _, a := range goals.DisjointReadySet(d.maxGoals()) {
			admitted[a.ID] = true
		}
		var inflightStack bool
		for _, id := range goals.RunningGoalIDs() {
			if g, ok := goals.GoalByID(id); ok && isStackConsuming(g) {
				inflightStack = true
				break
			}
		}
		if !inflightStack {
			for _, a := range goals.DisjointReadySet(d.maxGoals()) {
				if isStackConsuming(a) {
					inflightStack = true
					break
				}
			}
		}
		if inflightStack {
			for _, c := range goals.RunnableCandidates() {
				if !admitted[c.ID] && isStackConsuming(c) {
					d.stackGateSkips++
					log.Printf("%s: stack-gated (runtime-resource co-scheduling guard)", c.ID)
				}
			}
		}
	}

	// (5) Teardown when no work is in flight and none is runnable. At MaxGoals=1
	// this is the same condition the old GoalDone/Failed branch reached via
	// NextPendingGoal()==false. deactivateOnCompletion self-guards on resumable
	// parks / recoverable cascade blocks / AllResolved, so a parked-but-not-done
	// goals.yaml stays active (the old GoalBlocked idle branch).
	if !goals.AnyRunning() && len(goals.RunnableCandidates()) == 0 {
		return d.deactivateOnCompletion(goals)
	}
	return nil
}

// dispatchCandidate is the single per-candidate dispatch decision shared by the
// single- and multi-goal scheduler paths. Decision order (RC-D):
//
//  1. NextDispatch == dispatchGeneration → full dispatch (planner
//     re-generation), REGARDLESS of code budget. The legacy heuristic below
//     keys on codeBudgetConsumed — a STICKY historical fact: once any code
//     retry was ever burned, every later dispatch (including a spec-defect
//     bounce) took dispatchRetry and re-executed the defective spec verbatim,
//     so the planner never saw it (test-project goal-064, 2026-06-04). The
//     explicit marker set by bounceToGeneration overrides that.
//  2. NextDispatch == dispatchImplementer AND a per-goal tasks.yaml exists →
//     dispatchRetry (reuse tasks.yaml, skip planning). A missing tasks.yaml
//     falls through to the heuristic, which routes to full dispatch.
//  3. Empty marker (legacy mid-flight goals.yaml) → the EXISTING heuristic,
//     unchanged: re-dispatch the implementer only when a prior cycle consumed
//     code-defect budget AND a per-goal tasks.yaml exists; otherwise a full
//     dispatch. (goal.Retries > 0 kept for fixtures / pre-migration goals
//     that still carry a legacy count.)
//
// The marker is consumed (cleared) inside dispatch/dispatchRetry once acted
// on, so it never leaks into the next cycle's decision.
//
// It also maintains the scalar CurrentGoal head: when the current head is not a
// running goal (e.g. it just completed, or this is the first dispatch of the
// tick), the dispatched goal becomes the head. At MaxGoals=1 the dispatched goal
// is always the head, so this reproduces the old switch's CurrentGoal tracking.
// The retry-ceiling halt is the caller's responsibility (it is a whole-file
// query and returns from the tick), matching the old per-branch guard order.
func (d *Daemon) dispatchCandidate(goal *Goal, goals *GoalsFile) error {
	if cur, ok := goals.GoalByID(goals.CurrentGoal); !ok || cur.Status != GoalRunning {
		goals.CurrentGoal = goal.ID
		d.currentGoal = goal.ID
	}
	if goal.NextDispatch == dispatchGeneration {
		return d.dispatch(goal, goals)
	}
	if goal.NextDispatch == dispatchImplementer && d.tasksYamlExists(goal.ID) {
		return d.dispatchRetry(goal, goals)
	}
	codeBudgetConsumed := goal.CodeRetries < goal.MaxCodeRetries
	if (goal.Retries > 0 || codeBudgetConsumed) && d.tasksYamlExists(goal.ID) {
		return d.dispatchRetry(goal, goals)
	}
	return d.dispatch(goal, goals)
}

func (d *Daemon) checkProgress(goal *Goal, goals *GoalsFile) error {
	switch d.runtime(goal.ID).phase {
	case phaseSupervising:
		return d.checkSupervisingPhase(goal, goals)
	case phaseValidating:
		return d.checkValidatingPhase(goal, goals)
	}
	return nil
}

func (d *Daemon) checkSupervisingPhase(goal *Goal, goals *GoalsFile) error {
	rt := d.runtime(goal.ID)
	mg := d.maxGoals()
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		// Ordering is load-bearing (P2): heartbeat → hard-timeout → crash-detect.
		// The heartbeat fires BEFORE the 1h hard timeout so a wedged-but-booted
		// supervisor is recovered in minutes, not after the full dispatchTimeout.
		// A heartbeat-internal error (e.g. a transient CaptureWindowOutput failure)
		// is logged and swallowed so the hard-timeout/crash branches still run — the
		// heartbeat is purely additive and never blocks the existing safety nets.
		if stuck, herr := d.checkProgressHeartbeat(rt, supervisorWindow(goal.ID, mg), executePrefix(goal.ID, mg)); herr != nil {
			log.Printf("%s: supervisor heartbeat check error: %v", goal.ID, herr)
		} else if stuck {
			return d.handleStuckSupervisor(goal, goals)
		}
		if !rt.dispatchTime.IsZero() && d.now().Sub(rt.dispatchTime) >= d.dispatchTimeout {
			return d.handleFailedCycle(goal, goals, "Cycle timed out — no completion signal received.", "code-defect")
		}
		if !rt.bootConfirmedAt.IsZero() && d.now().Sub(rt.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			supName := supervisorWindow(goal.ID, mg)
			found := false
			for _, w := range windows {
				if w.Name == supName {
					found = true
					if w.CurrentCommand == "zsh" {
						return d.handleFailedCycle(goal, goals, "Crash detected — supervisor returned to shell.", "code-defect")
					}
				}
			}
			if !found {
				return d.handleFailedCycle(goal, goals, "Crash detected — supervisor window vanished.", "code-defect")
			}
		}
		return nil
	}

	supSig, ok := sig.(*SupervisorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during supervising phase.", "code-defect")
	}

	rt.lastSupervisorStatus = supSig.Status
	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowsByPrefix(executePrefix(goal.ID, mg)); err != nil {
		return err
	}
	if err := d.killWindowByName(supervisorWindow(goal.ID, mg)); err != nil {
		return err
	}

	passed, reason, stderr, err := d.runValidateScript(goal)
	if err != nil {
		return fmt.Errorf("validate script: %w", err)
	}
	rt.scriptPassed = passed
	rt.scriptReason = reason
	if stderr != "" && !d.hasValidateMd(goal.ID) {
		return d.handleFailedCycle(goal, goals, stderr, "code-defect")
	}

	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}

	log.Printf("%s: phase supervising -> validating", goal.ID)
	rt.phase = phaseValidating
	rt.validateTime = d.now()
	return nil
}

func (d *Daemon) checkValidatingPhase(goal *Goal, goals *GoalsFile) error {
	rt := d.runtime(goal.ID)
	mg := d.maxGoals()
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		// Ordering is load-bearing (P2): heartbeat → hard-timeout → crash-detect.
		// The validating phase is the highest-risk wait (multi-finding synthesis
		// hangs), so the heartbeat fires BEFORE the validate hard-timeout to recover
		// a wedged validator in minutes. A heartbeat-internal error is logged and
		// swallowed so the existing timeout/crash branches still run unchanged.
		if stuck, herr := d.checkProgressHeartbeat(rt, validatorWindow(goal.ID, mg), investigatorPrefix(goal.ID, mg)); herr != nil {
			log.Printf("%s: validator heartbeat check error: %v", goal.ID, herr)
		} else if stuck {
			return d.handleStuckValidator(goal, goals)
		}
		if !rt.validateTime.IsZero() && d.now().Sub(rt.validateTime) >= d.validateTimeout {
			// The validator never reported before the deadline. This is a
			// validator error (verdict=error / owner=ops), NOT a code defect —
			// synthesize it and re-run validation only. Do not route through
			// handleFailedCycle, which would charge a code-defect retry.
			log.Printf("%s: validation timed out — no verdict; synthesizing error/ops, re-running validation only", goal.ID)
			return d.rerunValidationOnly(goal, goals, nil)
		}
		if !rt.bootConfirmedAt.IsZero() && d.now().Sub(rt.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			for _, w := range windows {
				if w.Name == validatorWindow(goal.ID, mg) && w.CurrentCommand == "zsh" {
					return d.handleFailedCycle(goal, goals, "Crash detected — validator returned to shell.", "code-defect")
				}
			}
		}
		return nil
	}

	valSig, ok := sig.(*ValidatorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during validating phase.", "code-defect")
	}

	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowByName(validatorWindow(goal.ID, mg)); err != nil {
		return err
	}

	// Route by the rolled-up verdict CLASS (C1's ClassifyVerdict), not a binary
	// pass/fail. ClassifyVerdict folds the per-finding classes into a single
	// (verdict, owner) pair; when the validator carried a top-level verdict but
	// no classifiable finding (e.g. a synthesized error/blocked), fall back to
	// that reported verdict — and its reported owner — so a non-pass verdict
	// never misclassifies as pass and the blocked owner-split still works.
	verdict, owner := ClassifyVerdict(valSig.Findings)
	if verdict == VerdictPass && valSig.Verdict != "" && valSig.Verdict != VerdictPass {
		verdict = valSig.Verdict
		if owner == "" {
			owner = valSig.Owner
		}
	}

	// P7-fresh gate-time re-run: rt.scriptPassed is stamped ONCE per cycle in
	// checkSupervisingPhase and is never refreshed by the rerunValidationOnly /
	// handleStuckValidator re-validation loops — both re-enter this phase with
	// the stale value. A single transient validate.sh non-pass (timeout,
	// lock-error, exec flake) therefore vetoed EVERY subsequent LLM pass until
	// ValidationRetries bled out and the goal hard-failed with a green suite
	// (goals 004/006/007, 2026-06-08). Re-run the script here, ONLY on the
	// otherwise-fatal combination (LLM pass + declared validate + stale false),
	// so the gate always judges a FRESH deterministic result. A persistently red
	// validate.sh still gates below — and now logs why.
	if verdict == VerdictPass && len(goal.Validate) > 0 && !rt.scriptPassed {
		passed, reason, _, serr := d.runValidateScript(goal)
		if serr != nil {
			return fmt.Errorf("validate script (gate-time re-run): %w", serr)
		}
		rt.scriptPassed = passed
		rt.scriptReason = reason
		if passed {
			log.Printf("%s: gate-time validate.sh re-run passed — stale script failure cleared", goal.ID)
		}
	}

	// P7 deterministic terminal-pass gate: a goal that DECLARES validate steps
	// cannot terminally pass on the LLM validator's judgment alone — the only
	// independent anchor is validate.sh. ScriptPassed carries the real validate.sh
	// result from checkSupervisingPhase (threaded via goalRuntime.scriptPassed,
	// refreshed by the gate-time re-run above). When ScriptPassed=true, the gate
	// permits the terminal pass; when false, a `pass` is downgraded to error/ops
	// (rerunValidationOnly, charges ValidationRetries). No-validate goals and
	// non-pass verdicts pass through.
	preGate := verdict
	verdict, owner = GateTerminalPass(verdict, owner, PassGate{RequireValidate: len(goal.Validate) > 0, ScriptPassed: rt.scriptPassed})
	if preGate == VerdictPass && verdict != VerdictPass {
		log.Printf("%s: LLM pass downgraded to %s/%s — validate.sh not passed (%s)", goal.ID, verdict, owner, rt.scriptReason)
	}

	// An unsubstantiated spec-defect (blocked/planner with no concretely-cited
	// contradiction) is a validator failure, not a planner failure — re-validate
	// (bounded by ValidationRetries) rather than burning the single SpecRetries
	// and cascading. Mirrors the timeout (:1248) and empty-verdict-synth routes.
	// A genuine, concretely-cited spec defect still falls through to the
	// blocked/planner case below and charges SpecRetries.
	if verdict == VerdictBlocked && owner == "planner" && !HasSubstantiveSpecDefect(valSig.Findings) {
		log.Printf("%s: blocked/planner verdict carries no substantive spec contradiction — re-validating (not charging spec retry)", goal.ID)
		return d.rerunValidationOnly(goal, goals, valSig)
	}

	// B7: per-goal/per-cycle cost record at the verdict-resolution seam. The
	// spawned/reused split is derived from the validator findings already in hand
	// (counted at actual spawn — reused investigators are never re-launched).
	// Side-effect-only: emits one COUNTERS line, never alters the verdict, owner,
	// or the switch below.
	sp, ru, inl := countInvFindings(valSig.Findings)
	d.logCounters(goal, verdict, sp, ru, inl)

	// Each non-pass branch moves exactly one per-class budget counter (or none):
	//   fail            -> implementer re-dispatch, dec CodeRetries
	//   blocked+planner -> bounce to generation (re-plan), dec SpecRetries
	//   blocked+ops     -> park + runbook (env/infra hold), NO budget (§5 resumes)
	//   error           -> re-run validation only, dec ValidationRetries
	switch {
	case verdict == VerdictPass:
		goal.Status = GoalDone
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> done (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		// After the successful SaveGoals so a commit only happens for a durably
		// recorded done; warn-only — never alters the verdict flow.
		d.autoCommitGoal(goal)
		return d.advanceToNextGoal(goals, goal.ID, true)
	case verdict == VerdictFail:
		// Code defect — the implementer must fix it. Charges CodeRetries only.
		return d.handleFailedCycle(goal, goals, valSig.NextAction, "code-defect")
	case verdict == VerdictBlocked && owner == "planner":
		// G5: demote BEFORE applyStructuredCorrections so the zero-budget
		// correction path still counts as a consumed first cycle for the lane.
		if err := d.demoteSoloLane(goal, goals, "spec defect bounce"); err != nil {
			return err
		}
		// B5b: before charging the single scarce SpecRetries on a planner bounce,
		// try the mechanical-correction applier. When the failing findings carry a
		// structured correction_edit CONFINED to spec artifacts (goal.md / dispatch
		// spec), the daemon applies it directly (idempotent) and re-validates the
		// goal charging ZERO budget. If absent, out-of-scope, or ineffective (no
		// on-disk change), applyStructuredCorrections returns handled=false and we
		// fall through to the unchanged spec-defect bounce below.
		if done, err := d.applyStructuredCorrections(goal, goals, valSig); err != nil {
			return err
		} else if done {
			return nil
		}
		// Spec defect — re-plan via the generation slot. Charges SpecRetries only.
		return d.bounceToGeneration(goal, goals, valSig)
	case verdict == VerdictBlocked:
		// Env/infra precondition (owner=ops) — park + runbook, NO budget. The
		// auto-resume loop is owned by §5; this branch never starts it.
		return d.haltBlockedEnv(goal, goals, valSig)
	case verdict == VerdictError:
		// The validator could not run. Re-validate only — never a code defect.
		return d.rerunValidationOnly(goal, goals, valSig)
	default:
		// Defensive: C1's enum is closed and its leaf-4 catch-all maps unknowns to
		// (fail, implementer), so this should not occur. Treat as a code defect.
		return d.handleFailedCycle(goal, goals, valSig.NextAction, "code-defect")
	}
}

// hashPane returns the FNV-1a hex digest of a captured pane string — the cheap,
// allocation-light per-tick progress fingerprint the heartbeat compares across
// ticks (it stores the digest, never the full pane text).
func hashPane(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// checkProgressHeartbeat reports whether the goal's ENTIRE window namespace —
// the lead window (windowName: supervisor or validator) plus its worker pool
// (windows named with poolPrefix: execute-<ns>-* or investigator-<ns>-*) — has
// emitted NO new pane output for at least d.progressTimeout while the lead
// window is still running the agent — the signal that the goal has wedged but
// not crashed. It is the parallel mechanism to diagnostics.go's stall watchdog,
// which self-disables while a worker runs (AnyRunning early-return), leaving the
// running worker invisible until the 1h hard timeout.
//
// Pool output counts as goal progress because the stuck handlers sweep the WHOLE
// namespace: an idle lead window with a grinding pool is not a harmful wedge (it
// wakes when the pool reports), but killing it destroys the pool's in-flight
// work. Goal-005 (2026-06-12): an exec-replace deploy + interrupted supervisor
// left the lead pane static for 5m while two workers were mid-task; the old
// lead-only heartbeat stuck-killed and recreated all of them. Pool MEMBERSHIP is
// folded into the digest too (the \x00-framed window names), so a worker
// appearing or vanishing also refreshes the heartbeat.
//
// Returns (false, nil) — never fires — in every guard case:
//   - d.progressTimeout <= 0: the heartbeat is DISABLED (literal-Daemon legacy
//     harness). NO CaptureWindowOutput / ListWindows call is made, so the
//     pre-P2 tick is byte-identical.
//   - the lead window is gone (or the listing failed): swallowed; the hard
//     timeout still applies.
//   - the lead's CurrentCommand is "zsh" or "" (back at the shell): the existing
//     crash-detect branch owns this, not the heartbeat.
//
// On a live lead window it captures the lead + pool panes and hashes them: a
// changed digest refreshes lastProgressHash + lastProgressAt (progress, no
// fire); an unchanged digest whose lastProgressAt is older than
// d.progressTimeout fires (stuck). The first observation always seeds (IsZero)
// and never fires the same tick. A LEAD capture error is returned to the caller,
// which logs it and falls through to the hard timeout (the heartbeat never
// blocks the existing safety nets); a POOL capture error skips that window only
// — it raced a kill between list and capture, and its disappearance already
// changes the digest next tick.
func (d *Daemon) checkProgressHeartbeat(rt *goalRuntime, windowName, poolPrefix string) (bool, error) {
	if d.progressTimeout <= 0 {
		return false, nil
	}
	windows, err := d.listWindows()
	if err != nil {
		return false, nil // listing failed — swallow; hard-timeout still applies
	}
	lead := -1
	for i := range windows {
		if windows[i].Name == windowName {
			lead = i
			break
		}
	}
	if lead == -1 {
		return false, nil // window absent — swallow; hard-timeout still applies
	}
	if windows[lead].CurrentCommand == "zsh" || windows[lead].CurrentCommand == "" {
		return false, nil // back at the shell — crash-detect owns this, not us
	}
	output, err := d.executor.CaptureWindowOutput(d.session, windows[lead].TmuxWindowID)
	if err != nil {
		return false, err
	}
	var combined strings.Builder
	combined.WriteString(output)
	if poolPrefix != "" {
		for i := range windows {
			if !strings.HasPrefix(windows[i].Name, poolPrefix) {
				continue
			}
			combined.WriteString("\x00" + windows[i].Name + "\x00")
			poolOut, perr := d.executor.CaptureWindowOutput(d.session, windows[i].TmuxWindowID)
			if perr != nil {
				continue
			}
			combined.WriteString(poolOut)
		}
	}
	h := hashPane(combined.String())
	if h != rt.lastProgressHash {
		rt.lastProgressHash = h
		rt.lastProgressAt = d.now()
		return false, nil
	}
	if rt.lastProgressAt.IsZero() {
		rt.lastProgressAt = d.now()
		return false, nil
	}
	if d.now().Sub(rt.lastProgressAt) >= d.progressTimeout {
		return true, nil
	}
	return false, nil
}

// handleStuckSupervisor recovers a supervisor window the heartbeat found wedged.
// It tears down the goal's implementer pool + supervisor window, charges the
// stuck budget (StuckRetries — NOT ValidationRetries/CodeRetries/SpecRetries,
// since the code/spec/validation is not at fault — this is an operational hang),
// and either re-dispatches (budget remaining) or hard-halts cascading failure to
// dependents (exhausted). Charging StuckRetries bounds an otherwise-infinite
// re-dispatch loop of a perpetually-wedged supervisor: dispatchRetry resets
// dispatchTime, so the 1h hard timeout alone cannot bound it.
func (d *Daemon) handleStuckSupervisor(goal *Goal, goals *GoalsFile) error {
	if err := d.demoteSoloLane(goal, goals, "stuck supervisor"); err != nil {
		return err
	}
	mg := d.maxGoals()
	log.Printf("%s: STUCK — supervisor made no pane progress for %v while still running; recovering (no code/spec budget charged)", goal.ID, d.progressTimeout)
	if err := d.killWindowsByPrefix(executePrefix(goal.ID, mg)); err != nil {
		return err
	}
	if err := d.killWindowByName(supervisorWindow(goal.ID, mg)); err != nil {
		return err
	}
	goal.StuckRetries--
	if goal.StuckRetries <= 0 {
		goals.CascadeFailure(goal.ID, "fail")
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=stuck-supervisor cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		log.Printf("%s: stuck budget exhausted — cascading failure to dependents", goal.ID)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}
	log.Printf("%s: stuck supervisor — re-dispatching (stuck budget left %d)", goal.ID, goal.StuckRetries)
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	return d.dispatchRetry(goal, goals)
}

// handleStuckValidator recovers a validator window the heartbeat found wedged.
// It charges StuckRetries (NOT ValidationRetries — stuck is an operational hang,
// not a validation defect), tears down the wedged validator, and either re-creates
// it (budget remaining) or hard-halts cascading failure to dependents (exhausted).
// Inlined rather than delegating to rerunValidationOnly because that function
// charges ValidationRetries, sets FailedBy="validation-timeout", and calls
// applyStructuredCorrections — none of which apply to a stuck recovery.
func (d *Daemon) handleStuckValidator(goal *Goal, goals *GoalsFile) error {
	if err := d.demoteSoloLane(goal, goals, "stuck validator"); err != nil {
		return err
	}
	log.Printf("%s: STUCK — validator made no pane progress for %v while still running; recovering (no code/spec budget charged)", goal.ID, d.progressTimeout)
	goal.StuckRetries--
	if goal.StuckRetries <= 0 {
		goals.CascadeFailure(goal.ID, "fail")
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=stuck-validator cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		log.Printf("%s: stuck budget exhausted — cascading failure to dependents", goal.ID)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}
	log.Printf("%s: stuck validator — re-creating (stuck budget left %d)", goal.ID, goal.StuckRetries)
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	if err := d.killWindowByName(validatorWindow(goal.ID, d.maxGoals())); err != nil {
		return err
	}
	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}
	rt := d.runtime(goal.ID)
	rt.phase = phaseValidating
	rt.validateTime = d.now()
	return nil
}

// salvageLateVerdicts: for every GoalFailed goal marked FailedBy ==
// "validation-timeout", poll signal.json. A late ValidatorSignal that classifies
// PASS flips the goal failed→done (the daemon stopped listening before the
// validator finished — the pass is real and the work is in the base tree, the
// marker is never set for worktree goals). Any other verdict clears the marker
// and keeps the failure. ReconcileBlocks (running right after in tick) un-sticks
// the hard-blocked dependents once the blocker is GoalDone — cascade reversal
// needs no code here. Persists via SaveGoals per mutation (the tick already
// holds the goals flock, mirroring the ReconcileBlocks self-persist pattern).
// The exhausted-timeout branch deliberately leaves the validator window alive;
// salvage kills it once its (late) verdict has been read.
func (d *Daemon) salvageLateVerdicts(goals *GoalsFile) error {
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalFailed || g.FailedBy != "validation-timeout" {
			continue
		}
		sig, err := LoadSignal(d.workDir, g.ID)
		if err != nil || sig == nil {
			continue // no verdict yet (or unreadable) — keep watching
		}
		valSig, ok := sig.(*ValidatorSignal)
		_ = DeleteSignal(d.workDir, g.ID)
		_ = d.killWindowByName(validatorWindow(g.ID, d.maxGoals()))
		g.FailedBy = ""
		if !ok {
			// A non-validator signal settles nothing — stop watching, failure stands.
			if err := SaveGoals(d.workDir, goals); err != nil {
				return err
			}
			continue
		}
		// Verdict classification mirrors checkValidatingPhase: roll up the
		// finding classes, falling back to a non-pass top-level verdict so a
		// synthesized error/blocked never misclassifies as pass.
		verdict, _ := ClassifyVerdict(valSig.Findings)
		if verdict == VerdictPass && valSig.Verdict != "" && valSig.Verdict != VerdictPass {
			verdict = valSig.Verdict
		}
		// P7 deterministic terminal-pass gate (same rule as checkValidatingPhase):
		// a late LLM pass on a goal that DECLARES validate steps has no deterministic
		// backing (validate.sh is provably not-passed on this path) — gate it so the
		// late-salvage bypass cannot flip a declared-validate goal to done on judgment
		// alone. Owner is unused on this path (failure stands); discard it.
		verdict, _ = GateTerminalPass(verdict, "", PassGate{RequireValidate: len(g.Validate) > 0, ScriptPassed: false})
		if verdict == VerdictPass {
			g.Status = GoalDone
			g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			log.Printf("%s: LATE pass verdict salvaged after timeout-synthesized failure — failed -> done", g.ID)
		} else {
			log.Printf("%s: late verdict %q after timeout failure — failure stands", g.ID, verdict)
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		// Pass arm only, after the successful SaveGoals: a salvaged done gets the
		// same per-goal commit boundary as the primary done site (warn-only).
		if verdict == VerdictPass {
			d.autoCommitGoal(g)
		}
	}
	return nil
}

// rerunValidationOnly handles a validator-error verdict: the validator itself
// could not run, or never reported before the deadline. It re-runs validation
// ONLY — it decrements ValidationRetries (the REMAINING validation budget) and
// re-creates the validator window without touching CodeRetries/SpecRetries and
// without re-dispatching the implementer (that would be the handleFailedCycle
// code-defect path). This is the single bounded error/ops route shared by both
// the caller-reported empty-verdict synthesis (MCP GoalValidationDone), the
// never-reported timeout watchdog, and the error branch of checkValidatingPhase
// (C1 introduced an unbounded reRunValidation; this consolidation makes it
// bounded so a wedged validator can no longer re-spawn forever). When the
// validation budget reaches 0 the goal hard-halts (verdict class "error" — kept
// available for §5.1's CascadeFailure wiring; CascadeFailure's signature change
// is owned by §5.1).
//
// valSig is the verdict signal that routed here, when the caller has one (the
// error branch and the unsubstantiated-blocked guard pass theirs); the timeout
// watchdog has none and passes nil — the applier treats nil as "no structured
// remedy" and the charging path runs unchanged.
func (d *Daemon) rerunValidationOnly(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) error {
	if err := d.demoteSoloLane(goal, goals, "validator error"); err != nil {
		return err
	}
	// RC-C: BEFORE charging the scarce ValidationRetries (often 1 — the first
	// infra/config error would be instantly terminal), try the B5b
	// mechanical-correction applier, mirroring the blocked/planner call site. When
	// the failing findings carry a structured correction_edit CONFINED to spec
	// artifacts (goal.md / dispatch spec), the daemon applies it directly
	// (idempotent) and re-validates the goal charging ZERO budget. If absent,
	// out-of-scope, or ineffective (no on-disk change), applyStructuredCorrections
	// returns handled=false and we fall through to the unchanged charging path
	// below — that contract keeps this route budget-bounded (an edit that never
	// fixes the finding cannot re-validate for free forever).
	if handled, err := d.applyStructuredCorrections(goal, goals, valSig); err != nil {
		return err
	} else if handled {
		log.Printf("%s: error/ops corrections applied — re-validating (zero budget)", goal.ID)
		return nil
	}
	goal.ValidationRetries--
	if goal.ValidationRetries <= 0 {
		// Only the timeout route (valSig == nil — the watchdog passes nil) can
		// still receive a meaningful late verdict: the validator window is left
		// alive below and goal-validation-done may land after the daemon stopped
		// listening (goal-061: real pass arrived 5m51s post-failure). Gate on the
		// runtime WorktreeDir, captured HERE while the runtime still exists
		// (advanceToNextGoal -> clearRuntime drops it): the halt path discards
		// worktrees, so a late pass for discarded work must NOT flip to done. At
		// MaxGoals=1 (default) WorktreeDir is always empty. The error-verdict
		// route (valSig != nil) already has its verdict — nothing late is pending.
		if valSig == nil && d.runtime(goal.ID).WorktreeDir == "" {
			goal.FailedBy = "validation-timeout"
		}
		log.Printf("%s: validation budget exhausted (error/ops) — cascading failure to dependents", goal.ID)
		// Exhausted validation budget is a hard halt: the goal genuinely cannot
		// complete, so dependents are blocked (hard class "fail").
		goals.CascadeFailure(goal.ID, "fail")
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=validation-exhausted cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}
	log.Printf("%s: validator error (error/ops) — re-running validation only (validation budget left %d)", goal.ID, goal.ValidationRetries)
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	// Tear down any stale validator before re-creating one. In the timeout path
	// the previous validator is still alive; in the verdict path it has already
	// been killed (killWindowByName is then a no-op).
	if err := d.killWindowByName(validatorWindow(goal.ID, d.maxGoals())); err != nil {
		return err
	}
	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}
	rt := d.runtime(goal.ID)
	rt.phase = phaseValidating
	rt.validateTime = d.now()
	return nil
}

// hasNonPassFindings reports whether the signal carries at least one non-pass
// finding (i.e. writeCorrectionFile will emit structured per-finding blocks
// rather than the NextAction fallback).
func hasNonPassFindings(sig *ValidatorSignal) bool {
	if sig == nil {
		return false
	}
	for _, f := range sig.Findings {
		if f.Status != VerdictPass {
			return true
		}
	}
	return false
}

// circuitBreakerK returns the configured convergence circuit-breaker threshold
// (consecutive identical-signature cycles before halting). It reads
// Taskvisor.CircuitBreakerK from setting.yaml; a missing/invalid (<1) value
// falls back to the documented default of 2.
func (d *Daemon) circuitBreakerK() int {
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || s == nil || s.Taskvisor.CircuitBreakerK < 1 {
		return 2
	}
	return s.Taskvisor.CircuitBreakerK
}

// equalSorted reports whether two ALREADY-sorted string slices are element-wise
// equal. ComputeSignatures returns ascending-sorted slices and the stored
// Goal.ConvergenceSignatures is likewise the previously-computed sorted set, so
// a positional comparison is a correct set-equality test here.
func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// demoteSoloLane enforces the one-way G5 lane demotion: any validation
// failure, stuck recovery, or retry permanently flips a solo-lane goal to the
// full lane. Strict no-op for full/lane-absent goals on the goals.yaml and
// goal.md surfaces (lane-absent behavior is untouched, and a repeat call
// performs no save and no MD write). Persists goals.yaml FIRST, then patches
// the goal.md `## Lane` section, so both surfaces flip in the same call; both
// errors propagate (crashRecovery's call site downgrades to log-and-continue).
// The THIRD surface — the per-goal tasks.yaml top-level `lane:` key, which
// wins supervisor step 3c's resolution precedence — is spliced to full on
// EVERY call, including repeat calls on an already-full goal: dispatchRetry's
// defensive funnel call is exactly such a repeat, and the unconditional guard
// is what repairs a tasks.yaml left solo by a crash between SetGoalMDLane and
// the splice. Called at the TOP of every failure-classification sink — BEFORE
// the exhausted→fail branches — so a terminally-failed solo goal is already
// full and a later ResetGoal re-pend cannot resurrect the solo discount
// (ResetGoal does not clear Lane).
func (d *Daemon) demoteSoloLane(goal *Goal, goals *GoalsFile, reason string) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if goal.LaneOrFull() != LaneSolo {
		return demoteTasksYamlLane(goalDir)
	}
	goal.Lane = LaneFull
	log.Printf("%s: solo lane demoted to full (%s)", goal.ID, reason)
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	if err := SetGoalMDLane(goalDir, LaneFull); err != nil {
		return err
	}
	return demoteTasksYamlLane(goalDir)
}

// demoteTasksYamlLane splices the per-goal tasks.yaml top-level `lane:` key to
// full when it currently reads solo, mirroring SetGoalMDLane's philosophy:
// only the one matching line is rewritten, every other byte is carried through
// verbatim (a yaml round-trip would reformat the whole file). An absent file
// (pre-plan demotion) or absent key is a silent no-op and never creates the
// file; a file whose key already reads full (or anything non-solo) is left
// byte-untouched. Only a column-0 `lane:` line is top-level — an indented
// task-entry field never matches — and only the FIRST such line is considered
// (duplicate top-level keys are yaml-invalid anyway). Non-ENOENT read errors
// propagate to the demoteSoloLane caller.
func demoteTasksYamlLane(goalDir string) error {
	path := filepath.Join(goalDir, "tasks.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimRight(key, " \t") != "lane" {
			continue
		}
		v := strings.TrimSpace(rest)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if v != LaneSolo {
			return nil
		}
		lines[i] = "lane: " + LaneFull
		return atomicWrite(path, []byte(strings.Join(lines, "\n")), 0o644)
	}
	return nil
}

// handleFailedCycle is the code-defect route: the implementer must fix the
// reported defect. It writes the correction file, decrements CodeRetries (the
// REMAINING code-defect budget) toward zero, and either re-dispatches the
// implementer (budget remaining) or hard-halts (budget exhausted). verdictClass
// names the failure class ("code-defect") and is kept in scope at the
// CascadeFailure call site so §5.1 can wire CascadeFailure(goal.ID,
// verdictClass) — CascadeFailure's signature change is owned by §5.1, not here.
//
// reason is the correction text surfaced to the implementer on the next cycle;
// it is retained as a distinct parameter (not collapsed into verdictClass) so
// the correction file keeps the actionable remediation guidance.
func (d *Daemon) handleFailedCycle(goal *Goal, goals *GoalsFile, reason, verdictClass string) error {
	if err := d.demoteSoloLane(goal, goals, "failed cycle"); err != nil {
		return err
	}
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	// Cycle number is the unified CurrentCycle(goal) (consumed per-class budget +
	// 1) so the corrections file (cycle-N.md) and the per-cycle research dir
	// (research/cycle-N/) share ONE source of truth — changing one without the
	// other reintroduces drift (C7). Computed BEFORE the CodeRetries decrement
	// below, so cycle K's corrections land in cycle-K.md and the subsequent retry
	// (after the decrement) allocates cycle-(K+1).
	cycleNum := CurrentCycle(goal)
	rt := d.runtime(goal.ID)

	var header string
	if rt.lastSupervisorStatus == "stopped" {
		header = "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup."
	} else {
		header = "Implementation completed but failed acceptance criteria."
	}

	// Load the validator signal so the correction file carries full per-finding
	// detail (failing_command/output_excerpt/expected_state/correction) VERBATIM.
	// On the timeout/crash routes no signal exists yet — synthesize one from the
	// reason. When the signal has no structured findings, fold the daemon framing
	// header into NextAction so the fallback branch keeps the supervisor-context
	// line the implementer relies on; when findings ARE present, the structured
	// blocks carry the detail and take precedence.
	loaded, loadErr := LoadSignal(d.workDir, goal.ID)
	if loadErr != nil {
		log.Printf("%s: handleFailedCycle: load signal: %v (using reason fallback)", goal.ID, loadErr)
	}
	valSig, _ := loaded.(*ValidatorSignal)
	if valSig == nil {
		valSig = &ValidatorSignal{}
	}
	if !hasNonPassFindings(valSig) {
		detail := strings.TrimSpace(valSig.NextAction)
		if detail == "" {
			detail = strings.TrimSpace(reason)
		}
		valSig.NextAction = header + "\n\n" + detail
	}

	// C6 convergence circuit-breaker — checked BEFORE the CodeRetries decrement.
	// If this cycle's non-pass findings produce the SAME sorted signature set as
	// the prior failed cycle (persisted on the goal), the goal is not converging:
	// burning the rest of the code budget on an identical failure only wastes
	// cycles a human must eventually unblock. On K consecutive identical sets we
	// halt to blocked/owner=human REGARDLESS of remaining budget, WITHOUT
	// decrementing any counter and WITHOUT re-dispatching. The breaker fires only
	// on signature recurrence, never on budget exhaustion (that stays the
	// GoalFailed cascade below). An empty set (no non-pass findings, e.g. the
	// timeout/crash routes) never fires.
	cur := ComputeSignatures(valSig.Findings)
	k := d.circuitBreakerK()
	streak := 1
	if len(cur) > 0 && equalSorted(cur, goal.ConvergenceSignatures) {
		streak = goal.ConvergenceStreak + 1
	}
	goal.ConvergenceSignatures = cur
	goal.ConvergenceStreak = streak
	if len(cur) > 0 && streak >= k {
		goal.Status = GoalBlocked
		goal.BlockedBy = "convergence-circuit-breaker"
		if err := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
			Verdict:    VerdictBlocked,
			Owner:      "human",
			Findings:   valSig.Findings,
			Signatures: cur,
			NextAction: strings.TrimSpace(reason),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		log.Printf("%s: running -> blocked (circuit-breaker, streak=%d/%d)", goal.ID, streak, k)
		d.reportBreakerTrip(goal, "code", cur, streak, k)
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}

	if err := d.writeCorrectionFile(goalDir, cycleNum, valSig); err != nil {
		return err
	}

	// Decrement-toward-zero: CodeRetries is the REMAINING code-defect budget.
	// Legacy goal.Retries stays read-only post-migration (never incremented here).
	goal.CodeRetries--
	if goal.CodeRetries <= 0 {
		// Code budget exhausted — hard halt. verdictClass is in scope here (C2
		// threads it from ClassifyVerdict) and is a hard class ("fail"/"code-defect"),
		// so dependents are blocked.
		goals.CascadeFailure(goal.ID, verdictClass)
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=code-exhausted cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		log.Printf("%s: code budget exhausted (%s) — cascading failure to dependents", goal.ID, verdictClass)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}

	goal.Status = GoalPending
	// RC-D: stamp the explicit routing marker so the next dispatch retries the
	// IMPLEMENTER against the existing tasks.yaml (dispatchCandidate honors the
	// marker first; cleared on consume in dispatchRetry/dispatch).
	goal.NextDispatch = dispatchImplementer
	log.Printf("%s: running -> pending (code budget left %d)", goal.ID, goal.CodeRetries)
	oldPhase := rt.phase
	rt.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising", goal.ID, phaseName(oldPhase))
	return SaveGoals(d.workDir, goals)
}

// bounceToGeneration is the spec-defect route (verdict=blocked, owner=planner):
// the goal/spec itself is contradictory or under-specified, so it must be
// re-planned via the generation/planner slot — NOT re-run against the unchanged
// spec by the implementer. It charges SpecRetries only (decrement-toward-zero)
// and leaves CodeRetries untouched. The planner re-dispatch is guaranteed by
// the EXPLICIT NextDispatch=generation marker stamped on the re-pend tail
// (RC-D): relying on an untouched CodeRetries was wrong — codeBudgetConsumed is
// sticky, so any previously-burned code retry silently routed the bounce to
// dispatchRetry and the planner never saw the defective spec. On exhausted spec
// budget it hard-halts (verdict class "spec-defect").
func (d *Daemon) bounceToGeneration(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	cycleNum := goal.MaxSpecRetries - goal.SpecRetries + 1
	if cycleNum < 1 {
		cycleNum = 1
	}
	// Spec-defect bounce. The framing header is the planner-facing context +
	// remediation; the live valSig.Findings carry the REAL per-finding detail
	// (which rule, what contradiction, the remedy) the re-generation must fix.
	// Declared ABOVE the convergence breaker so a breaker halt can reuse it as the
	// blocked signal's NextAction.
	const framing = "Validation reports a SPEC DEFECT (owner: PLANNER). The current spec is contradictory or under-specified — regenerate/repair the plan; do NOT re-run the implementer against the unchanged spec.\n\nBounce to generation: regenerate the goal plan to resolve the spec defect before re-implementing."

	// B10 spec-route convergence circuit-breaker — the verbatim mirror of
	// handleFailedCycle's code-route breaker, checked BEFORE writeCorrectionFile
	// and BEFORE the SpecRetries decrement. If this bounce's non-pass finding
	// signatures match the prior spec bounce's for K consecutive bounces, the
	// planner is re-emitting an identical, non-converging spec defect; halt to
	// blocked/owner=human with the shared sentinel REGARDLESS of remaining spec
	// budget, WITHOUT decrementing SpecRetries and WITHOUT writing a misleading
	// bounce-correction file. The streak lives in DEDICATED spec-side fields
	// (SpecConvergenceSignatures/SpecConvergenceStreak), isolated from the code
	// route so an interleaved code-defect cycle never resets or inflates it. An
	// empty set (nil/findingless bounce) never fires.
	var findings []ValidationFinding
	if valSig != nil {
		findings = valSig.Findings
	}
	cur := ComputeSignatures(findings)
	k := d.circuitBreakerK()
	streak := 1
	if len(cur) > 0 && equalSorted(cur, goal.SpecConvergenceSignatures) {
		streak = goal.SpecConvergenceStreak + 1
	}
	goal.SpecConvergenceSignatures = cur
	goal.SpecConvergenceStreak = streak
	if len(cur) > 0 && streak >= k {
		goal.Status = GoalBlocked
		goal.BlockedBy = "convergence-circuit-breaker"
		if err := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
			Verdict:    VerdictBlocked,
			Owner:      "human",
			Findings:   findings,
			Signatures: cur,
			NextAction: framing,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		log.Printf("%s: running -> blocked (spec circuit-breaker, streak=%d/%d)", goal.ID, streak, k)
		d.reportBreakerTrip(goal, "spec", cur, streak, k)
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}

	// Prime the NextAction fallback so a findingless bounce still writes the
	// framing header; forward the validator's findings when present (a
	// synthesized/never-reported bounce may pass nil → header-only fallback).
	sig := &ValidatorSignal{NextAction: framing}
	if valSig != nil {
		sig.Findings = valSig.Findings
	}
	if err := d.writeCorrectionFile(goalDir, cycleNum, sig); err != nil {
		return err
	}
	// writeCorrectionFile's XOR suppresses the NextAction framing once structured
	// findings are rendered. Prepend the framing header ABOVE the rendered blocks
	// so the planner gets both the context and the concrete detail. (No non-pass
	// findings → the fallback already wrote the framing; skip the read-modify-write.)
	if hasNonPassFindings(sig) {
		correctionPath := filepath.Join(goalDir, "corrections", fmt.Sprintf("cycle-%d.md", cycleNum))
		body, err := os.ReadFile(correctionPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(correctionPath, []byte(framing+"\n\n"+string(body)), 0o644); err != nil {
			return err
		}
	}

	// Decrement-toward-zero: SpecRetries only. CodeRetries/ValidationRetries are
	// left untouched (one counter per verdict).
	goal.SpecRetries--
	if goal.SpecRetries <= 0 {
		// Spec budget exhausted — hard halt. An unrepairable spec leaves dependents
		// genuinely blocked, so cascade with the hard class "fail".
		goals.CascadeFailure(goal.ID, "fail")
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=spec-exhausted cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		log.Printf("%s: spec budget exhausted (spec-defect) — cascading failure to dependents", goal.ID)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID, false)
	}

	goal.Status = GoalPending
	goal.Phase = "generation"
	// RC-D: stamp the explicit routing marker so the next dispatch is a FULL
	// planner re-generation even when a prior cycle consumed code budget (the
	// sticky codeBudgetConsumed heuristic would otherwise route this bounce to
	// dispatchRetry and re-execute the defective spec verbatim). Persisted in
	// goals.yaml — survives a daemon restart; cleared on consume in dispatch.
	goal.NextDispatch = dispatchGeneration
	log.Printf("%s: spec defect (planner) — bouncing to generation (spec budget left %d)", goal.ID, goal.SpecRetries)
	rt := d.runtime(goal.ID)
	oldPhase := rt.phase
	rt.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising (generation re-dispatch)", goal.ID, phaseName(oldPhase))
	return SaveGoals(d.workDir, goals)
}

// haltBlockedEnv is the env/infra route (verdict=blocked, owner=ops): a
// precondition (missing secret, unreachable service) is unmet. It charges NO
// budget — re-running anything cannot clear an environmental block — and parks
// the goal after writing an operator runbook and emitting an operator-facing log
// line. The polling auto-resume loop (resumeDownstreamLoop / BlockedByPrecondition)
// is owned by §5 and is deliberately NOT started here; this branch only parks +
// notifies and lets §5 resume the goal once the precondition clears.
func (d *Daemon) haltBlockedEnv(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) error {
	if err := d.demoteSoloLane(goal, goals, "blocked env/infra"); err != nil {
		return err
	}
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)

	remedy := valSig.Remedy
	if remedy == "" {
		remedy = valSig.NextAction
	}
	if remedy == "" {
		remedy = "Resolve the missing environment/infrastructure precondition, then resume the goal."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Runbook — %s blocked (env/infra)\n\n", goal.ID)
	b.WriteString("Owner: **ops**. This is an environment/infrastructure precondition, not a code or spec defect. No retry budget was charged; the goal is parked until the precondition clears (auto-resume is handled by §5).\n\n")
	fmt.Fprintf(&b, "## Remedy\n\n%s\n", remedy)
	if len(valSig.Findings) > 0 {
		b.WriteString("\n## Blocking findings\n\n")
		for _, f := range valSig.Findings {
			if f.Status == VerdictPass {
				continue
			}
			fmt.Fprintf(&b, "- [%s/%s] %s — %s\n", f.Status, f.FailureClass, f.Rule, f.Detail)
		}
	}
	if err := os.WriteFile(filepath.Join(goalDir, "runbook.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}

	// notify(log): operator-facing, mirrors the preflight precondition-gate line.
	log.Printf("[BLOCKED - OPERATOR ACTION REQUIRED] %s: env/infra precondition unmet — %s (parked; no budget charged, §5 auto-resumes)", goal.ID, remedy)

	// SOFT cascade: an env/infra hold is transient, so dependents must NOT be
	// hard-blocked — they stay GoalPending with BlockedBy recorded and resume
	// automatically once this goal's precondition clears and it completes. Derive
	// the soft class from the validator signal (env-config / infra-flake); default
	// to env-config. This is the ONE soft CascadeFailure call site.
	softClass := valSig.Class
	if softClass != "env-config" && softClass != "infra-flake" {
		softClass = "env-config"
	}
	goals.CascadeFailure(goal.ID, softClass)

	goal.Status = GoalBlocked
	goal.BlockedBy = "env_precondition"
	// Flag for the §5 auto-resume loop: scanPreconditionBlocked re-evaluates this
	// goal's preconditions on each tick and resumes it when they pass.
	goal.BlockedByPrecondition = true
	// C-1: persist the precondition-classified signal so §5's resume gate recognizes
	// this park. Stamp the resolved soft class onto valSig first (it may be empty),
	// then persist BEFORE SaveGoals so the goal is never parked without its signal —
	// on a persist error, return early.
	valSig.Class = softClass
	if err := SaveValidatorSignal(d.workDir, goal.ID, valSig); err != nil {
		return err
	}
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) haltRetryCeiling(goal *Goal, goals *GoalsFile) error {
	log.Printf("%s: retry ceiling reached (total retries: %d), halting", goal.ID, goals.TotalRetries())
	goal.Status = GoalFailed
	goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	// Ceiling = exhausted retries → hard fail; downstream genuinely blocked.
	goals.CascadeFailure(goal.ID, "fail")
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=retry-ceiling cascade=%d]",
		goal.ID, goal.Description, countCascaded(goals, goal.ID)))
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	return d.advanceToNextGoal(goals, goal.ID, false)
}

func (d *Daemon) checkStaleBinary(goals *GoalsFile) error {
	if d.now().Sub(d.lastStaleCheck) < time.Minute {
		return nil
	}
	d.lastStaleCheck = d.now()

	stale, detail := setup.BinaryStale()
	if !stale {
		d.staleBanner = ""
		return nil
	}

	d.staleBanner = fmt.Sprintf("BINARY STALE — restart taskvisor to apply (%s)", detail)
	// Best-effort: rewrite installed command templates from the new binary's
	// embedded FS before any exec-replace restart (and on the banner/halt paths).
	// Sits past the throttle gate (≤1/min) and never alters the decision below.
	d.refreshCommands()
	if d.restartOnStaleBinary {
		return d.restartStaleBinary(goals, detail)
	}
	if d.haltOnStaleBinary {
		return d.haltStaleBinary(goals, detail)
	}
	return nil
}

// refreshCommands rewrites the installed .claude/commands/tmux/ templates from the
// new binary's embedded FS via the injected commandRefreshFn. Best-effort and gated:
// a nil fn or a disabled Commands setting is a silent no-op, and a write error is
// logged and swallowed so the stale-binary restart/halt/banner decision is unaffected.
func (d *Daemon) refreshCommands() {
	if d.commandRefreshFn == nil {
		return
	}
	// Gate on Commands.Enabled for parity with setup.Run — an operator who disabled
	// command installation must not have files silently re-created. Fail-closed: if
	// settings are unreadable, skip rather than assume enabled.
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || !s.Commands.Enabled {
		return
	}
	if err := d.commandRefreshFn(); err != nil {
		log.Printf("stale-binary: command refresh failed: %v (continuing)", err)
	}
}

func (d *Daemon) haltStaleBinary(goals *GoalsFile, detail string) error {
	log.Printf("ALARM: binary stale (%s) — halting daemon (HaltOnStaleBinary=true)", detail)
	d.haltReason = fmt.Sprintf("HALTED: binary replaced — restart taskvisor (%s)", detail)
	return d.deactivate()
}

func (d *Daemon) backupGoalsBeforeRestart() {
	src := GoalsFilePath(d.workDir)
	if _, err := os.Stat(src); err != nil {
		return
	}
	backupDir := filepath.Join(d.workDir, ".tmux-cli", "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		log.Printf("exec-replace backup: mkdir failed: %v", err)
		return
	}
	dst := filepath.Join(backupDir, fmt.Sprintf("goals-%s.yaml", time.Now().Format("20060102-150405")))
	data, err := os.ReadFile(src)
	if err != nil {
		log.Printf("exec-replace backup: read failed: %v", err)
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		log.Printf("exec-replace backup: write failed: %v", err)
		return
	}
	log.Printf("exec-replace: backed up goals.yaml to %s", dst)
}

func (d *Daemon) restartStaleBinary(goals *GoalsFile, detail string) error {
	if d.restartAttempted {
		return nil
	}
	d.restartAttempted = true
	log.Printf("ALARM: binary stale (%s) — exec-replacing (RestartOnStaleBinary=true)", detail)

	d.backupGoalsBeforeRestart()
	restartMarker := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-restart")
	_ = os.WriteFile(restartMarker, nil, 0o644)
	signal.Stop(d.signalCh)

	resolved, err := os.Executable()
	if err != nil {
		log.Printf("exec-replace: os.Executable failed: %v — skipping restart", err)
		signal.Notify(d.signalCh, syscall.SIGTERM, syscall.SIGINT)
		return nil
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		log.Printf("exec-replace: EvalSymlinks failed: %v — using unresolved path", err)
	}

	if err := d.execReplaceFn(resolved, os.Args, os.Environ()); err != nil {
		signal.Notify(d.signalCh, syscall.SIGTERM, syscall.SIGINT)
		log.Printf("exec-replace: exec failed: %v — continuing poll loop", err)
		return nil
	}
	return nil
}

// haltGoalWallClock fails the single goal named by goalID when its PER-GOAL P3
// wall-clock budget (now()-goalRuntime.activatedAt) is exhausted. Unlike the old
// daemon-deactivating haltWallClock, this is SCOPED to the offending goal: it
// flips ONLY that goal to GoalFailed (reusing the established single-goal failure
// finalization — FinishedAt + FailedBy + CascadeFailure for hard-blocked
// dependents), then advanceToNextGoal(resume=false). At MaxGoals=1 with no next
// pending, advanceToNextGoal reaches deactivateOnCompletion and the daemon ends
// idle — operator-visible end-state stays effectively identical to the old halt;
// at MaxGoals>1 under-budget siblings keep running and the daemon stays active.
// It deliberately does NOT write d.haltReason (that daemon-level banner infra is
// retained but no longer driven by the wall-clock path) nor deactivate the whole
// daemon for one goal's breach.
func (d *Daemon) haltGoalWallClock(goals *GoalsFile, goalID string, elapsed time.Duration) error {
	log.Printf("ALARM: wall-clock budget exhausted (%s elapsed >= %s budget) — halting goal %s",
		elapsed.Round(time.Second), d.maxWallClock, goalID)
	if goal, ok := goals.GoalByID(goalID); ok {
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		goal.FailedBy = "wall-clock-budget"
		// Budget exhaustion is a hard fail — dependents are genuinely blocked.
		goals.CascadeFailure(goalID, "fail")
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=wall-clock-budget cascade=%d]",
			goalID, goal.Description, countCascaded(goals, goalID)))
	}
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	return d.advanceToNextGoal(goals, goalID, false)
}

// advanceToNextGoal is called when the goal named by completedID leaves the
// in-flight set (terminal/halt). completedID is ALWAYS that goal's id (so the
// right per-goal runtime is dropped, even when a non-head sibling completes at
// MaxGoals>1). resume gates the synchronous downstream unblock: true only when
// completedID reached GoalDone, so its soft-held dependents are cleared in the
// SAME locked critical section (the caller — poll — already holds the goals
// lock); false for failure/halt callers, because CascadeFailure already settled
// their dependents (hard-block) and resuming would be wrong.
//
// Teardown is gated on AnyRunning: a completing goal with siblings still in
// flight (MaxGoals>1) must NOT deactivate — it just persists and stays active so
// the running siblings continue. At MaxGoals=1 the completing goal was the only
// runner, so AnyRunning is false and deactivateOnCompletion fires exactly as the
// old switch did. The scalar CurrentGoal head moves only when it pointed at the
// goal that just left flight: it prefers another running sibling (MaxGoals>1) so
// the dashboard/crashRecovery head stays in flight, falling back to the next
// pending goal (the byte-identical MaxGoals=1 choice, where nothing else runs).
func (d *Daemon) advanceToNextGoal(goals *GoalsFile, completedID string, resume bool) error {
	// E1-1a worktree lifecycle, BEFORE the downstream resume and clearRuntime (the
	// merge/discard helpers read the per-goal WorktreeDir that clearRuntime drops).
	// On the success path merge the worktree back into base then remove it; a merge
	// conflict flips the goal done→failed, surfaces the conflicting paths, and
	// suppresses the resume. On the halt path discard the worktree (no merge). All
	// of this is a zero-git no-op at MaxGoals=1 (empty WorktreeDir).
	if completedID != "" {
		if resume {
			if g, ok := goals.GoalByID(completedID); ok {
				failed, err := d.finalizeWorktreeOnDone(goals, g)
				if err != nil {
					return err
				}
				if failed {
					resume = false
				}
			}
		} else if g, ok := goals.GoalByID(completedID); ok {
			d.cleanupWorktreeOnHalt(g)
		}
	}
	if resume && completedID != "" {
		d.resumeDownstream(goals, completedID)
	}
	if completedID != "" {
		d.clearRuntime(completedID)
	}
	next, hasNext := goals.NextPendingGoal()
	if !hasNext {
		if goals.AnyRunning() {
			// Siblings still in flight (MaxGoals>1) — persist the terminal state and
			// stay active; the scheduler keeps driving them.
			return d.persistAfterAdvance(goals, completedID)
		}
		return d.deactivateOnCompletion(goals)
	}
	if goals.CurrentGoal == completedID {
		if rid, ok := goals.FirstRunningGoalID(); ok {
			goals.CurrentGoal = rid
			d.currentGoal = rid
		} else {
			goals.CurrentGoal = next.ID
			d.currentGoal = next.ID
		}
	}
	return SaveGoals(d.workDir, goals)
}

func countCascaded(goals *GoalsFile, failedGoalID string) int {
	n := 0
	for _, g := range goals.Goals {
		if g.BlockedBy == failedGoalID && g.ID != failedGoalID {
			n++
		}
	}
	return n
}

// persistAfterAdvance moves the scalar CurrentGoal head off a just-completed goal
// to a still-running sibling (MaxGoals>1, no pending goal to advance to) and
// persists. If the completed goal was not the head, or no sibling is running, it
// just persists. Keeps the head pointing at an in-flight goal for the dashboard.
func (d *Daemon) persistAfterAdvance(goals *GoalsFile, completedID string) error {
	if goals.CurrentGoal == completedID {
		if rid, ok := goals.FirstRunningGoalID(); ok {
			goals.CurrentGoal = rid
			d.currentGoal = rid
		}
	}
	return SaveGoals(d.workDir, goals)
}

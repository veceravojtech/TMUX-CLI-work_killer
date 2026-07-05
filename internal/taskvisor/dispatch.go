package taskvisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"gopkg.in/yaml.v3"
)

type ScriptRunnerFunc func(ctx context.Context, scriptPath, dir string, env []string) (stdout, stderr string, exitCode int, err error)

// validateScriptTimeout seeds Daemon.scriptTimeout (New()); overridable via
// taskvisor.validate_script_timeout_sec. 120s (was 30s — which silently killed
// any script wrapping a real test suite). Kept modest deliberately: the script
// runs synchronously inside the tick under the goals+db locks, so the whole
// daemon blocks while it runs — slow suites should raise the setting per
// project rather than this seed.
// Raised 120s→600s for the validation-as-goal model: the heavy validate stack
// (phpunit integration + deptrac + phpstan L9 + kernel boot) now runs in a
// dedicated validation goal's OWN supervising cycle, so the original
// "blocks the whole daemon under locks" caution is relaxed; 600s covers a
// Symfony stack with margin under the validate_timeout: 1260 envelope.
// validateScriptTimeout bounds ONE execution of the worktree integration gate
// script (worktree.go runIntegrationGate), which shares the scriptRunnerFn seam.
const validateScriptTimeout = 600 * time.Second

func defaultScriptRunner(ctx context.Context, scriptPath, dir string, env []string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Env = env
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// writeCycleMarker pre-creates the current cycle's goal-scoped research dir and
// writes the cycle marker(s) so investigate.xml's step-2a resolution can locate
// research/cycle-<N>/ for the current dispatch attempt. Idempotent; called on
// every (re-)dispatch BEFORE any worker (supervisor or validator) is spawned,
// inside the goals flock — no extra locking needed.
//
// The global .tmux-cli/taskvisor-current-cycle marker (sibling of
// taskvisor-current-goal) is written unconditionally: it remains the MaxGoals<=1
// resolution source and the standalone fallback. At mg>1 it is last-writer-wins
// across concurrent goals, so a PER-GOAL marker .tmux-cli/goals/<id>/current-cycle
// is ALSO written — fed by the same CurrentCycle computation (one computation,
// two destinations, zero drift) — and investigate.xml reads the per-goal marker
// FIRST so one goal's dispatch can never clobber another's cycle number. At
// mg<=1 the per-goal marker is REMOVED instead (mirroring writeWorktreeMarker's
// gate-and-remove), so single-goal runs produce zero new artifacts and a stale
// marker from a prior mg>1 run cannot leak into the fallback chain.
func (d *Daemon) writeCycleMarker(goal *Goal, mg int) error {
	if _, err := EnsureCycleResearchDir(d.workDir, goal); err != nil {
		return fmt.Errorf("ensure cycle research dir: %w", err)
	}
	n := CurrentCycle(goal)
	markerPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-cycle")
	if err := os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", n)), 0o644); err != nil {
		return err
	}
	perGoalPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "current-cycle")
	if mg > 1 {
		return os.WriteFile(perGoalPath, []byte(fmt.Sprintf("%d", n)), 0o644)
	}
	if err := os.Remove(perGoalPath); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: clear per-goal cycle marker: %v", err)
	}
	return nil
}

// writeSupervisorWindowMarker publishes the EXACT supervisor window name the
// daemon computed for this goal to .tmux-cli/goals/<id>/supervisor-window, so a
// supervisor/plan agent booting in a goal-namespaced (never tmux-active) window
// can self-identify its window authoritatively instead of guessing via
// `tmux display-message -p #W` (which returns the session-active window — wrong
// at MaxGoals>1). It takes the already-computed `name` (NOT (goalID, mg)) so the
// persisted marker is provably identical to the createWindow(supWin,...) argument
// — no recompute-drift. Byte-exact: the name is written with NO trailing newline.
// MkdirAll is defensive/idempotent (the goal dir is created earlier in both
// dispatch paths). Mirrors the writeCycleMarker/writeWorktreeMarker idiom.
func (d *Daemon) writeSupervisorWindowMarker(goalID, name string) error {
	dir := filepath.Join(d.workDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir supervisor-window marker dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "supervisor-window"), []byte(name), 0o644); err != nil {
		return fmt.Errorf("write supervisor-window marker: %w", err)
	}
	return nil
}

// writeValidatorWindowMarker publishes the EXACT validator window name the daemon
// computed for this goal to .tmux-cli/goals/<id>/validator-window, mirroring
// writeSupervisorWindowMarker. Now that validator windows are ALWAYS namespaced
// (validator-<ns>), investigate.xml can no longer hardcode bare "validator" as
// VALIDATOR_WID; it reads this marker verbatim instead — authoritative, and immune
// to the unreliable `tmux display-message -p #W` probe ([[plan-wid-is-goal-namespaced]]).
// Takes the already-computed `name` (NOT (goalID, mg)) so the persisted marker is
// provably identical to the createWindow(valWin,...) argument — no recompute-drift.
// Byte-exact: the name is written with NO trailing newline.
func (d *Daemon) writeValidatorWindowMarker(goalID, name string) error {
	dir := filepath.Join(d.workDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir validator-window marker dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "validator-window"), []byte(name), 0o644); err != nil {
		return fmt.Errorf("write validator-window marker: %w", err)
	}
	return nil
}

// worktreeStackEnabled reports whether the per-worktree compose stack lifecycle
// should engage: ONLY when the project's resolved runtime is docker. A local
// (non-docker) project has no compose stack to bring up or tear down — gating here
// keeps a parallel-goal run on a local/Go project (worktrees but no docker) from
// trying to `compose up` a non-existent stack and halting dispatch.
func (d *Daemon) worktreeStackEnabled() bool {
	return ResolveExecRuntime(d.workDir).RunTarget == "docker"
}

// stackBaselineCmd reads the operator-declared "Stack Baseline:" / "Baseline
// Command:" migration command from test-environment.md (resolveBaselineCmd), or ""
// when none is documented — in which case the stack is brought up without a
// migrate step. Best-effort: an absent/unreadable file yields "".
func (d *Daemon) stackBaselineCmd() string {
	data, err := os.ReadFile(filepath.Join(d.workDir, "docs", "architecture", "test-environment.md"))
	if err != nil {
		return ""
	}
	return resolveBaselineCmd(string(data))
}

// bringUpWorktreeStack brings a worktree goal's OWN compose stack
// (taskvisor-<goalID>) to a migrated baseline with cwd=the worktree, so the stack
// sees the goal's uncommitted edits and the validator's commands run
// against the goal's code — not the operator's MAIN stack (task-275).
//
//   - cwd == d.workDir (no-worktree: MaxGoals=1 / non-git) ⇒ no-op, zero compose
//     calls (byte-identical).
//   - RunTarget != docker (local runtime) ⇒ no-op: there is no compose stack.
//   - otherwise ⇒ T1 ComposeStack.Up. An Up error is RETURNED so the dispatch
//     caller HALTS the dispatch (infra/ops fault, like a missing runner) — it never
//     falls back to inline-on-master (the 275 regression) and never charges a
//     code-retry cycle (the caller returns the error BEFORE creating any window).
func (d *Daemon) bringUpWorktreeStack(goal *Goal, cwd string) error {
	if cwd == d.workDir {
		return nil
	}
	er := ResolveExecRuntime(d.workDir)
	if er.RunTarget != "docker" {
		return nil
	}
	stack := NewComposeStack(er, goal.ID, cwd, d.stackBaselineCmd(), d.composeRunner())
	if stack.BaseFile == "" {
		// Base compose file is a deliverable of a LATER goal; defer the stack
		// and run locally — mirrors the local-runtime / no-worktree no-ops.
		// One-shot notice (a successful dispatch flips the goal to running, so
		// it is not re-polled), never a repeating poll-error loop.
		log.Printf("[stack] no base compose file yet for %s, deferring stack; running locally", goal.ID)
		return nil
	}
	timeout := d.scriptTimeout
	if timeout <= 0 {
		timeout = validateScriptTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := stack.Up(ctx); err != nil {
		return fmt.Errorf("bring up worktree stack for %s: %w", goal.ID, err)
	}
	return nil
}

func (d *Daemon) dispatch(goal *Goal, goals *GoalsFile) error {
	// B4: repair-at-dispatch. A planner re-write of goal.md can strip the
	// `## Investigation Config` section post-creation; re-assert it (>=2
	// investigators derived from the in-memory Validate rules) BEFORE
	// writeDispatchMd reads goal.md, so the validator never hard-fails for
	// missing/<2. Runs inside the goals-lock; never blocks dispatch (an
	// unreadable goal.md is logged and skipped — writeDispatchMd's own fallback
	// still applies).
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if rep, err := EnsureInvestigationConfig(d.workDir, goalDir, goal.Validate); err != nil {
		log.Printf("[repair] %s: %v", goal.ID, err)
	} else if rep {
		log.Printf("[repair] %s: re-asserted Investigation Config (was missing/<2)", goal.ID)
	}

	// H3: spec-drift gate — detect and repair goal.md Validation Rules drift
	// from goals.yaml BEFORE writeDispatchMd reads goal.md.
	if drifted, err := goalMDDrift(goalDir, goal); err != nil {
		return fmt.Errorf("spec-drift check: %w", err)
	} else if len(drifted) > 0 {
		log.Printf("[spec-drift] %s: goal.md diverges from goals.yaml on %d commands — repairing", goal.ID, len(drifted))
		if err := repairValidationRules(goalDir, goal); err != nil {
			return fmt.Errorf("spec-drift repair: %w", err)
		}
		d.specRepairs++
		log.Printf("[spec-drift] %s: goal.md repaired from goals.yaml", goal.ID)
	}

	if err := d.writeDispatchMd(goal); err != nil {
		return fmt.Errorf("write dispatch.md: %w", err)
	}

	currentGoalPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal")
	if err := os.MkdirAll(filepath.Dir(currentGoalPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(currentGoalPath, []byte(goal.ID), 0o644); err != nil {
		return err
	}
	mg := d.maxGoals()
	// C7: allocate the per-cycle research dir + write the current-cycle marker(s)
	// BEFORE spawning any worker, so reports land under research/cycle-<N>/.
	if err := d.writeCycleMarker(goal, mg); err != nil {
		return err
	}

	// killGoalWindows is the canonical kill sequence; unlike the old inline 4-kill
	// block it ALSO kills planAuditWindow (windows.go), so the kill-set ⊇ the
	// wait-set built by collectManagedNames below — the daemon can no longer wedge
	// in waitWindowsGone on a surviving plan-audit window. It computes its own mg.
	if err := d.killGoalWindows([]string{goal.ID}); err != nil {
		return err
	}

	allNames := d.collectManagedNames(goal.ID)
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	// Preflight precondition gate: never spawn a worker for a goal whose
	// declared environment is unmet. On a block we emit a blocked signal with a
	// class + remedy runbook, log an owner-facing line, mark the goal blocked
	// (which excludes it from pending selection, halting re-dispatch), and
	// return without spawning or consuming a retry.
	if ok, class, remedy := d.evaluatePreconditions(goal); !ok {
		owner := ownerFor(class)
		failingSpec := failingPreconditionSpec(goal, class, remedy)
		prefix := "[BLOCKED - OPERATOR ACTION REQUIRED]"
		if owner == "planner" {
			prefix = "[SPEC-DEFECT - GENERATOR ACTION REQUIRED]"
		}
		sig := &ValidatorSignal{
			Verdict: "blocked",
			Class:   class,
			Owner:   owner,
			Remedy:  remedy,
			Findings: []ValidationFinding{{
				Rule:   failingSpec,
				Status: "blocked",
				Detail: remedy,
			}},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := SaveValidatorSignal(d.workDir, goal.ID, sig); err != nil {
			return fmt.Errorf("save block signal: %w", err)
		}
		log.Printf("%s %s: precondition %q failed — %s", prefix, goal.ID, failingSpec, remedy)
		goal.Status = GoalBlocked
		// env/infra precondition blocks are auto-resumable: flag the goal so the
		// resume loop (scanPreconditionBlocked, §5) re-evaluates its preconditions
		// and resumes it once they clear. A spec-defect (planner) block needs a
		// re-plan, not a re-check, so it is deliberately left unflagged.
		if owner == "ops" {
			goal.BlockedByPrecondition = true
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return nil
	}

	// Git-freshness preflight (goal-005): fetch origin and refuse to dispatch a
	// diverged checkout. Runs AFTER the precondition block and BEFORE
	// ensureWorktree so a refused dispatch creates no window and consumes no
	// worktree. A no-op when taskvisor.git_freshness is off (d.gitFreshness false,
	// e.g. direct-construct dispatch tests). On a diverged/fetch-fail it blocks the
	// goal and returns nil; only an infra error (signal/goals write) returns err.
	if err := d.gitFreshnessGate(goal, goals); err != nil {
		return err
	}
	if goal.Status == GoalBlocked {
		return nil
	}

	// Per-goal worktree isolation (E1-1a), gated on MaxGoals>1. At MaxGoals=1 this
	// returns d.workDir with NO git call; at MaxGoals>1 it materializes (or reuses)
	// .tmux-cli-worktrees/<id> and returns its path so the supervisor — and the
	// workers it spawns — edit tracked source inside the isolated checkout.
	cwd, err := d.ensureWorktree(goal, mg > 1)
	if err != nil {
		return fmt.Errorf("ensure worktree: %w", err)
	}

	// Bring the goal's per-worktree compose stack up (cwd=worktree, migrated
	// baseline) BEFORE any window is created, so the supervisor — and the validate
	// that follows — exec against the goal's OWN stack (task-275). A no-op on the
	// no-worktree / local-runtime path. An Up failure is an infra/ops halt: returning
	// here leaves the goal GoalPending with NO window created and NO code-retry
	// charged (we never fall back to the operator's master stack — the 275 regression).
	if err := d.bringUpWorktreeStack(goal, cwd); err != nil {
		return err
	}

	supWin := supervisorWindow(goal.ID, mg)
	if err := d.writeSupervisorWindowMarker(goal.ID, supWin); err != nil {
		return fmt.Errorf("write supervisor-window marker: %w", err)
	}
	winInfo, err := d.createWindow(supWin, "", cwd)
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}

	log.Printf("dispatch: waitClaudeBoot done, waiting for prompt...")
	if err := d.waitForPromptOrFail(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("dispatch: wait for supervisor prompt: %w", err)
	}
	log.Printf("dispatch: prompt detected, sending command")

	d.currentGoal = goal.ID
	rt := d.runtime(goal.ID)
	rt.bootConfirmedAt = d.now()
	oldPhase := rt.phase
	rt.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising", goal.ID, phaseName(oldPhase))

	dispatchPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "dispatch.md")
	// Phase decides the first-dispatch command (dispatchcmd.go matrix): most
	// phases run the /tmux:plan pre-planner, but a skip-planning phase (gate)
	// dispatches the supervisor directly, which self-specs its single-task fan-out.
	kind := d.dispatchKindForGoal(goal)
	dispatchCmd := dispatchCommand(kind, DispatchArgs{DispatchPath: dispatchPath, GoalID: goal.ID})
	log.Printf("dispatch: phase=%s kind=%s sending to session=%s window=%s cmd=%s", goal.Phase, kind, d.session, winInfo.TmuxWindowID, dispatchCmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, dispatchCmd); err != nil {
		return fmt.Errorf("send dispatch command (kind=%s): %w", kind, err)
	}
	log.Printf("dispatch: SendMessage returned successfully")

	goal.Status = GoalRunning
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-DISPATCHED id=%s desc=%q cycle=%d]", goal.ID, goal.Description, CurrentCycle(goal)))
	// RC-D: the routing marker is consume-once — the dispatch decision it
	// encoded has now been acted on (worker spawned), so clear it before the
	// persist below or a stale marker would leak into the next cycle's
	// dispatchCandidate decision. Deliberately NOT cleared on the preflight
	// precondition-blocked early return above: no worker was spawned there, so
	// a parked goal keeps its re-plan intent for the §5 resume.
	goal.NextDispatch = ""
	log.Printf("%s: pending -> running", goal.ID)
	if goal.StartedAt == "" {
		goal.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	// Fire AFTER the durable SaveGoals so the notification never precedes state.
	d.fireGoalTransitionHook(goal.ID, "pending", "running", "supervising", CurrentCycle(goal))

	// B7: per-cycle cost record at the dispatch seam. Investigators are unknown
	// pre-validation, so inv counts are zero here (the verdict-resolution line
	// carries the real spawn/reuse split).
	d.logCounters(goal, "dispatch", 0, 0, 0)

	// Successful dispatch ends the stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	rt.dispatchTime = d.now()
	// P3 per-goal wall-clock budget epoch: stamp once per in-flight episode. The
	// IsZero() guard makes redispatch within the same episode PRESERVE the epoch
	// (the budget caps total in-flight wall time, not per-redispatch); clearRuntime
	// zeros it on terminal exit so a re-pended goal gets a fresh budget.
	if rt.activatedAt.IsZero() {
		rt.activatedAt = d.now()
	}
	rt.lastSupervisorStatus = "dispatched"
	return nil
}

// tasksYamlExists probes the per-goal fan-out file only. It deliberately does
// NOT fall back to the top-level planning-queue: a missing per-goal file must
// route to full dispatch (planner re-generation), not retry.
func (d *Daemon) tasksYamlExists(goalID string) bool {
	_, err := os.Stat(tasks.GoalTasksFilePath(d.workDir, goalID))
	return err == nil
}

func (d *Daemon) dispatchRetry(goal *Goal, goals *GoalsFile) error {
	// G5 defensive funnel: demote FIRST, before resetTaskStatuses/
	// injectCorrections, so even the fallback-to-dispatch() paths below run
	// post-demotion. Idempotent — the failure sink that routed here usually
	// already demoted.
	if err := d.demoteSoloLane(goal, goals, "retry dispatch"); err != nil {
		return err
	}
	if err := d.resetTaskStatuses(goal.ID); err != nil {
		log.Printf("dispatchRetry: resetTaskStatuses failed, falling back to full dispatch: %v", err)
		return d.dispatch(goal, goals)
	}

	if err := d.injectCorrections(goal); err != nil {
		log.Printf("dispatchRetry: injectCorrections failed, falling back to full dispatch: %v", err)
		return d.dispatch(goal, goals)
	}

	// H3: spec-drift gate — same as dispatch(), repair goal.md from goals.yaml.
	retryGoalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if drifted, err := goalMDDrift(retryGoalDir, goal); err != nil {
		return fmt.Errorf("spec-drift check: %w", err)
	} else if len(drifted) > 0 {
		log.Printf("[spec-drift] %s: goal.md diverges from goals.yaml on %d commands — repairing (retry)", goal.ID, len(drifted))
		if err := repairValidationRules(retryGoalDir, goal); err != nil {
			return fmt.Errorf("spec-drift repair: %w", err)
		}
		d.specRepairs++
		log.Printf("[spec-drift] %s: goal.md repaired from goals.yaml (retry)", goal.ID)
	}

	if err := d.writeDispatchMd(goal); err != nil {
		return fmt.Errorf("write dispatch.md: %w", err)
	}

	currentGoalPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal")
	if err := os.MkdirAll(filepath.Dir(currentGoalPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(currentGoalPath, []byte(goal.ID), 0o644); err != nil {
		return err
	}
	mg := d.maxGoals()
	// C7: allocate the per-cycle research dir + write the current-cycle marker(s)
	// BEFORE spawning any worker, so reports land under research/cycle-<N>/.
	if err := d.writeCycleMarker(goal, mg); err != nil {
		return err
	}

	// killGoalWindows is the canonical kill sequence; unlike the old inline 4-kill
	// block it ALSO kills planAuditWindow (windows.go), so the kill-set ⊇ the
	// wait-set built by collectManagedNames below — the daemon can no longer wedge
	// in waitWindowsGone on a surviving plan-audit window. It computes its own mg.
	if err := d.killGoalWindows([]string{goal.ID}); err != nil {
		return err
	}

	allNames := d.collectManagedNames(goal.ID)
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	// Git-freshness preflight (goal-005): same gate as dispatch(), before
	// ensureWorktree, so a retry against a now-diverged checkout blocks rather
	// than rebuilding on a stale base.
	if err := d.gitFreshnessGate(goal, goals); err != nil {
		return err
	}
	if goal.Status == GoalBlocked {
		return nil
	}

	// Reuse (or create) this goal's worktree for the retry cycle (E1-1a). ensureWorktree
	// is idempotent: a worktree from an earlier cycle of the SAME goal is reused.
	cwd, err := d.ensureWorktree(goal, mg > 1)
	if err != nil {
		return fmt.Errorf("ensure worktree: %w", err)
	}

	// Bring the goal's per-worktree compose stack up for the retry cycle too (see
	// dispatch). ComposeStack.Up is idempotent (`up -d` is a no-op for an already-up
	// stack), so a retry that reuses the worktree just reconciles the stack. Same
	// infra/ops halt contract: an Up failure returns before any window is created.
	if err := d.bringUpWorktreeStack(goal, cwd); err != nil {
		return err
	}

	supWin := supervisorWindow(goal.ID, mg)
	if err := d.writeSupervisorWindowMarker(goal.ID, supWin); err != nil {
		return fmt.Errorf("write supervisor-window marker: %w", err)
	}
	winInfo, err := d.createWindow(supWin, "", cwd)
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}

	log.Printf("dispatchRetry: waitClaudeBoot done, waiting for prompt...")
	if err := d.waitForPromptOrFail(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("dispatchRetry: wait for supervisor prompt: %w", err)
	}
	log.Printf("dispatchRetry: prompt detected, sending /tmux:supervisor (skip planning)")

	d.currentGoal = goal.ID
	rt := d.runtime(goal.ID)
	rt.bootConfirmedAt = d.now()
	oldPhase := rt.phase
	rt.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising (retry, skip plan)", goal.ID, phaseName(oldPhase))

	// Ship the goal id as a leading token. The daemon writes the GLOBAL
	// .tmux-cli/taskvisor-current-goal marker on every dispatch (last-writer-wins),
	// so a bare /tmux:supervisor would force the supervisor to re-derive its goal
	// from that marker — which, under concurrent dispatch, may name ANOTHER
	// in-flight goal. We know goal.ID authoritatively here, so we hand it over and
	// supervisor.xml step 0b consumes it as the highest-precedence GOAL_ID source.
	supervisorCmd := dispatchCommand(DispatchImplement, DispatchArgs{GoalID: goal.ID})
	log.Printf("dispatchRetry: sending to session=%s window=%s cmd=%s", d.session, winInfo.TmuxWindowID, supervisorCmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, supervisorCmd); err != nil {
		return fmt.Errorf("send supervisor command: %w", err)
	}
	log.Printf("dispatchRetry: SendMessage returned successfully")

	goal.Status = GoalRunning
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-DISPATCHED id=%s desc=%q cycle=%d retry=true]", goal.ID, goal.Description, CurrentCycle(goal)))
	// RC-D: consume the routing marker (see dispatch) — cleared only here on
	// the success path, so a mid-dispatch error keeps the marker for the next
	// tick's re-decision.
	goal.NextDispatch = ""
	log.Printf("%s: pending -> running (retry %d/%d, reusing tasks.yaml)", goal.ID, goal.Retries, goal.MaxRetries)
	if goal.StartedAt == "" {
		goal.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	// Fire AFTER the durable SaveGoals so the notification never precedes state.
	d.fireGoalTransitionHook(goal.ID, "pending", "running", "supervising", CurrentCycle(goal))

	// B7: per-cycle cost record at the re-dispatch seam (zero inv counts, see dispatch).
	d.logCounters(goal, "redispatch", 0, 0, 0)

	// Successful re-dispatch ends the stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	rt.dispatchTime = d.now()
	// P3 per-goal wall-clock budget epoch (see dispatch()). The IsZero() guard means
	// a redispatch within the same episode PRESERVES the epoch set at first dispatch
	// — the budget is NOT extended by retries. Missing this site would leave a goal
	// first dispatched via the retry path with a permanently-zero epoch (never halts).
	if rt.activatedAt.IsZero() {
		rt.activatedAt = d.now()
	}
	rt.lastSupervisorStatus = "dispatched"
	return nil
}

func (d *Daemon) resetTaskStatuses(goalID string) error {
	p := tasks.GoalTasksFilePath(d.workDir, goalID)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}

	tasksRaw, ok := raw["tasks"].([]interface{})
	if !ok {
		return fmt.Errorf("tasks field not found or not a list")
	}

	for _, t := range tasksRaw {
		taskMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		taskMap["status"] = "pending"
	}

	raw["status"] = "ready"

	out, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return atomicWrite(p, out, 0o644)
}

func (d *Daemon) injectCorrections(goal *Goal) error {
	// Read the corrections file handleFailedCycle wrote one cycle earlier. That
	// write used cycleNum = CurrentCycle(goal) computed BEFORE the CodeRetries
	// decrement; this code runs AFTER the decrement (consumed budget is one
	// higher), so the just-written file is cycle-(CurrentCycle(goal)-1).md. This
	// stays in lockstep with handleFailedCycle's unified numbering (C7) — in the
	// code-only path it equals the legacy (MaxCodeRetries - CodeRetries) value,
	// and it stays correct when a prior spec/validation defect also consumed
	// budget (where the legacy formula would read the wrong file).
	cycle := CurrentCycle(goal) - 1
	cycleFile := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID,
		"corrections", fmt.Sprintf("cycle-%d.md", cycle))
	correction, err := os.ReadFile(cycleFile)
	if err != nil {
		return nil
	}

	p := tasks.GoalTasksFilePath(d.workDir, goal.ID)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}

	tasksRaw, ok := raw["tasks"].([]interface{})
	if !ok {
		return nil
	}

	for _, t := range tasksRaw {
		taskMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		ctxRel, ok := taskMap["context"].(string)
		if !ok || ctxRel == "" {
			continue
		}
		ctxPath := filepath.Join(d.workDir, ctxRel)
		existing, err := os.ReadFile(ctxPath)
		if err != nil {
			continue
		}
		amended := fmt.Sprintf("%s\n\n## Prior Corrections (Cycle %d)\n\n%s\n",
			strings.TrimRight(string(existing), "\n"), cycle, string(correction))
		if err := os.WriteFile(ctxPath, []byte(amended), 0o644); err != nil {
			log.Printf("injectCorrections: failed to write %s: %v", ctxPath, err)
		}
	}
	return nil
}

func (d *Daemon) writeDispatchMd(goal *Goal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if err := os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# Dispatch: " + goal.Description + "\n\n")

	sb.WriteString("## Acceptance Criteria\n\n")
	goalMdPath := filepath.Join(goalDir, "goal.md")
	goalMdData, goalMdErr := os.ReadFile(goalMdPath)
	if goalMdErr == nil && strings.TrimSpace(string(goalMdData)) != "" {
		sb.WriteString(string(goalMdData))
		if !strings.HasSuffix(string(goalMdData), "\n") {
			sb.WriteString("\n")
		}
	} else if len(goal.Acceptance) > 0 {
		for _, a := range goal.Acceptance {
			sb.WriteString("- " + a + "\n")
		}
	} else {
		sb.WriteString("(none specified)\n")
	}

	sb.WriteString("\n## Prior Corrections\n\n")
	correctionsDir := filepath.Join(goalDir, "corrections")
	entries, err := os.ReadDir(correctionsDir)
	if err != nil || len(entries) == 0 {
		sb.WriteString("None (first attempt)\n")
	} else {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		hasCorrections := false
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(correctionsDir, e.Name()))
			if err != nil {
				continue
			}
			sb.WriteString("### " + e.Name() + "\n\n")
			sb.WriteString(string(data) + "\n\n")
			hasCorrections = true
		}
		if !hasCorrections {
			sb.WriteString("None (first attempt)\n")
		}
	}

	dispatchPath := filepath.Join(goalDir, "dispatch.md")
	return os.WriteFile(dispatchPath, []byte(sb.String()), 0o644)
}

// writeCorrectionFile emits the per-cycle correction the implementer reads on
// re-dispatch. For every NON-pass finding it writes a structured block
// (### Finding / Command / Output / Expected / Correction) so the full failure
// detail flows VERBATIM through writeDispatchMd + injectCorrections — never
// collapsed to a one-liner. When the signal carries no non-pass findings it
// falls back to NextAction (which the call site primes with the daemon framing
// header) so the file is never empty.
// clipEvidence collapses s to a single greppable log fragment: first non-empty
// line, trimmed, capped at n bytes with an ellipsis when truncated.
func clipEvidence(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// envNoiseLine reports whether a captured-output line is environment/
// infrastructure chatter — docker-compose unset-variable warnings, blank-default
// notices, structured log warn/info lines — rather than a code-level failure
// signal. Such lines must NEVER be surfaced to the implementer as a code
// correction: the DATADOG docker-compose warnings that produced a useless
// goal-001 cycle-1.md are the canonical case.
func envNoiseLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return true
	}
	for _, m := range []string{
		"variable is not set",
		"defaulting to a blank string",
		"level=warning",
		"level=info",
	} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// substantiveEvidence strips env-noise lines from raw and returns the remaining
// failure-bearing text plus whether ANY substantive line survived. A false
// result means the captured output was pure environment noise and carries no
// code signal, so the fallback correction must be classified ops/validator-error,
// never code-defect.
func substantiveEvidence(raw string) (string, bool) {
	var keep []string
	for _, ln := range strings.Split(raw, "\n") {
		if envNoiseLine(ln) {
			continue
		}
		keep = append(keep, ln)
	}
	joined := strings.TrimSpace(strings.Join(keep, "\n"))
	return joined, joined != ""
}

// splitFraming separates the daemon framing header (the first paragraph the call
// site primes into NextAction) from the captured detail/output beneath it.
func splitFraming(nextAction string) (framing, body string) {
	na := strings.TrimSpace(nextAction)
	if i := strings.Index(na, "\n\n"); i >= 0 {
		return strings.TrimSpace(na[:i]), strings.TrimSpace(na[i+2:])
	}
	return na, ""
}

// writeCorrectionFile renders the per-cycle correction. INVARIANT (RC-3/RC-4):
// every fail is recorded as a STRUCTURED block pointing to what ran (Command),
// what failed (the finding rule), and how (Output) — never a raw stderr dump —
// and every fail is LOGGED.
//
// rawCaptured distinguishes the two findingless callers: the code-defect route
// (rawCaptured=true) primes NextAction with the daemon framing header + RAW
// captured validate output, which is restructured + classified here (when the
// captured output is environment/infrastructure noise it is owned by ops, NOT
// blamed on the implementer — the goal-001 DATADOG cycle-1.md case). The
// spec-defect bounce (rawCaptured=false) primes NextAction with an already
// actionable planner instruction, which is written verbatim.
func (d *Daemon) writeCorrectionFile(goalDir string, cycleNum int, signal *ValidatorSignal, rawCaptured bool) error {
	correctionsDir := filepath.Join(goalDir, "corrections")
	if err := os.MkdirAll(correctionsDir, 0o755); err != nil {
		return err
	}
	goalID := filepath.Base(goalDir)

	var sb strings.Builder
	wrote := false
	if signal != nil {
		for _, f := range signal.Findings {
			if f.Status == VerdictPass {
				continue
			}
			fmt.Fprintf(&sb, "### Finding: %s\n", f.Rule)
			fmt.Fprintf(&sb, "Command: %s\n", f.FailingCommand)
			fmt.Fprintf(&sb, "Output: %s\n", f.OutputExcerpt)
			fmt.Fprintf(&sb, "Expected: %s\n", f.ExpectedState)
			fmt.Fprintf(&sb, "Correction: %s\n\n", f.Correction)
			// RC-4: every validator fail is logged — pointing to what ran (Command),
			// what failed (Rule), and how (Output). Never a silent fail.
			log.Printf("%s: validator fail [cycle %d]: rule=%q class=%s owner=%s ran=%q how=%q",
				goalID, cycleNum, f.Rule,
				firstNonEmpty(f.FailureClass, "unclassified"),
				firstNonEmpty(f.Owner, "implementer"),
				f.FailingCommand, clipEvidence(firstNonEmpty(f.OutputExcerpt, f.Detail), 200))
			wrote = true
		}
	}
	if !wrote {
		// No structured findings. Split the daemon framing header from any captured
		// output beneath it.
		full := ""
		if signal != nil {
			full = strings.TrimSpace(signal.NextAction)
		}
		framing, body := splitFraming(full)
		if !rawCaptured || body == "" {
			// Verbatim: the NextAction is already an actionable instruction (a
			// spec-defect bounce or primed next-action), or there is no captured
			// output to restructure. Writing a synthetic finding would only add noise.
			fallback := full
			if fallback == "" {
				fallback = "Implementation failed acceptance criteria — re-check the goal acceptance and fix."
			}
			sb.WriteString(fallback)
			log.Printf("%s: validator fail [cycle %d]: no structured findings; verbatim correction=%q",
				goalID, cycleNum, clipEvidence(fallback, 160))
		} else {
			// RC-3: captured raw output with NO structured finding. NEVER dump it
			// raw. Preserve the framing, then render a STRUCTURED, classified finding
			// pointing to how it failed — and env/infra noise is never blamed on code.
			if framing != "" {
				sb.WriteString(framing)
				sb.WriteString("\n\n")
			}
			evidence, hasSignal := substantiveEvidence(body)
			sb.WriteString("### Finding: validation failed (no structured validator findings)\n")
			if hasSignal {
				sb.WriteString("Command: (the validator did not report the failing command — fix the Investigation Config so the next cycle names it)\n")
				fmt.Fprintf(&sb, "Output: %s\n", evidence)
				sb.WriteString("Expected: the goal's acceptance criteria are met\n")
				sb.WriteString("Correction: address the captured failure above; if it is not a code defect, repair the validation command so the next cycle reports a structured finding\n")
				log.Printf("%s: validator fail [cycle %d]: no structured findings; captured failure=%q",
					goalID, cycleNum, clipEvidence(evidence, 200))
			} else {
				fmt.Fprintf(&sb, "Command: (not reported)\nOutput: %s\n", strings.TrimSpace(body))
				sb.WriteString("Expected: a code-level validation signal\n")
				sb.WriteString("Correction: the validator produced NO actionable code finding — the captured output is environment/infrastructure noise only. Verify the validation command can execute (owner=ops); do NOT change code on this signal.\n")
				log.Printf("%s: validator fail [cycle %d]: NO structured findings and captured output is env-noise only — owner=ops, NOT a code defect",
					goalID, cycleNum)
			}
		}
	}

	filename := fmt.Sprintf("cycle-%d.md", cycleNum)
	return os.WriteFile(filepath.Join(correctionsDir, filename), []byte(sb.String()), 0o644)
}

// writeWorktreeMarker publishes the validator's resolved cwd to
// .tmux-cli/taskvisor-current-worktree (a sibling of taskvisor-current-goal/-cycle,
// always at base d.workDir — the .tmux-cli control plane is shared, never a worktree
// copy). investigate.xml step 3 reads it to thread workingDirectory into each inv-*
// worker so they inherit the goal's worktree. When cwd is the base tree (MaxGoals=1
// or a stale-degraded worktree) the marker is REMOVED, not written, so single-goal
// operation produces zero new artifacts and a stale prior-goal marker can never leak
// a wrong worktree into the next validation. Best-effort: a marker I/O failure must
// never block validation.
func (d *Daemon) writeWorktreeMarker(cwd string) {
	markerPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-worktree")
	if cwd == "" || cwd == d.workDir {
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: clear worktree marker: %v", err)
		}
		return
	}
	if err := os.WriteFile(markerPath, []byte(cwd), 0o644); err != nil {
		log.Printf("warning: write worktree marker: %v", err)
	}
}

func (d *Daemon) createValidatorAndSendPayload(goal *Goal) error {
	valWin := validatorWindow(goal.ID, d.maxGoals())
	// Publish the resolved validator window name so investigate.xml can self-identify
	// VALIDATOR_WID by reading the marker verbatim (never the bare-name guess). Log,
	// don't fail dispatch — a missing marker degrades investigator reply routing but
	// must not block validation. Written BEFORE createWindow so it is on disk by the
	// time the validator agent boots and reads it.
	if err := d.writeValidatorWindowMarker(goal.ID, valWin); err != nil {
		log.Printf("warning: write validator-window marker: %v", err)
	}
	// Route the validator window into the goal's worktree (E1-1c) so the
	// orchestrator and its inv-* investigators run quality/test commands against
	// ONLY this goal's (uncommitted) edits. goalWorkDir is the single chokepoint;
	// under MaxGoals=1 cwd resolves to base and createWindow gets "" — byte-identical.
	cwd := d.goalWorkDir(goal.ID)
	d.writeWorktreeMarker(cwd)
	winInfo, err := d.createWindow(valWin, "", cwd)
	if err != nil {
		return fmt.Errorf("create validator: %w", err)
	}

	if err := d.waitClaudeBoot(valWin, 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot validator: %w", err)
	}

	if err := d.waitForPromptOrFail(valWin, 30*time.Second); err != nil {
		return fmt.Errorf("create validator: wait for prompt: %w", err)
	}

	d.runtime(goal.ID).bootConfirmedAt = d.now()

	goalMdPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "goal.md")
	investigateCmd := dispatchCommand(DispatchInvestigate, DispatchArgs{GoalMdPath: goalMdPath})
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, investigateCmd); err != nil {
		return fmt.Errorf("send investigate command: %w", err)
	}

	return nil
}

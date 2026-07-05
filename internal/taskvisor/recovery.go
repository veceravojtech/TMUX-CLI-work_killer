package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
)

// crashRecovery restores the daemon's in-flight goal state on startup when the
// taskvisor-active guard says a previous run was supervising. plannedRestart
// distinguishes a deliberate exec-replace restart (the taskvisor-restart marker
// was present, i.e. a binary deploy with RestartOnStaleBinary) from a genuine
// crash: the RESUME logic is identical — live windows are resumed in place, never
// recreated — but the supervisor notification says STATE exec-replace-restart
// instead of CRASH-RECOVERY, so a routine deploy is not reported as a crash.
func (d *Daemon) crashRecovery(plannedRestart bool) error {
	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(guardPath); os.IsNotExist(err) {
		return nil
	}

	sessionID, err := d.discoverSession()
	if err != nil {
		log.Printf("crash recovery: no session found: %v", err)
		d.cleanRuntimeMarkers()
		return nil
	}
	d.session = sessionID

	goals, err := LoadGoals(d.workDir)
	if err != nil || goals == nil {
		log.Printf("crash recovery: invalid goals.yaml: %v", err)
		return d.deactivate()
	}

	// Route B (task 445): BEFORE the GoalRunning-only passes, re-integrate any
	// validated-but-uncommitted terminal goal a restart/crash stranded between the
	// GoalDone save (statemachine.go:599) and the completion auto-commit
	// (statemachine.go:606). GoalDone goals are EXCLUDED from the running filter
	// below, so this is an independent scan; it never touches GoalRunning goals.
	finalized, err := d.reintegrateValidatedTerminalGoals(goals)
	if err != nil {
		return err
	}

	// Collect ALL in-flight goals. After a crash NO supervisor survives, and at
	// MaxGoals>1 several goals may have been running concurrently — recovering only
	// the first (the old behavior) strands the rest as zombie GoalRunning entries
	// that permanently consume the running budget (free = maxGoals - running), so no
	// free slot ever refills and the daemon under-schedules forever.
	var running []*Goal
	for i := range goals.Goals {
		if goals.Goals[i].Status == GoalRunning {
			running = append(running, &goals.Goals[i])
		}
	}
	if len(running) == 0 {
		if finalized > 0 {
			// The re-commit pass above already drove advanceToNextGoal, which OWNS the
			// terminal decision (teardown-on-completion, incremental plan-next, or
			// stay-active with a pending successor). A second deactivate here would
			// double-tear-down, so defer to what the pass already decided.
			return nil
		}
		return d.deactivate()
	}

	// Survey the running goals' namespace windows ONCE, up front. The recovery
	// announcement carries the detected-live set as name(@id) pairs so an
	// operator can verify against the post-recovery state whether those windows
	// were resumed in place or recreated: names are REUSED on recreation, the
	// tmux window ID is not — a supervisor-005 that was @23 before and @58 after
	// was recreated. Pass 2 reuses this listing (it used to list lazily; the
	// survey makes the one listing unconditional). A listing failure degrades
	// the survey to "unknown" and is re-raised only if pass 2 needs the list.
	mg := d.maxGoals()
	windows, winErr := d.executor.ListWindows(d.session)
	if winErr != nil {
		log.Printf("crash recovery: list windows for survey: %v", winErr)
	}
	liveStr := "unknown"
	if winErr == nil {
		liveStr = "none"
		if live := liveGoalWindows(windows, running, mg); len(live) > 0 {
			liveStr = strings.Join(live, ",")
		}
	}

	d.mode = modeActive
	if plannedRestart {
		log.Printf("exec-replace restart: resuming %d in-flight goal(s) in place where possible; live windows: %s", len(running), liveStr)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:STATE exec-replace-restart resumed=%d live-windows=%s]", len(running), liveStr))
	} else {
		log.Printf("crash recovery: %d in-flight goal(s); live windows: %s", len(running), liveStr)
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:CRASH-RECOVERY goals=%d live-windows=%s]", len(running), liveStr))
	}
	// CurrentGoal is the legacy scalar head-tracker; bind it to the first in-flight
	// goal for compatibility. The per-goal runtime restored below is the
	// authoritative state at MaxGoals>1.
	d.currentGoal = running[0].ID
	goals.CurrentGoal = running[0].ID

	// Pass 1: a goal with a pending signal resumes its phase in place (still
	// GoalRunning) ONLY when the signal is corroborated by a live window in the
	// CURRENT session — winErr != nil (survey unverifiable → fail-safe resume) OR
	// the goal still owns a goal-namespace window. A stale signal inherited from a
	// destroyed session, whose windows died with it, is NOT honored: the goal falls
	// through to pass 2, which re-dispatches no-live-window goals. Pass 1 REUSES the
	// survey slice fetched above — it adds NO ListWindows call (the count-2 contract).
	// Goals without a signal are always deferred to pass 2.
	var needWindowCheck []*Goal
	for _, g := range running {
		rt := d.runtime(g.ID)
		sig, sigErr := LoadSignal(d.workDir, g.ID)
		if sigErr != nil {
			log.Printf("crash recovery: failed to read signal for %s: %v", g.ID, sigErr)
		}
		if sig != nil && (winErr != nil || goalHasLiveWindow(windows, g.ID, mg)) {
			switch sig.(type) {
			case *SupervisorSignal:
				rt.phase = phaseSupervising
			case *ValidatorSignal:
				rt.phase = phaseValidating
			}
			rt.phaseStartedAt = d.now()
			// Defense-in-depth (task 399): an exec-replace restart restored only
			// rt.phase — re-derive WorktreeDir/Branch from disk so heartbeat/
			// validator-cwd/merge-back consumers see a consistent runtime the moment
			// this goal resumes, not only the two done-path sites. Zero-git no-op at
			// MaxGoals=1 (no worktree on disk ⇒ WorktreeDir stays "").
			d.rehydrateWorktreeDir(g.ID)
			continue
		}
		if sig != nil {
			log.Printf("crash recovery: %s: orphaned running (%s gone in current session) -> re-dispatch", g.ID, supervisorWindow(g.ID, mg))
		}
		needWindowCheck = append(needWindowCheck, g)
	}

	if len(needWindowCheck) == 0 {
		return nil
	}

	// Pass 2: a goal reaches here with no signal, or with a stale signal whose
	// window died in the current session (pass 1 declined to resume it). A live
	// validator/investigator window means work was mid-validation (resume),
	// otherwise the supervisor state is lost and the goal is re-dispatched (re-pended
	// with marker cleanup, or failed when its retry budget is spent). The window
	// list is the survey's; a listing failure surfaces here, where the list is
	// load-bearing (the survey alone tolerates it as "unknown").
	if winErr != nil {
		return winErr
	}
	changed := false
	for _, g := range needWindowCheck {
		rt := d.runtime(g.ID)
		resumed := false
		for _, w := range windows {
			if w.Name == validatorWindow(g.ID, mg) || strings.HasPrefix(w.Name, investigatorPrefix(g.ID, mg)) {
				rt.phase = phaseValidating
				rt.phaseStartedAt = d.now()
				log.Printf("crash recovery: %s validator/investigator window found, resuming validating phase", g.ID)
				resumed = true
				break
			}
			if w.Name == supervisorWindow(g.ID, mg) {
				rt.phase = phaseSupervising
				rt.dispatchTime = d.now()
				rt.bootConfirmedAt = d.now()
				log.Printf("crash recovery: %s supervisor window alive, resuming supervising phase", g.ID)
				resumed = true
				break
			}
		}
		if resumed {
			// Same restart-rehydration as pass 1: restore WorktreeDir/Branch on each
			// resume branch (validator/investigator or supervisor) so a resumed
			// worktree goal merges back on done. Zero-git no-op at MaxGoals=1.
			d.rehydrateWorktreeDir(g.ID)
			continue
		}

		tasksPath := tasks.GoalTasksFilePath(d.workDir, g.ID)
		allDone := false
		if data, rerr := os.ReadFile(tasksPath); rerr == nil {
			allDone = !strings.Contains(string(data), "status: pending") &&
				strings.Contains(string(data), "status: done")
		}

		if allDone {
			rt := d.runtime(g.ID)
			rt.phase = phaseValidating
			rt.validateTime = d.now()
			log.Printf("crash recovery: %s — tasks all done; spawning validator", g.ID)
			if err := d.createValidatorAndSendPayload(g); err != nil {
				log.Printf("crash recovery: %s — validator spawn failed: %v; re-pending", g.ID, err)
			} else {
				continue
			}
		}

		log.Printf("crash recovery: re-dispatching %s (no live window, tasks not all done)", g.ID)
		// G5: a crash re-dispatch is a failure event — demote a solo goal here
		// because a re-pended goal with no tasks.yaml routes to fresh dispatch(),
		// bypassing dispatchRetry's funnel. Log-and-continue on error: recovery
		// must not abort the remaining goals. Pass 1 (resume in place) never
		// demotes — nothing failed there.
		if derr := d.demoteSoloLane(g, goals, "crash-recovery re-dispatch"); derr != nil {
			log.Printf("crash recovery: %s: lane demotion failed: %v (continuing)", g.ID, derr)
		}
		action := "re-dispatch"
		if g.Retries < g.MaxRetries {
			g.Status = GoalPending
			// Orphan reconcile: drop the run timestamp and delete the stale runtime
			// handles the dead session left behind, so the next poll dispatches a
			// clean pending goal (never an active-but-idle zombie). taskvisor-active
			// is deliberately untouched — the daemon stays active.
			g.StartedAt = ""
			d.clearOrphanedGoalMarkers(g.ID)
			log.Printf("crash recovery: %s: orphaned running (%s gone) -> pending", g.ID, supervisorWindow(g.ID, mg))
			if _, serr := os.Stat(tasks.GoalTasksFilePath(d.workDir, g.ID)); serr == nil {
				g.NextDispatch = dispatchImplementer
			}
		} else {
			action = "fail"
			g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			g.Status = GoalFailed
		}
		reportWorkerCrashFn(d, g, mg, action, allDone, windows)
		changed = true
	}

	if changed {
		return SaveGoals(d.workDir, goals)
	}
	return nil
}

// reintegrateValidatedTerminalGoals is the route-B safety net (task 445). A
// restart or crash injected between the durable GoalDone save
// (statemachine.go:599) and the completion auto-commit (statemachine.go:606)
// leaves a validated goal recorded GoalDone but with its in-scope changeset
// UNCOMMITTED — crashRecovery's GoalRunning-only passes never re-process it, so
// the fix is stranded and the goal is later mis-surfaced as
// reason=no-integrated-changes (goal-001 hit exactly this, operator-committed as
// 62feb0a). This pass scans the GoalDone goals independently (they are excluded
// from the running filter) and, for an INLINE (!goalUsesWorktree) non-empty-scope
// goal whose in-scope tree is DIRTY, re-runs autoCommitGoal and completes the
// SAME durable terminal bookkeeping the normal done-path produces
// (resolveTaskOnTerminal + advanceToNextGoal(true) — mirroring
// statemachine.go:606-624). A clean in-scope tree means the done-path already
// committed: it is left completely untouched (no double-commit, no re-fail — the
// double-commit guard). A dirty tree that nonetheless produces no commit
// (autoCommitGoal's warn-only git failure) is left for reconcile rather than
// finalized — recovery never fabricates a committed terminal state, so the
// done-without-integration invariant is preserved. Returns the count of goals
// finalized so the caller can skip the redundant empty-running deactivate when
// this pass already drove the terminal decision.
func (d *Daemon) reintegrateValidatedTerminalGoals(goals *GoalsFile) (int, error) {
	finalized := 0
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalDone || len(g.Scope) == 0 || d.goalUsesWorktree(g) {
			continue
		}
		// Double-commit guard: only re-commit a DIRTY in-scope tree. A clean tree
		// means the normal done-path already committed this goal's changeset, so
		// leave it entirely alone (no new commit, no status change).
		if !d.scopeDirty(g) {
			continue
		}
		committed := d.autoCommitGoal(g)
		if !committed {
			// autoCommitGoal is warn-only: a git failure — or an in-scope diff that
			// emptied out between the guard probe and the commit — returns false. Do
			// NOT fabricate a committed terminal state or re-fail the durably-done
			// goal; leave it for reconcile / the next run. The done-without-integration
			// invariant stays intact because recovery only finalizes on a real commit.
			log.Printf("crash recovery: %s validated terminal goal had a dirty in-scope tree but auto-commit produced no commit — leaving for reconcile", g.ID)
			continue
		}
		log.Printf("crash recovery: %s validated-but-uncommitted terminal goal re-committed on restart (task 445)", g.ID)
		// Mirror the done-path's terminal bookkeeping (statemachine.go:623-624): push
		// the mapped backend task to resolved, then advance/resume with resume=true.
		d.resolveTaskOnTerminal(g, "resolved", doneResolution(g, nil))
		if err := d.advanceToNextGoal(goals, g.ID, true); err != nil {
			return finalized, err
		}
		finalized++
	}
	return finalized, nil
}

// scopeDirty reports whether the goal's in-scope tree carries uncommitted work:
// a non-empty `git status --porcelain -- <scope pathspecs>` under workDir. It is
// the route-B double-commit guard — a clean result means the done-path already
// committed. It reuses autoCommitGit + scopePathspecs so the pathspec semantics
// are byte-identical to autoCommitGoal's own in-scope probe. Any git error is
// warn-logged and treated as NOT dirty (fail-safe: never re-commit against an
// uncertain tree). An empty scope yields false — the caller already requires a
// non-empty scope, but this keeps the helper total.
func (d *Daemon) scopeDirty(g *Goal) bool {
	pathspecs := scopePathspecs(g.Scope)
	if len(pathspecs) == 0 {
		return false
	}
	out, stderr, code, err := d.autoCommitGit(append([]string{"-C", d.workDir, "status", "--porcelain", "--"}, pathspecs...)...)
	if code != 0 || err != nil {
		log.Printf("crash recovery: %s: in-scope dirty probe failed (exit %d, err %v): %s — treating as clean", g.ID, code, err, strings.TrimSpace(stderr))
		return false
	}
	return strings.TrimSpace(out) != ""
}

// windowBelongsToGoal reports whether a tmux window name belongs to goalID's
// namespace — its supervisor or validator window, or any window in its
// investigator/execute worker pool. It is the single per-window match shared by
// liveGoalWindows (the survey announcement) and goalHasLiveWindow (the pass-1
// liveness gate), so "this window is the goal's" is computed exactly one way and
// the two can never drift. Names come only from the window_names.go helpers.
func windowBelongsToGoal(name, goalID string, mg int) bool {
	return name == supervisorWindow(goalID, mg) ||
		name == validatorWindow(goalID, mg) ||
		strings.HasPrefix(name, investigatorPrefix(goalID, mg)) ||
		strings.HasPrefix(name, executePrefix(goalID, mg))
}

// goalHasLiveWindow reports whether any window in the survey slice belongs to
// goalID's namespace. It is the liveness predicate behind pass 1's signal-resume
// gate: a stale signal inherited from a destroyed session resolves to false here
// (its windows are gone), so the goal is re-dispatched instead of resumed onto a
// dead window. It walks the already-fetched survey slice and issues NO new
// ListWindows call (preserving the recovery count-2 contract).
func goalHasLiveWindow(windows []tmux.WindowInfo, goalID string, mg int) bool {
	for _, w := range windows {
		if windowBelongsToGoal(w.Name, goalID, mg) {
			return true
		}
	}
	return false
}

// liveGoalWindows returns a name(@id) entry for every window in the listing that
// belongs to one of the running goals' namespaces — supervisor, validator,
// investigator-pool and execute-pool windows. It is the pure half of the recovery
// survey: the announcement embeds these pairs so recreation is detectable later
// (the name survives a kill+recreate, the tmux window ID does not). Order follows
// the tmux listing, so consecutive surveys of an unchanged session compare equal.
func liveGoalWindows(windows []tmux.WindowInfo, running []*Goal, mg int) []string {
	var live []string
	for _, w := range windows {
		for _, g := range running {
			if windowBelongsToGoal(w.Name, g.ID, mg) {
				live = append(live, fmt.Sprintf("%s(%s)", w.Name, w.TmuxWindowID))
				break
			}
		}
	}
	return live
}

// clearOrphanedGoalMarkers idempotently deletes the stale runtime handles a dead
// session left pointing at an orphaned goal window: the per-goal supervisor-window
// and current-cycle markers, plus the top-level taskvisor-current-goal and
// taskvisor-current-cycle pointers (paths byte-identical to dispatch.go's
// writeSupervisorWindowMarker/writeCycleMarker). It deliberately NEVER removes
// taskvisor-active — the daemon stays active and dispatches the next pending goal.
// os.IsNotExist is tolerated (the markers may already be absent, and the call is
// idempotent across recovery cycles); any other remove error is logged but never
// fatal, so a marker-delete failure cannot abort recovery of the remaining goals.
func (d *Daemon) clearOrphanedGoalMarkers(goalID string) {
	clearGoalRuntimeMarkers(d.workDir, goalID)
}

// clearGoalRuntimeMarkers is the free-function core of clearOrphanedGoalMarkers:
// it idempotently deletes the same four stale runtime handles (the per-goal
// supervisor-window + current-cycle markers and the top-level
// taskvisor-current-goal + taskvisor-current-cycle pointers) for goalID under
// workDir. Extracted so the consume-path zombie reconcile (zombie_reconcile.go)
// can clean up an orphaned goal with byte-identical semantics to startup crash
// recovery — detection and cleanup can never drift between the two entry points.
// It deliberately NEVER removes taskvisor-active (the daemon stays active);
// os.IsNotExist is tolerated and any other remove error is logged but non-fatal.
func clearGoalRuntimeMarkers(workDir, goalID string) {
	paths := []string{
		filepath.Join(workDir, ".tmux-cli", "goals", goalID, "supervisor-window"),
		filepath.Join(workDir, ".tmux-cli", "goals", goalID, "current-cycle"),
		filepath.Join(workDir, ".tmux-cli", "taskvisor-current-goal"),
		filepath.Join(workDir, ".tmux-cli", "taskvisor-current-cycle"),
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("crash recovery: %s: clear orphaned marker %s: %v", goalID, p, err)
		}
	}
}

// reportWorkerCrashFn is the indirection the genuine-crash branch invokes to emit
// the recovered-worker-crash report. It defaults to (*Daemon).reportWorkerCrash
// and is a package var ONLY so recovery_test.go can count and inspect crash
// reports deterministically: producer.Client is a concrete type with an
// unexported constructor and no daemon-level injection seam, so a swappable
// function is the only way to observe a submission without a live backend.
// Production never reassigns it.
var reportWorkerCrashFn = (*Daemon).reportWorkerCrash

// reportWorkerCrash assembles and submits ONE execute/warning report for a
// recovered worker crash — a GoalRunning goal whose worker window vanished and is
// being re-dispatched (action="re-dispatch") or failed (action="fail"). The
// submission is delegated to reportFailure, whose nil-producer no-op lives in
// submitReport, so recovery never blocks on the network and never panics with
// reporting disabled. Both backend NotBlank contract fields are explicit and
// action-dependent: a re-dispatch is self-healing (watch for recurrence), a fail
// is terminal (diagnose, then goal reset). The only I/O here is the bounded
// log-tail read; surviving windows and allDone are reused from the caller (no
// extra ListWindows or tasks-file read).
func (d *Daemon) reportWorkerCrash(g *Goal, mg int, action string, allDone bool, surviving []tmux.WindowInfo) {
	logPath := filepath.Join(d.workDir, ".tmux-cli", "logs", "taskvisor.log")
	payload := crashReportPayload(g, mg, action, allDone, surviving, readLogTail(logPath))
	title := fmt.Sprintf("Worker crash recovered for %s", g.ID)
	desc := fmt.Sprintf(
		"GoalRunning goal %s lost its worker window (expected %s); recovery action: %s.",
		g.ID, supervisorWindow(g.ID, mg), action,
	)
	var fix string
	if action == "re-dispatch" {
		fix = fmt.Sprintf(
			"No action needed yet: %s was re-dispatched automatically. If crashes recur, inspect the log_tail payload and .tmux-cli/logs/taskvisor.log for what killed the worker window.",
			g.ID)
	} else {
		fix = fmt.Sprintf(
			"Diagnose the crash from the log_tail payload and .tmux-cli/logs/taskvisor.log, fix the cause, then run `taskvisor goal reset %s` to re-pend the goal.",
			g.ID)
	}
	expected := expectedGreenState(*g)
	if strings.TrimSpace(expected) == "" {
		expected = fmt.Sprintf("Goal %s runs to a terminal status with its worker window alive until completion.", g.ID)
	}
	d.reportFailure("execute", "warning", title, desc, payload,
		withProposedFix(fix), withExpectedGreenState(expected))
}

// crashReportPayload builds the worker-crash report payload deterministically (no
// I/O) so it is fully unit-testable. status_before is always GoalRunning (the
// only status that reaches the crash branch); surviving_windows lists the live
// window names from the slice the recovery loop already fetched; log_tail carries
// the bounded tail of the SHARED daemon log (labelled via log_tail_source so a
// consumer does not over-attribute it to this one goal).
func crashReportPayload(g *Goal, mg int, action string, allDone bool, surviving []tmux.WindowInfo, logTail string) map[string]any {
	names := make([]string, 0, len(surviving))
	for _, w := range surviving {
		names = append(names, w.Name)
	}
	return map[string]any{
		"goal_id":           g.ID,
		"status_before":     GoalRunning,
		"recovery_action":   action,
		"expected_window":   supervisorWindow(g.ID, mg),
		"surviving_windows": names,
		"tasks_all_done":    allDone,
		"log_tail":          logTail,
		"log_tail_source":   "shared daemon log (.tmux-cli/logs/taskvisor.log)",
	}
}

// readLogTail returns a bounded tail of the file at path: at most the last 4096
// bytes, then at most the last 50 lines of that slice. Any read error (missing
// file, permission) yields "" — the report is still sent without a tail.
func readLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	return strings.Join(lines, "\n")
}

// clearBlock lifts every hold flag from a goal: the BlockedBy upstream pointer
// and the BlockedByPrecondition env/infra flag. It NEVER touches status or any
// retry counter — the caller decides the resulting status (always GoalPending on
// the resume paths). Centralizing the clear keeps the two hold fields in lock-step.
func (d *Daemon) clearBlock(g *Goal) {
	g.BlockedBy = ""
	g.BlockedByPrecondition = false
}

// resumeDownstream is the SYNCHRONOUS resume path, called from advanceToNextGoal
// when an upstream goal reaches GoalDone. For every goal still GoalPending whose
// BlockedBy points at the just-completed doneGoalID, it clears the hold so the
// goal becomes dispatchable again (its dependency is now satisfied). It mutates
// the in-memory *GoalsFile in place; the caller (poll → advanceToNextGoal) already
// holds the goals lock and persists via SaveGoals, so this does NOT re-acquire the
// lock (doing so would deadlock the flock). It skips goals that are not pending or
// whose BlockedBy does not match doneGoalID — including the "deps_unsatisfied"
// sentinel, which is never a real goal ID — and it touches NO retry budget.
func (d *Daemon) resumeDownstream(goals *GoalsFile, doneGoalID string) {
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalPending || g.BlockedBy != doneGoalID {
			continue
		}
		d.clearBlock(g)
		log.Printf("%s: upstream %s done — cleared block, staying pending for re-validation", g.ID, doneGoalID)
	}
}

// resumeDownstreamLoop is the §5 background auto-resume poll. On every
// autoResumeInterval tick it re-evaluates precondition-blocked goals; it exits
// cleanly when ctx is cancelled (the daemon's shared ctx from setupSignalHandler),
// leaking no goroutine. The interval is read from a Daemon field so tests can set a
// tiny cadence; a non-positive value falls back to 30s.
func (d *Daemon) resumeDownstreamLoop(ctx context.Context) {
	interval := d.autoResumeInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanPreconditionBlocked()
		}
	}
}

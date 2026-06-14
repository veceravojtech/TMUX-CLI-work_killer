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
				if passed, reason, _, rerr := d.runValidateScript(g); rerr == nil {
					rt.scriptPassed = passed
					rt.scriptReason = reason
				}
				log.Printf("crash recovery: %s supervisor window alive, resuming supervising phase (scriptPassed=%v)", g.ID, rt.scriptPassed)
				resumed = true
				break
			}
		}
		if resumed {
			continue
		}

		tasksPath := tasks.GoalTasksFilePath(d.workDir, g.ID)
		allDone := false
		if data, rerr := os.ReadFile(tasksPath); rerr == nil {
			allDone = !strings.Contains(string(data), "status: pending") &&
				strings.Contains(string(data), "status: done")
		}

		if allDone {
			if passed, _, _, verr := d.runValidateScript(g); verr == nil && passed {
				rt := d.runtime(g.ID)
				rt.phase = phaseValidating
				rt.scriptPassed = true
				rt.validateTime = d.now()
				log.Printf("crash recovery: %s — tasks all done + validate.sh passes; spawning investigator", g.ID)
				if err := d.createValidatorAndSendPayload(g); err != nil {
					log.Printf("crash recovery: %s — validator spawn failed: %v; re-pending", g.ID, err)
				} else {
					continue
				}
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
	paths := []string{
		filepath.Join(d.workDir, ".tmux-cli", "goals", goalID, "supervisor-window"),
		filepath.Join(d.workDir, ".tmux-cli", "goals", goalID, "current-cycle"),
		filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal"),
		filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-cycle"),
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

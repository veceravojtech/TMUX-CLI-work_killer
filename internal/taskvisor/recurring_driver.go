package taskvisor

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"gopkg.in/yaml.v3"
)

// recurring_driver.go — the daemon-side cycle machine for the recurring
// supervisor task (dispatch → settle → advance → finish). This file currently
// holds ONLY the runtime enum, the per-cycle runtime struct, and NO-OP stubs:
// the real driver behavior is the GREEN half of a red→green pair and lands in a
// later goal. The call SEAMS into tick() (statemachine.go) and poll() (daemon.go)
// route through these stubs so the package compiles and the TestRecurring* suite
// fails on ASSERTIONS (not compile errors).
//
// cyclePhase is a daemon-RUNTIME enum, deliberately distinct from the PERSISTED
// RecurringCycle.Phase string (recurring.go): cyclePhaseName is the int→string
// bridge, mirroring the type phase int / phaseName(p) pair (daemon.go:27-33,
// diagnostics.go:34).

// cyclePhase is the in-flight phase of one recurring cycle, mirroring `type phase
// int`. The zero value is cyclePhaseDispatching: a freshly-seeded cycle starts by
// dispatching its prompt to the supervisor.
type cyclePhase int

const (
	cyclePhaseDispatching cyclePhase = iota
	cyclePhaseSettling
	cyclePhaseSettled
)

// cyclePhaseName maps a cyclePhase to its persisted RecurringCycle.Phase string,
// mirroring phaseName(p phase) string (diagnostics.go:34).
func cyclePhaseName(p cyclePhase) string {
	switch p {
	case cyclePhaseDispatching:
		return "dispatching"
	case cyclePhaseSettling:
		return "settling"
	case cyclePhaseSettled:
		return "settled"
	default:
		return "dispatching"
	}
}

// recurringRuntime holds the in-flight cycle runtime hoisted off Daemon, mirroring
// goalRuntime (daemon.go:43-95). The zero value (phase == cyclePhaseDispatching,
// zero timers, empty hash) is "never dispatched". Timers are read against d.now()
// so tests advance a fakeClock deterministically.
type recurringRuntime struct {
	phase            cyclePhase
	dispatchedAt     time.Time
	lastProgressHash string
	lastActivityAt   time.Time
}

// cyclePhaseFromName is the reverse of cyclePhaseName: the persisted
// RecurringCycle.Phase string → the daemon-runtime enum. An unknown/empty value
// defaults to cyclePhaseDispatching (a freshly-seeded cycle).
func cyclePhaseFromName(s string) cyclePhase {
	switch s {
	case "settling":
		return cyclePhaseSettling
	case "settled":
		return cyclePhaseSettled
	default:
		return cyclePhaseDispatching
	}
}

// recurStamp renders an RFC3339 UTC timestamp for the persisted RecurringCycle
// (named to avoid colliding with the test-file rfc3339 helper).
func recurStamp(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }

// recurParse reverses recurStamp; an empty/unparseable string yields the zero
// time, which makes elapsed-since comparisons satisfy their floors (a never-set
// timer reads as "long ago").
func recurParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// recurDur converts a whole-second config field to a time.Duration.
func recurDur(sec int) time.Duration { return time.Duration(sec) * time.Second }

// recurringMarkerFile is the recurring-active guard marker path (named to avoid
// colliding with the test-file recurringMarkerPath helper).
func (d *Daemon) recurringMarkerFile() string {
	return filepath.Join(d.workDir, ".tmux-cli", "recurring-active")
}

// saveRecurring persists a single mutation best-effort, mirroring the lock-free
// SaveRecurring contract (the tick already holds the goals flock).
func (d *Daemon) saveRecurring(task *RecurringTask) {
	if err := SaveRecurring(d.workDir, &RecurringFile{Task: task}); err != nil {
		log.Printf("[RECUR] save recurring.yaml: %v", err)
	}
}

// recurWorkerCount counts live worker windows (classifyWindow non-empty and not a
// supervisor pane). A listWindows error is surfaced so callers treat it as
// activity / not-drained.
func (d *Daemon) recurWorkerCount() (int, error) {
	wins, err := d.listWindows()
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range wins {
		c := classifyWindow(wins[i].Name)
		if c != "" && c != "supervisor" {
			n++
		}
	}
	return n, nil
}

// recurPendingCount counts pending/in_progress tasks in the TOP-LEVEL
// .tmux-cli/tasks.yaml. An absent or unparseable file yields 0 (does not block a
// settle on its own).
func (d *Daemon) recurPendingCount() int {
	data, err := os.ReadFile(tasks.TasksFilePath(d.workDir))
	if err != nil {
		return 0
	}
	var tf tasks.TasksFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return 0
	}
	n := 0
	for _, t := range tf.Tasks {
		if t.Status == tasks.StatusPending || t.Status == tasks.StatusInProgress {
			n++
		}
	}
	return n
}

// recurSupervisorHash captures the bare supervisor pane and hashes it. Any
// window-lookup or capture error is surfaced so callers treat it as activity.
func (d *Daemon) recurSupervisorHash() (string, error) {
	win, err := d.findWindowByName("supervisor")
	if err != nil {
		return "", err
	}
	out, err := d.executor.CaptureWindowOutput(d.session, win.TmuxWindowID)
	if err != nil {
		return "", err
	}
	return hashPane(out), nil
}

// recurSettle moves the in-flight cycle to settled with the given outcome,
// stamps last_activity_at, appends a History copy, and increments the completed
// count. Shared by the normal drain-settle and the wall-clock force-settle.
func (d *Daemon) recurSettle(task *RecurringTask, outcome string) {
	task.CurrentCycle.Phase = cyclePhaseName(cyclePhaseSettled)
	task.CurrentCycle.Outcome = outcome
	task.CurrentCycle.LastActivityAt = recurStamp(d.now())
	task.History = append(task.History, task.CurrentCycle)
	task.CompletedCycles++
}

// driveRecurring is the per-tick recurring cycle machine: it loads recurring.yaml,
// reconstructs the runtime from the persisted CurrentCycle, runs one
// dispatch → settle → advance → finish step, and persists the mutation. It is a
// no-op when no active recurring task is present. All time flows through d.now();
// every tmux send is best-effort (log + swallow), mirroring notifySupervisor.
func (d *Daemon) driveRecurring(goals *GoalsFile) {
	rf, err := LoadRecurring(d.workDir)
	if err != nil {
		log.Printf("[RECUR] load recurring.yaml: %v", err)
		return
	}
	if rf == nil || rf.Task == nil || rf.Task.Status != RecurringActive {
		return
	}
	task := rf.Task
	rt := &recurringRuntime{
		phase:            cyclePhaseFromName(task.CurrentCycle.Phase),
		dispatchedAt:     recurParse(task.CurrentCycle.DispatchedAt),
		lastProgressHash: task.CurrentCycle.LastProgressHash,
		lastActivityAt:   recurParse(task.CurrentCycle.LastActivityAt),
	}

	switch rt.phase {
	case cyclePhaseDispatching:
		win, err := d.findWindowByName("supervisor")
		if err != nil {
			log.Printf("[RECUR] supervisor window not found, skipping dispatch: %v", err)
			return
		}
		if task.ClearBetween {
			if err := d.executor.SendMessageWithDelay(d.session, win.TmuxWindowID, "/clear"); err != nil {
				log.Printf("[RECUR] send /clear: %v", err)
			}
		}
		recurCmd := dispatchCommand(DispatchRecurringSupervisor, DispatchArgs{Prompt: task.Prompt})
		if err := d.executor.SendMessageWithDelay(d.session, win.TmuxWindowID, recurCmd); err != nil {
			log.Printf("[RECUR] send supervisor prompt: %v", err)
		}
		task.CurrentCycle.Phase = cyclePhaseName(cyclePhaseSettling)
		task.CurrentCycle.DispatchedAt = recurStamp(d.now())
		d.saveRecurring(task)

	case cyclePhaseSettling:
		now := d.now()
		// Wall-clock cap: force settle regardless of unmet drain.
		if task.MaxCycleWallSec > 0 && now.Sub(rt.dispatchedAt) > recurDur(task.MaxCycleWallSec) {
			d.recurSettle(task, "timeout")
			d.saveRecurring(task)
			return
		}
		if d.recurringSettled(rt, task) {
			d.recurSettle(task, "settled")
			d.saveRecurring(task)
			return
		}
		// Still active OR waiting out grace: refresh the progress hash always, but
		// refresh last_activity_at ONLY on observed activity so the idle-grace
		// timer can accumulate to a settle when the cycle is fully drained.
		wc, werr := d.recurWorkerCount()
		pending := d.recurPendingCount()
		curHash := rt.lastProgressHash
		if h, herr := d.recurSupervisorHash(); herr == nil {
			curHash = h
		}
		activity := werr != nil || wc > 0 || pending > 0 || curHash != rt.lastProgressHash
		if activity {
			task.CurrentCycle.LastActivityAt = recurStamp(now)
		}
		task.CurrentCycle.LastProgressHash = curHash
		d.saveRecurring(task)

	case cyclePhaseSettled:
		now := d.now()
		if task.CompletedCycles >= task.TotalCycles {
			// Finish: go idle WITHOUT routing through deactivateOnCompletion /
			// notifyCompletion. The ALL-COMPLETE notice is LOG-ONLY — never sent to
			// the supervisor window.
			task.Status = RecurringDone
			d.saveRecurring(task)
			if err := os.Remove(d.recurringMarkerFile()); err != nil && !os.IsNotExist(err) {
				log.Printf("[RECUR] remove recurring-active marker: %v", err)
			}
			d.mode = modeIdle
			log.Printf("[RECUR:ALL-COMPLETE id=%s cycles=%d/%d]", task.ID, task.CompletedCycles, task.TotalCycles)
			return
		}
		if now.Sub(rt.lastActivityAt) >= recurDur(task.CooldownSec) {
			// Advance: re-enter dispatching but do NOT dispatch this tick.
			task.CurrentCycle.Index++
			task.CurrentCycle.Phase = cyclePhaseName(cyclePhaseDispatching)
			task.CurrentCycle.DispatchedAt = ""
			task.CurrentCycle.LastActivityAt = ""
			task.CurrentCycle.LastProgressHash = ""
			task.CurrentCycle.Outcome = ""
			d.saveRecurring(task)
			return
		}
		// Cooldown not elapsed: stay settled.
	}
}

// recurringSettled reports whether the in-flight cycle has STRICTLY drained AND
// aged past its idle-grace / boot-min floors. drain = no live worker AND no
// pending task AND a static supervisor pane AND drained goals. age =
// now-DispatchedAt >= boot_min AND now-LastActivityAt >= idle_grace. Any error
// is treated as NOT settled (still active) — the cycle never settles on missing
// evidence. Reads are lock-free (LoadGoals/os.ReadFile) — safe inside the tick's
// held goals flock.
func (d *Daemon) recurringSettled(rt *recurringRuntime, task *RecurringTask) bool {
	wc, err := d.recurWorkerCount()
	if err != nil || wc != 0 {
		return false
	}
	if d.recurPendingCount() != 0 {
		return false
	}
	curHash, err := d.recurSupervisorHash()
	if err != nil || curHash != rt.lastProgressHash {
		return false
	}
	if g, _ := LoadGoals(d.workDir); g != nil {
		if g.AnyRunning() || len(g.RunnableCandidates()) != 0 {
			return false
		}
	}
	now := d.now()
	if now.Sub(rt.dispatchedAt) < recurDur(task.BootMinSec) {
		return false
	}
	if now.Sub(rt.lastActivityAt) < recurDur(task.IdleGraceSec) {
		return false
	}
	return true
}

// recurringActive reports whether a recurring task is mid-run, gating the tick
// step-5 teardown predicate (statemachine.go: `&& !d.recurringActive()`). A load
// error is fail-safe false (allows normal teardown).
func (d *Daemon) recurringActive() bool {
	rf, err := LoadRecurring(d.workDir)
	return err == nil && rf != nil && rf.Task != nil && rf.Task.Status == RecurringActive
}

// recurringPickup is the poll() modeIdle activation seam: an active recurring.yaml
// activates the daemon into modeActive (writing the taskvisor-active +
// recurring-active markers) and returns true. With no active task it reconciles a
// stale recurring-active marker (idempotent) and returns false, leaving the
// existing taskvisor-start pickup path unchanged.
func (d *Daemon) recurringPickup() bool {
	rf, err := LoadRecurring(d.workDir)
	if err != nil || rf == nil || rf.Task == nil || rf.Task.Status != RecurringActive {
		if rerr := os.Remove(d.recurringMarkerFile()); rerr != nil && !os.IsNotExist(rerr) {
			log.Printf("[RECUR] reconcile stale recurring-active marker: %v", rerr)
		}
		return false
	}
	dir := filepath.Join(d.workDir, ".tmux-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[RECUR] mkdir .tmux-cli: %v", err)
		return false
	}
	if err := os.WriteFile(filepath.Join(dir, "taskvisor-active"), nil, 0o644); err != nil {
		log.Printf("[RECUR] write taskvisor-active marker: %v", err)
		return false
	}
	if err := os.WriteFile(d.recurringMarkerFile(), nil, 0o644); err != nil {
		log.Printf("[RECUR] write recurring-active marker: %v", err)
		return false
	}
	d.mode = modeActive
	return true
}

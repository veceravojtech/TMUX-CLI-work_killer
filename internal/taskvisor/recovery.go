package taskvisor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
)

func (d *Daemon) crashRecovery() error {
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

	d.mode = modeActive
	// CurrentGoal is the legacy scalar head-tracker; bind it to the first in-flight
	// goal for compatibility. The per-goal runtime restored below is the
	// authoritative state at MaxGoals>1.
	d.currentGoal = running[0].ID
	goals.CurrentGoal = running[0].ID

	// Pass 1: goals with a pending signal resume their phase in place (still
	// GoalRunning) — exactly the old single-goal behavior, now applied to each.
	// Goals without a signal are deferred to pass 2, which is the ONLY path that
	// needs the window list (kept lazy so the signal-resume path never lists).
	var needWindowCheck []*Goal
	for _, g := range running {
		rt := d.runtime(g.ID)
		sig, sigErr := LoadSignal(d.workDir, g.ID)
		if sigErr != nil {
			log.Printf("crash recovery: failed to read signal for %s: %v", g.ID, sigErr)
		}
		if sig != nil {
			switch sig.(type) {
			case *SupervisorSignal:
				rt.phase = phaseSupervising
			case *ValidatorSignal:
				rt.phase = phaseValidating
			}
			rt.phaseStartedAt = d.now()
			continue
		}
		needWindowCheck = append(needWindowCheck, g)
	}

	if len(needWindowCheck) == 0 {
		return nil
	}

	// Pass 2: no signal — a live validator/investigator window means work was
	// mid-validation (resume), otherwise the supervisor state is lost and the goal
	// is re-dispatched (re-pended, or failed when its retry budget is spent).
	windows, err := d.executor.ListWindows(d.session)
	if err != nil {
		return err
	}
	mg := d.maxGoals()
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
				if passed, _, rerr := d.runValidateScript(g); rerr == nil {
					rt.scriptPassed = passed
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
			if passed, _, verr := d.runValidateScript(g); verr == nil && passed {
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
		if g.Retries < g.MaxRetries {
			g.Status = GoalPending
			if _, serr := os.Stat(tasks.GoalTasksFilePath(d.workDir, g.ID)); serr == nil {
				g.NextDispatch = dispatchImplementer
			}
		} else {
			g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			g.Status = GoalFailed
		}
		changed = true
	}

	if changed {
		return SaveGoals(d.workDir, goals)
	}
	return nil
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

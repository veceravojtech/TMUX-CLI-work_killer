package taskvisor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// elaborationTimeout bounds a single Tier-2 elaboration episode. An elaborator
// that has not flipped its goal out of GoalRoadmap within this window is treated
// as wedged: driveElaboratingGoals blocks the goal (owner-visible) and tears down
// its window rather than re-dispatching into the same wedge. Generous because
// elaboration reads the live tree and authors fields, but well below a human's
// patience — and it is the fail-safe that makes wiring roadmap goals into the live
// dispatch loop safe (a missing/looping elaborator can never spin forever).
const elaborationTimeout = 20 * time.Minute

// dispatchElaborate dispatches a GoalRoadmap candidate to the Tier-2 elaborator
// (/tmux:elaborate). It deliberately does NOT flip the goal to GoalRunning: the
// goal stays GoalRoadmap for the whole elaboration episode, and completion is
// observed when the elaborator authors the goal's concrete fields and flips it to
// GoalPending via the goal-edit tool (driveElaboratingGoals). The runtime phase
// (phaseElaborating) is the in-flight marker that keeps the elaboration dispatch
// loop from re-selecting the same still-GoalRoadmap goal every tick.
//
// It mirrors dispatch()'s window plumbing but skips the concrete-goal preamble
// (investigation-config repair, spec-drift, validate.sh regen) — a skeleton has
// none of those surfaces.
func (d *Daemon) dispatchElaborate(goal *Goal, goals *GoalsFile) error {
	mg := d.maxGoals()

	// The elaborator reads the goal's dispatch.md for context, same as the planner.
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
	if err := d.writeCycleMarker(goal, mg); err != nil {
		return err
	}

	if err := d.killGoalWindows([]string{goal.ID}); err != nil {
		return err
	}
	if err := d.waitWindowsGone(d.collectManagedNames(goal.ID), 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	cwd, err := d.ensureWorktree(goal, mg > 1)
	if err != nil {
		return fmt.Errorf("ensure worktree: %w", err)
	}

	supWin := supervisorWindow(goal.ID, mg)
	if err := d.writeSupervisorWindowMarker(goal.ID, supWin); err != nil {
		return fmt.Errorf("write supervisor-window marker: %w", err)
	}
	winInfo, err := d.createWindow(supWin, "", cwd)
	if err != nil {
		return fmt.Errorf("create elaborate window: %w", err)
	}
	if err := d.waitClaudeBoot(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}
	if err := d.waitForPromptOrFail(supWin, 30*time.Second); err != nil {
		return fmt.Errorf("elaborate: wait for prompt: %w", err)
	}

	d.currentGoal = goal.ID
	rt := d.runtime(goal.ID)
	rt.bootConfirmedAt = d.now()
	oldPhase := rt.phase
	rt.phase = phaseElaborating
	log.Printf("%s: phase %s -> elaborating", goal.ID, phaseName(oldPhase))

	dispatchPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "dispatch.md")
	elabCmd := fmt.Sprintf("/tmux:elaborate %s %s", dispatchPath, goal.ID)
	log.Printf("dispatchElaborate: sending to session=%s window=%s cmd=%s", d.session, winInfo.TmuxWindowID, elabCmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, elabCmd); err != nil {
		return fmt.Errorf("send elaborate command: %w", err)
	}

	// Goal status stays GoalRoadmap (see func doc) — only runtime state changes, so
	// nothing on the goal is persisted here.
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-ELABORATING id=%s desc=%q]", goal.ID, goal.Description))
	d.idleTicks = 0
	d.stallReported = false
	rt.dispatchTime = d.now()
	if rt.activatedAt.IsZero() {
		rt.activatedAt = d.now()
	}
	return nil
}

// elaboratingGoalIDs returns the ids of goals currently mid-elaboration (runtime
// phase phaseElaborating). They are still GoalRoadmap on disk — the runtime phase
// is the only in-flight marker — so this is a pure runtime-map read, returned
// sorted for deterministic iteration.
func (d *Daemon) elaboratingGoalIDs() []string {
	var out []string
	for id, rt := range d.runtimes {
		if rt.phase == phaseElaborating {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// driveElaboratingGoals advances every mid-elaboration goal: COMPLETION when the
// elaborator has flipped it out of GoalRoadmap (to GoalPending via goal-edit, or to
// GoalBlocked if it could not spec an unmet dep), or a fail-safe BLOCK when the
// episode exceeds elaborationTimeout. On either exit it tears down the goal's
// window and clears the runtime so a now-pending goal re-dispatches through the
// normal path with a FRESH wall-clock budget. Returns whether any goal's persisted
// state changed (the caller saves goals.yaml when true). Mirrors the running-goal
// drive loop but for the GoalRoadmap→GoalPending elaboration episode.
func (d *Daemon) driveElaboratingGoals(goals *GoalsFile) (bool, error) {
	changed := false
	for _, id := range d.elaboratingGoalIDs() {
		g, ok := goals.GoalByID(id)
		if !ok {
			// Goal vanished from goals.yaml (pruned) — drop the stale runtime.
			d.clearRuntime(id)
			continue
		}
		if g.Status != GoalRoadmap {
			// The elaborator authored the concrete fields and flipped the goal out of
			// roadmap. Episode over: tear down and let the normal dispatch path pick up
			// the now-pending goal with a fresh runtime budget.
			log.Printf("%s: elaboration complete (status=%s) — clearing elaborating runtime", id, g.Status)
			if err := d.killGoalWindows([]string{id}); err != nil {
				return changed, err
			}
			d.clearRuntime(id)
			continue
		}
		rt := d.runtime(id)
		if !rt.dispatchTime.IsZero() && d.now().Sub(rt.dispatchTime) >= elaborationTimeout {
			log.Printf("%s: elaboration timed out after %s — blocking (blocked_by=elaboration-timeout)", id, elaborationTimeout)
			if err := d.killGoalWindows([]string{id}); err != nil {
				return changed, err
			}
			g.Status = GoalBlocked
			g.BlockedBy = "elaboration-timeout"
			d.clearRuntime(id)
			changed = true
		}
	}
	return changed, nil
}

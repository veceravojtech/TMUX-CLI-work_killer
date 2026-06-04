package taskvisor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (d *Daemon) deactivateOnCompletion(goals *GoalsFile) error {
	// Never tear down while a resumable precondition park is outstanding: AllResolved
	// counts GoalBlocked as resolved, but a BlockedByPrecondition park has pending
	// work that scanPreconditionBlocked will re-pend, so deactivating here would
	// deadlock it permanently (nothing would re-dispatch). Keys ONLY on the flag, so
	// manual/external holds (no flag) still allow deactivation.
	if goals.HasResumablePark() {
		log.Printf("deactivate skipped: resumable precondition park outstanding — staying active")
		return nil
	}
	// Never tear down while a recoverable cascade block is outstanding: AllResolved
	// counts GoalBlocked as resolved, but a goal blocked behind a now-Done goal with
	// satisfied deps is recoverable work that ReconcileBlocks re-pends. Deactivating
	// here would strand the whole cascade subtree permanently (the distinct sibling
	// of the precondition park above). The caller (poll → tick) already holds the
	// goals flock, so call the lock-free ReconcileBlocks/SaveGoals directly as the
	// tick top and precondition path do. The next tick re-pends + dispatches the
	// un-stuck frontier; deactivation proceeds only once no recoverable frontier
	// remains.
	if goals.HasRecoverableBlock() {
		if goals.ReconcileBlocks() {
			if err := SaveGoals(d.workDir, goals); err != nil {
				return err
			}
		}
		log.Printf("deactivate skipped: recoverable cascade block(s) outstanding — reconciling and staying active")
		return nil
	}
	if !goals.AllResolved() {
		for i := range goals.Goals {
			g := &goals.Goals[i]
			if g.Status == GoalPending && !g.DependsOnSatisfied(goals.Goals) {
				log.Printf("%s: pending -> blocked (deps unsatisfied)", g.ID)
				g.Status = GoalBlocked
				g.BlockedBy = "deps_unsatisfied"
			}
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}
	if err := d.generateCompletionReport(goals); err != nil {
		log.Printf("warning: completion report: %v", err)
	}

	// All goals are resolved here; CurrentGoal names the goal that just finished.
	// Tear down EVERY goal namespace (head + all goals) so no sibling goal's
	// windows are orphaned at MaxGoals>1 (a goal that completed earlier in the run
	// already had its windows killed in checkProgress, so the extra kills are
	// no-ops). At MaxGoals<=1 sweepGoalIDs collapses to [head] and the helpers
	// return bare names — byte-identical to the prior single-goal teardown.
	curGoal := goals.CurrentGoal
	if err := d.teardownGoalWindows(d.sweepGoalIDs(curGoal, allGoalIDs(goals))); err != nil {
		return err
	}

	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	_ = os.Remove(guardPath)

	// Deactivation closes any open stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	d.mode = modeIdle
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
}

func (d *Daemon) generateCompletionReport(goals *GoalsFile) error {
	var done, failed, blocked int
	for _, g := range goals.Goals {
		switch g.Status {
		case GoalDone:
			done++
		case GoalFailed:
			failed++
		case GoalBlocked:
			blocked++
		}
	}
	total := len(goals.Goals)

	var buf strings.Builder
	buf.WriteString("# Taskvisor Completion Report\n\n")
	buf.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))
	buf.WriteString("## Summary\n\n")
	buf.WriteString("| Status | Count |\n")
	buf.WriteString("|--------|-------|\n")
	buf.WriteString(fmt.Sprintf("| Done   | %d     |\n", done))
	buf.WriteString(fmt.Sprintf("| Failed | %d     |\n", failed))
	buf.WriteString(fmt.Sprintf("| Blocked| %d     |\n", blocked))
	buf.WriteString(fmt.Sprintf("| Total  | %d     |\n", total))
	buf.WriteString("\n## Goals\n\n")

	for _, g := range goals.Goals {
		buf.WriteString(fmt.Sprintf("### %s: %s\n", g.ID, g.Description))
		buf.WriteString(fmt.Sprintf("- **Status:** %s\n", g.Status))
		dur := goalDuration(&g)
		if dur != "" {
			buf.WriteString(fmt.Sprintf("- **Duration:** %s\n", dur))
		}
		buf.WriteString(fmt.Sprintf("- **Retries:** %d/%d\n\n", g.Retries, g.MaxRetries))
	}

	reportDir := filepath.Join(d.workDir, ".tmux-cli", "goals")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(reportDir, "completion-report.md"), []byte(buf.String()), 0o644)
}

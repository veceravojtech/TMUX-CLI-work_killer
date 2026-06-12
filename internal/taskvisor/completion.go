package taskvisor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tasks"
)

// salvageGrace is the window after a timeout-synthesized failure (Goal.FailedBy
// == "validation-timeout", FinishedAt stamped on that route) during which
// deactivateOnCompletion must NOT tear down: the exhausted-timeout branch
// deliberately leaves the validator window alive, and its late verdict can
// still salvage the goal via the tick's salvageLateVerdicts scan (goal-061:
// the real pass arrived 5m51s — 351s — after the timeout). Derived from
// setup.ValidatorOverheadSec (600s), the same constant that models
// validator-side wall time outside the per-worker budget: a verdict that has
// not landed within the validator's own modeled overhead is not worth holding
// the daemon active for.
const salvageGrace = setup.ValidatorOverheadSec * time.Second

// splitSalvageMarked partitions the timeout-marked failed goals (the
// salvageLateVerdicts watch set: Status==GoalFailed && FailedBy ==
// "validation-timeout") by salvage grace: "pending" goals failed less than
// grace ago — their late verdict may still arrive — while "expired" goals are
// past the grace OR carry an absent/unparseable FinishedAt, which is treated as
// expired so a bad timestamp can never wedge the daemon active forever.
func (gf *GoalsFile) splitSalvageMarked(now time.Time, grace time.Duration) (pending, expired []*Goal) {
	for i := range gf.Goals {
		g := &gf.Goals[i]
		if g.Status != GoalFailed || g.FailedBy != "validation-timeout" {
			continue
		}
		finished, err := time.Parse(time.RFC3339, g.FinishedAt)
		if err == nil && now.Sub(finished) < grace {
			pending = append(pending, g)
		} else {
			expired = append(expired, g)
		}
	}
	return pending, expired
}

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
	// Never tear down while a timeout-failed goal is still salvage-eligible: in
	// the goal-061 topology (timeout-failed goal + every remaining goal cascade-
	// blocked on it) nothing is runnable, AllResolved counts failed+blocked as
	// resolved, and HasRecoverableBlock correctly excludes GoalFailed blockers —
	// so without this guard the teardown below would kill the still-running
	// validator and modeIdle's poll would never reach the tick's
	// salvageLateVerdicts scan. Staying active keeps the validator alive to
	// deliver its late verdict; salvage success needs NO code here (tick flips
	// the goal to GoalDone, ReconcileBlocks re-pends the dependents, dispatch
	// resumes). On expiry the marker is cleared and persisted in the SAME call,
	// so the guard is self-terminating — a validator that never reports cannot
	// hold the daemon active forever.
	pendingSalvage, expiredSalvage := goals.splitSalvageMarked(time.Now().UTC(), salvageGrace)
	if len(pendingSalvage) > 0 {
		for _, g := range pendingSalvage {
			log.Printf("deactivate skipped: salvage grace open for %s — staying active", g.ID)
		}
		return nil
	}
	if len(expiredSalvage) > 0 {
		for _, g := range expiredSalvage {
			g.FailedBy = ""
			log.Printf("salvage grace expired for %s", g.ID)
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
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
	// Report every terminally-failed goal to the backend exactly once. Placed
	// here — AFTER the salvage-grace split above (an in-grace timeout goal returns
	// before this line, so it is never prematurely reported; an expired-salvage
	// goal had its FailedBy cleared but stays GoalFailed and IS reported) and
	// before the local completion report — so a failure that web operators must
	// see is surfaced over the network, not just to completion-report.md.
	d.reportFailedGoals(goals)
	if err := d.generateCompletionReport(goals); err != nil {
		log.Printf("warning: completion report: %v", err)
	}

	d.notifyCompletion(goals)

	// All goals are resolved here; CurrentGoal names the goal that just finished.
	// Tear down EVERY goal namespace (head + all goals) so no sibling goal's
	// windows are orphaned at MaxGoals>1 (a goal that completed earlier in the run
	// already had its windows killed in checkProgress, so the extra kills are
	// no-ops). At MaxGoals<=1 sweepGoalIDs collapses to [head]; the per-goal
	// namespaced names mean the human's window-0 "supervisor" is never swept.
	curGoal := goals.CurrentGoal
	if err := d.teardownGoalWindows(d.sweepGoalIDs(curGoal, allGoalIDs(goals))); err != nil {
		return err
	}

	if _, err := os.Stat(tasks.TasksFilePath(d.workDir)); err == nil {
		if archErr := tasks.ArchiveTasks(d.workDir); archErr != nil {
			log.Printf("archive tasks.yaml: %v", archErr)
		}
	}

	d.cleanRuntimeMarkers()

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
		buf.WriteString(fmt.Sprintf("- **Retries:** %s\n\n", retriesLine(&g)))
	}

	reportDir := filepath.Join(d.workDir, ".tmux-cli", "goals")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(reportDir, "completion-report.md"), []byte(buf.String()), 0o644)
}

// retriesLine renders a goal's retry consumption for the completion report
// from the four LIVE per-class counters. The live counters hold the REMAINING
// budget (decrement-toward-zero, re-seeded from the Max… budgets by
// LoadGoals), so consumed = Max… − live; the legacy g.Retries scalar has no
// live writer post-migration (goals.go: "NEVER read" by budget logic) and is
// not used for migrated goals. Rendering choice: a class whose Max… budget is
// 0 was never granted budget and is OMITTED — MaxBlockRetries is 0 for every
// migrated goal (MigrateRetries: "blocked never gets budget"), so rendering
// all four would print a constant "block 0/0" on every goal. Negative
// consumed (live > Max on a hand-edited goals.yaml) clamps to 0. A true
// pre-migration goal — all four Max… zero AND legacy MaxRetries > 0, possible
// only for an in-memory GoalsFile that bypassed LoadGoals (which always seeds
// the Max… budgets) — falls back to the legacy Retries/MaxRetries line; with
// no budgets anywhere the line reads "none".
func retriesLine(g *Goal) string {
	classes := []struct {
		name      string
		max, live int
	}{
		{"code", g.MaxCodeRetries, g.CodeRetries},
		{"spec", g.MaxSpecRetries, g.SpecRetries},
		{"validation", g.MaxValidationRetries, g.ValidationRetries},
		{"block", g.MaxBlockRetries, g.BlockRetries},
	}
	var parts []string
	for _, c := range classes {
		if c.max == 0 {
			continue
		}
		consumed := max(c.max-c.live, 0)
		parts = append(parts, fmt.Sprintf("%s %d/%d", c.name, consumed, c.max))
	}
	if len(parts) == 0 {
		if g.MaxRetries > 0 {
			return fmt.Sprintf("%d/%d", g.Retries, g.MaxRetries)
		}
		return "none"
	}
	return strings.Join(parts, " · ")
}

// failedGoalReport is the fully assembled backend report for one terminally-
// failed goal, built purely (no network, no Daemon mutation) from the goal and
// its last validator signal so the payload/category/severity contract is
// unit-testable independently of the fire-and-forget submission.
type failedGoalReport struct {
	category    string
	severity    string
	title       string
	description string
	payload     map[string]any
	proposedFix string
	expected    string
}

// buildFailedGoalReport assembles the report for a single GoalFailed goal,
// composing execute-1's reporting.go helpers (inferCategory, goalToYAML,
// proposedFixFromSignal, expectedGreenState) — it NEVER redefines them. sig may
// be nil (no signal.json / unparseable): category then falls back to "execute",
// verdict/findings are empty, and the goal YAML + FailedBy/cycle still populate
// the payload. proposedFix/expected are NEVER blank (the backend rejects blank
// contract fields with a 422): a missing correction or empty acceptance falls
// back to derived text naming the goal. Severity is ALWAYS "critical" — by the
// time a goal reaches here it is terminally failed (salvage grace, if any,
// already elapsed).
func buildFailedGoalReport(g *Goal, sig *ValidatorSignal) failedGoalReport {
	var verdict, findings string
	if sig != nil {
		verdict = sig.Verdict
		findings = summarizeFindings(sig)
	}
	description := fmt.Sprintf("Goal %s failed after exhausting its retry budget", g.ID)
	if g.FailedBy != "" {
		description += fmt.Sprintf(" (failed_by: %s)", g.FailedBy)
	}
	payload := map[string]any{
		"goal":      goalToYAML(*g),
		"verdict":   verdict,
		"findings":  findings,
		"failed_by": g.FailedBy,
		"cycle":     CurrentCycle(g),
	}
	proposedFix := proposedFixFromSignal(sig)
	if strings.TrimSpace(proposedFix) == "" {
		proposedFix = fmt.Sprintf(
			"No validator correction recorded for %s; inspect the .tmux-cli/goals/%s/ artifacts and the goal YAML in the payload, fix the cause, then run `taskvisor goal reset %s`.",
			g.ID, g.ID, g.ID)
	}
	expected := expectedGreenState(*g)
	if strings.TrimSpace(expected) == "" {
		expected = fmt.Sprintf("Goal %s revalidates to done: %s", g.ID, g.Description)
	}
	return failedGoalReport{
		category:    inferCategory(sig, *g),
		severity:    "critical",
		title:       fmt.Sprintf("Goal %s failed after retries", g.ID),
		description: description,
		payload:     payload,
		proposedFix: proposedFix,
		expected:    expected,
	}
}

// summarizeFindings renders a validator signal's non-pass findings as a compact
// "rule: detail" multiline block for the report payload. A nil/empty signal
// yields "".
func summarizeFindings(sig *ValidatorSignal) string {
	if sig == nil {
		return ""
	}
	var lines []string
	for _, f := range sig.Findings {
		if f.Status == VerdictPass {
			continue
		}
		detail := strings.TrimSpace(f.Detail)
		if f.Rule != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", f.Rule, detail))
		} else if detail != "" {
			lines = append(lines, detail)
		}
	}
	return strings.Join(lines, "\n")
}

// reportFailedGoals submits exactly one backend failure report per GoalFailed
// goal. It NEVER reports GoalDone (success or SkipGoal), GoalBlocked,
// GoalPending or GoalRunning. The d.reportedFailures mark is TRUTHFUL: it is
// set eagerly under reportedFailuresMu before submission starts (deduping both
// repeated deactivateOnCompletion passes and a sweep racing an in-flight async
// submission) and cleared by the submit callback when the submission errors, so
// the next sweep retries instead of silently dropping the report. With a nil
// producer submitReport invokes the callback synchronously with nil — the mark
// is kept (delivered-equivalent, preserving the disabled-reporting contract).
// Detection/iteration is cheap and synchronous; the network submit runs on
// submitReport's goroutine, so this is non-blocking. Best-effort: a missing/
// unparseable signal.json is logged and the report is still submitted with an
// empty signal.
func (d *Daemon) reportFailedGoals(goals *GoalsFile) {
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalFailed {
			continue
		}
		d.reportedFailuresMu.Lock()
		if d.reportedFailures == nil {
			d.reportedFailures = make(map[string]bool)
		}
		if d.reportedFailures[g.ID] {
			d.reportedFailuresMu.Unlock()
			continue
		}
		d.reportedFailures[g.ID] = true
		d.reportedFailuresMu.Unlock()

		var sig *ValidatorSignal
		if loaded, err := LoadSignal(d.workDir, g.ID); err != nil {
			log.Printf("reportFailedGoals: load signal for %s: %v", g.ID, err)
		} else if s, ok := loaded.(*ValidatorSignal); ok {
			sig = s
		}
		r := buildFailedGoalReport(g, sig)
		req := d.buildRequest(r.category, r.severity, r.title, r.description, r.payload,
			withProposedFix(r.proposedFix), withExpectedGreenState(r.expected))
		goalID := g.ID
		submitReportFn(d, req, func(err error) {
			if err == nil {
				return
			}
			d.reportedFailuresMu.Lock()
			delete(d.reportedFailures, goalID)
			d.reportedFailuresMu.Unlock()
			log.Printf("reportFailedGoals: submission for %s failed — will retry on next sweep: %v", goalID, err)
		})
	}
}

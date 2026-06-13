package taskvisor

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/console/tmux-cli/internal/producer"
)

// taskresolve.go — push-based backend task resolution (goal-032). When a mapped
// goal reaches a durable terminal state, the daemon PATCHes the backend task's
// terminal status and rewrites task-goals.yaml without the consumed entry.
//
// CONTRACT: best-effort and warn-only. The goal IS terminal (SaveGoals already
// persisted it), so resolution must NEVER block the transition beyond
// taskResolveTimeout, never fail it, and never propagate an error — every error
// path logs a warning and returns. On any failure (API down, disabled, slow)
// the mapping entry is LEFT in place so /tmux:task-list reconcile remains the
// fallback actor. Mirrors the autoCommitGoal independence contract.

// taskResolveTimeout bounds the synchronous PATCH so the goal transition is
// never delayed indefinitely by a slow backend.
const taskResolveTimeout = 5 * time.Second

// errReportingDisabled is returned by updateTaskStatus when the backend reporter
// is unconfigured (d.producer == nil — api.enabled false / no signing key). The
// resolver treats it like any other error: the ledger is left untouched.
var errReportingDisabled = errors.New("backend reporting disabled")

// updateTaskStatus PATCHes the task's status via the producer client. A nil
// producer means reporting is disabled — return errReportingDisabled so the
// caller leaves the mapping for reconcile rather than removing it.
func (d *Daemon) updateTaskStatus(ctx context.Context, id, status string, resolution map[string]any) error {
	if d.producer == nil {
		return errReportingDisabled
	}
	_, err := d.producer.UpdateTaskStatus(ctx, id, producer.UpdateStatusParams{Status: status, Resolution: resolution})
	return err
}

// updateTaskStatusFn is the indirection the resolver routes the PATCH through.
// It defaults to (*Daemon).updateTaskStatus and is a package var ONLY so tests
// observe the call deterministically without a live backend — mirroring
// submitReportFn (reporting.go). Production never reassigns it.
var updateTaskStatusFn = (*Daemon).updateTaskStatus

// resolveTaskOnTerminal pushes the backend task mapped to goal to its terminal
// status, then removes the mapping on success. Best-effort: a missing mapping is
// a silent no-op; an API error or disabled reporter leaves the mapping in place
// (reconcile fallback). NEVER returns or propagates — all failures are
// warn-logged. Called as a sibling of autoCommitGoal after the durable SaveGoals
// at each terminal site; the held tick lock makes the lock-free ledger I/O safe.
func (d *Daemon) resolveTaskOnTerminal(goal *Goal, status string, resolution map[string]any) {
	tgf, err := LoadTaskGoals(d.workDir)
	if err != nil {
		log.Printf("warning: task-resolve %s: load ledger: %v — leaving for reconcile", goal.ID, err)
		return
	}
	idx := tgf.indexOf(goal.ID)
	if idx < 0 {
		return // no ledger entry for this goal — nothing to resolve
	}
	taskID := tgf.Mappings[idx].TaskID

	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, taskResolveTimeout)
	defer cancel()

	if err := updateTaskStatusFn(d, ctx, taskID, status, resolution); err != nil {
		log.Printf("warning: task-resolve %s: PATCH task %s -> %s failed: %v — leaving for reconcile", goal.ID, taskID, status, err)
		return
	}

	tgf.Mappings = append(tgf.Mappings[:idx], tgf.Mappings[idx+1:]...)
	if err := SaveTaskGoals(d.workDir, tgf); err != nil {
		log.Printf("warning: task-resolve %s: PATCH succeeded but ledger rewrite failed: %v", goal.ID, err)
		return
	}
	log.Printf("%s: backend task %s -> %s (push-resolved on goal terminal transition)", goal.ID, taskID, status)
}

// doneResolution builds the resolution payload for a goal that reached done. The
// compact findings (name/verdict/evidence) come from the validator signal; the
// backend defines the opaque resolution schema.
func doneResolution(goal *Goal, sig *ValidatorSignal) map[string]any {
	return map[string]any{
		"goal_id":     goal.ID,
		"finished_at": goal.FinishedAt,
		"summary":     "goal validated and completed",
		"findings":    compactFindings(sig),
	}
}

// failResolution builds the resolution payload for a failed/blocked goal. The
// reason prefers the explicit FailedBy marker, then BlockedBy (the breaker
// sentinel), then the verdict class threaded from the failure site.
func failResolution(goal *Goal, class string) map[string]any {
	return map[string]any{
		"goal_id": goal.ID,
		"reason":  firstNonEmpty(goal.FailedBy, goal.BlockedBy, class),
	}
}

// compactFindings projects validator findings to compact {name,verdict,evidence}
// records. Nil-safe (a nil/findingless signal yields nil). Evidence prefers the
// OutputExcerpt, falling back to Detail — there is no dedicated evidence field.
func compactFindings(sig *ValidatorSignal) []map[string]any {
	if sig == nil || len(sig.Findings) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(sig.Findings))
	for _, f := range sig.Findings {
		out = append(out, map[string]any{
			"name":     f.Rule,
			"verdict":  f.Status,
			"evidence": firstNonEmpty(f.OutputExcerpt, f.Detail),
		})
	}
	return out
}

// firstNonEmpty returns the first non-empty string, or "" when all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

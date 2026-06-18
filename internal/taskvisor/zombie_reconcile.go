package taskvisor

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
)

// zombie_reconcile.go — consume-path zombie running-goal reconcile preflight.
//
// A goal stuck GoalRunning whose worker tmux session died (the daemon process
// itself stayed alive) head-of-line-blocks the queue forever: startup
// crashRecovery is the ONLY thing that re-pends such an orphan, and it never
// re-fires while a live daemon's session dies underneath it. Freshly claimed
// goals then sit GoalPending behind the zombie until an operator runs
// `tmux-cli taskvisor goal skip` by hand.
//
// This preflight is wired at the consume/claim MCP entry point (TaskClaim). It
// surveys the live tmux session ONCE and re-pends (or fails) every GoalRunning
// goal with no live worker window — REUSING the exact crash-recovery liveness
// predicate (goalHasLiveWindow) and marker cleanup (clearGoalRuntimeMarkers) so
// detection/cleanup can never drift from startup recovery — then lets the
// existing task-225 git-freshness gate and the claim proceed unchanged. It does
// NOT emit a worker-crash report or demote a solo lane: those are crash-recovery
// specifics, and this is stale-session cleanup, not a crash.

// PreflightReconcileZombieGoals is the PURE CORE of the consume-path zombie
// reconcile: given workDir and a survey of the live session's windows, it
// re-pends or fails every GoalRunning goal that has no live worker window in the
// survey. A nil/empty windows slice means a dead session (no live session at
// all), so EVERY running goal is a zombie. It holds WithGoalsLock around the
// load→mutate→save so a concurrent daemon write to goals.yaml cannot race it.
//
// The per-goal transition mirrors crashRecovery's orphan branch
// (recovery.go:199) EXACTLY: Retries < MaxRetries → re-pend (Status=GoalPending,
// StartedAt cleared, the four stale runtime markers deleted via
// clearGoalRuntimeMarkers, NextDispatch=dispatchImplementer when a per-goal
// tasks.yaml exists); otherwise fail (Status=GoalFailed, FinishedAt set).
// Retries is NEVER incremented (crash recovery does not either, so the retry
// budget means the same across both entry points) and taskvisor-active is NEVER
// touched. Returns the IDs reconciled (in goal-file order); an empty/nil slice
// means nothing was stale. The MCP wrapper (ReconcileZombieGoalsForSession)
// surveys the session and calls this; the required unit test calls it directly
// with a crafted window slice.
func PreflightReconcileZombieGoals(workDir string, windows []tmux.WindowInfo) ([]string, error) {
	var reconciled []string
	err := WithGoalsLock(workDir, func() error {
		goals, err := LoadGoals(workDir)
		if err != nil {
			return err
		}
		if goals == nil {
			return nil
		}

		mg := resolveMaxGoals(workDir)
		changed := false
		for i := range goals.Goals {
			g := &goals.Goals[i]
			if g.Status != GoalRunning || goalHasLiveWindow(windows, g.ID, mg) {
				continue
			}
			// Orphaned running goal: no live worker window in the live session.
			// Apply the crash-recovery mirrored transition (no crash report, no
			// solo-lane demotion — this is stale-session cleanup, not a crash).
			if g.Retries < g.MaxRetries {
				g.Status = GoalPending
				g.StartedAt = ""
				clearGoalRuntimeMarkers(workDir, g.ID)
				if _, serr := os.Stat(tasks.GoalTasksFilePath(workDir, g.ID)); serr == nil {
					g.NextDispatch = dispatchImplementer
				}
				log.Printf("zombie-reconcile: %s: orphaned running (%s gone) -> pending", g.ID, supervisorWindow(g.ID, mg))
			} else {
				g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
				g.Status = GoalFailed
				log.Printf("zombie-reconcile: %s: orphaned running, retries exhausted -> failed", g.ID)
			}
			reconciled = append(reconciled, g.ID)
			changed = true
		}

		if changed {
			return SaveGoals(workDir, goals)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reconciled, nil
}

// ReconcileZombieGoalsForSession is the executor-backed wrapper around the pure
// core: it surveys the live tmux session for workDir and feeds the window slice
// to PreflightReconcileZombieGoals. It is the form the MCP TaskClaim path calls,
// keeping session-discovery + window-listing out of internal/mcp (which must not
// import internal/tmux or log for this).
//
// Session discovery is via the TMUX_CLI_PROJECT_PATH env marker (the same key
// the daemon and dashboard use). FindSessionByEnvironment returns ("", nil) —
// NOT an error — when no session matches: that is the genuine dead-session case,
// so the window slice stays nil and EVERY running goal is reconciled. It is
// distinguished from a ListWindows failure on a FOUND session, which returns the
// error and reconciles nothing (we cannot prove liveness — fail-safe, never
// re-pend a goal whose window might actually be alive). Exactly one ListWindows
// call is issued (count-frugal, matching recovery's survey discipline).
func ReconcileZombieGoalsForSession(workDir string, exec tmux.TmuxExecutor) ([]string, error) {
	if exec == nil {
		return nil, nil
	}
	sessionID, err := exec.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", workDir)
	if err != nil {
		return nil, err
	}
	var windows []tmux.WindowInfo
	if sessionID != "" {
		windows, err = exec.ListWindows(sessionID)
		if err != nil {
			// Session found but its windows are unlistable: we cannot prove any
			// goal's liveness, so SKIP (fail-safe) rather than re-pend a goal
			// whose worker window may actually be alive.
			return nil, err
		}
	}
	reconciled, err := PreflightReconcileZombieGoals(workDir, windows)
	if err != nil {
		return nil, err
	}
	if len(reconciled) > 0 {
		log.Printf("zombie-reconcile: re-pended/failed %d stale running goal(s): %s",
			len(reconciled), strings.Join(reconciled, ", "))
	}
	return reconciled, nil
}

// resolveMaxGoals is the free-function form of (*Daemon).maxGoals
// (window_names.go:131): it reads Supervisor.MaxGoals from setting.yaml under
// workDir, defaulting to 1 when the setting is unset, <=0, or unreadable. The
// pure core needs the bound without a Daemon receiver; the namespaced window
// helpers no longer branch on it, so the default-1 path is exact for the common
// single-goal case.
func resolveMaxGoals(workDir string) int {
	s, err := setup.LoadSettings(workDir)
	if err != nil || s == nil || s.Supervisor.MaxGoals <= 0 {
		return 1
	}
	return s.Supervisor.MaxGoals
}

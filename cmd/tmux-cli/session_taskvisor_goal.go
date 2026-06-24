package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/spf13/cobra"
)

// This file holds the RunE handlers for the `taskvisor goal` cobra subcommands
// (add/list/delete/reset/priority/skip/stop/prune) and the skip-only window
// selection helper. The command vars, their flag wiring, and registration stay
// in session.go's init(); these are pure intra-package moves with no behavior
// change — split out only to keep session.go under the 2000-line file-length
// gate. Shared helpers used by non-goal commands (stopDaemonProcess,
// taskvisorProjectRoot, the restart handlers) remain in session.go.

func runTaskvisorGoalAdd(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// All validation, ID allocation, structured persistence (acceptance/
	// validate/scope INTO goals.yaml — the RC-A fix), and goal.md writing live
	// in the shared authoring core, converged with the MCP goal-create tool.
	id, derivedScope, err := taskvisor.CreateGoal(cwd, taskvisor.GoalSpec{
		Description: goalDescription,
		Acceptance:  goalAcceptance,
		Validate:    goalValidate,
		Context:     goalContext,
		NotInScope:  goalNotInScope,
		Phase:       goalPhase,
		MaxRetries:  goalMaxRetries,
		Scope:       goalScope,
		Priority:    goalPriority,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Goal created: %s\n", id)
	switch {
	case len(goalScope) > 0:
		fmt.Printf("scope: [%s]\n", strings.Join(goalScope, ", "))
	case derivedScope:
		// Re-derive for display only — pure function over the same input, so
		// it always matches what CreateGoal persisted.
		fmt.Printf("scope: [%s] (derived from acceptance)\n", strings.Join(taskvisor.DeriveScopeFromDeliverables(goalAcceptance), ", "))
	default:
		fmt.Println("⚠ scope: unknown — goal will serialize against all concurrent goals")
		if len(goalScope) == 0 {
			// Re-derive (pure) to surface a discarded incomplete derivation:
			// name the acceptance lines that contributed no path so the author
			// can declare --scope and regain parallelism.
			if _, incomplete, uncovered := taskvisor.DeriveScopeWithCompleteness(goalAcceptance); incomplete && len(uncovered) > 0 {
				fmt.Fprintf(os.Stderr, "⚠ discarded incomplete derived scope: these acceptance criteria named no file path:\n")
				for _, c := range uncovered {
					fmt.Fprintf(os.Stderr, "    - %s\n", c)
				}
				fmt.Fprintf(os.Stderr, "  pass --scope to declare the footprint and regain parallelism\n")
			}
		}
	}
	return nil
}

// runTaskvisorGoalEdit edits an existing goal's authoring fields via the shared
// taskvisor.EditGoal core (converged with the goal-edit MCP tool). Each flag maps
// to a tri-state GoalEdit pointer: a flag the user did NOT pass stays nil (leave
// untouched), a flag they DID pass is applied (an empty value clears it). This is
// what lets a Tier-2 elaboration write-back set only the fields it authored.
func runTaskvisorGoalEdit(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	goalID := args[0]
	edit := taskvisor.GoalEdit{}
	if cmd.Flags().Changed("acceptance") {
		edit.Acceptance = &goalEditAcceptance
	}
	if cmd.Flags().Changed("validate") {
		edit.Validate = &goalEditValidate
	}
	if cmd.Flags().Changed("scope") {
		edit.Scope = &goalEditScope
	}
	if cmd.Flags().Changed("status") {
		edit.Status = &goalEditStatus
	}
	if cmd.Flags().Changed("deliverable-area") {
		edit.DeliverableArea = &goalEditDeliverableArea
	}
	if cmd.Flags().Changed("phase") {
		edit.Phase = &goalEditPhase
	}

	if err := taskvisor.EditGoal(cwd, goalID, edit); err != nil {
		return err
	}

	fmt.Printf("Goal edited: %s\n", goalID)
	return nil
}

func runTaskvisorGoalList(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	gf, err := taskvisor.LoadGoals(cwd)
	if err != nil {
		return fmt.Errorf("load goals: %w", err)
	}
	if gf == nil || len(gf.Goals) == 0 {
		fmt.Println("No goals")
		return nil
	}

	fmt.Printf("%-10s %-10s %-10s %s\n", "ID", "Status", "Retries", "Description")
	fmt.Printf("%-10s %-10s %-10s %s\n", "---", "---", "---", "---")
	for _, g := range gf.Goals {
		fmt.Printf("%-10s %-10s %d/%-8d %s\n", g.ID, g.Status, g.Retries, g.MaxRetries, g.Description)
	}
	return nil
}

func runTaskvisorGoalDelete(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	goalID := args[0]
	if err := taskvisor.WithGoalsLock(cwd, func() error {
		gf, err := taskvisor.LoadGoals(cwd)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			return fmt.Errorf("goal not found: %s", goalID)
		}

		g, ok := gf.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("goal not found: %s", goalID)
		}
		if g.Status == taskvisor.GoalRunning {
			return fmt.Errorf("goal is currently running, stop the daemon first")
		}

		gf.DeleteGoal(goalID)

		return taskvisor.SaveGoals(cwd, gf)
	}); err != nil {
		return err
	}

	goalDir := filepath.Join(cwd, ".tmux-cli", "goals", goalID)
	if err := os.RemoveAll(goalDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove goal directory: %w", err)
	}

	fmt.Printf("Goal deleted: %s\n", goalID)
	return nil
}

func runTaskvisorGoalReset(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	goalID := args[0]
	if err := taskvisor.WithGoalsLock(cwd, func() error {
		gf, err := taskvisor.LoadGoals(cwd)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			return fmt.Errorf("goal not found: %s", goalID)
		}

		g, ok := gf.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("goal not found: %s", goalID)
		}
		switch g.Status {
		case taskvisor.GoalFailed, taskvisor.GoalDone:
			// accepted — unchanged terminal-status reset path.
		case taskvisor.GoalRunning:
			// A phantom-running goal (no live owning window after a restart) is
			// safe to re-pend in one command; one that still owns a live worker
			// window stays refused unless --force, so we never yank a live worker.
			if !resetForce && goalOwnsLiveWindowFn(cwd, goalID) {
				return fmt.Errorf("goal %s is running and still owns a live worker window; pass --force to reset it anyway", goalID)
			}
			// Window-less or forced — accepted. ResetGoal only re-pends a
			// terminal-status goal, so flip running→done first and let ResetGoal
			// perform the full clean re-pend (zeroing retries/counters/timestamps)
			// as the single source of truth — no duplicated field-clearing here.
			g.Status = taskvisor.GoalDone
		default:
			return fmt.Errorf("goal is not in failed or done status (current: %s)", g.Status)
		}

		gf.ResetGoal(goalID)

		return taskvisor.SaveGoals(cwd, gf)
	}); err != nil {
		return err
	}

	fmt.Printf("Goal reset to pending: %s\n", goalID)
	return nil
}

// goalOwnsLiveWindow reports whether goalID still owns ≥1 live namespace window
// in the project's tmux session. It resolves the session by TMUX_CLI_PROJECT_PATH
// and reuses the goalSkipWindowsToKill predicate (which already spares the bare
// window-0 "supervisor"). Fail-safe direction: an unresolved session or a
// ListWindows error returns false (treat as window-less — the recover-friendly
// case), so a positive result is only ever a CONFIRMED live window.
func goalOwnsLiveWindow(cwd, goalID string) bool {
	executor := tmux.NewTmuxExecutor()
	sessionID, err := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if err != nil || sessionID == "" {
		return false
	}
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return false
	}
	return len(goalSkipWindowsToKill(windows, goalID)) > 0
}

// goalOwnsLiveWindowFn is the indirection the reset handler invokes for the
// running-goal liveness check; the unit test swaps it to simulate live/dead
// windows with no real tmux server (mirrors reportWorkerCrashFn in recovery.go).
// Production never reassigns it.
var goalOwnsLiveWindowFn = goalOwnsLiveWindow

func runTaskvisorGoalPriority(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	goalID := args[0]
	// Parse BEFORE acquiring the lock so a bad value never opens goals.yaml or
	// holds the cross-process lock.
	prio, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("invalid priority value %q: %w", args[1], err)
	}

	if err := taskvisor.WithGoalsLock(cwd, func() error {
		gf, err := taskvisor.LoadGoals(cwd)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			return fmt.Errorf("goal not found: %s", goalID)
		}

		g, ok := gf.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("goal not found: %s", goalID)
		}
		g.Priority = prio

		return taskvisor.SaveGoals(cwd, gf)
	}); err != nil {
		return err
	}

	fmt.Printf("Goal priority set to %d: %s\n", prio, goalID)
	return nil
}

// goalSkipWindowsToKill selects, from a session's window list, ONLY the windows
// belonging to goalID's namespace — its supervisor-<ns>, validator-<ns> (plus the
// bare one-release fallback "validator"), and every execute-<ns>-/inv-<ns>- worker
// — using the real taskvisor naming helpers so the set can never drift from what
// the daemon spawns. The human's window-0 bare "supervisor" is EXPLICITLY spared:
// skipping a goal must never kill the interactive window ([[never-kill-tmux-server-pid]]).
// Sibling goals' namespaced windows don't match this goal's names, so they survive.
func goalSkipWindowsToKill(windows []tmux.WindowInfo, goalID string) []tmux.WindowInfo {
	sup := taskvisor.SupervisorWindowForGoal(goalID)
	vals := taskvisor.ValidatorWindowNames(goalID)
	ep := taskvisor.ExecutePrefixForGoal(goalID)
	ip := taskvisor.InvestigatorPrefixForGoal(goalID)
	var kill []tmux.WindowInfo
	for _, w := range windows {
		if w.Name == "supervisor" {
			continue // window-0 (human interactive) — never kill
		}
		match := w.Name == sup ||
			strings.HasPrefix(w.Name, ep) ||
			strings.HasPrefix(w.Name, ip)
		for _, v := range vals {
			if w.Name == v {
				match = true
				break
			}
		}
		if match {
			kill = append(kill, w)
		}
	}
	return kill
}

func runTaskvisorGoalSkip(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	goalID := args[0]
	if err := taskvisor.WithGoalsLock(cwd, func() error {
		gf, err := taskvisor.LoadGoals(cwd)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			return fmt.Errorf("goal not found: %s", goalID)
		}

		g, ok := gf.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("goal not found: %s", goalID)
		}
		if g.Status != taskvisor.GoalRunning {
			return fmt.Errorf("goal is not running (current: %s)", g.Status)
		}

		gf.SkipGoal(goalID)

		return taskvisor.SaveGoals(cwd, gf)
	}); err != nil {
		return err
	}

	executor := tmux.NewTmuxExecutor()
	sessionID, err := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if err == nil && sessionID != "" {
		windows, err := executor.ListWindows(sessionID)
		if err == nil {
			for _, w := range goalSkipWindowsToKill(windows, goalID) {
				_ = executor.KillWindow(sessionID, w.TmuxWindowID)
			}
		}
	}

	if _, err := taskvisor.EnsureGoalDir(cwd, goalID); err != nil {
		return fmt.Errorf("ensure goal dir: %w", err)
	}
	skippedPath := filepath.Join(cwd, ".tmux-cli", "goals", goalID, "corrections", "skipped.md")
	if err := os.WriteFile(skippedPath, []byte(skipReason), 0o644); err != nil {
		return fmt.Errorf("write skipped.md: %w", err)
	}

	fmt.Printf("Goal skipped: %s\n", goalID)
	return nil
}

func runTaskvisorGoalStop(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	if err := stopDaemonProcess(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stop daemon process: %v\n", err)
	}

	for _, name := range []string{"taskvisor-active", "taskvisor-start", "taskvisor-current-goal", "taskvisor-current-cycle", "taskvisor-current-worktree"} {
		p := filepath.Join(cwd, ".tmux-cli", name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}

	fmt.Println("Taskvisor stop signal sent")
	return nil
}

func runTaskvisorGoalPrune(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	activePath := filepath.Join(cwd, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(activePath); err == nil {
		return fmt.Errorf("taskvisor daemon is active — stop it first")
	}

	gf, err := taskvisor.LoadGoals(cwd)
	if err != nil {
		return fmt.Errorf("load goals: %w", err)
	}
	count := 0
	if gf != nil {
		count = len(gf.Goals)
	}

	goalsFile := taskvisor.GoalsFilePath(cwd)
	if err := os.Remove(goalsFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove goals.yaml: %w", err)
	}

	goalsDir := filepath.Join(cwd, ".tmux-cli", "goals")
	if err := os.RemoveAll(goalsDir); err != nil {
		return fmt.Errorf("remove goals directory: %w", err)
	}

	for _, name := range []string{"taskvisor-current-goal", "taskvisor-start", "taskvisor-current-cycle", "taskvisor-current-worktree"} {
		p := filepath.Join(cwd, ".tmux-cli", name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}

	fmt.Printf("Pruned %d goal(s)\n", count)
	return nil
}

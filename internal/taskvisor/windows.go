package taskvisor

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
)

// sweepGoalIDs returns the goal-id set a window teardown must cover. At
// MaxGoals<=1 it is ALWAYS just [head]: the naming helpers ignore the id and
// return bare names, so the teardown does exactly one bare-name sweep —
// byte-identical to the pre-namespacing single-goal teardown regardless of how
// many goals exist. At MaxGoals>1 it is head followed by every distinct id in
// extra (sorted for determinism), so no sibling goal's namespaced windows are
// orphaned. extra is the caller's candidate in-flight set (the runtime keys for
// an idle deactivate, or all goal ids for a startup/completion sweep).
func (d *Daemon) sweepGoalIDs(head string, extra []string) []string {
	if d.maxGoals() <= 1 {
		return []string{head}
	}
	seen := map[string]bool{head: true}
	var rest []string
	for _, id := range extra {
		if !seen[id] {
			seen[id] = true
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	return append([]string{head}, rest...)
}

// killGoalWindows kills the supervisor/validator windows and the execute-/inv-
// worker pools for every goal id in ids, with NO await. Used by activate's
// startup sweep. At MaxGoals<=1 ids is [head] and the names are bare, so the
// four-kill order matches the pre-namespacing sweep exactly.
func (d *Daemon) killGoalWindows(ids []string) error {
	mg := d.maxGoals()
	for _, id := range ids {
		if err := d.killWindowByName(supervisorWindow(id, mg)); err != nil {
			return err
		}
		if err := d.killWindowsByPrefix(executePrefix(id, mg)); err != nil {
			return err
		}
		if err := d.killWindowByName(validatorWindow(id, mg)); err != nil {
			return err
		}
		if err := d.killWindowsByPrefix(invPrefix(id, mg)); err != nil {
			return err
		}
	}
	return nil
}

// teardownGoalWindows kills every goal id's window groups (via killGoalWindows),
// accumulates the managed-name set across all of them, then awaits them all gone
// ONCE. waitWindowsGone failures are logged (not returned), matching the prior
// deactivate / deactivateOnCompletion behavior. At MaxGoals<=1 ids is [head], so
// the kill-then-collect-then-await sequence is byte-identical to the old single
// goal teardown.
func (d *Daemon) teardownGoalWindows(ids []string) error {
	if err := d.killGoalWindows(ids); err != nil {
		return err
	}
	var allNames []string
	for _, id := range ids {
		allNames = append(allNames, d.collectManagedNames(id)...)
	}
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		log.Printf("warning: waitWindowsGone: %v", err)
	}
	return nil
}

// allGoalIDs returns every goal id in the file, used as the teardown candidate
// set for startup and completion sweeps where any namespace may hold leftovers.
func allGoalIDs(goals *GoalsFile) []string {
	ids := make([]string, 0, len(goals.Goals))
	for i := range goals.Goals {
		ids = append(ids, goals.Goals[i].ID)
	}
	return ids
}

type CreatedWindow struct {
	TmuxWindowID string
	Name         string
}

// WindowCreateFunc creates a worker window. cwd is the working directory the
// window's shell starts in: "" or the base workDir for the shared base tree, or a
// per-goal worktree path (E1-1a) when MaxGoals>1 isolates the goal. The
// production factory forwards cwd to `tmux new-window -c <dir>`; "" leaves the
// session default (byte-identical to the pre-worktree build).
type WindowCreateFunc func(name, command, cwd string) (*CreatedWindow, error)

func (d *Daemon) createWindow(name, command, cwd string) (*CreatedWindow, error) {
	if d.createWindowFn != nil {
		return d.createWindowFn(name, command, cwd)
	}
	return nil, fmt.Errorf("no window create function configured")
}

// collectManagedNames enumerates the window names to await-gone before a
// (re-)dispatch for goalID: this goal's supervisor/validator windows plus every
// live worker window under this goal's execute-/inv- prefixes. Scoping the prefix
// scan to goalID means a sibling goal's namespaced windows are never collected
// (and thus never awaited/killed) when MaxGoals>1. At MaxGoals<=1 the prefixes are
// bare, so the result matches the pre-namespacing behavior exactly.
func (d *Daemon) collectManagedNames(goalID string) []string {
	mg := d.maxGoals()
	allNames := []string{supervisorWindow(goalID, mg), validatorWindow(goalID, mg)}
	execPrefix := executePrefix(goalID, mg)
	invWinPrefix := invPrefix(goalID, mg)
	windows, err := d.listWindows()
	if err == nil {
		for _, w := range windows {
			if strings.HasPrefix(w.Name, execPrefix) || strings.HasPrefix(w.Name, invWinPrefix) {
				allNames = append(allNames, w.Name)
			}
		}
	}
	return allNames
}

func (d *Daemon) killWindowByName(name string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Name == name {
			return d.executor.KillWindow(d.session, w.TmuxWindowID)
		}
	}
	return nil
}

func (d *Daemon) killWindowsByPrefix(prefix string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if strings.HasPrefix(w.Name, prefix) {
			if err := d.executor.KillWindow(d.session, w.TmuxWindowID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Daemon) waitWindowsGone(names []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if nameSet[w.Name] {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for windows to disappear: %v", names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) waitForPrompt(windowName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	winInfo, err := d.findWindowByName(windowName)
	if err != nil {
		return nil
	}
	for {
		output, err := d.executor.CaptureWindowOutput(d.session, winInfo.TmuxWindowID)
		if err != nil {
			return nil
		}
		if strings.Contains(output, "❯") {
			time.Sleep(d.promptSettleDelay)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for prompt in %q", windowName)
		}
		time.Sleep(d.promptPollInterval)
	}
}

func (d *Daemon) findWindowByName(name string) (*tmux.WindowInfo, error) {
	windows, err := d.listWindows()
	if err != nil {
		return nil, err
	}
	for i := range windows {
		if windows[i].Name == name {
			return &windows[i], nil
		}
	}
	return nil, fmt.Errorf("window %q not found", name)
}

func (d *Daemon) waitClaudeBoot(windowName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if w.Name == windowName {
				found = true
				if w.CurrentCommand != "zsh" && w.CurrentCommand != "" {
					return nil
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("window %q not found", windowName)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for Claude boot in %q", windowName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

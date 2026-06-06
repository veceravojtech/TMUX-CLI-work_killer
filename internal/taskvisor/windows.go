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
// MaxGoals<=1 it is ALWAYS just [head]: a single goal is ever in flight, so the
// teardown sweeps that one goal's namespaced windows. At MaxGoals>1 it is head
// followed by every distinct id in extra (sorted for determinism), so no sibling
// goal's namespaced windows are orphaned. extra is the caller's candidate
// in-flight set (the runtime keys for an idle deactivate, or all goal ids for a
// startup/completion sweep). The window-0 bare "supervisor" is never part of any
// goal namespace, so it is never in the returned set.
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
// startup sweep. The names are always the goal's namespaced forms (supervisor-<ns>
// etc.), so window-0 bare "supervisor" is never matched and never killed.
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
			if err := d.executor.ClosePipePane(d.session, w.TmuxWindowID); err != nil {
				log.Printf("warning: ClosePipePane %q: %v", name, err)
			}
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
			if err := d.executor.ClosePipePane(d.session, w.TmuxWindowID); err != nil {
				log.Printf("warning: ClosePipePane %q: %v", w.Name, err)
			}
			if err := d.executor.KillWindow(d.session, w.TmuxWindowID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Daemon) waitWindowsGone(names []string, timeout time.Duration) error {
	deadline := d.now().Add(timeout)
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
		if d.now().After(deadline) {
			return fmt.Errorf("timeout waiting for windows to disappear: %v", names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) waitForPrompt(windowName string, timeout time.Duration) error {
	deadline := d.now().Add(timeout)
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
		if d.now().After(deadline) {
			return fmt.Errorf("timeout waiting for prompt in %q", windowName)
		}
		time.Sleep(d.promptPollInterval)
	}
}

// waitForPromptOrFail is the loud, bounded-retry sibling of waitForPrompt: it
// re-polls for the prompt up to promptRetryAttempts times and, unlike the old
// log-and-swallow call sites, RETURNS the exhaustion error so a never-ready
// window surfaces immediately through tick() instead of idle-hanging to
// dispatchTimeout. This mirrors waitClaudeBoot, which already returns.
//
// The N attempts DIVIDE the caller's timeout (split, not multiplied): with N=3
// and a 30s budget each inner waitForPrompt gets ~10s, so the total wall-bound
// is unchanged vs the single 30s wait it replaces. waitForPrompt's success path
// (glyph found → promptSettleDelay → nil) is left byte-identical.
func (d *Daemon) waitForPromptOrFail(windowName string, timeout time.Duration) error {
	const promptRetryAttempts = 3
	per := timeout / promptRetryAttempts
	var err error
	for i := 0; i < promptRetryAttempts; i++ {
		if err = d.waitForPrompt(windowName, per); err == nil {
			return nil
		}
		log.Printf("waitForPrompt %q attempt %d/%d failed: %v", windowName, i+1, promptRetryAttempts, err)
	}
	return fmt.Errorf("prompt never arrived in %q after %d attempts: %w", windowName, promptRetryAttempts, err)
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
	deadline := d.now().Add(timeout)
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
		if d.now().After(deadline) {
			return fmt.Errorf("timeout waiting for Claude boot in %q", windowName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

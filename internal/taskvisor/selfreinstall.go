package taskvisor

// Repair-cycle self-reinstall phase (design §6 forward hook 1): when a repair
// goal's changes touch tmux-cli's OWN source, the freshly implemented fix
// exists only as source — the running daemon and the goal's validation cycle
// still use the OLD installed binary. This module inserts a thin rebuild step
// at the supervising→validating transition: shell out to
// `tmux-cli self-update --restart daemon` with the goal's build tree as
// --source, then let the existing stale-binary adoption
// (checkStaleBinary/restartStaleBinary) and Pass-1 resume carry the daemon
// restart while validation proceeds against the freshly installed binary.
//
// Composition contract: this hook only rebuilds (via self-update) and
// un-throttles the stale check (zeroing d.lastStaleCheck). It NEVER writes
// the restart marker or exec-replaces itself — restartStaleBinary owns that.
// The shell-out deliberately blocks the single-threaded tick for the build
// duration (5-min cap): adoption idempotency assumes tick-serial execution,
// so do NOT make it async.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/setup"
)

// selfUpdateTimeout caps the self-update shell-out (build + install): a wedged
// build must not block the tick forever — a timeout is the build-failure path.
const selfUpdateTimeout = 5 * time.Minute

// cliSourceDirs / cliSourceFiles are the path set whose changes make a rebuild
// meaningful — everything `make install` compiles in.
var (
	cliSourceDirs  = []string{"cmd/", "internal/"}
	cliSourceFiles = []string{"go.mod", "go.sum", "Makefile"}
)

// selfUpdateResult mirrors the single machine-readable JSON line self-update
// prints on stdout (cmd/tmux-cli/self_update.go selfUpdateOutput).
type selfUpdateResult struct {
	BinaryChanged bool   `json:"binary_changed"`
	Stage         string `json:"stage,omitempty"`
	Source        string `json:"source"`
	Restart       string `json:"restart"`
}

// parseSelfUpdateOutput decodes self-update's single JSON stdout line. Garbage
// output is an error — treated by the caller as the build-failure path.
func parseSelfUpdateOutput(out []byte) (selfUpdateResult, error) {
	var res selfUpdateResult
	line := strings.TrimSpace(string(out))
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return selfUpdateResult{}, fmt.Errorf("parse self-update output %q: %w", line, err)
	}
	return res, nil
}

// defaultSelfUpdate is the production selfUpdateFn: it invokes the running
// executable's own `self-update --source <sourceDir> --project <projectDir>
// --restart daemon` under selfUpdateTimeout and parses the JSON result line.
// --project is the BASE project dir so the restart marker lands under the
// .tmux-cli/ the daemon actually watches; --source alone points at the goal's
// build tree (worktree or base).
func defaultSelfUpdate(sourceDir, projectDir string) (selfUpdateResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return selfUpdateResult{}, fmt.Errorf("resolve executable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "self-update",
		"--source", sourceDir, "--project", projectDir, "--restart", "daemon")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return selfUpdateResult{}, fmt.Errorf("self-update --source %s: %w (stderr tail: %s)", sourceDir, err, tail)
	}
	return parseSelfUpdateOutput(stdout.Bytes())
}

// selfUpdate returns the configured selfUpdateFn, lazily defaulting to
// defaultSelfUpdate so a literal-constructed Daemon (not via New) never
// nil-panics — same discipline as gitRunner/composeRunner.
func (d *Daemon) selfUpdate() func(sourceDir, projectDir string) (selfUpdateResult, error) {
	if d.selfUpdateFn != nil {
		return d.selfUpdateFn
	}
	return defaultSelfUpdate
}

// isCliSourcePath reports whether a repo-relative path falls inside the cli
// source set.
func isCliSourcePath(p string) bool {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	if p == "" {
		return false
	}
	for _, dir := range cliSourceDirs {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	for _, f := range cliSourceFiles {
		if p == f {
			return true
		}
	}
	return false
}

// porcelainPaths extracts the repo-relative paths from `git status --porcelain`
// output; a rename line ("R  old -> new") contributes both sides.
func porcelainPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		entry := line[3:]
		for _, p := range strings.Split(entry, " -> ") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, strings.Trim(p, `"`))
			}
		}
	}
	return paths
}

// goalTouchesCliSource reports whether the goal's ACTUAL changed files in
// buildDir intersect the cli source set (cmd/**, internal/**, go.mod, go.sum,
// Makefile). Ground truth is git — `status --porcelain` (uncommitted +
// untracked) unioned with the merge-base diff against the base branch (commits
// already made on the goal's worktree branch; empty in inline mode where
// HEAD == base). When git enumeration fails (non-git dir, corrupt state) it
// falls back to a declared Scope/DeliverableArea prefix match — the diff is
// ground truth, declared scope only a proxy; a false positive costs one no-op
// build that self-update's binary_changed:false already suppresses. Never
// crashes the tick.
func (d *Daemon) goalTouchesCliSource(buildDir string, goal *Goal) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	run := d.gitRunner()

	out, _, code, err := run(ctx, "-C", buildDir, "status", "--porcelain")
	if err != nil || code != 0 {
		log.Printf("%s: self-reinstall: git status in %s failed (exit %d, err %v) — falling back to declared scope", goal.ID, buildDir, code, err)
		return goalScopeTouchesCliSource(goal)
	}
	paths := porcelainPaths(out)

	// Committed changes on the goal's branch: merge-base diff vs the base
	// checkout's branch. Inline mode (buildDir == base) diffs a branch against
	// itself — empty, as intended. A diff failure (e.g. unborn HEAD) degrades
	// to the status-only view rather than the declared-scope fallback: status
	// already succeeded, so git itself is healthy.
	base := d.baseBranch(ctx, run)
	if diffOut, _, dcode, derr := run(ctx, "-C", buildDir, "diff", "--name-only", base+"...HEAD"); derr == nil && dcode == 0 {
		for _, p := range strings.Split(diffOut, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
	} else {
		log.Printf("%s: self-reinstall: merge-base diff vs %s failed (exit %d, err %v) — using status view only", goal.ID, base, dcode, derr)
	}

	for _, p := range paths {
		if isCliSourcePath(p) {
			return true
		}
	}
	return false
}

// goalScopeTouchesCliSource is the git-error fallback: prefix-match the goal's
// declared Scope globs and DeliverableArea against the cli source set.
func goalScopeTouchesCliSource(goal *Goal) bool {
	declared := append([]string{}, goal.Scope...)
	if goal.DeliverableArea != "" {
		declared = append(declared, goal.DeliverableArea)
	}
	for _, p := range declared {
		if isCliSourcePath(p) {
			return true
		}
	}
	return false
}

// maybeSelfReinstall is the supervising→validating hook: rebuild+install the
// cli when this goal's build tree is a tmux-cli checkout whose changes touch
// cli source, at most once per goal cycle. The stamp is persisted BEFORE the
// build so a crash/resume re-entry within the same cycle can never rebuild
// twice; the next retry cycle rebuilds naturally (new cycle number).
//
// Build failure is non-destructive: no marker, no restart, goal status and
// retry counters untouched — a distinct log+notify is emitted and the caller
// still spawns the validator, whose own checks fail the broken code with
// actionable output. On binary_changed:true the stale-check throttle is zeroed
// so checkStaleBinary/restartStaleBinary adopt the new binary next tick.
// Never returns an error to the tick.
func (d *Daemon) maybeSelfReinstall(goal *Goal, goals *GoalsFile) {
	buildDir := d.goalWorkDir(goal.ID)
	if !setup.IsCliSourceCheckout(buildDir) {
		return
	}
	cycle := CurrentCycle(goal)
	if goal.LastSelfReinstallCycle == cycle {
		return
	}
	if !d.goalTouchesCliSource(buildDir, goal) {
		return
	}

	goal.LastSelfReinstallCycle = cycle
	if err := SaveGoals(d.workDir, goals); err != nil {
		log.Printf("%s: self-reinstall: persist cycle stamp failed: %v (continuing)", goal.ID, err)
	}

	log.Printf("%s: self-reinstall: rebuilding cli from %s (cycle %d)", goal.ID, buildDir, cycle)
	res, err := d.selfUpdate()(buildDir, d.workDir)
	if err != nil {
		log.Printf("self-reinstall build failed goal=%s cycle=%d: %v", goal.ID, cycle, err)
		d.notifySupervisor(fmt.Sprintf(
			"[TASKVISOR:SELF-REINSTALL-FAILED goal=%s cycle=%d] rebuild failed — validation proceeds against the previously installed binary",
			goal.ID, cycle))
		return
	}
	if res.BinaryChanged {
		// Un-throttle stale-binary adoption: the next tick's checkStaleBinary
		// bypasses the 60s gate and restartStaleBinary exec-replaces into the
		// binary self-update just installed. The restart marker was written by
		// self-update under the BASE project's .tmux-cli/, making the restart a
		// planned one (daemon.go startup consumption).
		d.lastStaleCheck = time.Time{}
		log.Printf("%s: self-reinstall: binary changed (restart=%s) — stale-binary adoption nudged", goal.ID, res.Restart)
		return
	}
	log.Printf("%s: self-reinstall: build succeeded, binary unchanged — no restart", goal.ID)
}

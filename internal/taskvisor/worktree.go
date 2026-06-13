package taskvisor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/setup"
)

// worktree.go — per-goal git-worktree isolation (E1-1a).
//
// When MaxGoals>1 the daemon co-schedules declared-disjoint goals (the
// DisjointReadySet gate). The scope gate is the first-line safety; a dedicated
// git worktree per goal is the PHYSICAL enforcement: each goal's supervisor (and
// the workers it spawns) edit tracked source files inside an isolated checkout at
// .tmux-cli-worktrees/<id> (a SIBLING of the control plane), branched from the
// current base HEAD. On GoalDone the
// worktree's commits are merged back into base under a serialization lock
// (rebase-then-ff-only); on conflict the goal fails cleanly with the conflicting
// paths surfaced and base left untouched; on hard-halt the worktree is discarded.
//
// CONTROL-PLANE SPLIT (the linchpin): d.workDir today is BOTH the worker cwd and
// the root of the single .tmux-cli/ control plane (goals.yaml, signals, markers,
// reports). Naively pointing a goal's cwd at its worktree would scatter control
// state into the per-goal checkout. Instead <worktree>/.tmux-cli is a symlink to
// <base>/.tmux-cli, so ONE shared control plane persists while only tracked
// source is isolated. The .tmux-cli/ dir is git-excluded (internal/setup
// gitexclude: "/.tmux-cli/"), so the symlink is never committed into a worktree
// branch, and the daemon keeps reading/writing every marker/signal/goals.yaml at
// base d.workDir unchanged.
//
// At MaxGoals=1 NONE of this runs: ensureWorktree returns d.workDir with no git,
// merge/discard/finalize short-circuit on the empty WorktreeDir, and prune
// short-circuits on the absent worktrees dir — behavior is byte-identical to the
// pre-worktree build with zero git invocations.

// GitRunnerFunc is the injectable seam for every git invocation (mirrors
// ScriptRunnerFunc). Each git call passes its own "-C <dir>" in args so the
// runner is cwd-independent; the default runner shells out to the git binary.
// Unit tests inject a recording fake to assert argv without a real repo.
type GitRunnerFunc func(ctx context.Context, args ...string) (stdout, stderr string, exitCode int, err error)

const gitTimeout = 60 * time.Second

func defaultGitRunner(ctx context.Context, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// SetGitRunnerFunc overrides the git runner (tests inject a fake). Mirrors
// SetScriptRunnerFunc.
func (d *Daemon) SetGitRunnerFunc(fn GitRunnerFunc) { d.gitRunnerFn = fn }

// gitRunner returns the configured runner, lazily defaulting to defaultGitRunner
// so callers never nil-check. With MaxGoals=1 no git path is ever reached, so the
// real runner is never invoked even though it is the default.
func (d *Daemon) gitRunner() GitRunnerFunc {
	if d.gitRunnerFn != nil {
		return d.gitRunnerFn
	}
	return defaultGitRunner
}

// errMergeConflict signals that rebasing a goal's worktree branch onto the
// advanced base produced a content conflict. It carries the conflicting paths so
// the failed goal surfaces them. The rebase is aborted before this is returned,
// so base is left with NO partial merge.
type errMergeConflict struct {
	paths []string
}

func (e errMergeConflict) Error() string {
	if len(e.paths) == 0 {
		return "worktree merge-back conflict"
	}
	return "worktree merge-back conflict on: " + strings.Join(e.paths, ", ")
}

// errIntegrationFailed signals that the configured post-merge integration command
// exited non-zero against the freshly-merged base. The FF has already advanced
// base (we deliberately do NOT revert); finalizeWorktreeOnDone turns this into a
// loud fail-signal + cascade so the broken line halts. It is a VALUE type (not a
// pointer) to match the errors.As(&x) value pattern used for errMergeConflict.
type errIntegrationFailed struct {
	stderr string
	exit   int
}

func (e errIntegrationFailed) Error() string {
	msg := fmt.Sprintf("post-merge integration command failed (exit %d)", e.exit)
	if s := strings.TrimSpace(e.stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// worktreeBranch is the deterministic branch name for a goal's worktree, so
// `worktree add -b`, `merge --ff-only`, and `branch -D` are all idempotent.
func worktreeBranch(goalID string) string { return "taskvisor/" + goalID }

// worktreesDirName is the in-repo sibling directory that holds every per-goal
// git worktree. It is a SIBLING of the control plane (.tmux-cli), never nested
// inside it: a worktree carries a <wt>/.tmux-cli back-symlink, so nesting it
// under .tmux-cli would make the control plane contain itself (the reproduced
// ELOOP for symlink-following walkers + broken MCP discovery). Keeping worktrees
// out of .tmux-cli is what kills that self-reference. The single source of the
// name; used by worktreePath, pruneOrphanWorktrees, and the removal guard.
const worktreesDirName = ".tmux-cli-worktrees"

// worktreePath is the absolute checkout path for a goal's worktree, under the
// in-repo (git-excluded) sibling worktrees dir — never inside the control plane.
func (d *Daemon) worktreePath(goalID string) string {
	return filepath.Join(d.workDir, worktreesDirName, goalID)
}

// safeToRemoveWorktree is a defense-in-depth guard consulted before every
// worktree removal (git worktree remove --force / os.RemoveAll). It returns a
// loud error if removing path could damage the control plane or base project,
// and nil only when path is provably a per-goal worktree.
//
// This is NOT a live data-loss fix (git 2.43.0 does not follow the back-symlink
// on remove); it is a guard against a FUTURE symlink-following remover. The
// load-bearing clause is the positive allowlist: path MUST live under
// <base>/.tmux-cli-worktrees/. The denylist clauses (base / control-plane /
// ancestor / symlink-resolving-into-base) are redundant belt-and-braces.
func safeToRemoveWorktree(workDir, path string) error {
	if path == "" {
		return fmt.Errorf("empty worktree path")
	}
	base := filepath.Clean(workDir)
	p := filepath.Clean(path)
	ctl := filepath.Join(base, ".tmux-cli")

	// Denylist: never the base project, never the control plane, never an
	// ancestor of the control plane.
	if p == base {
		return fmt.Errorf("path %q is the base project dir", p)
	}
	if p == ctl {
		return fmt.Errorf("path %q is the control plane", p)
	}
	if pathWithin(ctl, p) {
		return fmt.Errorf("path %q is an ancestor of the control plane %q", p, ctl)
	}

	// If the path is a symlink (or has symlinked components), refuse when it
	// resolves to the base, the control plane, or an ancestor of the control
	// plane — a symlink-following remover would then escape into base.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		rc := filepath.Clean(resolved)
		if rc == base || rc == ctl || pathWithin(ctl, rc) {
			return fmt.Errorf("path %q resolves to %q (base/control-plane/ancestor)", p, rc)
		}
	}

	// Positive allowlist (load-bearing): the path must be the sibling worktrees
	// root or live under it. Anything else is refused.
	wtRoot := filepath.Join(base, worktreesDirName)
	if p != wtRoot && !pathWithin(p, wtRoot) {
		return fmt.Errorf("path %q is not under the worktree root %q", p, wtRoot)
	}
	return nil
}

// pathWithin reports whether child is equal to or nested under parent (both
// assumed already cleaned/absolute by the caller).
func pathWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// isGitRepo reports whether the base workdir is a git repository (.git is a dir
// in a normal clone, a file in a linked worktree — os.Stat covers both). Used to
// degrade gracefully: a non-git project runs parallel goals in the base tree.
func (d *Daemon) isGitRepo() bool {
	_, err := os.Stat(filepath.Join(d.workDir, ".git"))
	return err == nil
}

// ensureWorktree returns the cwd a goal's worker windows should run in.
//
//   - parallel==false (MaxGoals=1): returns d.workDir with NO git call and leaves
//     the goalRuntime's WorktreeDir empty — byte-identical to the pre-worktree
//     build.
//   - non-git repo: returns d.workDir + warns; never errors (degrade to base).
//   - otherwise: idempotently materializes .tmux-cli-worktrees/<id> branched from
//     HEAD (stat-first so a retry of the SAME goal reuses its worktree), symlinks
//     the control plane in, records WorktreeDir/Branch on the goalRuntime, and
//     returns the worktree path.
func (d *Daemon) ensureWorktree(goal *Goal, parallel bool) (string, error) {
	if !parallel {
		return d.workDir, nil
	}
	if !d.isGitRepo() {
		log.Printf("warning: %s: not a git repository — running parallel goal in base tree (no isolation)", goal.ID)
		return d.workDir, nil
	}

	rt := d.runtime(goal.ID)
	wtPath := d.worktreePath(goal.ID)
	branch := worktreeBranch(goal.ID)

	// Idempotent reuse: a retry of the same goal in a later cycle keeps its
	// existing worktree (skip `worktree add`).
	if fi, err := os.Stat(wtPath); err == nil && fi.IsDir() {
		rt.WorktreeDir = wtPath
		rt.Branch = branch
		return wtPath, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	_, stderr, code, err := d.gitRunner()(ctx, "-C", d.workDir, "worktree", "add", "-b", branch, wtPath, "HEAD")
	if err != nil {
		return "", fmt.Errorf("git worktree add for %s: %w", goal.ID, err)
	}
	if code != 0 {
		return "", fmt.Errorf("git worktree add for %s failed (exit %d): %s", goal.ID, code, strings.TrimSpace(stderr))
	}

	if err := d.symlinkControlPlane(wtPath); err != nil {
		return "", err
	}

	rt.WorktreeDir = wtPath
	rt.Branch = branch
	return wtPath, nil
}

// symlinkControlPlane points <worktree>/.tmux-cli at <base>/.tmux-cli so worker
// reports/signals written relative to the worktree cwd resolve into the single
// shared control plane. This back-symlink must NEVER be committed: the exclude
// entry "/.tmux-cli" (name, not the old directory-only "/.tmux-cli/") matches the
// symlink so `git add -A` skips it, AND mergeWorktreeBack drops it from the index
// with `git rm --cached --ignore-unmatch` as a belt-and-suspenders guard. If it
// were committed, the ff-merge into base would replace base's real .tmux-cli
// directory with a self-referential symlink (ELOOP) and destroy the control plane.
func (d *Daemon) symlinkControlPlane(wtPath string) error {
	baseCtl := filepath.Join(d.workDir, ".tmux-cli")
	wtCtl := filepath.Join(wtPath, ".tmux-cli")
	if fi, err := os.Lstat(wtCtl); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil // already symlinked (reuse path)
		}
		// A real dir/file is shadowing the control plane (should not happen since
		// .tmux-cli is git-excluded and absent from the checkout) — clear it so the
		// single shared control plane is never duplicated per worktree. Guard the
		// removal (defense-in-depth): wtCtl is under the sibling worktrees root and
		// resolves into the worktree, so a legitimate shadow clears; only a path
		// resolving back into base is refused.
		if err := safeToRemoveWorktree(d.workDir, wtCtl); err != nil {
			log.Printf("warning: refusing unsafe worktree remove: %v", err)
			return fmt.Errorf("refusing to clear worktree control plane %s: %w", wtCtl, err)
		}
		if err := os.RemoveAll(wtCtl); err != nil {
			return fmt.Errorf("clear worktree control plane %s: %w", wtCtl, err)
		}
	}
	if err := os.Symlink(baseCtl, wtCtl); err != nil {
		return fmt.Errorf("symlink control plane into worktree: %w", err)
	}
	return nil
}

// integrationCmd reads Taskvisor.IntegrationCmd from setting.yaml, returning ""
// when unset or unreadable. Mirrors maxGoals(): a single impurity resolved per
// merge so the merge logic stays pure. An empty result disables the gate.
func (d *Daemon) integrationCmd() string {
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || s == nil {
		return ""
	}
	return s.Taskvisor.IntegrationCmd
}

// runIntegrationGate materializes IntegrationCmd into a temp `#!/bin/sh` script
// (0o755, removed via defer) and runs it against the merged base (d.workDir) via
// the shared scriptRunnerFn seam under d.scriptTimeout — the same injected seam
// validate.sh uses, so no signature change. GOAL_ID is exported mirroring
// runValidateScript. A non-zero exit (or an exec error) returns errIntegrationFailed;
// an unset command is a no-op (nil). Callers invoke this INSIDE WithMergeLock.
func (d *Daemon) runIntegrationGate(goal *Goal) error {
	cmd := d.integrationCmd()
	if cmd == "" {
		return nil
	}

	f, err := os.CreateTemp("", "integration-*.sh")
	if err != nil {
		return fmt.Errorf("create integration script: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString("#!/bin/sh\nset -e\n" + cmd + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("write integration script: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close integration script: %w", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("chmod integration script: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.scriptTimeout)
	defer cancel()
	env := append(os.Environ(), "GOAL_ID="+goal.ID)
	_, stderr, code, runErr := d.scriptRunnerFn(ctx, path, d.workDir, env)
	if runErr != nil {
		if strings.TrimSpace(stderr) == "" {
			stderr = runErr.Error()
		}
		return errIntegrationFailed{stderr: stderr, exit: code}
	}
	if code != 0 {
		return errIntegrationFailed{stderr: stderr, exit: code}
	}
	return nil
}

// mergeWorktreeBack merges a completed goal's worktree edits back into base under
// WithMergeLock: stage all, commit iff dirty, then (only if the branch is ahead
// of base) rebase the branch onto base and fast-forward base to it. A rebase
// conflict aborts cleanly and returns errMergeConflict (base untouched). When the
// goal has no worktree (MaxGoals=1 / non-git) this is a no-op with NO lock and NO
// git — the guard is the first statement so single-goal operation never touches
// the merge lock.
func (d *Daemon) mergeWorktreeBack(goal *Goal) error {
	rt := d.runtime(goal.ID)
	if rt.WorktreeDir == "" || rt.WorktreeDir == d.workDir {
		return nil
	}
	wt := rt.WorktreeDir
	branch := rt.Branch

	return WithMergeLock(d.workDir, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
		run := d.gitRunner()

		// Stage every implementer change. The .tmux-cli back-symlink (a symlink into
		// the shared base control plane; see symlinkControlPlane) is excluded by NAME
		// in both .gitignore and .git/info/exclude, so `git add -A` skips it silently.
		// If it were committed, fast-forwarding base would replace base's real
		// .tmux-cli directory with a self-referential symlink and ELOOP the whole
		// control plane.
		if _, se, code, err := run(ctx, "-C", wt, "add", "-A"); err != nil {
			return fmt.Errorf("git -C %s add -A: %w", wt, err)
		} else if code != 0 {
			return fmt.Errorf("git -C %s add -A failed (exit %d): %s", wt, code, strings.TrimSpace(se))
		}

		// Defense in depth at the merge seam: guarantee the control-plane back-symlink
		// is never in the commit even if an ignore rule regresses or a prior corruption
		// left .tmux-cli tracked in base. `rm --cached --ignore-unmatch` only edits the
		// index (never the working-tree symlink) and exits 0 whether or not .tmux-cli
		// is present — so it is a no-op normally and quietly untracks (self-heals) a
		// stale entry, never propagating an ELOOP through the ff-merge.
		if _, se, code, err := run(ctx, "-C", wt, "rm", "--cached", "--ignore-unmatch", "--", ".tmux-cli"); err != nil {
			return fmt.Errorf("git -C %s rm --cached .tmux-cli: %w", wt, err)
		} else if code != 0 {
			return fmt.Errorf("git -C %s rm --cached .tmux-cli failed (exit %d): %s", wt, code, strings.TrimSpace(se))
		}

		// Commit only when something is staged (empty diff ⇒ skip the commit).
		porcelain, _, _, err := run(ctx, "-C", wt, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("git -C %s status: %w", wt, err)
		}
		if strings.TrimSpace(porcelain) != "" {
			if _, se, code, err := run(ctx, "-C", wt, "commit", "-m", "goal "+goal.ID); err != nil {
				return fmt.Errorf("git -C %s commit: %w", wt, err)
			} else if code != 0 {
				return fmt.Errorf("git -C %s commit failed (exit %d): %s", wt, code, strings.TrimSpace(se))
			}
		}

		baseBranch := d.baseBranch(ctx, run)

		// Nothing to land: the branch carries no commits beyond base (no edits this
		// goal). Skip rebase+merge entirely — the caller still removes the worktree.
		if ahead := commitsAhead(ctx, run, wt, baseBranch); ahead == 0 {
			return nil
		}

		// Rebase the goal branch onto the (possibly peer-advanced) base, then
		// fast-forward base. A conflict means a peer goal touched the same content:
		// abort and fail the goal — never auto-resolve, never leave a partial merge.
		if _, _, code, err := run(ctx, "-C", wt, "rebase", baseBranch); err != nil {
			return fmt.Errorf("git -C %s rebase %s: %w", wt, baseBranch, err)
		} else if code != 0 {
			paths := unmergedPaths(ctx, run, wt)
			_, _, _, _ = run(ctx, "-C", wt, "rebase", "--abort")
			return errMergeConflict{paths: paths}
		}

		if _, se, code, err := run(ctx, "-C", d.workDir, "merge", "--ff-only", branch); err != nil {
			return fmt.Errorf("git -C %s merge --ff-only %s: %w", d.workDir, branch, err)
		} else if code != 0 {
			return fmt.Errorf("git merge --ff-only %s failed (exit %d): %s", branch, code, strings.TrimSpace(se))
		}

		// Post-merge integration gate (E1 P4): the FF just advanced base, so run the
		// combined suite against the merged base while STILL holding the merge lock.
		// A red suite means two scope-disjoint goals merged into a semantically-broken
		// base (the gap a path-prefix scope check cannot catch) — fail the goal loudly.
		// The no-worktree and ahead==0 guards above already exclude MaxGoals=1 and the
		// no-merge case, so reaching here implies a real merge under MaxGoals>1; no
		// extra maxGoals() check is needed at this insert point.
		if ic := d.integrationCmd(); ic != "" {
			if e := d.runIntegrationGate(goal); e != nil {
				return e
			}
		}
		return nil
	})
}

// baseBranch resolves the base checkout's current branch (e.g. "main"). A
// detached HEAD falls back to the commit SHA so rebase/merge still target a
// concrete commit.
func (d *Daemon) baseBranch(ctx context.Context, run GitRunnerFunc) string {
	out, _, code, err := run(ctx, "-C", d.workDir, "rev-parse", "--abbrev-ref", "HEAD")
	name := strings.TrimSpace(out)
	if err != nil || code != 0 || name == "" || name == "HEAD" {
		sha, _, c2, e2 := run(ctx, "-C", d.workDir, "rev-parse", "HEAD")
		if e2 == nil && c2 == 0 {
			if s := strings.TrimSpace(sha); s != "" {
				return s
			}
		}
		if name == "" {
			return "HEAD"
		}
	}
	return name
}

// commitsAhead returns how many commits the worktree's HEAD is ahead of
// baseBranch (0 ⇒ no edits to merge). A parse/exec failure returns 1 so the
// merge path runs rather than silently dropping edits.
func commitsAhead(ctx context.Context, run GitRunnerFunc, wt, baseBranch string) int {
	out, _, code, err := run(ctx, "-C", wt, "rev-list", "--count", baseBranch+"..HEAD")
	if err != nil || code != 0 {
		return 1
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil {
		return 1
	}
	return n
}

// unmergedPaths lists the files left in conflict after a failed rebase, so the
// failed goal can surface exactly what collided.
func unmergedPaths(ctx context.Context, run GitRunnerFunc, wt string) []string {
	out, _, _, err := run(ctx, "-C", wt, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// discardWorktree removes a goal's worktree and branch (idempotent). A no-op with
// NO git when the goal has no worktree (MaxGoals=1 / non-git), so single-goal
// teardown stays byte-identical. Crash-orphaned worktrees with no live runtime
// are handled by pruneOrphanWorktrees instead.
func (d *Daemon) discardWorktree(goal *Goal) error {
	rt := d.runtime(goal.ID)
	if rt.WorktreeDir == "" || rt.WorktreeDir == d.workDir {
		return nil
	}
	wt := rt.WorktreeDir
	branch := rt.Branch
	if branch == "" {
		branch = worktreeBranch(goal.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	run := d.gitRunner()
	// Both calls are best-effort/idempotent: a missing worktree or branch is not
	// an error (e.g. already pruned, or merge-back removed nothing). The remove is
	// guarded (defense-in-depth): if WorktreeDir was somehow corrupted to point at
	// the control plane or base, refuse it loudly and skip — but still attempt the
	// branch delete below.
	if err := safeToRemoveWorktree(d.workDir, wt); err != nil {
		log.Printf("warning: refusing unsafe worktree remove: %v", err)
	} else if _, _, _, err := run(ctx, "-C", d.workDir, "worktree", "remove", "--force", wt); err != nil {
		log.Printf("warning: %s: worktree remove: %v", goal.ID, err)
	}
	if _, _, _, err := run(ctx, "-C", d.workDir, "branch", "-D", branch); err != nil {
		log.Printf("warning: %s: branch -D %s: %v", goal.ID, branch, err)
	}

	rt.WorktreeDir = ""
	rt.Branch = ""
	return nil
}

// pruneOrphanWorktrees clears worktrees left by a crashed run on (re)activation:
// `git worktree prune` plus removal of any .tmux-cli-worktrees/<id> whose goal is
// not currently GoalRunning. It short-circuits with NO git when the worktrees dir
// does not exist, so a project that never ran parallel goals (MaxGoals=1) makes
// zero git calls on activate — preserving the byte-identical guarantee.
func (d *Daemon) pruneOrphanWorktrees(goals *GoalsFile) {
	base := filepath.Join(d.workDir, worktreesDirName)
	entries, err := os.ReadDir(base)
	if err != nil {
		return // no worktrees dir ⇒ nothing to prune, zero git
	}
	if !d.isGitRepo() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	run := d.gitRunner()
	_, _, _, _ = run(ctx, "-C", d.workDir, "worktree", "prune")

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if g, ok := goals.GoalByID(id); ok && g.Status == GoalRunning {
			continue // keep an in-flight goal's worktree (e.g. crash re-dispatch reuses it)
		}
		wt := filepath.Join(base, id)
		if err := safeToRemoveWorktree(d.workDir, wt); err != nil {
			log.Printf("warning: refusing unsafe worktree remove: %v", err)
		} else {
			_, _, _, _ = run(ctx, "-C", d.workDir, "worktree", "remove", "--force", wt)
		}
		_, _, _, _ = run(ctx, "-C", d.workDir, "branch", "-D", worktreeBranch(id))
	}
}

// finalizeWorktreeOnDone is the GoalDone hook invoked from advanceToNextGoal: it
// merges the goal's worktree back into base and removes it. On a merge conflict
// it flips the goal done→failed, persists the conflicting paths as a fail signal,
// cascade-blocks dependents, discards the conflicted worktree (base already left
// clean by the rebase --abort), and reports failed=true so the caller suppresses
// the downstream resume. With no worktree (MaxGoals=1) both inner calls are
// zero-git no-ops and it returns failed=false.
func (d *Daemon) finalizeWorktreeOnDone(goals *GoalsFile, goal *Goal) (failed bool, err error) {
	mergeErr := d.mergeWorktreeBack(goal)
	if mergeErr == nil {
		_ = d.discardWorktree(goal)
		return false, nil
	}

	// Post-merge integration failure: the FF already advanced base (we deliberately
	// do NOT revert — out of scope and risky). Mirror the merge-conflict path: write
	// a VerdictFail/owner=human signal, flip done→failed, cascade-block dependents,
	// discard the worktree, and report failed=true so the caller suppresses the
	// downstream resume. Checked BEFORE the errMergeConflict guard below so its
	// non-conflict early-return cannot swallow this error.
	var ifail errIntegrationFailed
	if errors.As(mergeErr, &ifail) {
		log.Printf("%s: post-merge integration gate failed (exit %d) — failing goal; base already advanced (not reverted)", goal.ID, ifail.exit)
		nextAction := fmt.Sprintf("Post-merge integration command failed (exit %d)", ifail.exit)
		if s := strings.TrimSpace(ifail.stderr); s != "" {
			if len(s) > 500 {
				s = s[:500]
			}
			nextAction += ": " + s
		}
		nextAction += ". The merged base fails the combined integration suite; fix and re-run."
		if sigErr := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
			Verdict:    VerdictFail,
			Owner:      "human",
			NextAction: nextAction,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}); sigErr != nil {
			return false, sigErr
		}
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		goals.CascadeFailure(goal.ID, "fail")
		d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=integration-gate-failed cascade=%d]",
			goal.ID, goal.Description, countCascaded(goals, goal.ID)))
		if saveErr := SaveGoals(d.workDir, goals); saveErr != nil {
			return false, saveErr
		}
		_ = d.discardWorktree(goal)
		return true, nil
	}

	var mc errMergeConflict
	if !errors.As(mergeErr, &mc) {
		return false, mergeErr
	}

	log.Printf("%s: merge-back conflict on %v — failing goal; base left unchanged", goal.ID, mc.paths)
	nextAction := "Worktree merge-back conflicted with base"
	if len(mc.paths) > 0 {
		nextAction += " on: " + strings.Join(mc.paths, ", ")
	}
	nextAction += ". A peer goal modified overlapping content; resolve and re-run."
	if sigErr := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
		Verdict:    VerdictFail,
		Owner:      "human",
		NextAction: nextAction,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}); sigErr != nil {
		return false, sigErr
	}

	goal.Status = GoalFailed
	goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	goals.CascadeFailure(goal.ID, "fail")
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=merge-conflict cascade=%d]",
		goal.ID, goal.Description, countCascaded(goals, goal.ID)))
	if saveErr := SaveGoals(d.workDir, goals); saveErr != nil {
		return false, saveErr
	}
	_ = d.discardWorktree(goal)
	return true, nil
}

// cleanupWorktreeOnHalt is the terminal-halt hook (failure / exhausted budget /
// circuit-break) invoked from advanceToNextGoal: discard the worktree with no
// merge. Zero-git no-op at MaxGoals=1.
func (d *Daemon) cleanupWorktreeOnHalt(goal *Goal) {
	_ = d.discardWorktree(goal)
}

// WithMergeLock serializes worktree merge-back across goals via an exclusive
// flock on .tmux-cli/worktree-merge.lock (a clone of WithGoalsLock, on a distinct
// lock file so it never contends with the goals flock).
func WithMergeLock(projectRoot string, fn func() error) error {
	lockPath := filepath.Join(projectRoot, ".tmux-cli", "worktree-merge.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open merge lock: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire merge lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

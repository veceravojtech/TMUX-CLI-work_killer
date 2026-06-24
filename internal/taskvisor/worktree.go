package taskvisor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// baseRootMarker is the repo-root file used to assert that a worktree has the
// base actually checked out. go.mod is committed and present in every real
// checkout of this module, so its presence at <wt>/go.mod is a cheap, reliable
// proof the worktree is not a base-less stub.
const baseRootMarker = "go.mod"

// baseCheckedOut reports whether the base is materialized in the worktree by
// checking for the repo-root marker (go.mod). A stray/empty dir or a worktree
// whose checkout never landed will be missing it.
func baseCheckedOut(wtPath string) bool {
	_, err := os.Stat(filepath.Join(wtPath, baseRootMarker))
	return err == nil
}

// isRegisteredWorktree reports whether wtPath is a worktree git actually knows
// about, by scanning `git worktree list --porcelain` for a matching `worktree
// <path>` line. On any git error / non-zero exit it returns false (treat an
// unverifiable dir as not-registered so the caller re-provisions). Routed
// through the injected runner so tests assert without a real repo.
func (d *Daemon) isRegisteredWorktree(ctx context.Context, run GitRunnerFunc, wtPath string) bool {
	out, _, code, err := run(ctx, "-C", d.workDir, "worktree", "list", "--porcelain")
	if err != nil || code != 0 {
		return false
	}
	want := filepath.Clean(wtPath)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// teardownStrayWorktree removes a directory at wtPath that is NOT a usable
// registered worktree (stray dir, or registered-but-base-less stub) so the
// caller can re-provision it cleanly with `git worktree add`. Every removal is
// guarded by safeToRemoveWorktree first. The `git worktree remove --force` is
// best-effort (a stray, unregistered dir makes git exit non-zero — ignored);
// os.RemoveAll does the real removal; `git worktree prune` clears any
// half-registration left behind.
func (d *Daemon) teardownStrayWorktree(ctx context.Context, run GitRunnerFunc, wtPath string) error {
	if err := safeToRemoveWorktree(d.workDir, wtPath); err != nil {
		return fmt.Errorf("refusing to tear down unsafe worktree path %s: %w", wtPath, err)
	}
	// Best-effort: a stray (unregistered) dir makes git exit non-zero — ignore it;
	// os.RemoveAll below does the real removal.
	_, _, _, _ = run(ctx, "-C", d.workDir, "worktree", "remove", "--force", wtPath)
	if err := os.RemoveAll(wtPath); err != nil {
		return fmt.Errorf("remove stray worktree dir %s: %w", wtPath, err)
	}
	// Best-effort: clear any half-registration the stray dir left behind.
	_, _, _, _ = run(ctx, "-C", d.workDir, "worktree", "prune")
	return nil
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

	// ctx + runner are shared by the reuse registration-check AND the add below, so
	// create them once before the stat block.
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	run := d.gitRunner()

	// Idempotent reuse: a retry of the same goal in a later cycle keeps its
	// existing worktree (skip `worktree add`) — but ONLY when the dir is a git-
	// registered worktree with the base actually checked out. A stray dir or a
	// base-less stub is torn down and re-provisioned so the goal never runs in a
	// directory with no source tree (the silent-STUCK failure mode).
	if fi, err := os.Stat(wtPath); err == nil && fi.IsDir() {
		if d.isRegisteredWorktree(ctx, run, wtPath) && baseCheckedOut(wtPath) {
			rt.WorktreeDir = wtPath
			rt.Branch = branch
			// Self-heal: a worktree created before this command-copy existed (or by an
			// older binary) lacks the slash-command set. Fill in any MISSING command
			// files without overwriting a scoped goal's edited command mirror.
			if err := d.copyClaudeCommands(wtPath, false); err != nil {
				return "", err
			}
			return wtPath, nil
		}
		log.Printf("%s: worktree %s exists but is not a usable registered worktree (registered/base check failed) — re-provisioning", goal.ID, wtPath)
		if err := d.teardownStrayWorktree(ctx, run, wtPath); err != nil {
			return "", err
		}
		// fall through to add
	}

	_, stderr, code, err := run(ctx, "-C", d.workDir, "worktree", "add", "-B", branch, wtPath, "HEAD")
	if err != nil {
		return "", fmt.Errorf("git worktree add for %s: %w", goal.ID, err)
	}
	if code != 0 {
		return "", fmt.Errorf("git worktree add for %s failed (exit %d): %s", goal.ID, code, strings.TrimSpace(stderr))
	}

	// Fail loud if the add did not actually check the base out: never hand back a
	// base-less stub as the goal's cwd. The just-added (baseless) worktree is left
	// on disk — self-healing on the next reuse check (registered-but-baseless →
	// re-provision).
	if !baseCheckedOut(wtPath) {
		return "", fmt.Errorf("worktree provisioning failed for %s: base not checked out at %s (missing %s)", goal.ID, wtPath, baseRootMarker)
	}

	if err := d.symlinkControlPlane(wtPath); err != nil {
		return "", err
	}
	if err := d.copyClaudeCommands(wtPath, true); err != nil {
		return "", err
	}

	rt.WorktreeDir = wtPath
	rt.Branch = branch
	return wtPath, nil
}

// copyClaudeCommands materializes the .claude/commands/tmux/ slash-command set
// into a freshly-created worktree by copying the canonical installed tree from
// base. `git worktree add` carries NONE of these files in because .claude is
// git-excluded (internal/setup gitexclude: "/.claude/commands/tmux/"), so a
// worktree checkout has no command definitions and a supervisor window launched
// with cwd=worktree fails every `/tmux:supervisor <goal>` with "Unknown command"
// — looping STUCK→recover forever (see ensureWorktree's dispatch cwd). Copying
// the base set makes the commands resolve in the worktree cwd. The copy stays
// git-excluded (the same exclude applies inside the worktree), so it is never
// committed to the goal branch nor merged back into base.
//
// Called from the creation path with overwrite=true (a just-`worktree add`ed,
// pristine tree — nothing to clobber) and from the reuse path with
// overwrite=false (fill MISSING files only): a scoped goal whose deliverable IS a
// .claude/commands/tmux/*.xml file edits its worktree command mirror (the
// embedded↔.claude dual-write), so a reused worktree must never have that
// in-flight edit overwritten. Best-effort: a missing base source dir (Commands
// disabled / not yet set up) is a silent no-op.
func (d *Daemon) copyClaudeCommands(wtPath string, overwrite bool) error {
	srcRoot := filepath.Join(d.workDir, ".claude", "commands", "tmux")
	if fi, err := os.Stat(srcRoot); err != nil || !fi.IsDir() {
		return nil // base has no installed command set — nothing to mirror
	}
	dstRoot := filepath.Join(wtPath, ".claude", "commands", "tmux")
	return copyTree(srcRoot, dstRoot, overwrite)
}

// copyTree recursively copies the regular files and directories under srcRoot
// into dstRoot, creating parents as needed. Symlinks and other non-regular
// entries are skipped (the command set is plain files). When overwrite is false,
// a destination file that already exists is left untouched (preserving a goal's
// edited command mirror).
func copyTree(srcRoot, dstRoot string, overwrite bool) error {
	return filepath.WalkDir(srcRoot, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if de.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !de.Type().IsRegular() {
			return nil
		}
		if !overwrite {
			if _, err := os.Stat(dst); err == nil {
				return nil // preserve an existing (possibly goal-edited) command file
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
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
//
// Returns integrated=true ONLY after a real ff-merge lands the branch's commits
// into base; false on the no-worktree guard, on ahead==0 (nothing to land), and
// on every error return. finalizeWorktreeOnDone consults integrated (gated on
// goalUsesWorktree) to enforce the done-without-integration invariant: a
// non-empty-scope worktree goal that integrated zero commits is failed, not left
// silently done.
func (d *Daemon) mergeWorktreeBack(goal *Goal) (integrated bool, err error) {
	rt := d.runtime(goal.ID)
	if rt.WorktreeDir == "" || rt.WorktreeDir == d.workDir {
		return false, nil
	}
	wt := rt.WorktreeDir
	branch := rt.Branch

	var didIntegrate bool
	mergeErr := WithMergeLock(d.workDir, func() error {
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
			if _, se, code, err := run(ctx, "-C", wt, "commit", "-m", goalCommitMessage(goal)); err != nil {
				return fmt.Errorf("git -C %s commit: %w", wt, err)
			} else if code != 0 {
				return fmt.Errorf("git -C %s commit failed (exit %d): %s", wt, code, strings.TrimSpace(se))
			}
		}

		// Pre-merge base refresh (best-effort, gated on git_freshness): pull the
		// freshest base from origin before the rebase so a clean integration never
		// races a peer push it could have rebased over. Warn-only — a fetch failure
		// never alters the always-on rebase below, which already lands the branch
		// onto the local base and prevents most races.
		d.refreshBase(ctx, run)

		baseBranch := d.baseBranch(ctx, run)

		// Nothing to land: the branch carries no commits beyond base (no edits this
		// goal). Skip rebase+merge entirely — the caller still removes the worktree.
		if ahead := commitsAhead(ctx, run, wt, baseBranch); ahead == 0 {
			return nil
		}

		// Rebase the goal branch onto the (possibly peer-advanced) base, then
		// fast-forward base. A conflict means a peer goal touched the same content.
		// Integrate-or-BLOCK: out-of-scope conflicts (peer config/compose churn) are
		// auto-resolved in base's favor so the integration still lands; an IN-scope
		// conflict (or an undeterminable empty scope) touches the goal's OWN
		// deliverable, where resolving either way is wrong — abort and BLOCK.
		if _, _, code, err := run(ctx, "-C", wt, "rebase", baseBranch); err != nil {
			return fmt.Errorf("git -C %s rebase %s: %w", wt, baseBranch, err)
		} else if code != 0 {
			if mc := d.resolveOutOfScopeConflicts(ctx, run, wt, goal); mc != nil {
				_, _, _, _ = run(ctx, "-C", wt, "rebase", "--abort")
				return *mc
			}
			// All conflicts were out-of-scope and resolved in base's favor; the
			// rebase completed, so fall through to the ff-merge.
		}

		if _, se, code, err := run(ctx, "-C", d.workDir, "merge", "--ff-only", branch); err != nil {
			return fmt.Errorf("git -C %s merge --ff-only %s: %w", d.workDir, branch, err)
		} else if code != 0 {
			return fmt.Errorf("git merge --ff-only %s failed (exit %d): %s", branch, code, strings.TrimSpace(se))
		}

		// Post-merge HEAD assertion: GoalDone is gated on base PROVABLY containing
		// the goal's commit (base ancestor-contains the branch tip). The ff-merge
		// above should guarantee this; if it somehow does not (near-impossible
		// guard), surface a needs-merge BLOCK rather than declaring the goal Done
		// over an un-integrated commit. errMergeConflict routes finalize to the
		// BLOCK path; didIntegrate stays false.
		if !baseContainsCommit(ctx, run, d.workDir, branch, baseBranch) {
			return errMergeConflict{paths: nil}
		}
		// The ff-merge landed the branch's commits into base: this goal integrated
		// real changes. Recorded BEFORE the integration gate so a post-merge gate
		// failure (which already advanced base) is still reported as integrated.
		didIntegrate = true

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
	return didIntegrate, mergeErr
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

// partitionConflicts splits conflicting paths into those that fall inside the
// goal's declared Scope (in-scope ⇒ the goal's own deliverable ⇒ BLOCK) and
// those outside it (peer churn, safe to resolve in base's favor). A path is
// in-scope iff some scope stem — scopePrefix, the literal prefix before the first
// glob metachar (scope_gate.go) — is an ancestor-or-equal of it (pathPrefix). An
// EMPTY scope cannot prove anything out-of-scope, so EVERY path is treated as
// in-scope: the conservative BLOCK that never strands a deliverable.
func partitionConflicts(scope, paths []string) (inScope, outScope []string) {
	for _, p := range paths {
		cp := normalizeSep(p)
		in := len(scope) == 0
		for _, s := range scope {
			if pathPrefix(normalizeSep(scopePrefix(s)), cp) {
				in = true
				break
			}
		}
		if in {
			inScope = append(inScope, p)
		} else {
			outScope = append(outScope, p)
		}
	}
	return inScope, outScope
}

// resolveOutOfScopeConflicts handles a rebase conflict during merge-back. Each
// round it partitions the currently-unmerged paths by goal.Scope: if ANY are
// in-scope (or the scope is empty, or the conflict set is undeterminable) it
// returns an errMergeConflict carrying the in-scope paths so the caller aborts
// the rebase and BLOCKs the goal — auto-resolving the goal's OWN deliverable in
// base's favor would strand it. If ALL conflicts are out-of-scope peer churn it
// resolves each in base's favor (under `git rebase base`, base is HEAD ⇒ --ours)
// then `git add`s it and runs `rebase --continue` with a non-interactive editor
// (-c core.editor=true) so the daemon never blocks on a commit-message prompt. A
// LATER replayed commit may conflict again, so it re-checks in a bounded loop;
// a nil return means the rebase completed cleanly (proceed to the ff-merge).
func (d *Daemon) resolveOutOfScopeConflicts(ctx context.Context, run GitRunnerFunc, wt string, goal *Goal) *errMergeConflict {
	const maxRounds = 50
	for round := 0; round < maxRounds; round++ {
		paths := unmergedPaths(ctx, run, wt)
		inScope, outScope := partitionConflicts(goal.Scope, paths)
		// In-scope conflict, empty scope (everything in-scope), or an
		// undeterminable empty conflict set ⇒ cannot safely auto-resolve ⇒ BLOCK.
		if len(inScope) > 0 || len(outScope) == 0 {
			return &errMergeConflict{paths: inScope}
		}
		// All conflicts are out-of-scope peer churn: take base's version of each.
		for _, p := range outScope {
			_, _, _, _ = run(ctx, "-C", wt, "checkout", "--ours", "--", p)
			_, _, _, _ = run(ctx, "-C", wt, "add", "--", p)
		}
		_, _, code, err := run(ctx, "-C", wt, "-c", "core.editor=true", "rebase", "--continue")
		if err == nil && code == 0 {
			return nil // rebase completed — no remaining conflicts
		}
		// A later replayed commit conflicted again — re-partition next round.
	}
	return &errMergeConflict{paths: []string{"merge-back conflict-resolution exceeded bounded rounds"}}
}

// baseContainsCommit reports whether base PROVABLY contains the goal's commit:
// `git merge-base --is-ancestor <branch> <base>` exits 0 iff the branch tip is an
// ancestor-or-equal of base. It gates the GoalDone/didIntegrate decision so a
// goal is never declared Done over an un-integrated commit. A runner error /
// non-zero exit ⇒ false (cannot prove containment ⇒ caller BLOCKs).
func baseContainsCommit(ctx context.Context, run GitRunnerFunc, workDir, branch, base string) bool {
	_, _, code, err := run(ctx, "-C", workDir, "merge-base", "--is-ancestor", branch, base)
	return err == nil && code == 0
}

// refreshBase best-effort fetches origin before a merge-back so the rebase lands
// onto the freshest base. Gated on d.gitFreshness (yaml taskvisor.git_freshness,
// default ON; daemon.go) — when disabled it is a zero-git no-op. Warn-only by
// contract: a fetch failure never alters goal status or the always-on rebase.
func (d *Daemon) refreshBase(ctx context.Context, run GitRunnerFunc) {
	if !d.gitFreshness {
		return
	}
	if _, se, code, err := run(ctx, "-C", d.workDir, "fetch"); err != nil {
		log.Printf("warning: merge-back base refresh fetch: %v", err)
	} else if code != 0 {
		log.Printf("warning: merge-back base refresh fetch (exit %d): %s", code, strings.TrimSpace(se))
	}
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
// merges the goal's worktree back into base and removes it. A post-merge
// integration failure flips the goal done→failed (base already advanced). A
// merge-back rebase CONFLICT is integrate-or-BLOCK: mergeWorktreeBack already
// auto-resolved any OUT-of-scope conflict in base's favor, so a surfaced
// errMergeConflict means an IN-scope (or undeterminable) conflict touched the
// goal's own deliverable — neither Done nor Failed is correct. The goal is set
// GoalBlocked / BlockedBy="needs-merge", its worktree/branch are PRESERVED, a
// needs-merge marker is written, dependents are halted (soft cascade + the
// GoalBlocked status), and it returns failed=true to suppress resumeDownstream —
// all WITHOUT a VerdictFail signal or [TASKVISOR:GOAL-FAILED notify (no false
// critical task). With no worktree (MaxGoals=1) both inner calls are zero-git
// no-ops and it returns failed=false.
func (d *Daemon) finalizeWorktreeOnDone(goals *GoalsFile, goal *Goal) (failed bool, err error) {
	integrated, mergeErr := d.mergeWorktreeBack(goal)
	if mergeErr == nil {
		// Done-without-integration invariant (worktree mode): a goal with a
		// non-empty declared Scope whose merge-back landed ZERO commits into base
		// produced no integrated work — surface it failed rather than leaving it
		// silently GoalDone. Gated on goalUsesWorktree so an inline goal's
		// integrated=false never trips this branch (its check lives at the
		// autoCommit done-sites in the state machine). Empty-scope / genuine no-op
		// goals are unaffected — no false positive.
		if d.goalUsesWorktree(goal) && len(goal.Scope) > 0 && !integrated {
			if ferr := d.failZeroIntegration(goals, goal); ferr != nil {
				return false, ferr
			}
			_ = d.discardWorktree(goal)
			return true, nil
		}
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

	// Post-validation merge-back conflict on an IN-scope path (or an
	// undeterminable/empty scope): the goal validated and committed, but base
	// cannot receive its work without clobbering the goal's OWN deliverable (or a
	// peer's). Integrate-or-BLOCK: do NEITHER Done NOR Failed — set
	// Status=GoalBlocked / BlockedBy="needs-merge" (the existing GoalBlocked
	// machinery, precedent BlockedBy="deps_unsatisfied" in completion.go; invent no
	// new status), persist a needs-merge marker, and PRESERVE the worktree/branch
	// so the deliverable's commit survives for a manual merge (no discardWorktree →
	// no `branch -D` of the goal's commit). Write NO VerdictFail signal and emit NO
	// [TASKVISOR:GOAL-FAILED notify (reportFailedGoals keys solely on GoalFailed, so
	// a Blocked goal files no false critical task — the task-163 anti-false-critical
	// intent is preserved); instead emit a NON-critical GOAL-NEEDS-MERGE notify.
	// Soft-cascade dependents (non-fail class ⇒ no Status flip, just BlockedBy for
	// dashboard clarity) and return failed=true so advanceToNextGoal suppresses
	// resumeDownstream; the parent ending GoalBlocked (not Done) already halts the
	// dependency gate. SaveGoals is REQUIRED here — the Done→Blocked status change
	// must be durable (the old keep-Done path skipped it because status was
	// unchanged).
	rt := d.runtime(goal.ID)
	wtPath, branch := rt.WorktreeDir, rt.Branch
	base := d.baseBranch(context.Background(), d.gitRunner())
	log.Printf("%s: worktree merge-back conflict on %v — work NOT integrated; goal BLOCKED (needs-merge); base unchanged, worktree %s preserved for manual merge",
		goal.ID, mc.paths, wtPath)
	goal.Status = GoalBlocked
	goal.BlockedBy = "needs-merge"
	if mErr := d.writeNeedsMergeMarker(goal.ID, mc.paths, wtPath, branch, base); mErr != nil {
		log.Printf("warning: %s: write needs-merge marker: %v", goal.ID, mErr)
	}
	// Soft cascade: "needs-merge" is neither "fail" nor "code-defect", so
	// CascadeFailure leaves dependents GoalPending and only stamps BlockedBy=goal.ID
	// (never marks them failed).
	goals.CascadeFailure(goal.ID, "needs-merge")
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-NEEDS-MERGE id=%s desc=%q paths=%q]",
		goal.ID, goal.Description, strings.Join(mc.paths, ", ")))
	if saveErr := SaveGoals(d.workDir, goals); saveErr != nil {
		return false, saveErr
	}
	// Do NOT discardWorktree — the worktree/branch hold the un-integrated commit.
	return true, nil
}

// goalUsesWorktree reports whether the goal is running in an isolated per-goal
// git worktree (MaxGoals>1, git repo) rather than inline in the base tree
// (MaxGoals=1 / non-git). It mirrors mergeWorktreeBack's WorktreeDir guard so
// the done-without-integration invariant routes each goal to exactly ONE check:
// worktree-mode goals key on the merge result (integrated), inline-mode goals
// key on whether autoCommitGoal committed. The two are mutually exclusive.
func (d *Daemon) goalUsesWorktree(goal *Goal) bool {
	rt := d.runtime(goal.ID)
	return rt.WorktreeDir != "" && rt.WorktreeDir != d.workDir
}

// failZeroIntegration flips a GoalDone goal whose non-empty declared Scope
// integrated zero committed changes into base to GoalFailed, mirroring the
// post-merge integration-failure surfacing (worktree.go integration-failed
// block): a VerdictFail/owner=human validator signal, FinishedAt, CascadeFailure
// of dependents, a GOAL-FAILED notify, and a durable SaveGoals. It is the single
// shared helper for BOTH modes — the worktree branch in finalizeWorktreeOnDone
// and the inline branches at the state-machine done-sites — so the surfacing,
// cascade, and reporting behavior can never drift between them. Returns the
// Signal/SaveGoals error (warn-mirror of the existing block).
func (d *Daemon) failZeroIntegration(goals *GoalsFile, goal *Goal) error {
	log.Printf("%s: goal has a non-empty declared scope but integrated zero committed changes — failing goal (no integrated changes)", goal.ID)
	nextAction := "no integrated changes: goal has a non-empty declared scope but integrated zero committed changes into base; the worker's output was not committed/landed — fix and re-run."
	if sigErr := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
		Verdict:    VerdictFail,
		Owner:      "human",
		NextAction: nextAction,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}); sigErr != nil {
		return sigErr
	}
	goal.Status = GoalFailed
	goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	goals.CascadeFailure(goal.ID, "fail")
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:GOAL-FAILED id=%s desc=%q reason=no-integrated-changes cascade=%d]",
		goal.ID, goal.Description, countCascaded(goals, goal.ID)))
	return SaveGoals(d.workDir, goals)
}

// writeNeedsMergeMarker persists a .tmux-cli/goals/<id>/needs-merge.md marker
// recording an IN-scope worktree merge-back conflict so the deliverable can be
// merged by hand. The goal validated and committed, but the work is NOT yet
// integrated — the goal is GoalBlocked / needs-merge, not Done. The marker
// carries the conflicting paths, the preserved worktree path + branch, and a
// manual-merge runbook. It is best-effort: a write error is warn-only at the call
// site and never changes the goal's status. The filename + the `needs-merge`
// token below also satisfy the validate grep rule.
func (d *Daemon) writeNeedsMergeMarker(goalID string, paths []string, wtPath, branch, base string) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		return err
	}
	pathsLine := "(conflicting paths unavailable)"
	if len(paths) > 0 {
		pathsLine = strings.Join(paths, ", ")
	}
	content := fmt.Sprintf(`# needs-merge: %s

This goal is BLOCKED with BlockedBy="needs-merge": it PASSED validation and its
commit landed in worktree branch %q, but the automatic merge-back rebase onto
base conflicted on an in-scope path. The work is NOT yet integrated — base was
left unchanged. Resolve the merge by hand, then re-run so the deliverable lands.

- Goal: %s
- Conflicting paths: %s
- Worktree: %s
- Branch: %s
- Base branch: %s

## Manual-merge runbook

    cd %s && git rebase %s
    # resolve the conflicts, then:
    #   git add <resolved paths> && git rebase --continue
    git -C %s merge --ff-only %s
`, goalID, branch, goalID, pathsLine, wtPath, branch, base, wtPath, base, d.workDir, branch)
	return os.WriteFile(filepath.Join(goalDir, "needs-merge.md"), []byte(content), 0o644)
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

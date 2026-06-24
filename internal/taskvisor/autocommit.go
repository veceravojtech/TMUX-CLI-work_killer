package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// autocommit.go — completion-time auto-commit (goal-009). When a goal
// transitions to done, the daemon commits that goal's scope-matched changeset
// to the currently checked-out branch, giving every resolved goal its own
// commit boundary so consecutive goals' changesets never accumulate into one
// tangled working tree. Gated behind taskvisor.auto_commit (default ON).
//
// Contract: warn-only. The goal IS done (SaveGoals already persisted it), so a
// failure here must never alter goal status, burn retries, or block teardown —
// every error path logs a warning and returns. The step never pushes and never
// creates/switches branches. Staging follows a three-tier fallback: the goal's
// scope pathspecs, else the files named in the goal's completion-report section,
// else — when both are empty — the whole working tree (`git add -A`, mirroring
// mergeWorktreeBack) gated on a non-empty UNSCOPED porcelain so a clean tree
// still skips. When scope IS present, only in-scope paths are ever staged.

// backendTaskRe extracts the backend task number from an acceptance entry like
// "Backend task 45 is satisfied: ...".
var backendTaskRe = regexp.MustCompile(`Backend task (\d+)`)

// backtickTokenRe captures `quoted` path tokens in a completion-report section.
var backtickTokenRe = regexp.MustCompile("`([^`]+)`")

// autoCommitGoal stages the goal's scope-matched dirty paths and commits them
// on the current branch (plain `git commit` — the branch is whatever
// rev-parse --abbrev-ref HEAD would say, never an argument). An empty
// scope-matched diff or no derivable pathspecs is a silent skip.
//
// Returns committed=true ONLY at the two commit-success returns (whole-tree
// fallback and scope-matched), and false at every other return (auto-commit
// disabled, clean tree, "no in-scope changes", any git error). The done-sites
// use this to enforce the done-without-integration invariant for INLINE goals:
// a non-empty-scope goal that committed nothing must not stay done. The result
// is meaningful only in inline mode — in worktree mode autoCommitGoal runs
// against the CLEAN base workDir (the edits live in the worktree), so it always
// returns false; callers gate the inline check on !goalUsesWorktree.
func (d *Daemon) autoCommitGoal(g *Goal) (committed bool) {
	if !d.autoCommit {
		return false
	}
	pathspecs := scopePathspecs(g.Scope)
	if len(pathspecs) == 0 {
		pathspecs = completionReportFiles(d.workDir, g.ID)
	}
	if len(pathspecs) == 0 {
		// Third fallback tier (reached only when scope AND completion-report
		// files are both empty): stage the whole working tree, mirroring
		// mergeWorktreeBack's add -A (worktree.go:373). In serial mode goals run
		// one at a time, so at goal-done the dirty tree IS this goal's output.
		// The probe is UNSCOPED (no `--`/pathspecs) and gates the commit: a clean
		// tree skips rather than making an empty commit.
		out, stderr, code, err := d.autoCommitGit("-C", d.workDir, "status", "--porcelain")
		if code != 0 || err != nil {
			log.Printf("warning: auto-commit %s: git status failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
			return false
		}
		if strings.TrimSpace(out) == "" {
			log.Printf("%s: auto-commit skipped — clean working tree", g.ID)
			return false
		}

		_, stderr, code, err = d.autoCommitGit("-C", d.workDir, "add", "-A")
		if code != 0 || err != nil {
			log.Printf("warning: auto-commit %s: git add failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
			return false
		}

		msg := goalCommitMessage(g)
		_, stderr, code, err = d.autoCommitGit("-C", d.workDir, "commit", "-m", msg)
		if code != 0 || err != nil {
			log.Printf("warning: auto-commit %s: git commit failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
			return false
		}
		log.Printf("%s: auto-committed whole working tree to the current branch (%q)", g.ID, msg)
		return true
	}

	// status --porcelain -- <pathspecs> covers untracked (??) files too, so new
	// in-scope files count as a dirty diff and get staged by add below.
	out, stderr, code, err := d.autoCommitGit(append([]string{"-C", d.workDir, "status", "--porcelain", "--"}, pathspecs...)...)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git status failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return false
	}
	if strings.TrimSpace(out) == "" {
		log.Printf("%s: auto-commit skipped — no in-scope changes", g.ID)
		return false
	}

	_, stderr, code, err = d.autoCommitGit(append([]string{"-C", d.workDir, "add", "--"}, pathspecs...)...)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git add failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return false
	}

	// No --no-verify: operator pre-commit hooks keep their say; a hook
	// rejection is just a warn-only skip like any other non-zero exit.
	msg := goalCommitMessage(g)
	_, stderr, code, err = d.autoCommitGit("-C", d.workDir, "commit", "-m", msg)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git commit failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return false
	}
	log.Printf("%s: auto-committed scope-matched changes to the current branch (%q)", g.ID, msg)
	return true
}

// autoCommitGit runs one git invocation through the injectable runner seam
// under a fresh gitTimeout deadline (mirrors the worktree call idiom).
func (d *Daemon) autoCommitGit(args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	return d.gitRunner()(ctx, args...)
}

// scopePathspecs converts the goal's scope globs into git pathspecs with the
// :(glob) magic prefix, so "internal/taskvisor/**" matches across directories
// and never widens beyond the declared footprint. Empty input ⇒ nil.
func scopePathspecs(scope []string) []string {
	var specs []string
	for _, s := range scope {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		specs = append(specs, ":(glob)"+s)
	}
	return specs
}

// goalCommitMessage renders the descriptive git commit subject for a resolved
// goal — `<id>: <description> (backend task N)`. It is the SINGLE source of
// truth for both commit paths: the serial completion auto-commit (autoCommitGoal
// here) and the parallel worktree merge (mergeWorktreeBack in worktree.go) both
// call it, so the two can never drift on message format.
func goalCommitMessage(g *Goal) string {
	return fmt.Sprintf("%s: %s%s", g.ID, g.Description, backendTaskSuffix(g.Acceptance))
}

// backendTaskSuffix derives the " (backend task N)" commit-message suffix from
// the first acceptance entry matching `Backend task (\d+)`; no match ⇒ "".
func backendTaskSuffix(acceptance []string) string {
	for _, a := range acceptance {
		if m := backendTaskRe.FindStringSubmatch(a); m != nil {
			return " (backend task " + m[1] + ")"
		}
	}
	return ""
}

// completionReportFiles is the scope-absent fallback: it returns the
// backtick-quoted paths named in this goal's "### <goalID>:" section of the
// global completion report (.tmux-cli/goals/completion-report.md) that exist on
// disk relative to workDir. The section spans until the next "### " heading or
// EOF; the heading line itself is not scanned (descriptions may carry
// backticks). A missing named path is dropped with a log line; an absent
// report or section ⇒ nil, which the caller treats as a silent skip.
func completionReportFiles(workDir, goalID string) []string {
	data, err := os.ReadFile(filepath.Join(workDir, ".tmux-cli", "goals", "completion-report.md"))
	if err != nil {
		return nil
	}
	var files []string
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "### ") {
			inSection = strings.HasPrefix(line, "### "+goalID+":")
			continue
		}
		if !inSection {
			continue
		}
		for _, m := range backtickTokenRe.FindAllStringSubmatch(line, -1) {
			p := strings.TrimSpace(m[1])
			if p == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(workDir, p)); err != nil {
				log.Printf("%s: auto-commit fallback dropped missing path %q", goalID, p)
				continue
			}
			files = append(files, p)
		}
	}
	return files
}

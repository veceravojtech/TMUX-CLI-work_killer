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
// every error path logs a warning and returns. The step never pushes, never
// creates/switches branches, and never stages paths outside the goal's scope:
// an empty scope falls back to the files named in the goal's section of the
// completion report, or skips silently — it NEVER means "stage everything".

// backendTaskRe extracts the backend task number from an acceptance entry like
// "Backend task 45 is satisfied: ...".
var backendTaskRe = regexp.MustCompile(`Backend task (\d+)`)

// backtickTokenRe captures `quoted` path tokens in a completion-report section.
var backtickTokenRe = regexp.MustCompile("`([^`]+)`")

// autoCommitGoal stages the goal's scope-matched dirty paths and commits them
// on the current branch (plain `git commit` — the branch is whatever
// rev-parse --abbrev-ref HEAD would say, never an argument). An empty
// scope-matched diff or no derivable pathspecs is a silent skip.
func (d *Daemon) autoCommitGoal(g *Goal) {
	if !d.autoCommit {
		return
	}
	pathspecs := scopePathspecs(g.Scope)
	if len(pathspecs) == 0 {
		pathspecs = completionReportFiles(d.workDir, g.ID)
	}
	if len(pathspecs) == 0 {
		log.Printf("%s: auto-commit skipped — no scope and no completion-report files", g.ID)
		return
	}

	// status --porcelain -- <pathspecs> covers untracked (??) files too, so new
	// in-scope files count as a dirty diff and get staged by add below.
	out, stderr, code, err := d.autoCommitGit(append([]string{"-C", d.workDir, "status", "--porcelain", "--"}, pathspecs...)...)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git status failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return
	}
	if strings.TrimSpace(out) == "" {
		log.Printf("%s: auto-commit skipped — no in-scope changes", g.ID)
		return
	}

	_, stderr, code, err = d.autoCommitGit(append([]string{"-C", d.workDir, "add", "--"}, pathspecs...)...)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git add failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return
	}

	// No --no-verify: operator pre-commit hooks keep their say; a hook
	// rejection is just a warn-only skip like any other non-zero exit.
	msg := fmt.Sprintf("%s: %s%s", g.ID, g.Description, backendTaskSuffix(g.Acceptance))
	_, stderr, code, err = d.autoCommitGit("-C", d.workDir, "commit", "-m", msg)
	if code != 0 || err != nil {
		log.Printf("warning: auto-commit %s: git commit failed (exit %d, err %v): %s", g.ID, code, err, strings.TrimSpace(stderr))
		return
	}
	log.Printf("%s: auto-committed scope-matched changes to the current branch (%q)", g.ID, msg)
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

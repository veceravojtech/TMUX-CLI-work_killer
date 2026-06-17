package taskvisor

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitfresh.go — git-freshness preflight (goal-005). The daemon dispatches goals
// and the consume flow claims backend tasks against whatever the local checkout
// happens to be. A checkout sitting on a stale base (never pulled origin's
// already-deployed commit) produces work built on a dead base — failing tests
// and near-miss prod incidents. This preflight fetches origin, compares HEAD to
// the configured upstream, fast-forwards a strictly-behind checkout, proceeds
// when in sync (or ahead-only), and refuses — dispatching/claiming nothing —
// when diverged or the fetch fails, naming the project and the ahead/behind
// counts. It is wired at BOTH entry points that turn a task into running work:
// the daemon goal-dispatch path (gitFreshnessGate) and the MCP task-claim path.
//
// Gated behind taskvisor.git_freshness (default ON). Reuses the GitRunnerFunc
// seam (worktree.go) + gitTimeout, so it is unit-testable via an injected fake
// runner with no real repo. Ahead-only MUST proceed — the daemon's own
// auto-commit/auto-push routinely leaves the branch ahead of origin, so refusing
// non-ff would self-deadlock dispatch; refusal is reserved STRICTLY for
// both-sided divergence or a failed fetch. A missing upstream / non-repo /
// detached HEAD SKIPs (proceeds), never refuses.

// FreshnessAction is the outcome of a freshness preflight.
type FreshnessAction string

const (
	// FreshnessSkipped — no upstream / not a repo / detached: the gate could not
	// evaluate, so it proceeds without refusing.
	FreshnessSkipped FreshnessAction = "skipped"
	// FreshnessInSync — HEAD == upstream (0 ahead, 0 behind): proceed unchanged.
	FreshnessInSync FreshnessAction = "in_sync"
	// FreshnessFastForward — strictly behind: `git pull --ff-only` ran; proceed.
	FreshnessFastForward FreshnessAction = "fast_forward"
	// FreshnessAhead — ahead only (the normal post-commit state): proceed.
	FreshnessAhead FreshnessAction = "ahead"
)

// PreflightGitFreshness verifies the checkout at workDir is in sync with its
// configured upstream before work starts. A nil runner defaults to the real git
// binary (defaultGitRunner). Steps: (1) resolve the upstream symref — absent ⇒
// FreshnessSkipped; (2) fetch the upstream's remote — failure ⇒ refuse; (3)
// count ahead/behind via `rev-list --left-right --count`; (4) decide per the
// matrix — behind ⇒ fast-forward, diverged ⇒ refuse, ahead-only ⇒ proceed,
// in-sync ⇒ proceed. Every git call passes its own "-C workDir" so the runner
// stays cwd-independent.
func PreflightGitFreshness(ctx context.Context, runner GitRunnerFunc, workDir, projectName string) (FreshnessAction, error) {
	if runner == nil {
		runner = defaultGitRunner
	}

	// (1) Resolve the configured tracking branch. No upstream / not a repo /
	// detached HEAD ⇒ rev-parse exits non-zero or errors ⇒ SKIP (never refuse).
	out, _, code, err := runner(ctx, "-C", workDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil || code != 0 {
		return FreshnessSkipped, nil
	}
	upstream := strings.TrimSpace(out)
	if upstream == "" {
		return FreshnessSkipped, nil
	}

	// (2) Fetch the upstream's remote (the part before the first '/', e.g.
	// "origin" of "origin/master"). The explicit fetch is intentional even though
	// `pull --ff-only` fetches again: rev-list must see fresh remote refs.
	remote := upstream
	if i := strings.Index(upstream, "/"); i >= 0 {
		remote = upstream[:i]
	}
	_, stderr, code, err := runner(ctx, "-C", workDir, "fetch", remote)
	if err != nil || code != 0 {
		return "", fmt.Errorf("refusing: %s git fetch %s failed (exit %d): %v %s",
			projectName, remote, code, err, strings.TrimSpace(stderr))
	}

	// (3) Count ahead/behind in one command: `rev-list --left-right --count
	// HEAD...<upstream>` prints "<ahead>\t<behind>" (left = commits on HEAD not
	// upstream, right = commits on upstream not HEAD).
	out, stderr, code, err = runner(ctx, "-C", workDir, "rev-list", "--left-right", "--count", "HEAD..."+upstream)
	if err != nil || code != 0 {
		return "", fmt.Errorf("refusing: %s git rev-list failed (exit %d): %v %s",
			projectName, code, err, strings.TrimSpace(stderr))
	}
	ahead, behind, perr := parseAheadBehind(out)
	if perr != nil {
		return "", fmt.Errorf("refusing: %s %v", projectName, perr)
	}

	// (4) Decide per the matrix.
	switch {
	case ahead > 0 && behind > 0:
		// Both-sided divergence — the ONLY non-fetch refusal. The error string
		// literally contains "diverged from origin" (validate greps cmd internal).
		return "", fmt.Errorf("refusing: %s local diverged from origin (%d ahead, %d behind) — reconcile before running",
			projectName, ahead, behind)
	case behind > 0:
		// Strictly behind (ahead==0) — fast-forward. A non-zero pull means the
		// fast-forward was rejected (treat as non-ff) ⇒ refuse.
		_, stderr, code, err := runner(ctx, "-C", workDir, "pull", "--ff-only")
		if err != nil || code != 0 {
			return "", fmt.Errorf("refusing: %s git pull --ff-only failed (exit %d): %v %s",
				projectName, code, err, strings.TrimSpace(stderr))
		}
		return FreshnessFastForward, nil
	case ahead > 0:
		// Ahead only — the normal post-commit/pre-push state. Proceed.
		return FreshnessAhead, nil
	default:
		return FreshnessInSync, nil
	}
}

// parseAheadBehind parses `git rev-list --left-right --count` output
// ("<ahead>\t<behind>") into the two integers. Whitespace-tolerant.
func parseAheadBehind(s string) (ahead, behind int, err error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", strings.TrimSpace(s))
	}
	if ahead, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("parse ahead count %q: %w", fields[0], err)
	}
	if behind, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("parse behind count %q: %w", fields[1], err)
	}
	return ahead, behind, nil
}

// gitFreshnessGate is the daemon-side dispatch wrapper around
// PreflightGitFreshness. It is a no-op when the setting is off (the zero-value
// field stays false in direct-construct dispatch tests, so the runner is never
// touched). On a refusal it mirrors the precondition-block disposition: save a
// blocked ValidatorSignal (owner=ops, remedy names the reconcile action), log an
// owner-facing line, mark the goal GoalBlocked, and return nil so the block is
// NOT miscounted as a dispatch crash. The auto-resume flag is deliberately left
// UNSET — a diverged checkout needs operator reconciliation, and looping
// auto-resume would hammer.
func (d *Daemon) gitFreshnessGate(goal *Goal, goals *GoalsFile) error {
	if !d.gitFreshness {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	action, err := PreflightGitFreshness(ctx, d.gitRunner(), d.workDir, filepath.Base(d.workDir))
	if err != nil {
		sig := &ValidatorSignal{
			Verdict: "blocked",
			Class:   "git-freshness",
			Owner:   "ops",
			Remedy:  "reconcile the local checkout with origin (git pull --ff-only or rebase onto origin), then re-pend the goal",
			Findings: []ValidationFinding{{
				Rule:   "git-freshness",
				Status: "blocked",
				Detail: err.Error(),
			}},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if serr := SaveValidatorSignal(d.workDir, goal.ID, sig); serr != nil {
			return fmt.Errorf("save git-freshness block signal: %w", serr)
		}
		log.Printf("[BLOCKED - OPERATOR ACTION REQUIRED] %s: %v", goal.ID, err)
		goal.Status = GoalBlocked
		if serr := SaveGoals(d.workDir, goals); serr != nil {
			return serr
		}
		return nil
	}
	if action == FreshnessFastForward {
		log.Printf("%s: git-freshness fast-forwarded local checkout to upstream", goal.ID)
	}
	return nil
}

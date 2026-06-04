package taskvisor

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// OwnSuiteScope is the selector contract mapping a goal's declared deliverables
// to the integration+functional phpunit scope that exercises them. It is
// render-ready for a goal.md `## Investigation Config` investigator: .Paths maps
// 1:1 to Investigator.Paths and .Command to Investigator.Commands (consumed by
// the gate investigator / WriteGoalMD wiring — not wired here).
//
// Empty==true is the sentinel meaning "no own-suite gate applies": the gate
// SKIPS rather than fails (a Domain-only goal with no integration/functional
// suite must not be reported falsely red).
type OwnSuiteScope struct {
	BoundedContexts []string // sorted, distinct, e.g. ["Catalog","Order"]
	Paths           []string // sorted existing test dirs (positional phpunit args)
	Command         string   // "vendor/bin/phpunit <paths...>" or "" when Empty
	Empty           bool     // true => no own-suite gate applies (gate skips)
}

// srcTokenRe extracts `src/<seg>[/<seg>...]` path tokens from arbitrary text.
// A leading \b prevents matching substrings such as "mysrc/...". Segment chars
// are limited to path-safe runes so surrounding backticks/quotes/whitespace
// terminate the match.
var srcTokenRe = regexp.MustCompile(`\bsrc(?:/[A-Za-z0-9_.\-]+)+`)

// shareKernel is the only bounded-context name skipped: src/Share is the shared
// kernel and owns no per-BC integration/functional suite.
const shareKernel = "Share"

// extractSrcTokens returns the sorted, distinct set of src/ path tokens found
// across the given lines (each line may be a full sentence/command, not a bare
// path). Backticks and trailing punctuation are stripped so tokens are stable.
func extractSrcTokens(lines []string) []string {
	set := make(map[string]struct{})
	for _, line := range lines {
		for _, m := range srcTokenRe.FindAllString(line, -1) {
			tok := strings.TrimRight(m, ".,;:")
			if tok != "" {
				set[tok] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for tok := range set {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// DeliverablesFromGoal extracts the distinct src/ path tokens from a goal's
// Acceptance + Validate fields (its persisted deliverable footprint). It is a
// pure read of read-only input — no Goal field is added or mutated. Returns a
// sorted, deduped slice; empty when the goal declares no src/ paths.
func DeliverablesFromGoal(g Goal) []string {
	lines := make([]string, 0, len(g.Acceptance)+len(g.Validate))
	lines = append(lines, g.Acceptance...)
	lines = append(lines, g.Validate...)
	return extractSrcTokens(lines)
}

// SelectOwnSuiteScope is a pure, deterministic function of (deliverable strings,
// filesystem root). It derives each deliverable's bounded context from the first
// segment after src/, maps to candidate suites tests/Integration/<BC> and
// tests/Functional/<BC>, keeps the ones that exist as directories under fsRoot,
// and emits a single phpunit invocation over those directory paths.
//
// Directory positional args (never --filter, never a \Domain class regex) keep
// the process exit code honest: a red suite exits non-zero, which the gate reads
// directly with no exit-code parsing. fsRoot is the goal's worktree/project root
// supplied by the caller so existence checks resolve in the goal's worktree.
func SelectOwnSuiteScope(deliverables []string, fsRoot string) OwnSuiteScope {
	// (1)+(2) extract src tokens and derive distinct bounded contexts.
	bcSet := make(map[string]struct{})
	for _, tok := range extractSrcTokens(deliverables) {
		parts := strings.Split(tok, "/")
		if len(parts) < 2 {
			continue
		}
		bc := parts[1] // segment after "src/"
		if bc == "" || bc == shareKernel {
			continue
		}
		bcSet[bc] = struct{}{}
	}

	// (3) sort bounded contexts.
	bcs := make([]string, 0, len(bcSet))
	for bc := range bcSet {
		bcs = append(bcs, bc)
	}
	sort.Strings(bcs)

	// (4) per-BC candidate suites; keep those that exist as directories.
	var paths []string
	for _, bc := range bcs {
		for _, suite := range []string{"Integration", "Functional"} {
			rel := "tests/" + suite + "/" + bc
			info, err := os.Stat(filepath.Join(fsRoot, filepath.FromSlash(rel)))
			if err == nil && info.IsDir() {
				paths = append(paths, rel)
			}
		}
	}

	// (5) sort kept paths for stable, deduped output.
	sort.Strings(paths)

	// (6) derive Empty + Command.
	scope := OwnSuiteScope{
		BoundedContexts: bcs,
		Paths:           paths,
		Empty:           len(paths) == 0,
	}
	if !scope.Empty {
		scope.Command = "vendor/bin/phpunit " + strings.Join(paths, " ")
	}
	return scope
}

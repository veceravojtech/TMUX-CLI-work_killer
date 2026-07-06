package taskvisor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Disjoint-scope co-scheduling gate (E1-0f). A conservative stand-in for
// per-goal worktree isolation (execute-33): until each goal runs in its own
// working tree, MaxGoals>1 must never co-schedule two goals that edit
// overlapping files in the SAME tree. The gate admits a second concurrent goal
// only when its declared Scope provably does NOT overlap every in-flight and
// already-admitted goal; overlap OR unknown scope ⇒ serialize.
//
// Every function here is PURE and lock-free — no I/O, no mutation of goal state.
// Callers (the scheduler tick) already hold the poll flock, the same contract as
// RunnableCandidates/ReconcileBlocks. The gate composes ON TOP of
// RunnableCandidates (it never widens or modifies that ready-set query).

// HasKnownScope reports whether the goal declares a known file scope. An empty
// Scope is UNKNOWN — the conservative case that serializes against everything.
func (g *Goal) HasKnownScope() bool { return len(g.Scope) > 0 }

// ScopesDisjoint reports whether two goals can safely run concurrently. It is
// true iff BOTH goals have a known scope AND no pattern pair overlaps. If EITHER
// side is unknown (empty Scope) it returns false — the linchpin of the
// conservative contract (unknown ⇒ assume collision ⇒ serialize).
func ScopesDisjoint(a, b *Goal) bool {
	if !a.HasKnownScope() || !b.HasKnownScope() {
		return false
	}
	for _, pa := range a.Scope {
		for _, pb := range b.Scope {
			if globsOverlap(pa, pb) {
				return false
			}
		}
	}
	return true
}

// globsOverlap reports whether two scope patterns touch the same path subtree.
// It is a path-BOUNDARY prefix test, NOT a substring test: each pattern is
// reduced to its literal stem before the first wildcard (scopePrefix), and two
// stems overlap iff one is an ancestor-or-equal of the other at a path boundary.
// So `internal/x` and `internal/xy` are DISJOINT (sibling dirs sharing a string
// prefix), while `internal/x/**` and `internal/x/y.go` OVERLAP. Both "/" and the
// PHP namespace separator "\" are treated as path boundaries.
func globsOverlap(a, b string) bool {
	pa := normalizeSep(scopePrefix(a))
	pb := normalizeSep(scopePrefix(b))
	return pathPrefix(pa, pb) || pathPrefix(pb, pa)
}

// scopePrefix reduces a scope pattern to its literal prefix before the first
// glob metacharacter (`*`, `?`, `[`). "internal/x/**" -> "internal/x";
// "path/to/file.go" -> "path/to/file.go"; `App\Billing` -> `App\Billing`. A
// trailing slash is trimmed so the stem is a clean path/namespace segment.
func scopePrefix(pattern string) string {
	if i := strings.IndexAny(pattern, "*?["); i >= 0 {
		pattern = pattern[:i]
	}
	return strings.TrimRight(pattern, "/")
}

// normalizeSep folds the PHP namespace separator "\" onto "/" so namespace
// prefixes (`App\Billing`) and file globs (`internal/x`) share one boundary
// rule in pathPrefix.
func normalizeSep(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// pathPrefix reports whether p is an ancestor-or-equal of q at a path boundary.
// An empty stem is the root — ancestor of everything — so it overlaps all
// (conservative bias for a degenerate "**"/"/" pattern).
func pathPrefix(p, q string) bool {
	if p == "" {
		return true
	}
	return p == q || strings.HasPrefix(q, strings.TrimRight(p, "/")+"/")
}

// stackMarkers is the conservative substring set for runtime-resource
// classification. A goal whose Validate or Acceptance lines contain any of
// these substrings is "stack-consuming" — it touches the shared docker compose
// stack, database, or host ports. Two stack-consuming goals must never be
// co-scheduled because concurrent fixture loads corrupt each other's data.
var stackMarkers = []string{
	"ensure-test-stack",
	"npx playwright",
	"docker compose",
	"curl -sf http",
	"curl -s http",
	"curl -s -o",
}

// isStackConsuming reports whether the goal touches shared runtime resources
// (docker stack, DB, host ports). Detection is mechanical substring matching
// against stackMarkers over Validate and Acceptance lines — conservative,
// case-sensitive, pure (no I/O).
func isStackConsuming(g *Goal) bool {
	for _, line := range g.Validate {
		for _, m := range stackMarkers {
			if strings.Contains(line, m) {
				return true
			}
		}
	}
	for _, line := range g.Acceptance {
		for _, m := range stackMarkers {
			if strings.Contains(line, m) {
				return true
			}
		}
	}
	return false
}

// coSchedulable reports whether candidate c may join the in-flight set this
// tick: its scope must be disjoint from EVERY in-flight goal, AND the candidate
// and in-flight set must not both be stack-consuming. Vacuously true over an
// empty in-flight set, so the very first goal of a tick always dispatches
// regardless of scope or stack status.
func coSchedulable(c *Goal, inflight []*Goal) bool {
	for _, f := range inflight {
		if !ScopesDisjoint(c, f) {
			return false
		}
	}
	if isStackConsuming(c) {
		for _, f := range inflight {
			if isStackConsuming(f) {
				return false
			}
		}
	}
	return true
}

// inflightHasMigrates reports whether any in-flight goal mutates the shared DB
// schema (Goal.Migrates). When true, NO further goal may be co-scheduled this
// tick — a migrating goal runs alone (E1-1b), because per-goal worktrees isolate
// files but not the shared schema. See Goal.Migrates.
func inflightHasMigrates(inflight []*Goal) bool {
	for _, f := range inflight {
		if f.Migrates {
			return true
		}
	}
	return false
}

// DisjointReadySet returns the prefix of RunnableCandidates admissible THIS tick
// under the disjoint-scope gate, capped so total in-flight ≤ maxGoals. The
// in-flight set is seeded with the currently-GoalRunning goals; the dispatch
// budget is maxGoals − running. Candidates are visited in RunnableCandidates
// (goal-file) order and greedily admitted only when co-schedulable with every
// in-flight AND already-admitted goal; an admitted goal joins the in-flight set
// so later candidates are checked against it too.
//
// maxGoals ≤ 1 yields at most one goal — byte-identical to today's head
// dispatch (the first runnable candidate when nothing is running, nil when one
// is). Pure and lock-free; the caller holds the poll flock.
func (gf *GoalsFile) DisjointReadySet(maxGoals int) []*Goal {
	if maxGoals < 1 {
		maxGoals = 1
	}
	var inflight []*Goal
	running := 0
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalRunning {
			inflight = append(inflight, &gf.Goals[i])
			running++
		}
	}
	budget := maxGoals - running
	if budget <= 0 {
		return nil
	}

	var out []*Goal
	for _, c := range gf.RunnableCandidates() {
		if len(out) >= budget {
			break
		}
		// Migration exclusion (E1-1b): a Migrates goal mutates the shared DB
		// schema, which worktrees do not isolate, so it must run ALONE.
		//  - if a Migrates goal is already in flight (running OR admitted this
		//    tick), admit NOTHING more — break.
		//  - a Migrates candidate may not join a non-empty in-flight set — skip it
		//    (it stays pending until the running set empties).
		// At MaxGoals=1 budget caps out to one goal regardless, so a lone migrating
		// head dispatches exactly as today — byte-identical.
		if inflightHasMigrates(inflight) {
			break
		}
		if c.Migrates && len(inflight) > 0 {
			continue
		}
		if coSchedulable(c, inflight) {
			out = append(out, c)
			inflight = append(inflight, c)
		}
	}
	return out
}

// DeriveScopeFromDeliverables is an AUTHORING-TIME helper (planner / goal-create)
// that extracts path-like tokens — those containing "/" and not a flag — from a
// goal's Deliverable lines, deduped in first-seen order. It returns nil when no
// path token is found (no clear file footprint ⇒ UNKNOWN ⇒ the runtime
// serializes the goal). It is NEVER called in the scheduler tick: the runtime
// reads only the persisted Goal.Scope.
//
// Token hygiene (F5): a leading "./" is stripped AFTER the path-likeness check,
// so './internal/x' and 'internal/x' dedupe to ONE stem — a raw './' prefix
// never path-prefix-matches its bare twin in globsOverlap, making the two
// FALSELY DISJOINT (dangerous co-schedule). Letterless leftovers like './...'
// (→ "...") or bare '1/2' carry no file footprint and are dropped.
func DeriveScopeFromDeliverables(deliverables []string) []string {
	scope, _, _ := DeriveScopeWithCompleteness(deliverables)
	return scope
}

// DeriveScopeWithCompleteness is the AUTHORING-TIME engine behind
// DeriveScopeFromDeliverables. It runs the identical token-hygiene loop and
// additionally assesses COMPLETENESS: a derivation is INCOMPLETE when any
// non-empty deliverable line contributes zero valid path tokens. Such a line is
// recorded (verbatim, original casing/spacing) in uncovered. Authoring callers
// downgrade an incomplete auto-derived scope to UNKNOWN so the goal serializes
// rather than FALSELY passing ScopesDisjoint with a partial footprint.
//
// Coverage is PER-LINE and decided BEFORE the output dedup: a line is covered
// the moment one of its tokens passes every hygiene check, EVEN IF the global
// first-seen dedup then drops that token from the returned scope. Blank or
// whitespace-only lines are NOT criteria — they are never marked uncovered.
//
// The returned scope is byte-identical (contents, order, nil-ness) to what the
// pre-completeness DeriveScopeFromDeliverables produced; completeness is layered
// on top and never alters the slice.
func DeriveScopeWithCompleteness(deliverables []string) (scope []string, incomplete bool, uncovered []string) {
	var out []string
	seen := map[string]bool{}
	for _, line := range deliverables {
		covered := false
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimLeft(tok, "`'\"([")
			tok = strings.TrimRight(tok, "`'\").,;:]")
			if tok == "" || strings.HasPrefix(tok, "-") {
				continue
			}
			if !strings.Contains(tok, "/") {
				continue
			}
			for strings.HasPrefix(tok, "./") {
				tok = tok[2:]
			}
			if !strings.ContainsFunc(tok, unicode.IsLetter) {
				continue
			}
			// This token is a valid path footprint: the line is covered even if
			// dedup skips it from the output slice below.
			covered = true
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
		if strings.TrimSpace(line) != "" && !covered {
			uncovered = append(uncovered, line)
			incomplete = true
		}
	}
	return out, incomplete, uncovered
}

// --- one-goal-per-TDD-unit gate (task 473) -----------------------------------
//
// A single-unit TDD change (red test + green impl) keeps getting emitted as two
// chained full goals (an impl goal ← a test-only goal that depends_on it, on the
// SAME unit). detectTDDPairSplit is a deterministic authoring-time REJECT for
// that shape: for each of the candidate's depends_on edges, if the resolved dep
// is a live (pending, non-validation) goal whose scope OVERLAPS the candidate's
// and exactly ONE of the pair is test-only-scope while the other is impl-scope,
// the candidate forms a red/green split and is refused (no goal persisted). It
// mirrors the task-436 authoring gate: a pure error-returning helper invoked
// from CreateGoal inside WithGoalsLock. The escape hatch (spec.AllowSplitTDD)
// lives at the call site; the detector itself has no bypass. Fail-open on any
// ambiguity (unknown candidate or dep scope) — a hard reject must never block a
// legitimate goal whose footprint could not be classified.

// isTestOnlyScope reports whether a scope is a CONFIDENT test-only footprint:
// non-empty AND every entry targets a Go test file (ends in "_test.go", so a
// glob like "internal/x/*_test.go" or a bare "internal/x/foo_test.go" both
// qualify). An empty/unknown scope, or any impl entry mixed in, yields false —
// the conservative case that keeps detectTDDPairSplit from firing on ambiguity.
func isTestOnlyScope(scope []string) bool {
	if len(scope) == 0 {
		return false
	}
	for _, entry := range scope {
		if !strings.HasSuffix(strings.TrimSpace(entry), "_test.go") {
			return false
		}
	}
	return true
}

// detectTDDPairSplit returns a non-nil error when cand forms a red/green TDD
// split of an existing PENDING goal on the same unit, else nil. For each id in
// cand.DependsOn it resolves the dep among existing and fires ONLY on a
// confident classification: the dep is pending and NOT a sanctioned validation
// goal (IsValidationGoal), BOTH goals have a known scope whose globs overlap
// (!ScopesDisjoint over two known scopes = a real same-unit overlap), and
// exactly one of {cand, dep} is test-only while the other is impl. It is PURE
// (no I/O, no state mutation) and fails open: an unknown candidate scope, an
// unknown dep scope, a missing/non-pending/validation dep, or disjoint scopes
// all yield nil.
func detectTDDPairSplit(cand *Goal, existing []Goal) error {
	if !cand.HasKnownScope() { // fail-open: candidate footprint unclassifiable
		return nil
	}
	gf := GoalsFile{Goals: existing}
	candTestOnly := isTestOnlyScope(cand.Scope)
	for _, depID := range cand.DependsOn {
		g, ok := gf.GoalByID(depID)
		if !ok || g.Status != GoalPending || g.IsValidationGoal() {
			continue
		}
		if !g.HasKnownScope() { // fail-open: dep footprint unclassifiable
			continue
		}
		if ScopesDisjoint(cand, g) { // disjoint units — not a split
			continue
		}
		if candTestOnly != isTestOnlyScope(g.Scope) { // exactly one side is test-only
			return fmt.Errorf("goal forms a red/green TDD split of pending goal %s (same unit); emit one goal per unit with red+green as a single changeset, or pass allow_split_tdd to bypass", g.ID)
		}
	}
	return nil
}

// --- goal-create scope resolution + zero-match guard (task 436) --------------
//
// A `<stem>/**` directory glob authored for a target that is actually a FILE
// (goal-001's `.../tmux/investigate/**` — there is NO investigate/ directory,
// only investigate.xml) matches ZERO tracked files. autoCommitGoal stages only
// git `:(glob)<pathspec>`-matched paths (scopePathspecs), so a zero-match entry
// silently DROPS the worker's edit at commit while build/test stay green — a
// done-without-integration false-green. resolveScopeEntries normalizes such a
// glob to the file (or a `<stem>*` sibling glob) it was meant to cover;
// validateScopeEntries is the backstop that flags anything STILL matching
// nothing. Both run in CreateGoal, mirror autoCommitGoal's exact match
// mechanism (`git ls-files ':(glob)<entry>'`, tracked files only), and no-op
// when workDir is not a git repo so git-free authoring tests stay green. This
// is a PER-ENTRY zero-match defect, distinct from task 434's empty-overall-diff
// gate: the overall diff can be non-empty while one entry contributes zero.

// isGitRepo reports whether workDir is inside a git work tree. Used to skip the
// tree-resolution steps in git-free authoring tests (t.TempDir() with no init).
func isGitRepo(workDir string) bool {
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// gitLsFiles returns the tracked files matching `:(glob)<pathspec>` in workDir,
// mirroring scopePathspecs / autoCommitGoal exactly so the guard sees what the
// stager sees. Any git error (not a repo, bad pathspec) yields nil — callers
// treat that as "no tracked match".
func gitLsFiles(workDir, pathspec string) []string {
	out, err := exec.Command("git", "-C", workDir, "ls-files", ":(glob)"+pathspec).Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}

// resolveScopeEntries normalizes each `<stem>/**` scope entry whose stem is NOT
// a real directory but whose siblings ARE tracked, so a single-FILE target
// expressed as a directory glob resolves to the exact file (one sibling) or a
// `<stem>*` sibling glob (several) instead of matching zero files. Directory
// globs over real dirs, non-glob entries, and the pathological file-stem `/**`
// case are passed through unchanged (the last is REJECTed by
// validateScopeEntries). No-ops when workDir is not a git repo. Applies to
// explicit spec.Scope and Acceptance-derived scope alike (goal-001's bad glob
// was explicit).
func resolveScopeEntries(workDir string, scope []string) []string {
	if len(scope) == 0 || !isGitRepo(workDir) {
		return scope
	}
	out := make([]string, len(scope))
	for i, entry := range scope {
		out[i] = entry
		stem := strings.TrimSuffix(entry, "/**")
		if stem == entry { // not a `<stem>/**` directory glob — leave alone
			continue
		}
		info, err := os.Stat(filepath.Join(workDir, stem))
		switch {
		case err == nil && info.IsDir():
			// real directory — a legitimate dir glob, keep verbatim.
		case len(gitLsFiles(workDir, stem+"/**")) > 0:
			// stem is a tracked directory even if os.Stat missed it — keep.
		case err == nil && !info.IsDir():
			// stem is a FILE — a file has no children; leave verbatim so
			// validateScopeEntries REJECTs it (unambiguously wrong).
		default:
			// stem is neither dir nor file: try sibling files (goal-001 case).
			if siblings := gitLsFiles(workDir, stem+"*"); len(siblings) == 1 {
				out[i] = siblings[0] // exact single file — tightest scope
			} else if len(siblings) > 1 {
				out[i] = stem + "*" // several siblings — sibling glob
			}
			// else: no siblings — keep verbatim; validateScopeEntries warns.
		}
	}
	return out
}

// validateScopeEntries is the zero-match backstop: after resolveScopeEntries,
// any scope entry that STILL matches zero tracked files can silently drop its
// edit at autoCommitGoal. It REJECTs (returns an error) the unambiguous
// file-stem `<file>/**` case (a file cannot have children) and loud WARN-LOGs
// every other residual zero-tracked-match entry, so a no-match pathspec can
// never silently reach autoCommitGoal — while greenfield / new-file goals whose
// paths are not yet tracked keep working (warn, not fatal). No-ops when workDir
// is not a git repo.
func validateScopeEntries(workDir string, scope []string) error {
	if !isGitRepo(workDir) {
		return nil
	}
	for _, entry := range scope {
		if stem := strings.TrimSuffix(entry, "/**"); stem != entry {
			if info, err := os.Stat(filepath.Join(workDir, stem)); err == nil && !info.IsDir() {
				return fmt.Errorf("scope entry %q globs a file: %q is a tracked file, not a directory — it has no children; use the exact file pathspec instead", entry, stem)
			}
		}
		if len(gitLsFiles(workDir, entry)) == 0 {
			log.Printf("warning: goal-create scope entry %q matches zero tracked files — its edits would be silently dropped at auto-commit unless this path is created", entry)
		}
	}
	return nil
}

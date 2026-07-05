package taskvisor

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type DepFinding struct {
	Consumer string
	Producer string
	Stem     string
	Evidence string
}

// DepEdge records an enforced depends_on edge injected by EnforceFileOverlapDeps.
// From is the dependent (higher goal-id), To is the dependency/producer (lower
// goal-id), and Stem is a representative overlapping file path (for logging and
// test assertions).
type DepEdge struct {
	From string
	To   string
	Stem string
}

var knownSourceExts = []string{
	".go", ".php", ".tsx", ".ts", ".jsx", ".js", ".py", ".rs", ".java", ".kt",
}

func hasKnownExtension(tok string) bool {
	for _, ext := range knownSourceExts {
		if strings.HasSuffix(tok, ext) {
			return true
		}
	}
	return false
}

func isPathLike(tok string) bool {
	return strings.Contains(tok, "/") || hasKnownExtension(tok)
}

// isConcreteFileStem reports whether a stem names a concrete source FILE (carries
// a known source extension) rather than a COARSE DIRECTORY area (path-like, no
// extension). It is the single overlap predicate shared by InferMissingDeps and
// EnforceFileOverlapDeps: only a shared concrete-file stem is the directional
// produce/consume proxy (one goal edits a file the other references). A shared
// coarse-directory stem alone is NOT a dependency signal — sibling action goals
// of the same bounded context legitimately share a deliverable_area directory yet
// edit disjoint files, so it must never force a serializing edge or a finding.
// Keeping both consumers on this one helper guarantees the read-only finding and
// the enforced edge can never diverge.
func isConcreteFileStem(s string) bool {
	return hasKnownExtension(s)
}

// toolBinaryNames are basenames of read-only tooling that appears in validate
// commands (e.g. `vendor/bin/phpunit`, `bin/console`). These are shared tools,
// not goal-produced deliverables, so they must never enter the produced-stem set
// used for file-overlap dependency enforcement.
var toolBinaryNames = map[string]bool{
	"composer": true,
	"npx":      true,
	"phpstan":  true,
	"phpunit":  true,
	"ecs":      true,
	"deptrac":  true,
	"console":  true,
}

// isToolBinaryToken reports whether a path-like token names a read-only tool
// binary rather than a goal-produced deliverable. It matches anything under
// vendor/ (the composer-managed tool dir) and any token whose basename is a
// known tool name. Pure, lock-free; assumes any leading "./" is already stripped.
func isToolBinaryToken(s string) bool {
	if s == "vendor" || strings.HasPrefix(s, "vendor/") {
		return true
	}
	base := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		base = s[i+1:]
	}
	return toolBinaryNames[base]
}

func extractExtensionOnlyTokens(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimLeft(tok, "`'\"([")
			tok = strings.TrimRight(tok, "`'\").,;:]")
			if tok == "" || strings.HasPrefix(tok, "-") {
				continue
			}
			if strings.Contains(tok, "/") {
				continue
			}
			if !hasKnownExtension(tok) {
				continue
			}
			for strings.HasPrefix(tok, "./") {
				tok = tok[2:]
			}
			if !strings.ContainsFunc(tok, unicode.IsLetter) {
				continue
			}
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	return out
}

func extractProducerStems(g *Goal) []string {
	seen := map[string]bool{}
	var stems []string

	add := func(s string) {
		for strings.HasPrefix(s, "./") {
			s = s[2:]
		}
		if isToolBinaryToken(s) {
			return
		}
		if s == "" || !isPathLike(s) || !strings.ContainsFunc(s, unicode.IsLetter) {
			return
		}
		if !seen[s] {
			seen[s] = true
			stems = append(stems, s)
		}
	}

	for _, s := range g.Scope {
		add(s)
	}

	combined := make([]string, 0, len(g.Acceptance)+len(g.Validate))
	combined = append(combined, g.Acceptance...)
	combined = append(combined, g.Validate...)
	derived, _, _ := DeriveScopeWithCompleteness(combined)
	for _, s := range derived {
		add(s)
	}
	for _, s := range extractExtensionOnlyTokens(combined) {
		add(s)
	}

	return stems
}

func InferMissingDeps(goals *GoalsFile) []DepFinding {
	if goals == nil || len(goals.Goals) == 0 {
		return []DepFinding{}
	}

	producerMap := map[string][]string{}
	for i := range goals.Goals {
		g := &goals.Goals[i]
		for _, s := range extractProducerStems(g) {
			producerMap[s] = append(producerMap[s], g.ID)
		}
	}

	var findings []DepFinding

	for i := range goals.Goals {
		g := &goals.Goals[i]

		accStems, _, _ := DeriveScopeWithCompleteness(g.Acceptance)
		accExt := extractExtensionOnlyTokens(g.Acceptance)
		scanConsumer := func(stems []string, evidence string) {
			for _, s := range stems {
				if !isConcreteFileStem(s) {
					continue
				}
				for _, prodID := range producerMap[s] {
					if prodID == g.ID {
						continue
					}
					if !hasTransitivePath(goals, g.ID, prodID) && !hasTransitivePath(goals, prodID, g.ID) {
						findings = append(findings, DepFinding{
							Consumer: g.ID,
							Producer: prodID,
							Stem:     s,
							Evidence: evidence,
						})
					}
				}
			}
		}
		scanConsumer(accStems, "acceptance")
		scanConsumer(accExt, "acceptance")

		valStems, _, _ := DeriveScopeWithCompleteness(g.Validate)
		valExt := extractExtensionOnlyTokens(g.Validate)
		scanConsumer(valStems, "validate")
		scanConsumer(valExt, "validate")
	}

	return findings
}

// EnforceFileOverlapDeps promotes the read-only file-overlap detection into an
// enforced PRE-DISPATCH constraint: for each pair of GoalPending goals whose
// produced/edited file sets overlap, it injects a deterministic depends_on edge
// (the higher goal-id depends on the lower goal-id) so the existing
// RunnableCandidates→DependsOnSatisfied gate serializes the pair every tick.
//
// PURE and lock-free (no I/O, no SaveGoals) — the caller (activate, holding the
// goals lock) owns persistence. Idempotent: a second call adds zero edges, since
// the bidirectional hasTransitivePath guard skips any pair already ordered.
// Cycle-safe: an edge is added only when NEITHER direction already has a path.
// Only GoalPending goals are mutated. Returns the recorded edges (non-nil,
// possibly empty), in deterministic sorted-id pair order.
func EnforceFileOverlapDeps(goals *GoalsFile) []DepEdge {
	edges := []DepEdge{}
	if goals == nil || len(goals.Goals) == 0 {
		return edges
	}

	// Build the per-goal stem set for GoalPending goals only, in deterministic
	// sorted-id order so pair iteration (and thus the edge set) is stable.
	type pendingGoal struct {
		id    string
		stems []string
		set   map[string]bool
	}
	var pending []pendingGoal
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalPending {
			continue
		}
		stems := extractProducerStems(g)
		set := make(map[string]bool, len(stems))
		for _, s := range stems {
			set[s] = true
		}
		pending = append(pending, pendingGoal{id: g.ID, stems: stems, set: set})
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].id < pending[j].id })

	// pending is sorted ascending by id, so for i<j the lower-id goal is pending[i]
	// (the producer) and the higher-id goal is pending[j] (the dependent).
	for i := 0; i < len(pending); i++ {
		for j := i + 1; j < len(pending); j++ {
			lo, hi := pending[i], pending[j]

			// First shared stem: iterate the lower-id goal's stems in slice order
			// and membership-test against the higher-id goal's stem set.
			stem := ""
			for _, s := range lo.stems {
				if hi.set[s] && isConcreteFileStem(s) {
					stem = s
					break
				}
			}
			if stem == "" {
				continue
			}

			// Cycle/already-ordered guard: skip when EITHER direction already has a
			// transitive path (hi→lo means already ordered; lo→hi means the reverse
			// edge would create a cycle). Also gives idempotency for free.
			if hasTransitivePath(goals, hi.id, lo.id) || hasTransitivePath(goals, lo.id, hi.id) {
				continue
			}

			// Mutate the backing slice element (GoalByID returns *Goal into
			// goals.Goals) — append lo to hi.DependsOn, deduped.
			hg, ok := goals.GoalByID(hi.id)
			if !ok {
				continue
			}
			already := false
			for _, d := range hg.DependsOn {
				if d == lo.id {
					already = true
					break
				}
			}
			if !already {
				hg.DependsOn = append(hg.DependsOn, lo.id)
			}
			edges = append(edges, DepEdge{From: hi.id, To: lo.id, Stem: stem})
		}
	}

	return edges
}

// SerializationFinding is the read-only result of DetectOverSerialized: a
// snapshot of the pending-goal DAG shape plus a human-readable Reason naming
// each fired signal. A nil *SerializationFinding means "no warning".
type SerializationFinding struct {
	PendingCount  int
	CriticalPath  int
	MaxFanOut     int
	RunnableCount int
	Reason        string
}

// DetectOverSerialized is a PURE, read-only detector (modeled on
// InferMissingDeps — it never mutates, prunes, or authors depends_on edges,
// and never calls SaveGoals). It computes a DAG-shape signal over the
// GoalPending goals and returns a *SerializationFinding when the plan-authored
// dependency graph is over-serialized — i.e. near-linear, single-runnable, or
// degenerate fan-out — the shape where a single stuck goal freezes all pending
// work and effective parallelism collapses to ~1.
//
// All graph signals are restricted to pending→pending edges: a pending goal
// depending on a GoalDone goal is satisfied (not serialized against pending
// work), so such an edge must not inflate fan-out or the critical path.
//
// Over-serialized when ANY of:
//   - criticalPath*4 >= pendingCount*3 (longest pending chain covers ≥75% of
//     pending nodes — a pure/near-linear chain),
//   - maxFanOut <= 1 (no pending goal unblocks more than one other),
//   - runnableCount <= 1 (given the done set, at most one pending goal is
//     startable, so the rest are transitively blocked behind it).
//
// Fires only when pendingCount >= 3 (below that the shape is too small to be
// meaningfully "over-serialized"). Returns nil otherwise. Integer math only.
func DetectOverSerialized(goals *GoalsFile) *SerializationFinding {
	if goals == nil || len(goals.Goals) == 0 {
		return nil
	}

	pending := map[string]bool{}
	for i := range goals.Goals {
		if goals.Goals[i].Status == GoalPending {
			pending[goals.Goals[i].ID] = true
		}
	}
	pendingCount := len(pending)
	if pendingCount < 3 {
		return nil
	}

	// maxFanOut: for each pending goal g, count pending goals y that list g in
	// their DependsOn (i.e. how many pending nodes g directly unblocks). Only
	// pending→pending edges count.
	fanOut := map[string]int{}
	runnableCount := 0
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalPending {
			continue
		}
		if g.DependsOnSatisfied(goals.Goals) {
			runnableCount++
		}
		for _, dep := range g.DependsOn {
			if pending[dep] {
				fanOut[dep]++
			}
		}
	}
	maxFanOut := 0
	for _, n := range fanOut {
		if n > maxFanOut {
			maxFanOut = n
		}
	}

	criticalPath := longestPendingPath(goals, pending)

	over := criticalPath*4 >= pendingCount*3 || maxFanOut <= 1 || runnableCount <= 1
	if !over {
		return nil
	}

	var signals []string
	if criticalPath*4 >= pendingCount*3 {
		signals = append(signals, "critical-path "+strconv.Itoa(criticalPath)+"/"+strconv.Itoa(pendingCount)+" ≈ pending count")
	}
	if maxFanOut <= 1 {
		signals = append(signals, "max fan-out "+strconv.Itoa(maxFanOut)+" (no pending goal unblocks >1 other)")
	}
	if runnableCount <= 1 {
		signals = append(signals, "only "+strconv.Itoa(runnableCount)+" runnable node")
	}

	return &SerializationFinding{
		PendingCount:  pendingCount,
		CriticalPath:  criticalPath,
		MaxFanOut:     maxFanOut,
		RunnableCount: runnableCount,
		Reason:        strings.Join(signals, "; "),
	}
}

// longestPendingPath returns the number of NODES on the longest path through
// the pending subgraph, following DependsOn edges whose target is itself
// pending. A single isolated pending node has length 1; a pure N-chain has
// length N. Cycle-safe: a memoized DFS with an on-stack `visiting` set treats a
// back-edge as contributing 0 depth, so an authored cycle terminates instead of
// recursing forever.
func longestPendingPath(goals *GoalsFile, pending map[string]bool) int {
	memo := map[string]int{}
	visiting := map[string]bool{}

	var depth func(id string) int
	depth = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		if visiting[id] {
			// Back-edge to a node already on the current DFS stack: contribute
			// 0 so the cycle cannot inflate (or hang) the longest path.
			return 0
		}
		visiting[id] = true
		best := 0
		if g, ok := goals.GoalByID(id); ok {
			for _, dep := range g.DependsOn {
				if !pending[dep] {
					continue
				}
				if d := depth(dep); d > best {
					best = d
				}
			}
		}
		visiting[id] = false
		memo[id] = best + 1
		return memo[id]
	}

	longest := 0
	for id := range pending {
		if d := depth(id); d > longest {
			longest = d
		}
	}
	return longest
}

func hasTransitivePath(goals *GoalsFile, from, to string) bool {
	visited := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		g, ok := goals.GoalByID(curr)
		if !ok {
			continue
		}
		for _, dep := range g.DependsOn {
			if dep == to {
				return true
			}
			if !visited[dep] {
				queue = append(queue, dep)
			}
		}
	}
	return false
}

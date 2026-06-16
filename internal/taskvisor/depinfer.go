package taskvisor

import (
	"sort"
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
				for _, prodID := range producerMap[s] {
					if prodID == g.ID {
						continue
					}
					if !hasTransitivePath(goals, g.ID, prodID) {
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
				if hi.set[s] {
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

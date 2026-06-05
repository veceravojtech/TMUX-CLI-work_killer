package taskvisor

import (
	"strings"
	"unicode"
)

type DepFinding struct {
	Consumer string
	Producer string
	Stem     string
	Evidence string
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

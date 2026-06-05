package taskvisor

import (
	"fmt"
	"regexp"
	"strings"
)

// Event-detection regexes. eventFQCNRe matches a PHP event FQCN with an
// explicit \Event\ segment (e.g. App\Share\Event\StockReserved); eventTokenRe
// is the short fallback for any CamelCase token ending in "Event". Backslashes
// are doubled inside the raw string so the compiled regex matches a single PHP
// namespace separator.
var (
	eventFQCNRe    = regexp.MustCompile(`[A-Z]\w*(?:\\[A-Z]\w*)*\\Event\\[A-Z]\w*`)
	eventTokenRe   = regexp.MustCompile(`\b[A-Z]\w*Event\b`)
	producerPathRe = regexp.MustCompile(`src/[A-Za-z0-9_]+(?:/[A-Za-z0-9_]+)*/?`)
)

// detectEventGoal reports whether a goal is event-driven. Dual signal (max
// recall): true if phase contains "event"/"choreograph" (case-insensitive) OR
// any description/acceptance/validate string references an event FQCN
// (...\Event\Name) or a CamelCase *Event token.
func detectEventGoal(phase, description string, acceptance, validate []string) bool {
	low := strings.ToLower(phase)
	if strings.Contains(low, "event") || strings.Contains(low, "choreograph") {
		return true
	}
	all := append([]string{description}, acceptance...)
	all = append(all, validate...)
	for _, s := range all {
		if eventFQCNRe.MatchString(s) || eventTokenRe.MatchString(s) {
			return true
		}
	}
	return false
}

// parseEventClass returns the first event FQCN found across the input groups
// (e.g. App\Share\Event\StockReserved), falling back to the first short *Event
// token if no FQCN is present. ok=false when neither matches.
func parseEventClass(groups ...[]string) (string, bool) {
	for _, g := range groups {
		for _, s := range g {
			if m := eventFQCNRe.FindString(s); m != "" {
				return m, true
			}
		}
	}
	for _, g := range groups {
		for _, s := range g {
			if m := eventTokenRe.FindString(s); m != "" {
				return m, true
			}
		}
	}
	return "", false
}

// eventDefDirFromFQCN maps an event FQCN to its source directory, dropping the
// root namespace segment and the class name: App\Share\Event\StockReserved ->
// src/Share/Event/. Returns "" for a short (non-namespaced) token.
func eventDefDirFromFQCN(fqcn string) string {
	parts := strings.Split(fqcn, `\`)
	if len(parts) < 3 {
		return ""
	}
	mid := parts[1 : len(parts)-1]
	if len(mid) == 0 {
		return ""
	}
	return "src/" + strings.Join(mid, "/") + "/"
}

// parseProducerPath returns the first src/<Context>/ token found across the
// inputs that is NOT the event-definition dir (the producer's own source tree).
// Falls back to "src/" when no distinct producer path can be parsed.
func parseProducerPath(acceptance, validate, context []string, eventDefDir string) string {
	for _, group := range [][]string{acceptance, validate, context} {
		for _, s := range group {
			for _, m := range producerPathRe.FindAllString(s, -1) {
				if !strings.HasSuffix(m, "/") {
					m += "/"
				}
				if strings.Contains(m, "tests/") {
					continue
				}
				if eventDefDir != "" && m == eventDefDir {
					continue
				}
				return m
			}
		}
	}
	return "src/"
}

// deriveEmissionInvestigator builds the non-skippable emission-check
// investigator for an event-driven goal. It returns ok=false unless the goal is
// detected as event-driven AND an event class can be parsed. The grep is rooted
// at the producer's src/ tree (never tests/, never the event-definition dir) so
// a listener test hand-building the event cannot satisfy it: zero matches means
// the producer never emits — dead choreography. Gating is at derivation time
// (no runtime Condition) so investigate.xml cannot SKIP it.
func deriveEmissionInvestigator(phase, description string, acceptance, validate []string) (Investigator, bool) {
	if !detectEventGoal(phase, description, acceptance, validate) {
		return Investigator{}, false
	}
	fqcn, ok := parseEventClass(acceptance, validate, []string{description})
	if !ok {
		return Investigator{}, false
	}

	eventName := fqcn
	if idx := strings.LastIndex(fqcn, `\`); idx >= 0 {
		eventName = fqcn[idx+1:]
	}

	eventDefDir := eventDefDirFromFQCN(fqcn)
	producerPath := parseProducerPath(acceptance, validate, nil, eventDefDir)

	// Regex-escape FQCN backslashes for the grep ERE (\ -> \\).
	escaped := strings.ReplaceAll(fqcn, `\`, `\\`)
	cmd := fmt.Sprintf(`grep -rEl --include='*.php' 'new\s+\\?%s\b|%s::class|->dispatch\(' %s`,
		escaped, escaped, producerPath)
	// Broad fallback: when no concrete producer path was parsed, exclude the
	// event-definition dir from the result so the event's own file is not a match.
	if producerPath == "src/" && eventDefDir != "" {
		cmd += fmt.Sprintf(" | grep -v '%s'", eventDefDir)
	}

	return Investigator{
		Name:      "Event emission",
		Type:      "emission-check",
		Paths:     []string{producerPath},
		Commands:  []string{cmd},
		Pass:      fmt.Sprintf("producer source constructs/dispatches %s (grep exit 0, ≥1 match outside tests/ and the event definition)", eventName),
		Fail:      fmt.Sprintf("producer never emits %s — grep exit 1, zero matches: dead choreography", eventName),
		Condition: "",
	}, true
}

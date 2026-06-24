package taskvisor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Investigator is the text-only model for one ## Investigation Config entry in a
// goal.md. It carries no goals.yaml backing — WriteGoalMD renders it verbatim and
// parseGoalFindings reads it back. Type is one of the inferred categories
// (quality-gate, test-execution, architecture-check, static-analysis); Pass/Fail
// are human-readable acceptance/failure descriptions; Condition is optional.
type Investigator struct {
	Name      string
	Type      string
	Paths     []string
	Commands  []string
	Pass      string
	Fail      string
	Condition string
}

// pureCommandExitTypes is the whitelist of investigator types whose verdict is
// decided solely by a command's exit status. Reasoning types (code-review,
// convention-audit, implementation-check, integration-check) and flaky external
// types (e2e-test, integration-test) are deliberately EXCLUDED — a
// misclassification would let execute-25's inline fast-path skip a needed
// reasoning worker, so the set is kept minimal and conservative.
var pureCommandExitTypes = map[string]bool{
	"static-analysis":    true,
	"quality-gate":       true,
	"test-execution":     true,
	"architecture-check": true,
	"environment-check":  true,
	"file-check":         true,
}

// exitOnlyPassMarkers / semanticPassMarkers classify a Pass string. The markers
// are derived from inferInvestigatorType's emitted Pass strings ("exit 0, no
// errors", "all green (exit 0)", "matches expected", "command succeeds", …) so
// derived investigators round-trip deterministically. Semantic markers VETO,
// so they are checked first in isExitOnlyPass.
var (
	exitOnlyPassMarkers = []string{"exit 0", "exit code", "succeeds", "green",
		"no error", "no violation", "no layer violation", "passes"}
	semanticPassMarkers = []string{"matches expected", "review", "audit",
		"compliance", "correct", "present", "well-formed", "design"}
)

// IsPureCommand reports whether inv's pass/fail is decided entirely by running a
// command and inspecting its exit status, with no semantic reasoning. execute-25's
// inline validation fast-path consumes it to run a check in-process instead of
// spawning a read-only worker. CONSERVATIVE by design: a false positive would
// silently skip a needed reasoning worker, so the predicate stacks three guards
// toward false — a type whitelist, a mandatory command, and a semantic-marker
// veto on the Pass string. Returns true on only two paths: an explicit
// type:command (the unambiguous signal), or a whitelisted exit-code type whose
// Pass is exit-only.
func IsPureCommand(inv Investigator) bool {
	if len(inv.Commands) == 0 {
		return false
	}
	if inv.Type == "command" {
		return true
	}
	return pureCommandExitTypes[inv.Type] && isExitOnlyPass(inv.Pass)
}

// isExitOnlyPass reports whether pass names an exit-status verdict and carries no
// semantic marker. Empty is false (nothing asserted). Semantic markers VETO
// first, so a reasoning Pass ("matches expected") on an otherwise exit-only type
// is rejected before any exit-only marker can match.
func isExitOnlyPass(pass string) bool {
	low := strings.ToLower(strings.TrimSpace(pass))
	if low == "" {
		return false
	}
	for _, m := range semanticPassMarkers {
		if strings.Contains(low, m) {
			return false
		}
	}
	for _, m := range exitOnlyPassMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// investigatorTypePriority orders types for the >4 truncation: prefer the most
// signal-rich check first. Lower number == kept first.
var investigatorTypePriority = map[string]int{
	// emission-check and own-suite-green both sort first (priority -1) so the
	// mandatory signals — dead-choreography and the goal's own integration+
	// functional suite — are never dropped by the >4 truncation; each outweighs a
	// 4th quality gate. Both surviving the cap is the B2b/B3 compose contract.
	"emission-check":     -1,
	"own-suite-green":    -1,
	// code-review is the mandatory functional/acceptance review (RC-1). Pinned at
	// -1 so the >4 truncation never drops the only behavioral gate on a goal whose
	// other investigators are all static analysis.
	"code-review":        -1,
	"test-execution":     0,
	"quality-gate":       1,
	"architecture-check": 2,
	"static-analysis":    3,
}

// deriveInvestigators builds 2-4 Investigators from a goal's validate rules when
// none were explicitly provided. Each rule maps to a typed investigator (see
// inferInvestigatorType); the result is padded to >=2 — project-aware via marker
// files at projectRoot (RC-B: a hardcoded `go build ./...` pad manufactured a
// guaranteed failure in non-Go projects) — and capped at 4, preferring
// higher-signal types. The two pad entries are always DISTINCT.
//
// When scope is non-nil and validate-rule-derived investigators number fewer
// than 2, scope-based investigators are derived by file extension (Go→test+build,
// shell→bash -n, XML→go build) and appended before generic padding fires.
func deriveInvestigators(projectRoot string, validate []string, scope []string) []Investigator {
	var list []Investigator
	for _, rule := range validate {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		typ, pass := inferInvestigatorType(rule)
		inv := Investigator{
			Name:     humanize(typ),
			Type:     typ,
			Commands: []string{rule},
			Pass:     pass,
			Fail:     "command fails / violation reported",
		}
		if p := firstPathToken(rule); p != "" {
			inv.Paths = []string{p}
		}
		list = append(list, inv)
	}

	if len(list) < 2 && len(scope) > 0 {
		profile := classifyScope(scope)
		for _, inv := range scopeDerivedInvestigators(projectRoot, profile) {
			if !hasInvestigatorType(list, inv.Type) {
				list = append(list, inv)
			}
		}
	}

	// Pad to the >=2 guarantee. First pad: stack-aware sanity check. Second pad
	// (validate was empty): a DIFFERENT repo-hygiene check — never two identical
	// entries, which would double a failure and waste a validation budget slot.
	if len(list) < 2 {
		list = append(list, projectSanityInvestigator(projectRoot))
	}
	if len(list) < 2 {
		list = append(list, repoReadableInvestigator(projectRoot))
	}

	return capInvestigators(list)
}

type scopeProfile struct {
	Go      []string
	Shell   []string
	XML     []string
	Unknown []string
}

func classifyScope(scope []string) scopeProfile {
	var p scopeProfile
	for _, entry := range scope {
		ext := strings.ToLower(filepath.Ext(entry))
		switch ext {
		case ".go":
			p.Go = append(p.Go, entry)
		case ".sh", ".bash":
			p.Shell = append(p.Shell, entry)
		case ".xml":
			p.XML = append(p.XML, entry)
		default:
			p.Unknown = append(p.Unknown, entry)
		}
	}
	return p
}

func scopeDerivedInvestigators(projectRoot string, profile scopeProfile) []Investigator {
	var list []Investigator
	list = append(list, goInvestigators(projectRoot, profile)...)
	list = append(list, shellInvestigators(profile)...)
	list = append(list, xmlInvestigators(profile)...)
	return list
}

func goInvestigators(projectRoot string, profile scopeProfile) []Investigator {
	if len(profile.Go) == 0 {
		return nil
	}
	var list []Investigator
	testPath := goTestPaths(profile.Go)
	list = append(list, Investigator{
		Name:     "Test execution",
		Type:     "test-execution",
		Commands: []string{"go test " + testPath},
		Pass:     "all green (exit 0)",
		Fail:     "command fails / violation reported",
	})
	list = append(list, Investigator{
		Name:     "Quality gate",
		Type:     "quality-gate",
		Commands: []string{"go build ./..."},
		Pass:     "exit 0, no errors",
		Fail:     "command fails / violation reported",
	})
	if goScopeComplex(profile.Go) {
		list = append(list, Investigator{
			Name:     "Static analysis",
			Type:     "static-analysis",
			Commands: []string{"go vet ./..."},
			Pass:     "exit 0, no errors",
			Fail:     "command fails / violation reported",
		})
	}
	return list
}

func goScopeComplex(goPaths []string) bool {
	if len(goPaths) > 3 {
		return true
	}
	dirs := make(map[string]bool)
	for _, p := range goPaths {
		dirs[filepath.Dir(p)] = true
	}
	return len(dirs) > 1
}

func shellInvestigators(profile scopeProfile) []Investigator {
	if len(profile.Shell) == 0 {
		return nil
	}
	target := profile.Shell[0]
	return []Investigator{{
		Name:     "Static analysis",
		Type:     "static-analysis",
		Commands: []string{"bash -n " + target},
		Pass:     "command succeeds",
		Fail:     "command fails / violation reported",
	}}
}

func xmlInvestigators(profile scopeProfile) []Investigator {
	if len(profile.XML) == 0 {
		return nil
	}
	if len(profile.Go) > 0 {
		return nil
	}
	return []Investigator{{
		Name:     "Quality gate",
		Type:     "quality-gate",
		Commands: []string{"go build ./..."},
		Pass:     "exit 0, no errors",
		Fail:     "command fails / violation reported",
	}}
}

func goTestPaths(goPaths []string) string {
	dirs := make(map[string]bool)
	for _, p := range goPaths {
		dirs[filepath.Dir(p)] = true
	}
	if len(dirs) == 1 {
		for d := range dirs {
			return "./" + filepath.ToSlash(d) + "/..."
		}
	}
	return "./..."
}

// projectSanityInvestigator returns the stack-aware pad entry. Detection is by
// marker files at projectRoot, first match wins; an UNKNOWN stack gets a
// harmless always-pass existence check — padding must never manufacture a fake
// failure (RC-B: `go build ./...` in a PHP project failed every cycle).
func projectSanityInvestigator(projectRoot string) Investigator {
	for _, m := range []struct{ marker, name, cmd, fail string }{
		{"go.mod", "Build sanity", "go build ./...", "build fails"},
		{"composer.json", "Composer sanity", "php -v && composer validate --no-check-publish --no-check-all", "command fails"},
		{"package.json", "Node sanity", "node --version && npm ls --depth=0", "command fails"},
		{"Makefile", "Make dry-run sanity", "make -n test", "command fails"},
	} {
		if _, err := os.Stat(filepath.Join(projectRoot, m.marker)); err == nil {
			return Investigator{
				Name:     m.name,
				Type:     "static-analysis",
				Commands: []string{m.cmd},
				Pass:     "command succeeds",
				Fail:     m.fail,
			}
		}
	}
	return Investigator{
		Name:     "Workspace sanity",
		Type:     "static-analysis",
		Commands: []string{"test -d ."},
		Pass:     "command succeeds",
		Fail:     "command fails",
	}
}

// repoReadableInvestigator returns the second, generic pad entry — distinct
// from projectSanityInvestigator by construction. `git status --short` always
// passes in a repo (a worktree's .git is a FILE, so a bare existence check is
// used, not IsDir); outside a repo it falls back to an always-pass read check.
func repoReadableInvestigator(projectRoot string) Investigator {
	cmd := "git status --short"
	if _, err := os.Stat(filepath.Join(projectRoot, ".git")); err != nil {
		cmd = "test -r ."
	}
	return Investigator{
		Name:     "Repo readable",
		Type:     "static-analysis",
		Commands: []string{cmd},
		Pass:     "command succeeds",
		Fail:     "command fails",
	}
}

// capInvestigators caps a list at 4, preferring higher-signal types (stable
// within equal priority). Lists of <=4 are returned unchanged (no reorder), so
// the extraction is behavior-identical to the old inline truncation. Shared by
// deriveInvestigators and WriteGoalMD (which re-caps after appending the
// emission investigator).
func capInvestigators(list []Investigator) []Investigator {
	if len(list) > 4 {
		sort.SliceStable(list, func(i, j int) bool {
			return investigatorTypePriority[list[i].Type] < investigatorTypePriority[list[j].Type]
		})
		list = list[:4]
	}
	return list
}

// producesAppCode reports whether a goal ships application source — the gate
// condition for auto-deriving the mandatory own-suite-green investigator. It is
// false for a phase=="gate" goal (build/grep-only validation, no src delivered);
// otherwise true iff any whitespace-token across acceptance/validate/context is a
// path beginning with the case-sensitive prefix "src/" or "app/". Matching the
// PREFIX of a path token (not a substring) avoids false positives from prose
// merely mentioning "source" or "app" — only a real path like `src/Catalog`
// counts. Leading quote/backtick/paren wrappers are trimmed so a token such as
// `src/Catalog` still matches.
func producesAppCode(phase string, acceptance, validate []string, context string) bool {
	if phase == "gate" {
		return false
	}
	lines := make([]string, 0, len(acceptance)+len(validate)+1)
	lines = append(lines, acceptance...)
	lines = append(lines, validate...)
	if context != "" {
		lines = append(lines, context)
	}
	for _, line := range lines {
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimLeft(tok, "`'\"([")
			if strings.HasPrefix(tok, "src/") || strings.HasPrefix(tok, "app/") {
				return true
			}
		}
	}
	return false
}

// ownSuiteGateInvestigator builds the mandatory own-suite-green gate over the
// goal's OWN integration+functional scope (the selector's existing test dirs).
// Its Command is a directory-positional phpunit invocation — NEVER a unit
// --filter slice (running only the unit slice IS the §0 bug) and never an
// unrelated suite — so a red suite exits non-zero and the worker reports the
// gate fail. The Fail text classifies that non-zero exit as code-defect/owner
// implementer, which ClassifyVerdict's code-defect tier rolls up to fail.
func ownSuiteGateInvestigator(scope []string) Investigator {
	return Investigator{
		Name:     "Own-suite green (integration+functional)",
		Type:     "own-suite-green",
		Paths:    scope,
		Commands: []string{"vendor/bin/phpunit " + strings.Join(scope, " ")},
		Pass:     "phpunit exits 0 for the goal's integration+functional scope",
		Fail:     "non-zero phpunit exit ⇒ code-defect (owner=implementer)",
	}
}

// functionalReviewInvestigator builds the mandatory functional acceptance review
// (RC-1). It is a reasoning (code-review) investigator with NO command, so
// IsPureCommand is false and investigate.xml ALWAYS spawns it — a behavior-bearing
// goal can therefore never pass on static analysis alone (the goal-001 "pure
// static-analysis, zero spawns" hole). Independent of TESTS_MODE: with tests off
// this review is the ONLY behavioral gate, so its Pass explicitly covers any
// authorization / deny-path / data-isolation criterion.
func functionalReviewInvestigator(scope []string) Investigator {
	return Investigator{
		Name:  "Functional acceptance review",
		Type:  "code-review",
		Paths: scope,
		Pass:  "every acceptance criterion is satisfied by the implementation — including any authorization / deny-path / data-isolation criterion — verified by reading the code, not just clean static analysis",
		Fail:  "an acceptance criterion is unmet or an authorization/deny-path control is missing ⇒ code-defect (owner=implementer)",
	}
}

// hasReasoningInvestigator reports whether list contains at least one reasoning
// (spawning) investigator — one IsPureCommand rejects (code-review,
// convention-audit, own-suite-green, emission-check, a semantic Pass, or any
// investigator with no command). A behavior-bearing goal with NONE would validate
// on static analysis alone; WriteGoalMD injects functionalReviewInvestigator to
// guarantee at least one.
func hasReasoningInvestigator(list []Investigator) bool {
	for _, inv := range list {
		if !IsPureCommand(inv) {
			return true
		}
	}
	return false
}

// hasInvestigatorType reports whether list already contains an investigator of
// the given type. Used to make the own-suite-green append idempotent: a planner
// explicit config that already declares own-suite-green is not duplicated.
func hasInvestigatorType(list []Investigator, typ string) bool {
	for _, inv := range list {
		if inv.Type == typ {
			return true
		}
	}
	return false
}

// ownSuiteFSRoot recovers the project root from a goal directory of the canonical
// shape <root>/.tmux-cli/goals/<id> by climbing three path segments. The selector
// resolves tests/Integration|Functional/<BC> existence checks against this root,
// so the gate's scope reflects the suites that actually exist in the goal's
// worktree. WriteGoalMD's signature is fixed (no fsRoot param), so the root is
// derived rather than passed.
func ownSuiteFSRoot(goalDir string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(goalDir)))
}

// inferInvestigatorType classifies a validate rule by scanning for tool tokens,
// returning the investigator type and a human-readable Pass description.
func inferInvestigatorType(rule string) (typ, pass string) {
	low := strings.ToLower(rule)
	switch {
	case strings.Contains(low, "phpstan"), strings.Contains(low, "stan"):
		return "quality-gate", "exit 0, no errors"
	case strings.Contains(low, "phpunit"), strings.Contains(low, "playwright"),
		strings.Contains(low, "npx"), strings.Contains(low, "--testsuite"):
		return "test-execution", "all green (exit 0)"
	case strings.Contains(low, "deptrac"):
		return "architecture-check", "exit 0, no layer violations"
	case strings.Contains(low, "ecs"), strings.Contains(low, "cs-fixer"),
		strings.Contains(low, "eslint"), strings.Contains(low, "lint"),
		strings.Contains(low, "jsf"):
		return "quality-gate", "exit 0, no violations"
	case strings.Contains(low, "grep"):
		// A `grep`/`grep -q` rule is a pure exit-code check (match → exit 0), not a
		// semantic comparison. Emitting the exit-only Pass "command succeeds (exit 0)"
		// (the literal MUST live in this file — a validate rule greps it here) lets
		// IsPureCommand classify it pure so the inline fast-path can run it in-process.
		// Placed AFTER the phpstan/phpunit/deptrac/ecs cases so a piped rule like
		// `phpstan analyse | grep error` still types quality-gate (precedence preserved).
		return "static-analysis", "command succeeds (exit 0)"
	case strings.Contains(low, "debug:router"),
		strings.Contains(low, "db-validate"), strings.Contains(low, "console"):
		return "static-analysis", "matches expected"
	default:
		return "static-analysis", "command succeeds"
	}
}

// humanize turns a kebab-case type into a capitalized label, e.g.
// "test-execution" -> "Test execution". Deterministic for test assertions.
func humanize(typ string) string {
	s := strings.ReplaceAll(typ, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// firstPathToken best-effort extracts a path-looking token from a rule (a token
// containing "/" that is not a flag), else returns "".
func firstPathToken(rule string) string {
	for _, tok := range strings.Fields(rule) {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if strings.Contains(tok, "/") {
			return tok
		}
	}
	return ""
}

package taskvisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	GoalPending = "pending"
	GoalRunning = "running"
	GoalDone    = "done"
	GoalFailed  = "failed"
	GoalBlocked = "blocked"
)

// Precondition is a declarative gate the daemon evaluates before spawning a
// worker. Kind is one of "env" (environment variable named by Spec must be set
// and non-empty) or "service" (TCP host:port in Spec must be reachable). Remedy
// is the operator/planner-facing runbook surfaced when the precondition fails.
type Precondition struct {
	Kind   string `yaml:"kind"`
	Spec   string `yaml:"spec"`
	Remedy string `yaml:"remedy"`
}

type Goal struct {
	ID            string         `yaml:"id"`
	Description   string         `yaml:"description"`
	Acceptance    []string       `yaml:"acceptance,omitempty"`
	Validate      []string       `yaml:"validate,omitempty"`
	Preconditions []Precondition `yaml:"preconditions,omitempty"`
	Status        string         `yaml:"status"`
	Retries       int            `yaml:"retries"`
	MaxRetries    int            `yaml:"max_retries"`

	CodeRetries       int `yaml:"code_retries,omitempty"`
	SpecRetries       int `yaml:"spec_retries,omitempty"`
	ValidationRetries int `yaml:"validation_retries,omitempty"`
	BlockRetries      int `yaml:"block_retries,omitempty"`

	MaxCodeRetries       int `yaml:"max_code_retries,omitempty"`
	MaxSpecRetries       int `yaml:"max_spec_retries,omitempty"`
	MaxValidationRetries int `yaml:"max_validation_retries,omitempty"`
	MaxBlockRetries      int `yaml:"max_block_retries,omitempty"`

	// Durable C6 convergence circuit-breaker state (daemon-owned, survives
	// cycles in goals.yaml). ConvergenceSignatures is the prior failed cycle's
	// sorted signature set; ConvergenceStreak counts consecutive cycles whose
	// signature set was identical. When the streak reaches circuit_breaker_k the
	// goal is halted to blocked/owner=human regardless of remaining retry budget.
	ConvergenceSignatures []string `yaml:"convergence_signatures,omitempty"`
	ConvergenceStreak     int      `yaml:"convergence_streak,omitempty"`

	Phase     string   `yaml:"phase,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"`
	BlockedBy string   `yaml:"blocked_by,omitempty"`
	// BlockedByPrecondition marks a goal parked on an unmet env/infra
	// precondition (set by haltBlockedEnv). It is the query key for the §5
	// auto-resume loop (resumeDownstreamLoop/scanPreconditionBlocked): only goals
	// with this flag set are re-evaluated against their preconditions, so the loop
	// never blindly re-probes every goal. Cleared by clearBlock when the
	// precondition passes or an upstream dependency completes.
	BlockedByPrecondition bool `yaml:"blocked_by_precondition,omitempty"`

	StartedAt  string `yaml:"started_at,omitempty"`
	FinishedAt string `yaml:"finished_at,omitempty"`
}

type GoalsFile struct {
	CurrentGoal      string `yaml:"current_goal"`
	GlobalMaxRetries int    `yaml:"global_max_retries,omitempty"`
	Goals            []Goal `yaml:"goals"`
}

func WithGoalsLock(projectRoot string, fn func() error) error {
	lockPath := filepath.Join(projectRoot, ".tmux-cli", "goals.yaml.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open goals lock: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire goals lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

func GoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals.yaml")
}

func LoadGoals(projectRoot string) (*GoalsFile, error) {
	data, err := os.ReadFile(GoalsFilePath(projectRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var gf GoalsFile
	if err := yaml.Unmarshal(data, &gf); err != nil {
		return nil, err
	}

	for i := range gf.Goals {
		g := &gf.Goals[i]

		// Per-class retry budgets decrement toward zero: the live counters
		// (CodeRetries/SpecRetries/ValidationRetries/BlockRetries) hold the
		// REMAINING budget — a branch hard-halts when its live counter hits 0 —
		// while the Max… counters hold the configured starting budget. Derive
		// the Max… budget from the legacy single max_retries when it is absent.
		if g.MaxCodeRetries == 0 && g.MaxSpecRetries == 0 && g.MaxValidationRetries == 0 && g.MaxBlockRetries == 0 {
			mb := MigrateRetries(g.MaxRetries)
			g.MaxCodeRetries, g.MaxSpecRetries, g.MaxValidationRetries, g.MaxBlockRetries =
				mb.CodeRetries, mb.SpecRetries, mb.ValidationRetries, mb.BlockRetries
		}

		// Seed the live counters to the FULL Max… budget for an UNSTARTED goal,
		// i.e. when all four live counters are zero AND the goal is not terminal.
		// Live = remaining budget, so an unstarted goal must start at the full
		// budget (not the legacy used-count, which is 0 for a fresh goal and
		// would hard-halt it on its very first defect). The non-zero guard makes
		// this idempotent: once any live counter is consumed, reload skips it and
		// the mid-decrement state is preserved. Terminal goals (GoalFailed/
		// GoalDone) are never re-seeded — an exhausted goal must not be
		// resurrected to full budget. Legacy mid-flight goals (rare) are granted
		// full budget on this one-time migration: per-class used-counts are
		// unknowable from the single legacy counter, and over-granting is safe
		// versus the hard-halt bug.
		if g.CodeRetries == 0 && g.SpecRetries == 0 && g.ValidationRetries == 0 && g.BlockRetries == 0 &&
			g.Status != GoalFailed && g.Status != GoalDone {
			g.CodeRetries, g.SpecRetries, g.ValidationRetries, g.BlockRetries =
				g.MaxCodeRetries, g.MaxSpecRetries, g.MaxValidationRetries, g.MaxBlockRetries
		}
	}

	return &gf, nil
}

// RetryBudgets carries per-class retry counts split from a single legacy count.
type RetryBudgets struct {
	CodeRetries       int
	SpecRetries       int
	ValidationRetries int
	BlockRetries      int
}

// MigrateRetries splits a single legacy retry count across the per-class
// budgets. SpecRetries is max(2, (oldRetries+1)/2) — a floor of 2 guarantees
// even old=0/1 never instant-fails on the first spec-defect verdict (the
// previo2 goal-043 false-failure); ValidationRetries defaults to 2;
// BlockRetries is always 0 ("blocked never gets budget"). With the default
// oldRetries=5 this yields Code 5 / Spec 3 / Val 2. Negative inputs clamp to 0
// so no budget is ever negative.
func MigrateRetries(oldRetries int) RetryBudgets {
	if oldRetries < 0 {
		oldRetries = 0
	}
	return RetryBudgets{
		CodeRetries:       oldRetries,
		SpecRetries:       max(2, (oldRetries+1)/2),
		ValidationRetries: 2,
		BlockRetries:      0,
	}
}

func SaveGoals(projectRoot string, gf *GoalsFile) error {
	p := GoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(gf)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func parseGoalIDNumber(id string) (int, error) {
	if !strings.HasPrefix(id, "goal-") {
		return 0, fmt.Errorf("invalid goal ID format: %s", id)
	}
	return strconv.Atoi(strings.TrimPrefix(id, "goal-"))
}

func NextGoalID(goals []Goal) string {
	max := 0
	for _, g := range goals {
		n, err := parseGoalIDNumber(g.ID)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("goal-%03d", max+1)
}

func EnsureGoalDir(projectRoot, goalID string) (string, error) {
	dir := filepath.Join(projectRoot, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(filepath.Join(dir, "corrections"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// CurrentCycle returns the one-indexed dispatch-attempt number for a goal,
// derived from CONSUMED retry budget across the per-class counters. The live
// CodeRetries/SpecRetries/ValidationRetries counters hold the REMAINING budget
// (decrement-toward-zero, seeded to the Max… budgets by LoadGoals), so consumed
// = Max… − live. A fresh full-budget goal is cycle 1, and the number rises
// monotonically as any class consumes budget — never backward. The legacy
// goal.Retries counter is read-only post-migration and is NEVER read here.
// Reproducible after a daemon restart because every input is persisted in
// goals.yaml. (NOTE: this intentionally uses consumed budget, not the live
// remaining sum — the live counters DECREASE per cycle, so a remaining-sum
// formula would make cycle numbers go backward and collide.)
func CurrentCycle(g *Goal) int {
	consumed := (g.MaxCodeRetries - g.CodeRetries) +
		(g.MaxSpecRetries - g.SpecRetries) +
		(g.MaxValidationRetries - g.ValidationRetries)
	if consumed < 0 {
		consumed = 0
	}
	return consumed + 1
}

// CycleResearchDir is the per-cycle research directory beneath the goal-scoped
// research root: .tmux-cli/goals/<id>/research/cycle-<CurrentCycle(g)>/. It
// appends the cycle layer to the already-existing goal-scoped root rather than
// introducing a parallel tree.
func CycleResearchDir(projectRoot string, g *Goal) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals", g.ID, "research",
		fmt.Sprintf("cycle-%d", CurrentCycle(g)))
}

// EnsureCycleResearchDir mkdir -p's the current cycle's research dir (idempotent)
// and returns it. The daemon calls this before spawning any worker so reports
// for cycle K land in cycle-K/ and never collide with a prior cycle's reports.
func EnsureCycleResearchDir(projectRoot string, g *Goal) (string, error) {
	dir := CycleResearchDir(projectRoot, g)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

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

func WriteGoalMD(goalDir, description, phase string, acceptance, validate []string, preconditions []Precondition, context, notInScope string, investigators []Investigator) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", description)

	if phase != "" {
		fmt.Fprintf(&b, "\n## Phase\n\n%s\n", phase)
	}

	b.WriteString("\n## Acceptance Criteria\n\n")
	for _, a := range acceptance {
		fmt.Fprintf(&b, "- %s\n", a)
	}

	b.WriteString("\n## Validation Rules\n\n")
	if len(validate) > 0 {
		for _, v := range validate {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	} else {
		b.WriteString("(none)\n")
	}

	if len(preconditions) > 0 {
		b.WriteString("\n## Preconditions\n\n")
		for _, p := range preconditions {
			if p.Remedy != "" {
				fmt.Fprintf(&b, "- [%s] %s — %s\n", p.Kind, p.Spec, p.Remedy)
			} else {
				fmt.Fprintf(&b, "- [%s] %s\n", p.Kind, p.Spec)
			}
		}
	}

	if context != "" {
		fmt.Fprintf(&b, "\n## Context\n\n%s\n", context)
	}

	if notInScope != "" {
		fmt.Fprintf(&b, "\n## Not In Scope\n\n%s\n", notInScope)
	}

	// Investigation Config: read by investigate.xml (requires >=2 ### Investigator
	// subsections) and by parseGoalFindings. When no investigators are supplied,
	// derive 2-4 from the validate rules so the section is never missing.
	list := investigators
	if len(list) == 0 {
		list = deriveInvestigators(validate)
	}
	b.WriteString("\n## Investigation Config\n\n")
	for i, inv := range list {
		fmt.Fprintf(&b, "### Investigator %d: %s\n", i+1, inv.Name)
		fmt.Fprintf(&b, "- type: %s\n", inv.Type)
		if len(inv.Paths) > 0 {
			fmt.Fprintf(&b, "- paths: %s\n", strings.Join(inv.Paths, ", "))
		}
		for _, c := range inv.Commands {
			fmt.Fprintf(&b, "- command: %s\n", c)
		}
		fmt.Fprintf(&b, "- Pass: %s\n", inv.Pass)
		fmt.Fprintf(&b, "- Fail: %s\n", inv.Fail)
		if inv.Condition != "" {
			fmt.Fprintf(&b, "- condition: %s\n", inv.Condition)
		}
		b.WriteString("\n")
	}

	// C10: incremental re-validation reuses a prior cycle's pass when its input
	// fingerprint (rule + in-scope changed files + preconditions) is unchanged.
	// --full forces a full re-validation — re-running every check regardless of
	// fingerprint; the final cycle before overall pass also re-runs all checks.
	// Appended last so the preceding section order is stable.
	b.WriteString("\n## Re-validation\n\n")
	b.WriteString("Incremental: only failed checks and checks whose inputs changed are re-run on retry. `--full` forces full re-validation.\n")

	return atomicWrite(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0o644)
}

// investigatorTypePriority orders types for the >4 truncation: prefer the most
// signal-rich check first. Lower number == kept first.
var investigatorTypePriority = map[string]int{
	"test-execution":     0,
	"quality-gate":       1,
	"architecture-check": 2,
	"static-analysis":    3,
}

// deriveInvestigators builds 2-4 Investigators from a goal's validate rules when
// none were explicitly provided. Each rule maps to a typed investigator (see
// inferInvestigatorType); the result is padded to >=2 with a generic build-sanity
// investigator and capped at 4, preferring higher-signal types.
func deriveInvestigators(validate []string) []Investigator {
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

	// Pad to the >=2 guarantee with a generic build-sanity investigator.
	for len(list) < 2 {
		list = append(list, Investigator{
			Name:     "Build sanity",
			Type:     "static-analysis",
			Commands: []string{"go build ./..."},
			Pass:     "command succeeds",
			Fail:     "build fails",
		})
	}

	// Cap at 4, preferring higher-signal types (stable within equal priority).
	if len(list) > 4 {
		sort.SliceStable(list, func(i, j int) bool {
			return investigatorTypePriority[list[i].Type] < investigatorTypePriority[list[j].Type]
		})
		list = list[:4]
	}
	return list
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
	case strings.Contains(low, "debug:router"), strings.Contains(low, "grep"),
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

func (gf *GoalsFile) GoalByID(id string) (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			return &gf.Goals[i], true
		}
	}
	return nil, false
}

func (gf *GoalsFile) NextPendingGoal() (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalBlocked {
			continue
		}
		if gf.Goals[i].Status != GoalPending {
			continue
		}
		if !gf.Goals[i].DependsOnSatisfied(gf.Goals) {
			continue
		}
		return &gf.Goals[i], true
	}
	return nil, false
}

// RunnableCandidates returns the goals the daemon *should* be able to dispatch
// right now: GoalPending, dependencies satisfied, and not parked on a
// precondition. Pure read over gf.Goals (no mutation, no lock — the caller
// already holds the poll flock). This is deliberately NOT AllResolved/
// NextPendingGoal: it is the watchdog's "is there work that ought to be moving"
// query, used only to distinguish a genuine stall (candidates exist but nothing
// dispatches) from a legitimate idle (no candidate at all).
func (gf *GoalsFile) RunnableCandidates() []*Goal {
	var out []*Goal
	for i := range gf.Goals {
		g := &gf.Goals[i]
		if g.Status != GoalPending {
			continue
		}
		if g.BlockedByPrecondition {
			continue
		}
		if !g.DependsOnSatisfied(gf.Goals) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// AnyRunning reports whether any goal is GoalRunning (a worker mid-flight). Pure
// read; used by the stall watchdog to treat head-of-line waits as non-idle.
func (gf *GoalsFile) AnyRunning() bool {
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalRunning {
			return true
		}
	}
	return false
}

func (gf *GoalsFile) SetStatus(id, status string) bool {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			gf.Goals[i].Status = status
			return true
		}
	}
	return false
}

func (gf *GoalsFile) DeleteGoal(id string) (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			removed := gf.Goals[i]
			gf.Goals = append(gf.Goals[:i], gf.Goals[i+1:]...)
			if gf.CurrentGoal == id {
				gf.CurrentGoal = ""
			}
			return &removed, true
		}
	}
	return nil, false
}

func (gf *GoalsFile) ResetGoal(id string) bool {
	g, ok := gf.GoalByID(id)
	if !ok || g.Status != GoalFailed {
		return false
	}
	g.Status = GoalPending
	g.Retries = 0
	g.StartedAt = ""
	g.FinishedAt = ""
	return true
}

func (gf *GoalsFile) SkipGoal(id string) bool {
	g, ok := gf.GoalByID(id)
	if !ok || g.Status != GoalRunning {
		return false
	}
	g.Status = GoalDone
	g.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return true
}

func (g *Goal) IncrementRetries() int {
	g.Retries++
	return g.Retries
}

func (g *Goal) DependsOnSatisfied(goals []Goal) bool {
	if len(g.DependsOn) == 0 {
		return true
	}
	status := make(map[string]string, len(goals))
	for _, gl := range goals {
		status[gl.ID] = gl.Status
	}
	for _, dep := range g.DependsOn {
		if status[dep] != GoalDone {
			return false
		}
	}
	return true
}

// CascadeFailure propagates an upstream failure to its dependent subtree,
// branching on the upstream's verdict CLASS:
//
//   - HARD (verdictClass == "fail" || "code-defect"): the upstream is genuinely
//     broken, so every dependent is set GoalBlocked with BlockedBy recorded —
//     the original indiscriminate behavior, now reached only on a hard verdict.
//   - SOFT (any other class — "blocked"/"env-config"/"infra-flake"): the upstream
//     is parked on a transient/environmental hold, so dependents stay GoalPending
//     (NOT failed/blocked) with BlockedBy recorded; they auto-resume once the
//     upstream completes (resumeDownstream) or its precondition clears.
//
// There is deliberately NO unguarded blocking default: only the two explicit hard
// classes block. Callers always pass an explicit class (no implicit re-block of
// an unknown verdict). The BFS traversal and the pending/running status guard are
// preserved from the original, as is BlockedBy recording on every dependent.
func (gf *GoalsFile) CascadeFailure(failedGoalID, verdictClass string) {
	hard := verdictClass == "fail" || verdictClass == "code-defect"
	visited := map[string]bool{}
	queue := []string{failedGoalID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		for i := range gf.Goals {
			g := &gf.Goals[i]
			if g.Status != GoalPending && g.Status != GoalRunning {
				continue
			}
			for _, dep := range g.DependsOn {
				if dep == id {
					g.BlockedBy = failedGoalID
					if hard {
						g.Status = GoalBlocked
					}
					// SOFT: leave g.Status == GoalPending; the dependency gate
					// (DependsOnSatisfied) keeps it from dispatching until the
					// upstream completes, at which point resumeDownstream clears it.
					queue = append(queue, g.ID)
					break
				}
			}
		}
	}
}

// ReconcileBlocks derives block-state from current dependency status, healing
// the staleness that makes block-state go wrong as ground truth (Bug A): a
// hard-failed upstream that later completes leaves its dependent subtree pinned
// GoalBlocked forever, because CascadeFailure writes GoalBlocked but
// resumeDownstream only re-pends GoalPending. Run at the top of every active
// tick (and in activate), it is the single source of truth for block-state.
//
// It is a PURE *GoalsFile method: in-memory mutation only, no I/O, no lock — so
// callers must already hold the goals flock (poll does). It touches ONLY Status
// and BlockedBy; retry budgets are never altered. Two derivations per goal:
//
//   - Re-block: the first dep in DependsOn ARRAY ORDER that exists in the file
//     and is GoalFailed pins the goal GoalBlocked,BlockedBy=<that dep>.
//   - Un-stick (NARROWLY): a GoalBlocked goal whose block is dependency-derived
//     (BlockedBy names a real goal id OR == "deps_unsatisfied") AND whose deps
//     are now satisfied is re-pended GoalPending,BlockedBy="".
//
// Skips terminal (GoalDone/GoalFailed) and GoalRunning goals (never orphan a
// live worker) and preserves operator/precondition holds:
// BlockedByPrecondition==true and BlockedBy=="convergence-circuit-breaker". The
// isDependencyBlock guard is what keeps non-dependency holds (BlockedBy=="",
// "external", a dangling deleted-dep id) pinned, since DependsOnSatisfied is
// vacuously true for zero-dep goals. changed is derived from real Status/
// BlockedBy field deltas (not a "visited" flag), so the function is idempotent.
func (gf *GoalsFile) ReconcileBlocks() (changed bool) {
	realIDs := make(map[string]bool, len(gf.Goals))
	for i := range gf.Goals {
		realIDs[gf.Goals[i].ID] = true
	}

	for i := range gf.Goals {
		g := &gf.Goals[i]
		prevStatus, prevBlockedBy := g.Status, g.BlockedBy

		if g.Status == GoalDone || g.Status == GoalFailed || g.Status == GoalRunning {
			continue
		}
		if g.BlockedByPrecondition {
			continue
		}
		if g.BlockedBy == "convergence-circuit-breaker" {
			continue
		}

		// Re-block behind the first genuinely-failed dep in array order.
		failed := ""
		for _, dep := range g.DependsOn {
			if realIDs[dep] && gf.statusOf(dep) == GoalFailed {
				failed = dep
				break
			}
		}
		if failed != "" {
			g.Status = GoalBlocked
			g.BlockedBy = failed
			if g.Status != prevStatus || g.BlockedBy != prevBlockedBy {
				changed = true
			}
			continue
		}

		// Narrowly un-stick a dependency-derived block whose deps recovered.
		isDependencyBlock := realIDs[g.BlockedBy] || g.BlockedBy == "deps_unsatisfied"
		if g.Status == GoalBlocked && isDependencyBlock && g.DependsOnSatisfied(gf.Goals) {
			g.Status = GoalPending
			g.BlockedBy = ""
		}

		if g.Status != prevStatus || g.BlockedBy != prevBlockedBy {
			changed = true
		}
	}

	return changed
}

func (gf *GoalsFile) statusOf(id string) string {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			return gf.Goals[i].Status
		}
	}
	return ""
}

func (gf *GoalsFile) retrySumAndCeiling() (sum, ceiling int) {
	for _, g := range gf.Goals {
		sum += g.Retries
	}
	ceiling = gf.GlobalMaxRetries
	if ceiling <= 0 {
		ceiling = max(50, len(gf.Goals)*2)
	}
	return sum, ceiling
}

func (gf *GoalsFile) TotalRetries() int {
	sum, ceiling := gf.retrySumAndCeiling()
	if sum > ceiling {
		return ceiling
	}
	return sum
}

func (gf *GoalsFile) RetryCeilingReached() bool {
	sum, ceiling := gf.retrySumAndCeiling()
	return sum >= ceiling
}

func (gf *GoalsFile) AllResolved() bool {
	for _, g := range gf.Goals {
		if g.Status == GoalPending || g.Status == GoalRunning {
			return false
		}
	}
	return true
}

// HasResumablePark reports whether any goal carries BlockedByPrecondition==true —
// a resumable env/infra park that scanPreconditionBlocked will re-pend once its
// precondition clears. Such a goal is outstanding work, not a resolved goal, so
// the daemon must not deactivate while one exists. Keys ONLY on the flag, so
// manual/external GoalBlocked holds (no flag) are not treated as resumable.
func (gf *GoalsFile) HasResumablePark() bool {
	for i := range gf.Goals {
		if gf.Goals[i].BlockedByPrecondition {
			return true
		}
	}
	return false
}

// HasRecoverableBlock reports whether any goal is an immediately-recoverable
// cascade block: GoalBlocked, not a precondition park, not the circuit-breaker
// sentinel, whose BlockedBy names a goal that has reached GoalDone, and whose own
// DependsOn are all satisfied. Such a goal will be re-pended by ReconcileBlocks,
// so the daemon must stay active (mirrors HasResumablePark for the precondition
// class). A blocker that is GoalFailed is a GENUINE hard block, not recoverable —
// statusOf(BlockedBy)==GoalDone is the discriminator.
func (gf *GoalsFile) HasRecoverableBlock() bool {
	for i := range gf.Goals {
		g := &gf.Goals[i]
		if g.Status != GoalBlocked || g.BlockedByPrecondition {
			continue
		}
		if g.BlockedBy == "" || g.BlockedBy == "convergence-circuit-breaker" {
			continue
		}
		if gf.statusOf(g.BlockedBy) != GoalDone {
			continue
		}
		if g.DependsOnSatisfied(gf.Goals) {
			return true
		}
	}
	return false
}

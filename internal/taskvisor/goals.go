package taskvisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

	// PhaseFinalGate is the Phase value the planter (task-plan-generate.xml)
	// writes for end-of-run validation gates. The stall watchdog keys on it to
	// detect a terminal deadlock (a final gate blocked behind a GoalFailed dep).
	// MUST match the planter's literal "final_gate" — never the bare "final".
	PhaseFinalGate = "final_gate"
)

// Goal.NextDispatch marker values (RC-D). Set at the verdict-resolution seam
// (bounceToGeneration / handleFailedCycle), honored FIRST by dispatchCandidate,
// and consumed (cleared) by dispatch/dispatchRetry. Unexported: the marker is a
// daemon-internal routing fact, never authored by planners or tools.
const (
	// dispatchGeneration forces the next dispatch onto the FULL plan path
	// (/tmux:plan, planner re-generation) regardless of consumed code budget —
	// the fix for the sticky codeBudgetConsumed heuristic that re-executed a
	// defective spec verbatim after a spec bounce.
	dispatchGeneration = "generation"
	// dispatchImplementer routes the next dispatch to dispatchRetry (reuse
	// tasks.yaml, skip planning) when the per-goal tasks.yaml still exists.
	dispatchImplementer = "implementer"
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

	// Durable spec-route convergence circuit-breaker state, ISOLATED from the
	// code-route ConvergenceSignatures/ConvergenceStreak above. Populated by
	// bounceToGeneration: SpecConvergenceSignatures is the prior spec bounce's
	// sorted signature set; SpecConvergenceStreak counts consecutive bounces whose
	// signature set was identical. At circuit_breaker_k the goal is halted to
	// blocked/owner=human without draining SpecRetries. Kept in DEDICATED fields so
	// an interleaved code-defect cycle (which only touches the code-route fields)
	// can never reset or inflate the spec streak, and vice versa.
	SpecConvergenceSignatures []string `yaml:"spec_convergence_signatures,omitempty"`
	SpecConvergenceStreak     int      `yaml:"spec_convergence_streak,omitempty"`

	// NextDispatch is the EXPLICIT routing marker for this goal's next dispatch
	// (RC-D): "generation" (set by bounceToGeneration — the next dispatch MUST
	// be a full planner re-generation) or "implementer" (set by the
	// handleFailedCycle code-defect re-pend — retry against the existing
	// tasks.yaml). Empty = legacy mid-flight goal; dispatchCandidate falls back
	// to the historical codeBudgetConsumed heuristic. Persisted in goals.yaml
	// (NOT daemon runtime) so it survives a daemon restart; consumed (cleared)
	// by dispatch/dispatchRetry once the routing decision is acted on. NOTE:
	// not yet mirrored by mcp.tvGoal (dual-struct) — an MCP load-resave between
	// bounce and dispatch drops it, degrading to the legacy heuristic.
	NextDispatch string `yaml:"next_dispatch,omitempty"`

	Phase     string   `yaml:"phase,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"`

	// EscalationCount is the durable count of escalation-driven prerequisites
	// wired onto this goal via the goal-add-prerequisite MCP tool. It bounds
	// runtime prerequisite chains against escalationCap (mirrors C1a's cap of 2).
	// DUAL-STRUCT (critical): mirrored field-for-field by mcp.tvGoal — the MCP
	// tools (de)serialize via tvGoal, the daemon via taskvisor.Goal. Omitting it
	// here makes the daemon's first SaveGoals silently erase the counter and the
	// cap leaks (TestGoal_EscalationCountSurvivesSaveGoalsRoundTrip guards this).
	EscalationCount int `yaml:"escalation_count,omitempty"`

	// Scope is the goal's declared file/namespace footprint (globs like
	// "internal/x/**" or namespace prefixes like "App\Billing"), authored
	// explicitly or derived-from-Deliverables by the planner. The disjoint-scope
	// co-scheduling gate (DisjointReadySet) reads it to decide whether two goals
	// may run concurrently under MaxGoals>1: empty Scope == UNKNOWN, treated
	// conservatively as overlapping everything (serialize). NOT the same concept
	// as the finding-level ValidationFinding.Scope in signal.go — same name,
	// different layer; they are deliberately uncoupled. The runtime reads ONLY
	// this persisted field; it never parses goal.md.
	Scope []string `yaml:"scope,omitempty"`

	// Migrates marks a goal that mutates the SHARED database schema (e.g. runs
	// doctrine:migrations:migrate inside its worker). Per-goal worktrees (E1-1a)
	// isolate FILES but NOT the shared DB, so the co-scheduling gate
	// (DisjointReadySet) honors this as a hard exclusion: a Migrates goal runs
	// ALONE — never co-scheduled with any in-flight goal, and no goal is
	// co-scheduled while a Migrates goal is in flight. It is the robust guarantee
	// for in-worker migrations the daemon cannot exec-wrap with WithDBLock; the
	// flock .tmux-cli/db.lock shell wrapper documented in execute.xml/validate.xml
	// is best-effort defense-in-depth. Absent ⇒ false (a normal goal).
	Migrates bool `yaml:"migrates,omitempty"`

	// FailedBy records WHY a goal reached GoalFailed when the cause matters
	// post-mortem. "validation-timeout" marks a timeout-SYNTHESIZED failure (no
	// verdict ever arrived): the salvage scan keeps watching signal.json for a
	// late verdict on such goals. Cleared by ResetGoal and by salvage itself.
	FailedBy string `yaml:"failed_by,omitempty"`

	BlockedBy string `yaml:"blocked_by,omitempty"`
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

// DBLockPath is the path of the cross-process advisory lock guarding the SHARED
// database schema, mirroring GoalsFilePath. Worker shells (and the daemon's
// validate wrap) flock this file around any migration/validate step so MaxGoals>1
// cannot race the schema that worktrees do not isolate.
func DBLockPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "db.lock")
}

// WithDBLock runs fn while holding an exclusive advisory lock on
// .tmux-cli/db.lock. It is a byte-for-byte clone of WithGoalsLock against
// DBLockPath — same MkdirAll → OpenFile(O_CREATE|O_RDWR,0644) →
// syscall.Flock(LOCK_EX) → defer LOCK_UN sequence — so reviewers diff the two and
// see only the path/error-string change. The daemon nests it strictly INSIDE the
// goals flock (lock order goals→db); worker shells take ONLY db.lock, so no actor
// ever acquires the two in the opposite order and there is no deadlock cycle. The
// lock is released by the defer even when fn returns an error.
func WithDBLock(projectRoot string, fn func() error) error {
	lockPath := DBLockPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open db lock: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire db lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
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
	return consumedRetries(g) + 1
}

// consumedRetries is the single source of "retry budget burned by this goal":
// the sum of CONSUMED budget across the three budgeted classes (code/spec/
// validation), where consumed = Max… − live (the live counters decrement toward
// zero from their seeded Max… budget). BlockRetries is deliberately EXCLUDED —
// MaxBlockRetries is always 0 (MigrateRetries: "blocked never gets budget"), so
// it would only ever subtract — which keeps this consistent with CurrentCycle's
// historical formula. The result is clamped to >= 0 so a corrupt goal with a
// live counter exceeding its Max (live > max) never contributes a negative
// amount to the global retry-ceiling sum. The legacy g.Retries scalar is NEVER
// read here. Both CurrentCycle and retrySumAndCeiling reuse this so the
// per-goal "budget consumed" quantity has exactly one definition.
func consumedRetries(g *Goal) int {
	consumed := (g.MaxCodeRetries - g.CodeRetries) +
		(g.MaxSpecRetries - g.SpecRetries) +
		(g.MaxValidationRetries - g.ValidationRetries)
	if consumed < 0 {
		consumed = 0
	}
	return consumed
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
		// WriteGoalMD's signature is fixed (no fsRoot param) — recover the
		// project root from the canonical goalDir shape, same as the B2b gate.
		list = deriveInvestigators(ownSuiteFSRoot(goalDir), validate)
		// Event goals get an extra, non-skippable emission investigator that
		// asserts the PRODUCER actually constructs/dispatches the event (catches
		// dead choreography). Only when the planner supplied no explicit
		// investigators — respect an explicit list. Re-cap so total stays <=4;
		// emission-check's -1 priority guarantees it survives truncation.
		if emi, ok := deriveEmissionInvestigator(phase, description, acceptance, validate); ok {
			list = capInvestigators(append(list, emi))
		}
	}

	// Mandatory own-suite-green gate (B2b): a code goal (declares src/|app/
	// deliverables, not a gate phase) ALWAYS gets an investigator that runs its
	// OWN integration+functional suite via phpunit — appended HERE, after the
	// list is built, so it applies to BOTH the explicit-config and the
	// deriveInvestigators-fallback paths and cannot be omitted by a planner. The
	// selector resolves the scope to existing test dirs under the project root;
	// when the goal owns no integration/functional suite (Empty), the gate SKIPS
	// rather than emit a paths-less phpunit that would run the whole suite. The
	// re-cap keeps the section at <=4 with both -1 pins (own-suite-green AND any
	// emission-check) surviving truncation — the B2b/B3 compose contract.
	if producesAppCode(phase, acceptance, validate, context) && !hasInvestigatorType(list, "own-suite-green") {
		deliverables := DeliverablesFromGoal(Goal{Acceptance: acceptance, Validate: validate})
		scope := SelectOwnSuiteScope(deliverables, ownSuiteFSRoot(goalDir))
		if !scope.Empty {
			list = capInvestigators(append(list, ownSuiteGateInvestigator(scope.Paths)))
		}
	}

	renderInvestigationConfig(&b, list)

	// C10: incremental re-validation reuses a prior cycle's pass when its input
	// fingerprint (rule + in-scope changed files + preconditions) is unchanged.
	// --full forces a full re-validation — re-running every check regardless of
	// fingerprint; the final cycle before overall pass also re-runs all checks.
	// Appended last so the preceding section order is stable.
	b.WriteString("\n## Re-validation\n\n")
	b.WriteString("Incremental: only failed checks and checks whose inputs changed are re-run on retry. `--full` forces full re-validation.\n")

	return atomicWrite(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0o644)
}

// renderInvestigationConfig writes the `## Investigation Config` section — the
// heading followed by one `### Investigator N` block per entry — into b. It is
// the SINGLE rendering shared by WriteGoalMD (creation) and
// EnsureInvestigationConfig (repair-at-dispatch) so the two can never drift;
// TestRenderInvestigationConfig_MatchesWriteGoalMDOutput guards parity. The
// output is byte-identical to the inline loop it was extracted from.
func renderInvestigationConfig(b *strings.Builder, list []Investigator) {
	b.WriteString("\n## Investigation Config\n\n")
	for i, inv := range list {
		fmt.Fprintf(b, "### Investigator %d: %s\n", i+1, inv.Name)
		fmt.Fprintf(b, "- type: %s\n", inv.Type)
		if len(inv.Paths) > 0 {
			fmt.Fprintf(b, "- paths: %s\n", strings.Join(inv.Paths, ", "))
		}
		for _, c := range inv.Commands {
			fmt.Fprintf(b, "- command: %s\n", c)
		}
		fmt.Fprintf(b, "- Pass: %s\n", inv.Pass)
		fmt.Fprintf(b, "- Fail: %s\n", inv.Fail)
		if inv.Condition != "" {
			fmt.Fprintf(b, "- condition: %s\n", inv.Condition)
		}
		b.WriteString("\n")
	}
}

// countInvestigators inspects rendered goal.md markdown for the Investigation
// Config section. hasSection is true when a line is `## Investigation Config`
// (a level-2 heading at line start — `### Investigator` lines never match the
// `## ` prefix); n counts `### Investigator ` headings from that point until the
// next `## ` heading (or EOF). It mirrors the section-scoped `### ` counting
// parseGoalFindings (cmd/tmux-cli/session.go) performs, so EnsureInvestigationConfig
// repairs exactly when that downstream parser — and investigate.xml — would see <2.
func countInvestigators(md string) (hasSection bool, n int) {
	inSection := false
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			if strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Investigation Config" {
				hasSection = true
				inSection = true
			} else if inSection {
				break // the next level-2 heading closes the section
			}
			continue
		}
		if inSection && strings.HasPrefix(line, "### Investigator ") {
			n++
		}
	}
	return hasSection, n
}

// EnsureInvestigationConfig is the B4 repair-at-dispatch guard. It reads
// goalDir/goal.md and guarantees a `## Investigation Config` section with >=2
// `### Investigator` entries — the floor investigate.xml hard-requires — WITHOUT
// rewriting any other byte of the file. WriteGoalMD always emits a valid section
// at CREATION, but a planner re-write of goal.md can strip it post-creation; this
// re-asserts it at dispatch so the validator never hard-fails for missing/<2.
//
// Behavior (never panics, never blocks dispatch):
//   - goal.md unreadable/absent -> (false, nil): creation should have written it,
//     and writeDispatchMd has its own fallback, so repair must not fail dispatch.
//   - section present with n>=2 -> (false, nil): a valid (planner-provided)
//     section is preserved byte-for-byte, never overwritten.
//   - section missing or n<2    -> derive the fallback from validate (the same
//     deriveInvestigators used at creation, padded to >=2 project-aware against
//     projectRoot), render it via the shared renderInvestigationConfig, splice it
//     in (replace a malformed section in place; else insert before
//     `## Re-validation`; else before `## Not In Scope`; else append),
//     atomicWrite, return (true, nil).
//
// SPLICE, never regenerate from the Goal struct: the planner adds prose the struct
// does not carry, so only the one section's byte range is ever touched, and
// exactly one `## Investigation Config` heading always remains.
func EnsureInvestigationConfig(projectRoot, goalDir string, validate []string) (repaired bool, err error) {
	mdPath := filepath.Join(goalDir, "goal.md")
	data, readErr := os.ReadFile(mdPath)
	if readErr != nil {
		// Unreadable/absent: log+continue at the caller, never block dispatch.
		return false, nil
	}
	md := string(data)

	if hasSection, n := countInvestigators(md); hasSection && n >= 2 {
		return false, nil // valid section: preserve verbatim
	} else {
		var sb strings.Builder
		renderInvestigationConfig(&sb, deriveInvestigators(projectRoot, validate))
		newMD := spliceInvestigationConfig(md, sb.String(), hasSection)
		if werr := atomicWrite(mdPath, []byte(newMD), 0o644); werr != nil {
			return false, werr
		}
		return true, nil
	}
}

// spliceInvestigationConfig returns md with the rendered Investigation Config
// section asserted exactly once. When malformedPresent is true it replaces the
// existing `## Investigation Config` section in place (heading -> next `## `
// heading or EOF); otherwise it inserts the section before `## Re-validation`,
// else before `## Not In Scope`, else appends it. Only the one section's byte
// range is touched — every other section is carried through verbatim.
func spliceInvestigationConfig(md, section string, malformedPresent bool) string {
	lines := strings.Split(md, "\n")

	if malformedPresent {
		if start := indexOfHeading(lines, "Investigation Config"); start >= 0 {
			end := len(lines)
			for j := start + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
					end = j
					break
				}
			}
			return joinSections(lines[:start], section, lines[end:])
		}
		// hasSection true but no exact heading match (shouldn't happen): fall
		// through to insertion so the section is still asserted exactly once.
	}

	for _, target := range []string{"Re-validation", "Not In Scope"} {
		if at := indexOfHeading(lines, target); at >= 0 {
			return joinSections(lines[:at], section, lines[at:])
		}
	}
	return joinSections(lines, section, nil)
}

// indexOfHeading returns the index of the first line that is the level-2 heading
// `## <title>` (trimmed), or -1. Matches a level-2 heading only — a `### ` line
// never satisfies the `## ` prefix test.
func indexOfHeading(lines []string, title string) int {
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") && strings.TrimSpace(strings.TrimPrefix(t, "## ")) == title {
			return i
		}
	}
	return -1
}

// joinSections assembles before + section + after with exactly one blank line
// between each and a single trailing newline. It strips boundary blank lines
// (before's trailing, after's leading+trailing) so repeated repairs never
// accumulate blank lines — the spacing is idempotent. The interior bytes of
// before/after are preserved exactly (split on "\n", rejoin on "\n").
func joinSections(before []string, section string, after []string) string {
	for len(before) > 0 && strings.TrimSpace(before[len(before)-1]) == "" {
		before = before[:len(before)-1]
	}
	for len(after) > 0 && strings.TrimSpace(after[0]) == "" {
		after = after[1:]
	}
	for len(after) > 0 && strings.TrimSpace(after[len(after)-1]) == "" {
		after = after[:len(after)-1]
	}
	var parts []string
	if len(before) > 0 {
		parts = append(parts, strings.Join(before, "\n"))
	}
	parts = append(parts, strings.Trim(section, "\n"))
	if len(after) > 0 {
		parts = append(parts, strings.Join(after, "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
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
func deriveInvestigators(projectRoot string, validate []string) []Investigator {
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

// CountRunning returns the number of goals currently GoalRunning — the size of
// the in-flight set. The scheduler subtracts this from MaxGoals to size the free
// dispatch budget for a tick. Pure read; no lock (caller holds the poll flock).
func (gf *GoalsFile) CountRunning() int {
	n := 0
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalRunning {
			n++
		}
	}
	return n
}

// RunningGoalIDs returns the IDs of every GoalRunning goal, in goal-file order.
// The scheduler snapshots this at the TOP of a tick (before driving progress) so
// a goal that completes DURING the tick does not free its slot until the next
// tick — preserving the byte-identical single-goal dispatch cadence (a completion
// and the next dispatch never share a tick). Pure read; no lock.
func (gf *GoalsFile) RunningGoalIDs() []string {
	var out []string
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalRunning {
			out = append(out, gf.Goals[i].ID)
		}
	}
	return out
}

// FirstRunningGoalID returns the ID of the first GoalRunning goal in goal-file
// order, used to keep the scalar CurrentGoal head pointing at a goal that is
// actually in flight when the previous head leaves the running set (MaxGoals>1).
// ok is false when no goal is running. Pure read; no lock.
func (gf *GoalsFile) FirstRunningGoalID() (string, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].Status == GoalRunning {
			return gf.Goals[i].ID, true
		}
	}
	return "", false
}

// FinalGateBlockedByFailed reports whether any phase=final_gate goal is currently
// blocked behind a GoalFailed dependency — the terminal-deadlock signature the
// stall watchdog escalates. A GoalFailed blocker is unrecoverable: no retry and
// no in-flight worker clears it, only `taskvisor goal reset <id>` re-pends it.
// Returns the first such blocker id (in goal order) and n, the count of final
// gates so blocked (n>0 means at least one); ("", 0) when none. Pure read over
// gf.Goals — no mutation, no lock (the caller already holds the poll flock),
// matching RunnableCandidates/AnyRunning. Skips final gates that are themselves
// GoalDone/GoalFailed (their BlockedBy may be stale) and any gate whose BlockedBy
// is empty or names a blocker that is not GoalFailed.
func (gf *GoalsFile) FinalGateBlockedByFailed() (blocker string, n int) {
	for i := range gf.Goals {
		g := &gf.Goals[i]
		if g.Phase != PhaseFinalGate {
			continue
		}
		if g.Status == GoalDone || g.Status == GoalFailed {
			continue
		}
		if g.BlockedBy == "" || gf.statusOf(g.BlockedBy) != GoalFailed {
			continue
		}
		if n == 0 {
			blocker = g.BlockedBy
		}
		n++
	}
	return blocker, n
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
	// Zero ALL FOUR live per-class counters (not just the legacy Retries). With
	// status non-terminal and all four at 0, the LoadGoals re-seed guard
	// (goals.go re-seed block) fires on the next load and restores each from its
	// Max… value — the W1 "zero + re-seed" idiom. Do NOT hand-set to Max… here:
	// that duplicates LoadGoals and would wrongly grant budget when Max… is 0.
	g.CodeRetries = 0
	g.SpecRetries = 0
	g.ValidationRetries = 0
	g.BlockRetries = 0
	// A reset goal starts fresh: clear the explicit next-dispatch routing
	// marker too (RC-D). With no marker and zeroed counters, the next dispatch
	// takes the fresh-goal path (full planner dispatch) via the legacy
	// heuristic — exactly what "zero + re-seed" intends.
	g.NextDispatch = ""
	// Clear the timeout-salvage marker too: a reset goal starts fresh, so the
	// salvage scan must not keep watching (or late-flip) a re-pended goal.
	g.FailedBy = ""
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

		// Re-point a stale BlockedBy->done to the first still-incomplete dep,
		// keeping the goal GoalBlocked. Such a goal (blocker completed but other
		// deps still pending/running) matches neither re-block (no failed dep)
		// nor un-stick (deps unsatisfied), so it stays pinned with BlockedBy
		// naming a GoalDone goal — the exact signature checkInvariant floods on.
		// Gated on !DependsOnSatisfied so deps-satisfied cascade artifacts keep
		// the un-stick re-pend handled above (which already cleared Status).
		if g.Status == GoalBlocked && realIDs[g.BlockedBy] &&
			gf.statusOf(g.BlockedBy) == GoalDone && !g.DependsOnSatisfied(gf.Goals) {
			repointed := "deps_unsatisfied"
			for _, dep := range g.DependsOn {
				if realIDs[dep] && gf.statusOf(dep) != GoalDone {
					repointed = dep
					break
				}
			}
			g.BlockedBy = repointed // Status stays GoalBlocked — goal is not ready.
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

// retrySumAndCeiling computes the global retry-ceiling inputs: the sum of
// CONSUMED per-class budget across all goals (via consumedRetries) and the
// ceiling it is compared against. It deliberately does NOT sum the legacy
// g.Retries scalar — that counter has no live writer (IncrementRetries is
// uncalled post-migration), so summing it left the sum permanently 0 and the
// kill-switch dead. When GlobalMaxRetries is unset (<= 0) the ceiling defaults
// to max(60, len(goals)*3): default per-goal consumable budget is Code5+Spec3+
// Val2 = 10, so total available ≈ 10*N and N*3 halts at ~30% global burn —
// generous enough to never false-trip a healthy run, low enough to catch a
// runaway; the floor of 60 protects small plans. Range by index to take the
// goal's address without copying it.
func (gf *GoalsFile) retrySumAndCeiling() (sum, ceiling int) {
	for i := range gf.Goals {
		sum += consumedRetries(&gf.Goals[i])
	}
	ceiling = gf.GlobalMaxRetries
	if ceiling <= 0 {
		ceiling = max(60, len(gf.Goals)*3)
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

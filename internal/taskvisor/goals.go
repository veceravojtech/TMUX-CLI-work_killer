package taskvisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
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
	// GoalRoadmap is a SKELETON goal: it carries depends_on + phase +
	// DeliverableArea but NO validate/acceptance/scope. The roadmap generator
	// (task-plan-generate, roadmap-only mode) emits these; Tier-2 elaboration
	// authors the concrete fields against the live tree the instant the goal's
	// deps go satisfied (ElaborationCandidates), then flips it to GoalPending.
	// A roadmap goal is INVISIBLE to RunnableCandidates (which gates on
	// GoalPending), so it can never be dispatched to an implementer un-elaborated.
	GoalRoadmap = "roadmap"

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
	// dispatchElaboration routes a GoalRoadmap candidate to the Tier-2 elaborator
	// (/tmux:elaborate) instead of an implementer: it authors the goal's concrete
	// validate/acceptance/scope/goal.md against the now-real tree, then flips the
	// goal to GoalPending. dispatchCandidate selects this BY STATUS (GoalRoadmap),
	// not by a stamped NextDispatch marker — the marker stays reserved for the
	// implementer/generation routes.
	dispatchElaboration = "elaboration"
)

// Goal.Lane values (G5). LaneSolo grants the cheaper single-investigator
// validation lane; LaneFull (or an absent Lane — see LaneOrFull) is the
// deterministic multi-investigator gate. Lane is written ONLY by goal-create
// (authoring) and by the one-way G5 demotion (demoteSoloLane: solo→full,
// never back).
const (
	LaneSolo = "solo"
	LaneFull = "full"
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
	StuckRetries      int `yaml:"stuck_retries,omitempty"`

	MaxCodeRetries       int `yaml:"max_code_retries,omitempty"`
	MaxSpecRetries       int `yaml:"max_spec_retries,omitempty"`
	MaxValidationRetries int `yaml:"max_validation_retries,omitempty"`
	MaxBlockRetries      int `yaml:"max_block_retries,omitempty"`
	MaxStuckRetries      int `yaml:"max_stuck_retries,omitempty"`

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
	// by dispatch/dispatchRetry once the routing decision is acted on.
	// DUAL-STRUCT: mirrored by mcp.tvGoal (tools_taskvisor.go) so an MCP
	// load-resave between bounce and dispatch preserves it.
	NextDispatch string `yaml:"next_dispatch,omitempty"`

	Phase     string   `yaml:"phase,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"`

	// DeliverableArea is the COARSE deliverable footprint of a roadmap-tier goal
	// (e.g. "projects/api/src/Http/ErrorHandling/") — the only concrete hint a
	// skeleton goal carries. depinfer keys produce/consume edges on it at roadmap
	// time, and Tier-2 elaboration seeds its live-tree read from it before
	// authoring the real validate/scope. Empty for legacy fully-specced goals
	// (omitempty → byte-identical on-disk for pre-roadmap plans). DUAL-STRUCT:
	// mirrored by mcp.tvGoal (tools_taskvisor.go), guarded by
	// TestGoalTvGoalYamlTagParity.
	DeliverableArea string `yaml:"deliverable_area,omitempty"`

	// Priority biases dispatch order: RunnableCandidates sorts its output by
	// Priority DESCENDING with a stable file-order tiebreak, so a higher-priority
	// pending goal is admitted ahead of a lower one without physically reordering
	// goals.yaml. Default 0 (key absent via omitempty) preserves byte-identical
	// file-order dispatch; negative priorities sort below the default. DUAL-STRUCT:
	// mirrored field-for-field by mcp.tvGoal (TestGoalTvGoalYamlTagParity guards
	// the mirror). Sorting reorders only the dispatch view (a slice of pointers
	// into gf.Goals) — never the on-disk goals.yaml.
	Priority int `yaml:"priority,omitempty"`

	// Lane selects the validation lane: LaneSolo or LaneFull; absent (empty)
	// means full — LaneOrFull() is the read accessor, so lane-absent goals are
	// byte-identical to today (omitempty emits no key). Written ONLY at
	// goal-create and by the one-way G5 demotion (any validation failure, stuck
	// recovery, or retry flips solo→full permanently). ResetGoal deliberately
	// does NOT clear it: a demotion must survive a re-pend, or a reset would
	// resurrect the solo discount on a goal that already proved it needs the
	// full gate. DUAL-STRUCT: mirrored by mcp.tvGoal (tools_taskvisor.go,
	// guarded by TestGoalTvGoalYamlTagParity) so an MCP load-resave never
	// strips it.
	Lane string `yaml:"lane,omitempty"`

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

	// Validates inverts the dependency relationship: when non-empty it names the
	// IMPLEMENTATION goal id this goal exists to validate, marking THIS goal as a
	// dedicated VALIDATION goal (IsValidationGoal()). A validation goal carries the
	// heavy validate[] stack (phpunit integration, deptrac, phpstan L9, kernel
	// boot) and depends_on its implementer, so the costly checks run in the
	// validation goal's OWN supervising cycle — never inline in the implementer's
	// cycle under the goals+db locks. Its failure is terminal-TO-ITSELF: CascadeFailure
	// short-circuits on it so neither the implementer it validates nor any unrelated
	// downstream impl goal is ever cascade-blocked by a red validation goal.
	// Absent (empty) ⇒ an ordinary goal; omitempty preserves byte-identical
	// existing goals.yaml.
	Validates string `yaml:"validates,omitempty"`

	// Migrates marks a goal that mutates the SHARED database schema (e.g. runs
	// doctrine:migrations:migrate inside its worker). Per-goal worktrees (E1-1a)
	// isolate FILES but NOT the shared DB, so the co-scheduling gate
	// (DisjointReadySet) honors this as a hard exclusion: a Migrates goal runs
	// ALONE — never co-scheduled with any in-flight goal, and no goal is
	// co-scheduled while a Migrates goal is in flight. It is the robust guarantee
	// for in-worker migrations the daemon cannot exec-wrap with WithDBLock; the
	// flock .tmux-cli/db.lock shell wrapper documented in execute.xml/supervisor.xml
	// is best-effort defense-in-depth. Absent ⇒ false (a normal goal).
	Migrates bool `yaml:"migrates,omitempty"`

	// FailedBy records WHY a goal reached GoalFailed when the cause matters
	// post-mortem. "validation-timeout" marks a timeout-SYNTHESIZED failure (no
	// verdict ever arrived): the salvage scan keeps watching signal.json for a
	// late verdict on such goals. Cleared by ResetGoal and by salvage itself.
	FailedBy string `yaml:"failed_by,omitempty"`

	// LastSelfReinstallCycle is the goal cycle (CurrentCycle) whose
	// supervising→validating transition already ran the repair-cycle
	// self-reinstall rebuild (maybeSelfReinstall, selfreinstall.go). Persisted
	// so the at-most-one-rebuild-per-cycle guarantee survives a daemon
	// crash/restart mid-transition; 0 = never rebuilt. Cleared by ResetGoal so
	// a re-pended goal rebuilds on its fresh cycle. DUAL-STRUCT: mirrored by
	// mcp.tvGoal (tools_taskvisor.go) so an MCP load-resave never erases the
	// stamp (TestGoalTvGoalYamlTagParity guards the mirror).
	LastSelfReinstallCycle int `yaml:"last_self_reinstall_cycle,omitempty"`

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

		if g.MaxStuckRetries == 0 && g.Status != GoalFailed && g.Status != GoalDone {
			g.MaxStuckRetries = 3
		}
		if g.StuckRetries == 0 && g.MaxStuckRetries > 0 && g.Status != GoalFailed && g.Status != GoalDone {
			g.StuckRetries = g.MaxStuckRetries
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
		if rmErr := os.Remove(tmp); rmErr != nil {
			log.Printf("atomicWrite: failed to remove stale tmp %q: %v", tmp, rmErr)
		}
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

func (gf *GoalsFile) GoalByID(id string) (*Goal, bool) {
	for i := range gf.Goals {
		if gf.Goals[i].ID == id {
			return &gf.Goals[i], true
		}
	}
	return nil, false
}

// IsValidationGoal reports whether this goal is a dedicated validation goal —
// one that exists solely to run the heavy validate[] checks for the
// implementation goal named in Validates. See the Validates field doc.
func (g *Goal) IsValidationGoal() bool { return g.Validates != "" }

// HasValidationGoalFor reports whether a dedicated validation goal validating
// implGoalID exists in the file. It is the precondition for the
// taskvisor.validation=false DEFER path (deferValidationToSeparateGoal): the
// impl goal may be marked done without an inline validate ONLY when a separate
// validation goal will run the checks in its own cycle — otherwise the caller
// MUST fall through to the inline LLM validator (never a silent false-pass).
func (gf *GoalsFile) HasValidationGoalFor(implGoalID string) bool {
	for i := range gf.Goals {
		if gf.Goals[i].IsValidationGoal() && gf.Goals[i].Validates == implGoalID {
			return true
		}
	}
	return false
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
	// Dispatch-order bias: higher Priority first. SliceStable's stability IS the
	// file-order tiebreak (equal priorities retain gf.Goals order), so an all-zero
	// (default) candidate set is reordered as a no-op — preserving today's
	// byte-identical file-order dispatch. Reorders the pointer slice only, never
	// the backing gf.Goals.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}

// ElaborationCandidates returns the GoalRoadmap goals whose deps are satisfied
// and which are not precondition-parked — the skeleton goals ready to be specced
// against the now-real tree (Tier-2 elaboration). It is the exact sibling of
// RunnableCandidates: same dependency + precondition gate, same Priority-desc
// stable-file-order sort, differing ONLY in the status it admits (GoalRoadmap vs
// GoalPending). DependsOnSatisfied is the single source of "predecessors are real
// now", reused verbatim — an elaborator dispatched from here reads a worktree in
// which its producers have already merged. Pure read over gf.Goals (no mutation,
// no lock — caller holds the poll flock). Empty for any legacy fully-specced
// goals.yaml (no GoalRoadmap rows), so it is inert until the roadmap generator
// emits skeletons.
func (gf *GoalsFile) ElaborationCandidates() []*Goal {
	var out []*Goal
	for i := range gf.Goals {
		g := &gf.Goals[i]
		if g.Status != GoalRoadmap {
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
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
	if !ok || (g.Status != GoalFailed && g.Status != GoalDone) {
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
	g.StuckRetries = 0
	// A reset goal starts fresh: clear the explicit next-dispatch routing
	// marker too (RC-D). With no marker and zeroed counters, the next dispatch
	// takes the fresh-goal path (full planner dispatch) via the legacy
	// heuristic — exactly what "zero + re-seed" intends.
	g.NextDispatch = ""
	// Clear the timeout-salvage marker too: a reset goal starts fresh, so the
	// salvage scan must not keep watching (or late-flip) a re-pended goal.
	g.FailedBy = ""
	// Clear the self-reinstall cycle stamp: with counters zeroed the re-seeded
	// goal restarts at cycle 1, and a stale stamp from a prior life must not
	// suppress the fresh cycle's rebuild.
	g.LastSelfReinstallCycle = 0
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

// LaneOrFull returns the goal's effective validation lane: an empty Lane is
// the full lane, so lane-absent goals behave exactly as before G5.
func (g *Goal) LaneOrFull() string {
	if g.Lane == "" {
		return LaneFull
	}
	return g.Lane
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
	// A validation goal is terminal-TO-ITSELF: it validates an already-Done
	// implementer (depends_on it) and exists only to run heavy checks, so a red
	// validation goal must NOT block the implementer it validates NOR any
	// unrelated downstream impl goal that happens to gate on it. Short-circuit
	// BEFORE the hard/soft branch so the isolation covers BOTH verdict classes.
	if g, ok := gf.GoalByID(failedGoalID); ok && g.IsValidationGoal() {
		log.Printf("%s: validation goal terminal — no cascade (validates %s)", failedGoalID, g.Validates)
		return
	}
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

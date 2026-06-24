package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
	"gopkg.in/yaml.v3"
)

type tvGoal struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Acceptance  []string `yaml:"acceptance,omitempty"`
	Validate    []string `yaml:"validate,omitempty"`

	Preconditions []taskvisor.Precondition `yaml:"preconditions,omitempty"`

	Status     string `yaml:"status"`
	Retries    int    `yaml:"retries"`
	MaxRetries int    `yaml:"max_retries"`

	// Per-class LIVE retry counters (remaining budget) mirroring
	// taskvisor.Goal.CodeRetries/SpecRetries/ValidationRetries/BlockRetries
	// (same yaml keys). DUAL-STRUCT (critical): must stay in lock-step with
	// taskvisor.Goal — the daemon (de)serializes via taskvisor.Goal, these tools
	// via tvGoal; without the mirror every MCP load-resave (GoalCreate,
	// GoalAddPrerequisite) zeroes the counters, and the LoadGoals re-seed guard
	// then wrongly restores a mid-decrement goal to FULL budget.
	CodeRetries       int `yaml:"code_retries,omitempty"`
	SpecRetries       int `yaml:"spec_retries,omitempty"`
	ValidationRetries int `yaml:"validation_retries,omitempty"`
	BlockRetries      int `yaml:"block_retries,omitempty"`
	StuckRetries      int `yaml:"stuck_retries,omitempty"`

	// Per-class retry BUDGETS mirroring taskvisor.Goal.Max…Retries (same yaml
	// keys). DUAL-STRUCT (critical): erasing a budget to 0 corrupts the
	// LoadGoals zero + re-seed invariant (AGENTS.md) — a 0 budget grants 0
	// retries, so a goal whose Max… fields were dropped by an MCP resave gets
	// hard-halted on its first defect instead of consuming its real budget.
	MaxCodeRetries       int `yaml:"max_code_retries,omitempty"`
	MaxSpecRetries       int `yaml:"max_spec_retries,omitempty"`
	MaxValidationRetries int `yaml:"max_validation_retries,omitempty"`
	MaxBlockRetries      int `yaml:"max_block_retries,omitempty"`
	MaxStuckRetries      int `yaml:"max_stuck_retries,omitempty"`

	// Durable C6 code-route convergence circuit-breaker state mirroring
	// taskvisor.Goal.ConvergenceSignatures/ConvergenceStreak (same yaml keys).
	// DUAL-STRUCT (critical): an MCP resave that drops these resets the streak
	// to 0, so a goal looping on identical failure signatures is never halted —
	// the breaker built by sibling goal-020 work (B7) is silently disarmed.
	ConvergenceSignatures []string `yaml:"convergence_signatures,omitempty"`
	ConvergenceStreak     int      `yaml:"convergence_streak,omitempty"`

	// Durable spec-route convergence breaker state mirroring
	// taskvisor.Goal.SpecConvergenceSignatures/SpecConvergenceStreak (same yaml
	// keys), ISOLATED from the code-route pair above. DUAL-STRUCT (critical):
	// dropping these defeats the spec-bounce breaker (sibling B10) exactly like
	// the code-route pair.
	SpecConvergenceSignatures []string `yaml:"spec_convergence_signatures,omitempty"`
	SpecConvergenceStreak     int      `yaml:"spec_convergence_streak,omitempty"`

	// NextDispatch mirrors taskvisor.Goal.NextDispatch (same yaml key) — the
	// RC-D explicit routing marker ("generation"/"implementer") stamped at the
	// verdict seam and consumed by the daemon's next dispatch. DUAL-STRUCT
	// (critical): an MCP resave that drops it degrades a spec bounce back to
	// the sticky codeBudgetConsumed heuristic, re-executing a defective spec
	// verbatim instead of re-planning (guarded by TestGoalTvGoalYamlTagParity).
	NextDispatch string `yaml:"next_dispatch,omitempty"`

	Phase     string   `yaml:"phase,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"`
	// Priority mirrors taskvisor.Goal.Priority (same yaml key, same int type) — the
	// dispatch-order bias RunnableCandidates sorts on (descending, stable file-order
	// tiebreak). DUAL-STRUCT (critical): must stay in lock-step with taskvisor.Goal
	// — without this mirror an MCP load-resave (GoalCreate, GoalAddPrerequisite)
	// silently erases a non-default priority back to 0, and TestGoalTvGoalYamlTagParity
	// fails the build the instant Goal gains the field with no twin here.
	Priority int `yaml:"priority,omitempty"`
	// Lane mirrors taskvisor.Goal.Lane (same yaml key) — the validation-lane
	// marker ("solo"/"full") written by goal-create and the one-way G5
	// demotion. DUAL-STRUCT (critical): without this mirror an MCP load-resave
	// silently strips `lane:`, losing a solo goal's lane (or a demoted goal's
	// permanent full pin) — guarded by TestGoalTvGoalYamlTagParity.
	Lane string `yaml:"lane,omitempty"`
	// EscalationCount mirrors taskvisor.Goal.EscalationCount (same yaml key). It is
	// the durable escalation-prerequisite counter that GoalAddPrerequisite
	// increments and caps. DUAL-STRUCT (critical): must stay in lock-step with
	// taskvisor.Goal — the daemon (de)serializes via taskvisor.Goal, these tools
	// via tvGoal; a drift drops the counter on a round-trip and the cap leaks.
	EscalationCount int `yaml:"escalation_count,omitempty"`
	// Migrates mirrors taskvisor.Goal.Migrates (same yaml key). It marks a goal
	// that mutates the shared database schema; the daemon's co-scheduling
	// exclusion (DisjointReadySet) runs such a goal alone. DUAL-STRUCT
	// (critical): must stay in lock-step with taskvisor.Goal — without this
	// mirror, every MCP load-resave (GoalCreate, GoalAddPrerequisite) silently
	// erases migrates: true and disarms the exclusion at MaxGoals>1.
	Migrates bool `yaml:"migrates,omitempty"`
	// Validates mirrors taskvisor.Goal.Validates (same yaml key). When non-empty
	// it names the implementation goal id this goal validates, marking it a
	// dedicated validation goal (validation-as-goal model): the daemon's
	// CascadeFailure short-circuits on it (terminal-to-itself) so a red
	// validation goal never cascade-blocks its implementer or downstream impl
	// goals. DUAL-STRUCT (critical): without this mirror every MCP load-resave
	// (GoalCreate, GoalAddPrerequisite) silently erases validates: and the
	// daemon re-treats the goal as ordinary — re-arming the very cascade the
	// model removes. Guarded by TestGoalTvGoalYamlTagParity.
	Validates string `yaml:"validates,omitempty"`
	// FailedBy mirrors taskvisor.Goal.FailedBy (same yaml key) — the
	// timeout-salvage marker ("validation-timeout") the daemon's salvage scan
	// keys on to keep watching signal.json for a late verdict. DUAL-STRUCT
	// (critical): an MCP load-resave that drops it stops the scan forever, so a
	// late pass verdict for the marked goal is silently discarded again.
	FailedBy string `yaml:"failed_by,omitempty"`
	// Scope mirrors taskvisor.Goal.Scope (same yaml key) so goal-create persists
	// the disjoint-scope co-scheduling footprint that the daemon reads back.
	Scope     []string `yaml:"scope,omitempty"`
	BlockedBy string   `yaml:"blocked_by,omitempty"`
	// BlockedByPrecondition mirrors taskvisor.Goal.BlockedByPrecondition (same
	// yaml key) — the query key for the §5 auto-resume loop
	// (scanPreconditionBlocked/resumeDownstreamLoop). DUAL-STRUCT (critical):
	// an MCP resave that drops it strands a precondition-blocked goal forever —
	// the resume loop only re-evaluates goals carrying this flag.
	BlockedByPrecondition bool   `yaml:"blocked_by_precondition,omitempty"`
	StartedAt             string `yaml:"started_at,omitempty"`
	FinishedAt            string `yaml:"finished_at,omitempty"`
}

type tvGoalsFile struct {
	CurrentGoal      string   `yaml:"current_goal"`
	GlobalMaxRetries int      `yaml:"global_max_retries,omitempty"`
	Goals            []tvGoal `yaml:"goals"`
}

func tvGoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals.yaml")
}

func tvLoadGoals(projectRoot string) (*tvGoalsFile, error) {
	data, err := os.ReadFile(tvGoalsFilePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gf tvGoalsFile
	if err := yaml.Unmarshal(data, &gf); err != nil {
		return nil, err
	}
	return &gf, nil
}

func tvSaveGoals(projectRoot string, gf *tvGoalsFile) error {
	p := tvGoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(gf)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func withGoalsFileLock(projectRoot string, fn func() error) error {
	return taskvisor.WithGoalsLock(projectRoot, fn)
}

// TaskvisorStart checks for pending goals and writes the taskvisor-start signal file.
func (s *Server) TaskvisorStart() (*TaskvisorStartOutput, error) {
	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goals.yaml not found", ErrInvalidInput)
	}

	hasPending := false
	for _, g := range gf.Goals {
		if g.Status == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return nil, fmt.Errorf("%w: no pending goals in goals.yaml", ErrInvalidInput)
	}

	signalPath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-start")
	if err := os.MkdirAll(filepath.Dir(signalPath), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create directory: %w", ErrInvalidInput, err)
	}
	if err := os.WriteFile(signalPath, []byte("start"), 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}

	return &TaskvisorStartOutput{Started: true}, nil
}

var allowedPhases = map[string]bool{
	"gate": true, "scaffold": true, "fixtures": true, "domain": true,
	"application": true, "infrastructure": true, "action": true, "auth": true,
	"event": true, "cross-cutting": true, "deployment": true, "ci": true, "final": true,
}

// allowedInvestigatorTypes is the enum accepted in an explicit investigation_config.
// It mirrors allowedPhases in shape and MUST stay a superset of the types the
// planner (task-plan-generate.xml) emits — quality-gate, test-execution,
// convention-audit, static-analysis, architecture-check, command,
// environment-check, file-check, implementation-check, integration-check — plus
// code-review (design §3) and e2e-test/integration-test (investigate.xml) — so a
// valid planner config is never rejected (M2 design §88: superset of planner output).
var allowedInvestigatorTypes = map[string]bool{
	"static-analysis": true, "quality-gate": true, "test-execution": true,
	"architecture-check": true, "convention-audit": true, "code-review": true,
	"e2e-test": true, "integration-test": true,
	"command": true, "environment-check": true, "file-check": true,
	"implementation-check": true, "integration-check": true,
	// own-suite-green: the mandatory B2b gate auto-derived in WriteGoalMD. Listed
	// so an explicit planner config MAY legitimately carry it (the dedup target of
	// hasInvestigatorType) without validateInvestigators rejecting it.
	"own-suite-green": true,
}

// validateInvestigators enforces the explicit investigation_config contract: 2–4
// entries, each with a Name, an enum Type, at least one Command, and a Pass.
// Index in messages is 1-based for operator readability. Callers must invoke it
// only when len(invs) > 0 — omission is valid and triggers M1's fallback.
func validateInvestigators(invs []taskvisor.Investigator) error {
	if len(invs) < 2 || len(invs) > 4 {
		return fmt.Errorf("%w: investigation_config requires 2–4 entries (got %d)", ErrInvalidInput, len(invs))
	}
	for i, inv := range invs {
		idx := i + 1
		if inv.Name == "" {
			return fmt.Errorf("%w: investigator[%d]: name is required", ErrInvalidInput, idx)
		}
		if !allowedInvestigatorTypes[inv.Type] {
			names := make([]string, 0, len(allowedInvestigatorTypes))
			for k := range allowedInvestigatorTypes {
				names = append(names, k)
			}
			sort.Strings(names)
			return fmt.Errorf("%w: investigator[%d]: invalid type %q; allowed: %s", ErrInvalidInput, idx, inv.Type, strings.Join(names, ","))
		}
		if len(inv.Commands) == 0 {
			return fmt.Errorf("%w: investigator[%d]: at least one command is required", ErrInvalidInput, idx)
		}
		if inv.Pass == "" {
			return fmt.Errorf("%w: investigator[%d]: pass is required", ErrInvalidInput, idx)
		}
	}
	return nil
}

// GoalCreate generates a sequential goal ID, appends the goal to goals.yaml, creates the goal directory, and writes goal.md.
// MCP-specific enum validation (phase, investigation_config) runs here before
// any filesystem side effect; everything else — the core authoring rules
// (description <=120, >=1 validate, MaxRetries 0→5), ID allocation, FULL
// structured persistence under the goals lock (acceptance/validate/scope now
// land in goals.yaml — F5/RC-A, with the derive-from-acceptance scope
// fallback), and goal.md — is delegated to the shared authoring core
// taskvisor.CreateGoal, converged with the `taskvisor goal add` CLI command.
func (s *Server) GoalCreate(description string, acceptance, validate []string, context, notInScope, phase string, maxRetries int, dependsOn []string, preconditions []taskvisor.Precondition, investigators []taskvisor.Investigator, scope []string, priority int, lane string) (*GoalCreateOutput, error) {
	if phase != "" && !allowedPhases[phase] {
		names := make([]string, 0, len(allowedPhases))
		for k := range allowedPhases {
			names = append(names, k)
		}
		return nil, fmt.Errorf("%w: invalid phase %q; allowed: %s", ErrInvalidInput, phase, strings.Join(names, ","))
	}

	// Thin MCP enum check (mirrors the phase check above) for a typed
	// ErrInvalidInput; the authoritative validation lives in the shared
	// taskvisor.CreateGoal core, protecting any future CLI surface.
	if lane != "" && lane != taskvisor.LaneSolo && lane != taskvisor.LaneFull {
		return nil, fmt.Errorf("%w: invalid lane %q; allowed: %s,%s", ErrInvalidInput, lane, taskvisor.LaneSolo, taskvisor.LaneFull)
	}

	// Solo-lane creation cross-checks (task 47): cheap machine proxies for the
	// lane gate's G2/G3 at the creation seam. The G2 proxy rejects BEFORE the
	// shared core's generic empty-validate error so the message names the
	// cross-check; the G3 proxy warn-logs only (scope spans are heuristic).
	if lane == taskvisor.LaneSolo {
		if len(validate) == 0 {
			return nil, fmt.Errorf("%w: lane=solo requires a non-empty validate list (solo-lane creation cross-check, G2 proxy: solo presumes deterministic validate commands)", ErrInvalidInput)
		}
		if dirs := scopeTopLevelDirs(scope); len(dirs) > 1 {
			log.Printf("warning: lane=solo goal scope spans %d top-level directories (%s) — solo-lane creation cross-check, G3 proxy: solo presumes a localized edit", len(dirs), strings.Join(dirs, ", "))
		}
	}

	// Validate an explicit investigation_config before any filesystem side effect
	// (no goal ID burned, no dir created). Omission (nil/empty) is valid and lets
	// M1's deriveInvestigators fallback run in WriteGoalMD.
	if len(investigators) > 0 {
		if err := validateInvestigators(investigators); err != nil {
			return nil, err
		}
	}

	goalID, _, err := taskvisor.CreateGoal(s.workingDir, taskvisor.GoalSpec{
		Description:   description,
		Acceptance:    acceptance,
		Validate:      validate,
		Context:       context,
		NotInScope:    notInScope,
		Phase:         phase,
		MaxRetries:    maxRetries,
		DependsOn:     dependsOn,
		Preconditions: preconditions,
		Investigators: investigators,
		Scope:         scope,
		Priority:      priority,
		Lane:          lane,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}

	return &GoalCreateOutput{ID: goalID}, nil
}

// scopeTopLevelDirs returns the sorted distinct first path segments of the
// explicit scope entries. Empty, "." and "..."-style wildcard segments are
// skipped — a "./..." glob expresses breadth, not a directory — so only
// concrete top-level paths count toward the solo-lane G3 span proxy.
func scopeTopLevelDirs(scope []string) []string {
	seen := make(map[string]bool, len(scope))
	for _, entry := range scope {
		entry = strings.TrimPrefix(strings.TrimSpace(entry), "./")
		seg, _, _ := strings.Cut(entry, "/")
		if seg == "" || seg == "." || seg == "..." {
			continue
		}
		seen[seg] = true
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// escalationCap bounds the number of escalation-driven prerequisites that may be
// wired onto a single goal via GoalAddPrerequisite, keeping runtime prerequisite
// chains bounded. It mirrors C1a/execute-11's "Open Decision #3 = 2" (the
// supervisor refuses a 3rd ESCALATE for the same goal). If C1a ever makes the cap
// configurable, switch this to that single source.
const escalationCap = 2

// GoalAddPrerequisite rewrites one existing goal's depends_on to include an
// existing prerequisite goal, under the goals-file lock. It is the generation-
// side backstop for an escalation bounce: when an executor escalates a missing
// out-of-scope prerequisite, generation creates the prerequisite goal and calls
// this to make the bounced goal wait on it. Generation is the SINGLE safe writer
// of goals.yaml during a bounce (the daemon is parked in phaseSupervising), so
// the rewrite races nothing — a worker must NEVER call it. Validates both IDs
// exist (reusing GoalCreate's existing-ID wording), rejects self-dependency and
// cycles, is idempotent when the edge already exists, and enforces escalationCap.
func (s *Server) GoalAddPrerequisite(goalID, prerequisiteID string) (*GoalAddPrerequisiteOutput, error) {
	var out GoalAddPrerequisiteOutput
	if err := withGoalsFileLock(s.workingDir, func() error {
		gf, err := tvLoadGoals(s.workingDir)
		if err != nil {
			return fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
		}
		if gf == nil {
			return fmt.Errorf("%w: depends_on references non-existent goal: %s", ErrInvalidInput, goalID)
		}

		// Locate the target goal and confirm BOTH ends exist. Reuse GoalCreate's
		// existing-ID wording (tools_taskvisor.go: depends_on validation) so
		// operators see a consistent message.
		var target *tvGoal
		existingIDs := make(map[string]bool, len(gf.Goals))
		for i := range gf.Goals {
			existingIDs[gf.Goals[i].ID] = true
			if gf.Goals[i].ID == goalID {
				target = &gf.Goals[i]
			}
		}
		if target == nil {
			return fmt.Errorf("%w: depends_on references non-existent goal: %s", ErrInvalidInput, goalID)
		}
		if !existingIDs[prerequisiteID] {
			return fmt.Errorf("%w: depends_on references non-existent goal: %s", ErrInvalidInput, prerequisiteID)
		}

		// Reject a self-dependency.
		if prerequisiteID == goalID {
			return fmt.Errorf("%w: a goal cannot depend on itself: %s", ErrInvalidInput, goalID)
		}

		// Reject a cycle: adding goalID -> prerequisiteID closes a cycle iff
		// prerequisiteID can already reach goalID by following depends_on edges.
		if tvDependsOnReaches(gf.Goals, prerequisiteID, goalID) {
			return fmt.Errorf("%w: adding %s as a prerequisite of %s would create a dependency cycle", ErrInvalidInput, prerequisiteID, goalID)
		}

		// Idempotent: if the edge already exists, succeed without re-incrementing
		// the cap (a benign re-wire must not consume escalation budget).
		for _, dep := range target.DependsOn {
			if dep == prerequisiteID {
				out = GoalAddPrerequisiteOutput{
					DependsOn:       append([]string(nil), target.DependsOn...),
					EscalationCount: target.EscalationCount,
				}
				return nil
			}
		}

		// Enforce the escalation cap BEFORE mutating: a fresh wire pushes the
		// counter to EscalationCount+1; reject when that would exceed the cap so
		// the chain stays bounded and no edit persists.
		if target.EscalationCount+1 > escalationCap {
			return fmt.Errorf("%w: escalation cap %d reached for goal %s", ErrInvalidInput, escalationCap, goalID)
		}

		target.DependsOn = append(target.DependsOn, prerequisiteID)
		target.EscalationCount++
		out = GoalAddPrerequisiteOutput{
			DependsOn:       append([]string(nil), target.DependsOn...),
			EscalationCount: target.EscalationCount,
		}

		return tvSaveGoals(s.workingDir, gf)
	}); err != nil {
		return nil, err
	}
	return &out, nil
}

// tvDependsOnReaches reports whether startID can reach targetID by following
// depends_on edges (DFS over the goal graph). GoalAddPrerequisite uses it as a
// generic cycle guard for hand-edited goals.yaml; the real-world escalation path
// (a freshly-created prerequisite depending only on the scaffold anchor) never
// cycles. The visited set bounds the walk on already-cyclic input.
func tvDependsOnReaches(goals []tvGoal, startID, targetID string) bool {
	deps := make(map[string][]string, len(goals))
	for _, g := range goals {
		deps[g.ID] = g.DependsOn
	}
	visited := make(map[string]bool, len(goals))
	var dfs func(id string) bool
	dfs = func(id string) bool {
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, dep := range deps[id] {
			if dep == targetID || dfs(dep) {
				return true
			}
		}
		return false
	}
	return dfs(startID)
}

// contentlessCorrections is a small, deliberately-short deny-list of stub
// "corrections" that carry no actionable instruction. It is compared after
// strings.TrimSpace + strings.ToLower and folds in the common abbreviated
// spellings of each filler phrase. Maintainers may extend it as new
// contentless stubs surface in the wild.
var contentlessCorrections = map[string]bool{
	"":                 true, // empty (also caught by the TrimSpace check)
	"fix it":           true,
	"none":             true,
	"n/a":              true,
	"na":               true,
	"not applicable":   true,
	"to be determined": true,
	"tbd":              true,
}

// validateFindings enforces the C5 non-lossy contract: every code-defect
// (status==fail) finding must carry concrete failing_command, output_excerpt,
// expected_state and correction detail (correction must also not be a
// contentless stub) so the re-dispatched implementer sees which command failed,
// what it produced and what state was expected. The four fields describe a
// failing COMMAND, which is only meaningful for a code defect — blocked
// (missing precondition) and error (validator could not run) findings, plus the
// daemon's own synthesized error finding, legitimately omit them and are NOT
// enforced here (C1 froze that shape). A pass finding may also leave all four
// empty. The error names all four required fields plus the offending ones; on
// error the caller writes NO signal.
func validateFindings(verdict string, findings []ValidationFinding) error {
	for _, f := range findings {
		// B5a: any structured correction_edit entry must name a target file. An
		// edit with no file is unusable by the downstream applier. Status-agnostic
		// (a remedy may ride any finding); leaves the prose-Correction rules below
		// untouched. Empty list is treated as absent (loop is a no-op).
		for _, e := range f.CorrectionEdits {
			if strings.TrimSpace(e.File) == "" {
				return fmt.Errorf("%w: finding %q has a correction_edit entry with an empty correction_edit.file", ErrInvalidInput, f.Rule)
			}
		}
		if f.Status == taskvisor.VerdictBlocked {
			// C5/WS3: a parked goal's runbook must be actionable — require a
			// concrete remedy. COMMAND-shaped fields stay fail-only (blocked has
			// no failing command). Normalize exactly like the fail branch below.
			if c := strings.ToLower(strings.TrimSpace(f.Correction)); c == "" || contentlessCorrections[c] {
				return fmt.Errorf("%w: blocked finding %q requires a non-empty, non-stub correction (remedy)", ErrInvalidInput, f.Rule)
			}
			continue
		}
		if f.Status != taskvisor.VerdictFail {
			continue
		}
		var bad []string
		if strings.TrimSpace(f.FailingCommand) == "" {
			bad = append(bad, "failing_command")
		}
		if strings.TrimSpace(f.OutputExcerpt) == "" {
			bad = append(bad, "output_excerpt")
		}
		if strings.TrimSpace(f.ExpectedState) == "" {
			bad = append(bad, "expected_state")
		}
		if c := strings.ToLower(strings.TrimSpace(f.Correction)); c == "" || contentlessCorrections[c] {
			bad = append(bad, "correction")
		}
		if len(bad) > 0 {
			return fmt.Errorf("%w: non-pass finding %q requires non-empty failing_command, output_excerpt, expected_state, correction (missing/stub: %s)",
				ErrInvalidInput, f.Rule, strings.Join(bad, ", "))
		}
	}
	return nil
}

// GoalValidationDone validates caller authorization and writes signal.json for the goal.
func (s *Server) GoalValidationDone(goalID, verdict string, findings []ValidationFinding, nextAction string, results []FindingResult) (*GoalValidationDoneOutput, error) {
	// C1 verdict guard — validate the reported verdict/findings shape before any
	// I/O. Verdict strings are referenced via the taskvisor.Verdict* consts so
	// the enum stays single-sourced across packages (no literal duplication).
	//
	// Empty verdict: the validator reported back but produced no verdict. We
	// synthesize an error verdict owned by ops and carry that on a synthesized
	// finding so the daemon routes it as a validator error (re-validate only),
	// never as a code defect. This is the caller-reported synthesis site; the
	// daemon watchdog (taskvisor.go ~:1044) is the distinct never-reported site.
	if verdict == "" {
		verdict = taskvisor.VerdictError
		findings = append(findings, ValidationFinding{
			Rule:         "validator",
			Status:       taskvisor.VerdictError,
			FailureClass: "validator-error",
			Owner:        "ops",
			Detail:       "validator reported an empty verdict",
		})
	}
	allowed := map[string]bool{
		taskvisor.VerdictPass:    true,
		taskvisor.VerdictFail:    true,
		taskvisor.VerdictBlocked: true,
		taskvisor.VerdictError:   true,
	}
	if !allowed[verdict] {
		return nil, fmt.Errorf("%w: verdict must be one of %s, %s, %s, %s; got %q",
			ErrInvalidInput, taskvisor.VerdictPass, taskvisor.VerdictFail, taskvisor.VerdictBlocked, taskvisor.VerdictError, verdict)
	}
	for _, f := range findings {
		if f.Status != taskvisor.VerdictPass && strings.TrimSpace(f.FailureClass) == "" {
			return nil, fmt.Errorf("%w: finding %q has status %q but no failure_class (required when status is not pass)",
				ErrInvalidInput, f.Rule, f.Status)
		}
	}

	// C5 non-lossy guard — every code-defect (fail) finding must carry concrete
	// failing_command/output_excerpt/expected_state/correction detail so the
	// re-dispatched implementer never re-discovers the failure blind. Runs at the
	// top, before any I/O, so a malformed report writes NO signal.json.
	if err := validateFindings(verdict, findings); err != nil {
		return nil, err
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	found := false
	for _, g := range gf.Goals {
		if g.ID == goalID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Resolve the validator window the daemon spawned for THIS goal. At
	// MaxGoals>1 the daemon suffixes the name per goal ("validator-046"), so a
	// hard-coded "validator" match left those windows unsignalable ("no validator
	// window found"). ValidatorWindowNames yields both the namespaced and bare
	// forms (most-specific first) from the daemon's own naming helper; the UUID
	// check below is the real authorization gate.
	wantNames := taskvisor.ValidatorWindowNames(goalID)
	var validatorWindow *tmux.WindowInfo
	for _, want := range wantNames {
		for i := range windows {
			if windows[i].Name == want {
				validatorWindow = &windows[i]
				break
			}
		}
		if validatorWindow != nil {
			break
		}
	}
	if validatorWindow == nil {
		return nil, fmt.Errorf("%w: no validator window found (expected one of %v)", ErrInvalidInput, wantNames)
	}

	validatorUUID, err := s.executor.GetWindowOption(sessionID, validatorWindow.TmuxWindowID, tmux.WindowUUIDOption)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read validator window UUID: %w", ErrTmuxCommandFailed, err)
	}

	callerUUID := os.Getenv("TMUX_WINDOW_UUID")
	if callerUUID != validatorUUID {
		return nil, fmt.Errorf("%w: caller is not the validator window (caller=%s, validator=%s)", ErrInvalidInput, callerUUID, validatorUUID)
	}

	sig := validatorSignalJSON{
		Source:     "validator",
		Verdict:    verdict,
		Findings:   findings,
		NextAction: nextAction,
		Timestamp:  time.Now().Format(time.RFC3339),
	}

	signalData, err := json.Marshal(sig)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal signal: %w", ErrInvalidInput, err)
	}

	signalDir := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(signalDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create goal directory: %w", ErrInvalidInput, err)
	}

	signalPath := filepath.Join(signalDir, "signal.json")
	tmpPath := signalPath + ".tmp"
	if err := os.WriteFile(tmpPath, signalData, 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}
	if err := os.Rename(tmpPath, signalPath); err != nil {
		return nil, fmt.Errorf("%w: failed to rename signal file: %w", ErrInvalidInput, err)
	}

	// C10 — orchestrator-owned results.json write path. When the caller supplies
	// per-finding re-validation inputs, compute each finding's input fingerprint
	// (rule + in-scope changed files + preconditions denormalized from the goal)
	// and persist the cycle's ledger. The daemon only READS results.json; this
	// MCP path (triggered by the validator orchestrator at cycle end) is the sole
	// writer. Absent results leave any prior ledger untouched.
	if len(results) > 0 {
		if err := s.writeRevalidationResults(goalID, results); err != nil {
			return nil, err
		}
	}

	return &GoalValidationDoneOutput{Written: true}, nil
}

// writeRevalidationResults computes input fingerprints for the supplied
// per-finding results and writes the orchestrator-owned results.json ledger.
func (s *Server) writeRevalidationResults(goalID string, results []FindingResult) error {
	// Load the full goal to denormalize preconditions onto each finding and to
	// derive the current cycle number from consumed code-defect budget.
	preconds := []string{}
	cycle := 1
	if gf, err := taskvisor.LoadGoals(s.workingDir); err == nil && gf != nil {
		if g, ok := gf.GoalByID(goalID); ok {
			preconds = stringifyPreconditions(g.Preconditions)
			if c := g.MaxCodeRetries - g.CodeRetries + 1; c > cycle {
				cycle = c
			}
		}
	}

	ledger := &taskvisor.Results{Results: make(map[string]taskvisor.ResultEntry, len(results))}
	for _, r := range results {
		f := taskvisor.ValidationFinding{
			Rule:          r.ID,
			Status:        r.Status,
			Scope:         r.ScopeFiles,
			Preconditions: preconds,
		}
		ledger.Results[r.ID] = taskvisor.ResultEntry{
			FindingID:        r.ID,
			Status:           r.Status,
			InputFingerprint: taskvisor.ComputeInputFingerprint(f, r.ChangedFiles),
			CycleNumber:      cycle,
		}
	}

	if err := taskvisor.SaveResults(s.workingDir, goalID, ledger); err != nil {
		return fmt.Errorf("%w: failed to write results.json: %w", ErrInvalidInput, err)
	}
	return nil
}

// stringifyPreconditions renders a goal's preconditions into a deterministic,
// sorted "<kind>:<spec>" slice so they can ride on a finding for fingerprinting.
func stringifyPreconditions(pcs []taskvisor.Precondition) []string {
	out := make([]string, 0, len(pcs))
	for _, p := range pcs {
		out = append(out, p.Kind+":"+p.Spec)
	}
	sort.Strings(out)
	return out
}

// GoalPrune atomically removes all taskvisor goal state.
func (s *Server) GoalPrune() (*GoalPruneOutput, error) {
	activePath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(activePath); err == nil {
		return nil, fmt.Errorf("%w: taskvisor daemon is active — stop it first", ErrInvalidInput)
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	count := 0
	if gf != nil {
		count = len(gf.Goals)
	}

	goalsFile := tvGoalsFilePath(s.workingDir)
	if err := os.Remove(goalsFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: failed to remove goals.yaml: %w", ErrInvalidInput, err)
	}

	goalsDir := filepath.Join(s.workingDir, ".tmux-cli", "goals")
	if err := os.RemoveAll(goalsDir); err != nil {
		return nil, fmt.Errorf("%w: failed to remove goals directory: %w", ErrInvalidInput, err)
	}

	for _, name := range []string{"taskvisor-current-goal", "taskvisor-start", "taskvisor-current-cycle", "taskvisor-current-worktree"} {
		p := filepath.Join(s.workingDir, ".tmux-cli", name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: failed to remove %s: %w", ErrInvalidInput, name, err)
		}
	}

	return &GoalPruneOutput{Pruned: true, GoalsRemoved: count}, nil
}

type validatorSignalJSON struct {
	Source     string              `json:"source"`
	Verdict    string              `json:"verdict"`
	Findings   []ValidationFinding `json:"findings"`
	NextAction string              `json:"next_action"`
	Timestamp  string              `json:"timestamp"`
}

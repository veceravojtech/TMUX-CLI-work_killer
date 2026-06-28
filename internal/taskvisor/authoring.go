package taskvisor

import (
	"fmt"
	"strings"
)

// Shared goal-authoring core (F5). Both creation surfaces — the `taskvisor
// goal add` CLI command and the MCP goal-create tool — converge here instead
// of carrying parallel persistence blocks. Before F5 the CLI persisted only
// ID/Description/Status/MaxRetries/Phase and silently dropped
// acceptance/validate from the structured Goal (RC-A: the daemon reads them
// from goals.yaml — EnsureInvestigationConfig falls back to Validate when
// goal.md's Investigation Config needs repair, and the own-suite gate mines
// Acceptance+Validate — so dropped fields degrade every later cycle).

// GoalSpec is the structured input of CreateGoal. Context and NotInScope have
// no structured Goal fields — they are goal.md prose only, same as before.
type GoalSpec struct {
	Description     string
	Acceptance      []string
	Validate        []string
	Context         string
	NotInScope      string
	Phase           string
	MaxRetries      int
	MaxStuckRetries int
	DependsOn       []string
	Preconditions   []Precondition
	Investigators   []Investigator
	Scope           []string
	Priority        int
	// Lane is the validation lane ("solo"/"full"); empty means full. Validated
	// here (the shared authoring core) so every creation surface — the MCP
	// goal-create tool and any future CLI flag — enforces the same enum.
	Lane string
	// Status selects the creation tier. Empty (the default) creates a normal
	// GoalPending goal exactly as before. GoalRoadmap creates a Tier-1 SKELETON:
	// no validate/acceptance/scope is required or authored — the
	// roadmap generator emits only id/description/phase/depends_on/DeliverableArea,
	// and Tier-2 elaboration authors the concrete fields later (design §5). Any
	// other value is rejected (running/done/failed are daemon-owned).
	Status string
	// DeliverableArea is the coarse roadmap-tier deliverable footprint persisted on
	// a skeleton goal (e.g. "projects/api/src/Http/ErrorHandling/"). Ignored for
	// normal (non-roadmap) creation.
	DeliverableArea string
}

// validateGoalSpec enforces the core-owned authoring rules shared by every
// creation surface: the description is a short title (max 120 chars, the
// AGENTS.md invariant — detail belongs in Acceptance/Validate) and at least
// one validation rule is required (an empty Validate is RC-A's trigger for
// the blind investigator pad).
//
// Validate steps are written into goal.md as the checks the LLM validator
// performs when grading the goal. They are GUIDANCE for the validator — not
// compiled into a script or executed by the daemon. A declared-validate goal
// therefore needs validate entries the validator can actually run and judge
// against, or every cycle re-validates until the validation budget is exhausted.
func validateGoalSpec(spec GoalSpec) error {
	if spec.Description == "" {
		return fmt.Errorf("description cannot be empty")
	}
	if len(spec.Description) > 120 {
		return fmt.Errorf("description exceeds 120 characters (got %d); use --acceptance for detailed criteria", len(spec.Description))
	}
	// Roadmap skeletons (Tier-1) legitimately carry NO validate — elaboration
	// authors it later (design §5). Only "" (normal GoalPending) and GoalRoadmap
	// are creatable; daemon-owned lifecycle statuses are rejected.
	if spec.Status != "" && spec.Status != GoalRoadmap {
		return fmt.Errorf("invalid creation status %q; only %q (roadmap skeleton) or empty (pending) may be created", spec.Status, GoalRoadmap)
	}
	if spec.Status != GoalRoadmap && len(spec.Validate) == 0 {
		return fmt.Errorf("at least one validation rule is required")
	}
	if spec.Lane != "" && spec.Lane != LaneSolo && spec.Lane != LaneFull {
		return fmt.Errorf("invalid lane %q; allowed values: %s, %s", spec.Lane, LaneSolo, LaneFull)
	}
	return nil
}

// CreateGoal validates spec, allocates the next sequential goal ID, persists
// the FULL structured Goal under WithGoalsLock, then writes goal.md (same
// write order as the pre-F5 call sites: goals.yaml first, prose second). It
// returns the new goal ID and whether the persisted Scope was derived.
//
// Scope fallback: when spec.Scope is empty, the footprint is derived via
// DeriveScopeFromDeliverables over Acceptance ONLY — validate commands are
// too noisy to mine (runner flags, ./... wildcards, tool paths). A nil
// derivation stays nil: unknown scope makes DisjointReadySet serialize the
// goal against all concurrent goals, the conservative contract.
//
// MaxRetries 0 coerces to the default 5 (LoadGoals migrates it into the
// per-class budgets Code 5 / Spec 3 / Val 2 / Block 0).
//
// Authoring guidance: the spec's validate steps are the checks the LLM validator
// performs — a declared-validate goal needs validate entries the validator can
// run and judge against, or its pass is withheld and the goal is re-validated.
func CreateGoal(workDir string, spec GoalSpec) (string, bool, error) {
	if err := validateGoalSpec(spec); err != nil {
		return "", false, err
	}

	maxRetries := spec.MaxRetries
	if maxRetries == 0 {
		maxRetries = 5
	}

	maxStuckRetries := spec.MaxStuckRetries
	if maxStuckRetries == 0 {
		maxStuckRetries = 3
	}

	scope := spec.Scope
	derivedScope := false
	if len(scope) == 0 {
		// Auto-derive only a COMPLETE footprint. An incomplete derivation (some
		// non-empty acceptance line named no path) is downgraded to UNKNOWN so
		// the goal serializes instead of FALSELY passing ScopesDisjoint with a
		// partial scope. The bool return therefore means "a COMPLETE derived
		// scope was persisted." Explicit spec.Scope is never touched.
		if derived, incomplete, _ := DeriveScopeWithCompleteness(spec.Acceptance); len(derived) > 0 && !incomplete {
			scope = derived
			derivedScope = true
		}
	}

	// Lane auto-derivation (task 70): a caller that passed NO explicit lane gets
	// the lane classified here, in the shared core, so the MCP goal-create tool
	// and the `taskvisor goal add` CLI both inherit it. An explicit spec.Lane is
	// never touched. The derivation runs AFTER validateGoalSpec (guarantees >=1
	// validate for G2's deriveInvestigators) and AFTER the scope block, so it
	// consumes the SAME effective scope the goal persists — no second derivation.
	// A non-solo result stays "" (LaneOrFull → full): omitempty zero-change.
	lane := spec.Lane
	if lane == "" {
		lane = AutoDeriveLane(workDir, spec, scope)
	}

	// Resolve the persisted status once (validated above): GoalRoadmap for a
	// Tier-1 skeleton, GoalPending otherwise. Hoisted out of the lock so the
	// post-lock goal.md tail can branch on it.
	status := GoalPending
	if spec.Status == GoalRoadmap {
		status = GoalRoadmap
	}

	var id string
	if err := WithGoalsLock(workDir, func() error {
		gf, err := LoadGoals(workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			gf = &GoalsFile{}
		}

		if len(spec.DependsOn) > 0 {
			existingIDs := make(map[string]bool, len(gf.Goals))
			for _, g := range gf.Goals {
				existingIDs[g.ID] = true
			}
			for _, dep := range spec.DependsOn {
				if !existingIDs[dep] {
					return fmt.Errorf("depends_on references non-existent goal: %s", dep)
				}
			}
		}

		id = NextGoalID(gf.Goals)
		budget := MigrateRetries(maxRetries)
		gf.Goals = append(gf.Goals, Goal{
			ID:                   id,
			Description:          spec.Description,
			Acceptance:           spec.Acceptance,
			Validate:             spec.Validate,
			DeliverableArea:      spec.DeliverableArea,
			Preconditions:        spec.Preconditions,
			Status:               status,
			MaxRetries:           maxRetries,
			CodeRetries:          budget.CodeRetries,
			SpecRetries:          budget.SpecRetries,
			ValidationRetries:    budget.ValidationRetries,
			BlockRetries:         budget.BlockRetries,
			MaxCodeRetries:       budget.CodeRetries,
			MaxSpecRetries:       budget.SpecRetries,
			MaxValidationRetries: budget.ValidationRetries,
			MaxBlockRetries:      budget.BlockRetries,
			MaxStuckRetries:      maxStuckRetries,
			StuckRetries:         maxStuckRetries,
			Phase:                spec.Phase,
			DependsOn:            spec.DependsOn,
			Scope:                scope,
			Priority:             spec.Priority,
			Lane:                 lane,
		})

		return SaveGoals(workDir, gf)
	}); err != nil {
		return "", false, err
	}

	goalDir, err := EnsureGoalDir(workDir, id)
	if err != nil {
		return "", false, fmt.Errorf("create goal directory: %w", err)
	}
	if err := WriteGoalMD(goalDir, spec.Description, spec.Phase, lane, spec.Acceptance, spec.Validate, spec.Preconditions, spec.Context, spec.NotInScope, spec.Investigators); err != nil {
		return "", false, fmt.Errorf("write goal.md: %w", err)
	}
	return id, derivedScope, nil
}

// GoalEdit carries the OPTIONAL field edits applied to an existing goal by
// EditGoal — the Tier-2 elaboration write-back primitive (design §6 step 6). Each
// field is a POINTER so the caller expresses a tri-state per field:
//   - nil           → leave the existing value untouched.
//   - non-nil value → set the field to it (an empty slice or "" CLEARS it).
//
// This is what lets elaboration author a roadmap skeleton's concrete fields and
// flip its status roadmap→pending in one converged call, while never disturbing
// the daemon-owned durable state (retry counters, convergence streaks, lane,
// depends_on) it does not name. Description is deliberately NOT editable here —
// it is the goal's stable title; detail belongs in Acceptance/Validate.
type GoalEdit struct {
	Acceptance      *[]string
	Validate        *[]string
	Scope           *[]string
	Status          *string
	DeliverableArea *string
	Phase           *string
}

// editableGoalStatuses are the ONLY statuses EditGoal (and thus the goal-edit MCP
// tool / `taskvisor goal edit` CLI) may write. running/done/failed are
// daemon-owned lifecycle states — an authoring tool that wrote them would corrupt
// the daemon's runtime view of a goal — so they are rejected. roadmap/pending/
// blocked are the authoring-tier statuses elaboration legitimately moves a goal
// between.
var editableGoalStatuses = map[string]bool{
	GoalRoadmap: true,
	GoalPending: true,
	GoalBlocked: true,
}

// EditGoal applies the provided GoalEdit to an EXISTING goal, under the goals
// flock, via the SAME canonical LoadGoals→edit→SaveGoals path CreateGoal uses —
// so the dual-struct durable-field invariant and the LoadGoals retry re-seed both
// hold (the full taskvisor.Goal is loaded and resaved; no field is dropped). It
// is the shared core behind both the goal-edit MCP tool and the `taskvisor goal
// edit` CLI (authoring convergence — never duplicate the apply logic in either
// surface).
//
// Only fields the caller set (non-nil) are written; omitted fields are left
// exactly as the daemon last persisted them. An explicit empty slice/string
// CLEARS its field (e.g. dropping a stale scope). The status guard runs FIRST,
// before the lock, so an invalid target status persists nothing.
//
// EditGoal intentionally does NOT regenerate goal.md: the daemon repairs
// goal.md from the in-memory goal.Validate on every (re-)dispatch
// (dispatch.go spec-drift gate), and the elaborator worker authors
// goal.md itself (design §6 step 5). Keeping EditGoal to a goals.yaml field write
// holds the surface minimal and single-responsibility.
func EditGoal(workDir, goalID string, edit GoalEdit) error {
	if edit.Status != nil && !editableGoalStatuses[*edit.Status] {
		return fmt.Errorf("status %q is not editable; goal-edit may only set %s/%s/%s (running/done/failed are daemon-owned)",
			*edit.Status, GoalRoadmap, GoalPending, GoalBlocked)
	}

	return WithGoalsLock(workDir, func() error {
		gf, err := LoadGoals(workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if gf == nil {
			return fmt.Errorf("goal not found: %s", goalID)
		}
		g, ok := gf.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("goal not found: %s", goalID)
		}

		if edit.Acceptance != nil {
			g.Acceptance = *edit.Acceptance
		}
		if edit.Validate != nil {
			g.Validate = *edit.Validate
		}
		if edit.Scope != nil {
			g.Scope = *edit.Scope
		}
		if edit.DeliverableArea != nil {
			g.DeliverableArea = *edit.DeliverableArea
		}
		if edit.Phase != nil {
			g.Phase = *edit.Phase
		}
		if edit.Status != nil {
			g.Status = *edit.Status
		}

		return SaveGoals(workDir, gf)
	})
}

// criticalPriorityTier is the priority floor (G4) above which a goal is treated
// as critical and is NEVER auto-assigned the cheaper solo lane. A goal with
// Priority >= this tier always validates on the full multi-investigator lane.
// The threshold mirrors the dispatch / lane-gate evaluation-source definition of
// "critical" as priority < 10. Named const, not a bare literal (spec Boundaries).
const criticalPriorityTier = 10

// AutoDeriveLane decides the validation lane for a goal whose caller passed NO
// explicit lane. It returns LaneSolo only when the conservative solo gate holds
// (see autoDeriveSoloLane), otherwise "" — which LaneOrFull resolves to full, so
// an auto-derived full goal stays lane-absent (omitempty zero-change). The rule
// lives here in the shared authoring core, so the MCP goal-create tool and the
// `taskvisor goal add` CLI both inherit it without duplication. CreateGoal calls
// this AFTER validateGoalSpec and the scope block, so the derivation consumes the
// SAME effective scope the goal persists.
func AutoDeriveLane(projectRoot string, spec GoalSpec, scope []string) string {
	if autoDeriveSoloLane(projectRoot, spec, scope) {
		return LaneSolo
	}
	return ""
}

// autoDeriveSoloLane is the conservative predicate behind AutoDeriveLane. It
// biases HARD toward full — a false-solo runs inline (supervisor step 3c) with
// no recovery on the implementation side — so it returns true (⇒ solo) ONLY when
// EVERY gate holds:
//   - G4: spec.Priority < criticalPriorityTier (not a critical goal).
//   - G2: deriveInvestigators yields >=1 investigator AND every one is a pure
//     command (a semantic validate like `bin/console`/`db-validate` ⇒ full).
//   - G3: the effective scope is a single top-level dir AND no new-artifact
//     marker (new file / new public API / schema / migration) is present —
//     fails closed (zero or >1 top-level dirs ⇒ NOT solo).
//
// (G1 — exactly one CreateGoal call — is implicit at this call site.)
func autoDeriveSoloLane(projectRoot string, spec GoalSpec, scope []string) bool {
	if spec.Priority >= criticalPriorityTier { // G4
		return false
	}
	invs := deriveInvestigators(projectRoot, spec.Validate, scope) // G2
	if len(invs) == 0 {
		return false
	}
	for _, inv := range invs {
		if !IsPureCommand(inv) {
			return false
		}
	}
	if !laneScopeSingleTopLevelDir(scope) { // G3 span (fails closed)
		return false
	}
	if hasNewArtifactMarker(spec.Acceptance, spec.Context) { // G3 artifact
		return false
	}
	return true
}

// laneScopeSingleTopLevelDir reports whether scope resolves to EXACTLY ONE
// distinct top-level directory. It mirrors internal/mcp's scopeTopLevelDirs
// (trim a leading "./", take the first segment via strings.Cut, skip
// ""/"."/"..." wildcard segments). Fails closed for G3: zero OR more than one
// distinct top-level dir ⇒ false (not solo).
func laneScopeSingleTopLevelDir(scope []string) bool {
	seen := make(map[string]bool, len(scope))
	for _, entry := range scope {
		entry = strings.TrimPrefix(strings.TrimSpace(entry), "./")
		seg, _, _ := strings.Cut(entry, "/")
		if seg == "" || seg == "." || seg == "..." {
			continue
		}
		seen[seg] = true
	}
	return len(seen) == 1
}

// hasNewArtifactMarker reports whether the acceptance lines or context prose
// signal a NEW artifact whose creation argues for the full lane: a new file, a
// new public API/interface/class, or a schema/migration. The scan is
// case-insensitive over acceptance + context, and CONSERVATIVE — any single hit
// ⇒ true (⇒ full).
func hasNewArtifactMarker(acceptance []string, context string) bool {
	var b strings.Builder
	for _, line := range acceptance {
		b.WriteString(strings.ToLower(line))
		b.WriteByte('\n')
	}
	b.WriteString(strings.ToLower(context))
	low := b.String()
	if strings.Contains(low, "new file") ||
		strings.Contains(low, "new public") ||
		strings.Contains(low, "public api") ||
		strings.Contains(low, "schema") ||
		strings.Contains(low, "migration") {
		return true
	}
	// "create … interface/class" — a new public type, not an edit to one.
	if strings.Contains(low, "create ") &&
		(strings.Contains(low, "interface") || strings.Contains(low, "class")) {
		return true
	}
	return false
}

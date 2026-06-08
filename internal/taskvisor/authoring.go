package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
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
}

// validateGoalSpec enforces the core-owned authoring rules shared by every
// creation surface: the description is a short title (max 120 chars, the
// AGENTS.md invariant — detail belongs in Acceptance/Validate) and at least
// one validation rule is required (an empty Validate is RC-A's trigger for
// the blind investigator pad).
//
// P7: validate steps are now LOAD-BEARING for a terminal pass. A goal that
// declares validate steps cannot terminally `pass` on the LLM validator's
// judgment alone — GateTerminalPass (signal.go) downgrades such a pass to
// error/ops unless the deterministic validate.sh exits 0. A declared-validate
// goal therefore needs a working, executable validate.sh, or every cycle
// re-validates until the validation budget is exhausted.
func validateGoalSpec(spec GoalSpec) error {
	if spec.Description == "" {
		return fmt.Errorf("description cannot be empty")
	}
	if len(spec.Description) > 120 {
		return fmt.Errorf("description exceeds 120 characters (got %d); use --acceptance for detailed criteria", len(spec.Description))
	}
	if len(spec.Validate) == 0 {
		return fmt.Errorf("at least one validation rule is required")
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
// Authoring guidance: the spec's validate steps gate the terminal pass (P7) — a
// declared-validate goal needs a working validate.sh that exits 0, or the LLM
// validator's pass is downgraded to error/ops and re-validated.
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
			Preconditions:        spec.Preconditions,
			Status:               GoalPending,
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
		})

		return SaveGoals(workDir, gf)
	}); err != nil {
		return "", false, err
	}

	goalDir, err := EnsureGoalDir(workDir, id)
	if err != nil {
		return "", false, fmt.Errorf("create goal directory: %w", err)
	}
	if err := WriteGoalMD(goalDir, spec.Description, spec.Phase, spec.Acceptance, spec.Validate, spec.Preconditions, spec.Context, spec.NotInScope, spec.Investigators); err != nil {
		return "", false, fmt.Errorf("write goal.md: %w", err)
	}
	if err := WriteValidateScript(goalDir, spec.Validate); err != nil {
		return "", false, fmt.Errorf("write validate.sh: %w", err)
	}

	return id, derivedScope, nil
}

// WriteValidateScript generates an executable validate.sh in goalDir from the
// goal's validate rules. Each rule becomes a line in a set -e script so any
// failing command fails the whole validation. P7's GateTerminalPass requires
// this script to exit 0 for a terminal pass — without it, every LLM-validator
// pass is downgraded to error/ops and burns ValidationRetries.
func WriteValidateScript(goalDir string, rules []string) error {
	if len(rules) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	for _, r := range rules {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(goalDir, "validate.sh"), []byte(b.String()), 0o755)
}

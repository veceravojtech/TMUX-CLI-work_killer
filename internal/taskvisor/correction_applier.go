package taskvisor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/console/tmux-cli/internal/tasks"
	"gopkg.in/yaml.v3"
)

// correction_applier.go — B5b mechanical-correction applier.
//
// When a validator finding is a *mechanical* spec-artifact defect (a wrong
// path/string in goal.md or a dispatch spec), the default planner-bounce route
// (bounceToGeneration) burns the single scarce SpecRetries and re-runs the whole
// LLM planner for what is a one-line edit. This file intercepts that branch: when
// the failing findings carry a structured correction_edit (execute-7/B5a's
// CorrectionEdit schema) CONFINED to spec artifacts, the daemon applies the edits
// directly (idempotent), re-validates the goal, and charges ZERO budget. Absent,
// out-of-scope, or ineffective (no on-disk change) edits fall back to the
// unchanged bounceToGeneration so the no-budget loop is always budget-bounded.

// applyStructuredCorrections is the B5b entry point invoked from the verdict
// router's blocked/planner case BEFORE bounceToGeneration. It returns
// handled=true ONLY when it both applied at least one real on-disk change to a
// spec artifact AND queued a re-validation (zero budget charged). It returns
// (false, nil) — meaning "fall through to bounceToGeneration" — when:
//   - valSig is nil or carries no correction_edit on any non-pass finding;
//   - any edit targets a path outside the spec-artifact allowlist (refuse all);
//   - an edit is malformed / refuses (both old & new unusable);
//   - every edit is a no-op (no file changed) — ineffective, so the loop must
//     converge to the budget-charging bounce rather than re-validate for free.
//
// It NEVER decrements any retry counter; budget semantics on the fallback path
// stay entirely owned by bounceToGeneration.
func (d *Daemon) applyStructuredCorrections(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) (handled bool, err error) {
	if valSig == nil {
		return false, nil
	}

	// Collect structured edits from NON-pass findings only (a pass finding carries
	// no defect to correct). Prose-only findings contribute nothing here.
	var edits []CorrectionEdit
	for _, f := range valSig.Findings {
		if f.Status == VerdictPass {
			continue
		}
		edits = append(edits, f.CorrectionEdits...)
	}
	if len(edits) == 0 {
		return false, nil // prose-only / no structured remedy → bounce (back-compat)
	}

	allow, err := d.specArtifactPaths(goal)
	if err != nil {
		return false, err
	}

	// Resolve every edit to an absolute path and confirm it is a spec artifact
	// BEFORE writing anything. A single out-of-scope target refuses the WHOLE set
	// (no partial application of a batch that includes a forbidden file).
	type resolvedEdit struct {
		abs  string
		edit CorrectionEdit
	}
	resolved := make([]resolvedEdit, 0, len(edits))
	for _, e := range edits {
		if strings.TrimSpace(e.File) == "" {
			return false, nil // malformed (empty file) → bounce
		}
		abs := e.File
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(d.workDir, e.File)
		}
		abs = filepath.Clean(abs)
		if !isSpecArtifact(abs, allow, d.workDir) {
			log.Printf("%s: correction_edit target %q is outside the spec-artifact allowlist — refusing, falling back to bounce", goal.ID, e.File)
			return false, nil // out-of-scope → bounce, touch nothing
		}
		resolved = append(resolved, resolvedEdit{abs: abs, edit: e})
	}

	// Apply every (in-scope) edit. Track whether ANY file actually changed: an
	// all-no-op batch is ineffective and MUST fall back to the budget-charging
	// bounce, otherwise an edit that never fixes the finding would re-validate for
	// free forever.
	anyChanged := false
	for _, re := range resolved {
		changed, aerr := applyCorrectionEdit(re.abs, re.edit)
		if aerr != nil {
			// A refused / unreadable edit is a routing decision, not a daemon error:
			// fall back to bounce rather than crash the tick.
			log.Printf("%s: correction_edit refused (%v) — falling back to bounce", goal.ID, aerr)
			return false, nil
		}
		if changed {
			anyChanged = true
		}
	}
	if !anyChanged {
		log.Printf("%s: correction_edit batch produced no on-disk change (ineffective) — falling back to bounce", goal.ID)
		return false, nil // ineffective → bounce (loop is budget-bounded)
	}

	if err := d.revalidateAfterCorrection(goal, goals); err != nil {
		return false, err
	}
	return true, nil
}

// specArtifactPaths returns the absolute-path allowlist the applier is permitted
// to edit for this goal: the goal's goal.md PLUS every dispatch-spec context path
// declared in the PER-GOAL tasks.yaml (the same source injectCorrections reads).
// The top-level .tmux-cli/tasks.yaml is consulted only as a legacy fallback when
// no per-goal file exists — never alongside it, so a stale planning-queue left by
// another goal cannot leak its spec paths into this goal's editable set. Source
// files are deliberately absent — they are the implementer's domain
// (verdict=fail), never the applier's. A missing tasks.yaml degrades to goal.md
// only.
func (d *Daemon) specArtifactPaths(goal *Goal) (map[string]bool, error) {
	allow := map[string]bool{}
	goalMd := filepath.Clean(filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "goal.md"))
	allow[goalMd] = true

	p := tasks.GoalTasksFilePath(d.workDir, goal.ID)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		p = filepath.Join(d.workDir, ".tmux-cli", "tasks.yaml")
		data, err = os.ReadFile(p)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return allow, nil // no dispatch queue → goal.md is the only spec artifact
		}
		return nil, err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	tasksRaw, ok := raw["tasks"].([]interface{})
	if !ok {
		return allow, nil
	}
	for _, t := range tasksRaw {
		taskMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		ctxRel, ok := taskMap["context"].(string)
		if !ok || ctxRel == "" {
			continue
		}
		allow[filepath.Clean(filepath.Join(d.workDir, ctxRel))] = true
	}
	return allow, nil
}

// isSpecArtifact reports whether abs is an applier-editable spec artifact: it
// MUST be a member of the allowlist AND live under workDir. The containment guard
// is defense-in-depth against a `..`-escaping path that somehow reached the
// allowlist — a path outside workDir is never editable regardless of membership.
func isSpecArtifact(abs string, allow map[string]bool, workDir string) bool {
	if !allow[abs] {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(workDir), abs)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// applyCorrectionEdit applies one CorrectionEdit to the file at abs and reports
// whether the file's bytes changed. The decision matrix:
//
//   - Old present in file       → replace the occurrence nearest e.Line (or the
//     first when Line is unset) with New; changed=true.
//   - Old absent, New present   → idempotent no-op (the edit is already applied);
//     changed=false, no error.
//   - Old absent, New absent    → refuse: there is nothing to anchor on and
//     nothing already present to confirm; return an error and write nothing.
//
// All writes go through atomicWrite so a partial file is never observable.
func applyCorrectionEdit(abs string, e CorrectionEdit) (changed bool, err error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", abs, err)
	}
	content := string(data)

	if e.Old != "" {
		if !strings.Contains(content, e.Old) {
			// Old text is gone. If New is already present, the edit was applied on a
			// prior cycle → idempotent no-op. Otherwise we cannot anchor: refuse.
			if e.New != "" && strings.Contains(content, e.New) {
				return false, nil
			}
			return false, fmt.Errorf("correction_edit: old text not found and new text not present in %s", abs)
		}
		updated, ok := replaceAnchored(content, e.Old, e.New, e.Line)
		if !ok || updated == content {
			return false, nil
		}
		if err := atomicWrite(abs, []byte(updated), 0o644); err != nil {
			return false, err
		}
		return true, nil
	}

	// Old is empty — the only well-defined behavior is the idempotent no-op: New is
	// already present. A blind insert (New absent) has no anchor and is refused.
	if e.New != "" && strings.Contains(content, e.New) {
		return false, nil
	}
	return false, fmt.Errorf("correction_edit: no old anchor for %s and new text not already present (refusing blind insert)", abs)
}

// replaceAnchored replaces ONE occurrence of oldText with newText in content. When
// line > 0 and there are multiple occurrences, it picks the occurrence whose
// 1-based line number is closest to line (the validator's anchor hint); otherwise
// it replaces the first occurrence. Returns the updated content and whether a
// replacement was made.
func replaceAnchored(content, oldText, newText string, line int) (string, bool) {
	var offsets []int
	for i := 0; ; {
		idx := strings.Index(content[i:], oldText)
		if idx < 0 {
			break
		}
		offsets = append(offsets, i+idx)
		i += idx + len(oldText)
	}
	if len(offsets) == 0 {
		return content, false
	}
	chosen := offsets[0]
	if line > 0 && len(offsets) > 1 {
		best := -1
		for _, off := range offsets {
			ln := 1 + strings.Count(content[:off], "\n")
			dist := ln - line
			if dist < 0 {
				dist = -dist
			}
			if best < 0 || dist < best {
				best = dist
				chosen = off
			}
		}
	}
	return content[:chosen] + newText + content[chosen+len(oldText):], true
}

// revalidateAfterCorrection re-runs validation after a successful mechanical
// correction, charging ZERO budget — the entire point of B5b over
// bounceToGeneration. It tears down any stale validator window and re-creates one
// via createValidatorAndSendPayload (the full re-dispatch; a single-investigator
// targeted variant is not available, so the spec's documented full-validator
// fallback is used), advances the goal's runtime to phaseValidating, and stamps a
// fresh validateTime. It NEVER decrements CodeRetries / SpecRetries /
// ValidationRetries. The goal stays GoalRunning.
func (d *Daemon) revalidateAfterCorrection(goal *Goal, goals *GoalsFile) error {
	if err := d.killWindowByName(validatorWindow(goal.ID, d.maxGoals())); err != nil {
		return err
	}
	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}
	rt := d.runtime(goal.ID)
	rt.phase = phaseValidating
	rt.validateTime = d.now()
	log.Printf("%s: applied structured correction(s) — re-validating (no budget charged)", goal.ID)
	return SaveGoals(d.workDir, goals)
}

package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// correction_applier_test.go — B5b mechanical-correction applier (goal-020).
//
// TDD-first coverage for the daemon path that, instead of charging the single
// SpecRetries on a planner bounce, applies a structured correction_edit confined
// to spec artifacts (goal.md / dispatch spec), re-validates the goal, and charges
// ZERO budget. Out-of-scope, ineffective, or absent edits fall back to the
// unchanged bounceToGeneration. Consumes execute-2's goal-025 fixture for the
// replay case and execute-7/B5a's CorrectionEdit schema.

// writeGoalMd writes a goal.md with the given body to .tmux-cli/goals/<id>/goal.md
// and returns its absolute path. EnsureGoalDir mkdir -p's the goal dir first.
func writeGoalMd(t *testing.T, dir, goalID, body string) string {
	t.Helper()
	_, err := EnsureGoalDir(dir, goalID)
	require.NoError(t, err)
	p := filepath.Join(dir, ".tmux-cli", "goals", goalID, "goal.md")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// permissiveValidatorMocks wires a fully-permissive validator window so the
// re-validation re-spawn (kill stale + create + boot + prompt + send) succeeds
// regardless of call count. Mirrors setupValidatorMocks but also allows the kill.
func permissiveValidatorMocks(exec *testutil.MockTmuxExecutor, session, winID string, valName ...string) {
	name := "validator-001"
	if len(valName) > 0 {
		name = valName[0]
	}
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: winID, Name: name, CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", session, winID).Return(nil)
	exec.On("CaptureWindowOutput", session, winID).Return("ready ❯ ", nil)
	exec.On("SendMessage", session, winID, mock.Anything).Return(nil)
}

// --- applyStructuredCorrections ------------------------------------------------

func TestApplyStructuredCorrections_SpecArtifactEditAppliesNoBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	mdPath := writeGoalMd(t, dir, "goal-001", "acceptance: run vendor/bin/phpstan-wrong\n")

	valSig := &ValidatorSignal{
		Verdict: VerdictBlocked, Owner: "planner",
		Findings: []ValidationFinding{{
			Rule: "path-typo", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner",
			Detail:     "goal.md names a non-existent command path",
			Correction: "fix the command path in goal.md",
			CorrectionEdits: []CorrectionEdit{
				{File: ".tmux-cli/goals/goal-001/goal.md", Line: 1, Old: "phpstan-wrong", New: "phpstan"},
			},
		}},
		Timestamp: "2026-06-01T12:00:00Z",
	}

	permissiveValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	handled, err := d.applyStructuredCorrections(goal, gf, valSig)
	require.NoError(t, err)
	assert.True(t, handled, "the spec-artifact edit is applied and re-validated by the daemon")

	body, readErr := os.ReadFile(mdPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "vendor/bin/phpstan\n", "goal.md is corrected in place")
	assert.NotContains(t, string(body), "phpstan-wrong", "the wrong path is gone")

	assert.Equal(t, 2, goal.SpecRetries, "SpecRetries is NOT charged on the applier path")
	assert.Equal(t, 2, goal.CodeRetries, "CodeRetries untouched")
	assert.Equal(t, 1, goal.ValidationRetries, "ValidationRetries untouched")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "re-validation queued")
}

func TestApplyStructuredCorrections_OutOfScopeRefusedFallsBackToBounce(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// A correction_edit targeting a SOURCE file (outside the spec-artifact
	// allowlist) must be refused: no file written, route to bounceToGeneration.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictBlocked, Owner: "planner",
		Findings: []ValidationFinding{{
			Rule: "src-edit", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner",
			Detail:     "criteria contradict the implementation",
			Correction: "regenerate the plan",
			CorrectionEdits: []CorrectionEdit{
				{File: "internal/foo.go", Old: "bar", New: "baz"},
			},
		}},
		Timestamp: "2026-06-01T12:00:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(goal, gf))

	assert.Equal(t, 1, goal.SpecRetries, "out-of-scope edit refused → bounce charges SpecRetries 2->1")
	assert.Equal(t, GoalPending, goal.Status, "bounced to generation")
	assert.Equal(t, "generation", goal.Phase)
	_, statErr := os.Stat(filepath.Join(dir, "internal", "foo.go"))
	assert.True(t, os.IsNotExist(statErr), "no source file is ever written")
}

func TestApplyStructuredCorrections_IneffectiveEditFallsBackToBounce(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	// goal.md ALREADY contains the "new" text and NOT the "old" — the edit is a
	// no-op (idempotent), so no file changes. The finding still fails, so the
	// applier must NOT loop on a zero-budget re-validation: fall back to bounce.
	mdPath := writeGoalMd(t, dir, "goal-001", "acceptance: vendor/bin/phpstan passes\n")
	before, err := os.ReadFile(mdPath)
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictBlocked, Owner: "planner",
		Findings: []ValidationFinding{{
			Rule: "noop-edit", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner",
			Detail:     "the spec still contradicts the acceptance gate",
			Correction: "regenerate",
			CorrectionEdits: []CorrectionEdit{
				{File: ".tmux-cli/goals/goal-001/goal.md", Old: "phpstan-absent", New: "vendor/bin/phpstan"},
			},
		}},
		Timestamp: "2026-06-01T12:00:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(goal, gf))

	assert.Equal(t, 1, goal.SpecRetries, "ineffective (no on-disk change) → bounce charges SpecRetries 2->1")
	assert.Equal(t, GoalPending, goal.Status, "bounced to generation, loop is budget-bounded")
	after, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "goal.md is byte-identical — the no-op edit changed nothing")
}

func TestApplyStructuredCorrections_NoCorrectionEditBounces(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 2, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// Prose-only blocked/planner finding (no correction_edit) — exactly today's
	// behavior must be preserved (back-compat): bounceToGeneration runs.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictBlocked, Owner: "planner",
		Findings: []ValidationFinding{{
			Rule: "acceptance-3", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner",
			Detail:     "two acceptance criteria contradict each other",
			Correction: "pick one criterion and drop the other",
		}},
		Timestamp: "2026-06-01T12:00:00Z",
	}))
	noWindows(exec)

	goal := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(goal, gf))

	assert.Equal(t, 1, goal.SpecRetries, "prose-only spec defect bounces and charges SpecRetries 2->1")
	assert.Equal(t, GoalPending, goal.Status)
	assert.Equal(t, "generation", goal.Phase)
}

func TestApplyStructuredCorrections_Goal025Replay(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-025").phase = phaseValidating

	// execute-2's goal-025 fixture provides the goal layout; the real goal-025
	// failure was a trivial fixable typo in goal.md that consumed the spec budget.
	gf, _ := newGoal025Fixture(1, 2)
	// Give goal-025 a spec budget so a charged bounce would be observable, and
	// run it through the validating phase (GoalRunning).
	g := &gf.Goals[0]
	g.SpecRetries, g.MaxSpecRetries = 2, 2
	g.ValidationRetries, g.MaxValidationRetries = 1, 1
	writeGoals(t, dir, gf)
	mdPath := writeGoalMd(t, dir, "goal-025", "validate: vendor/bin/phpstan-typo analyse\n")

	require.NoError(t, SaveValidatorSignal(dir, "goal-025", &ValidatorSignal{
		Verdict: VerdictBlocked, Owner: "planner",
		Findings: []ValidationFinding{{
			Rule: "phpstan-path", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner",
			Detail:     "goal.md references vendor/bin/phpstan-typo which does not exist",
			Correction: "correct the binary path to vendor/bin/phpstan",
			CorrectionEdits: []CorrectionEdit{
				{File: ".tmux-cli/goals/goal-025/goal.md", Old: "phpstan-typo", New: "phpstan"},
			},
		}},
		Timestamp: "2026-06-01T12:00:00Z",
	}))

	permissiveValidatorMocks(exec, testSession, "@7", "validator-025")
	d.SetWindowCreateFunc(mockCreateWindowFn("@7"))

	require.NoError(t, d.checkValidatingPhase(g, gf))

	body, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "vendor/bin/phpstan analyse", "the typo in goal.md is corrected")
	assert.NotContains(t, string(body), "phpstan-typo")

	assert.Equal(t, 2, g.SpecRetries, "zero spec budget consumed on the replay")
	assert.Equal(t, 1, g.ValidationRetries, "zero validation budget consumed on the replay")
	assert.Equal(t, GoalRunning, g.Status, "goal stays running for the re-validation, not bounced")
	assert.Equal(t, phaseValidating, d.runtime("goal-025").phase, "re-validation queued")
}

// --- applyCorrectionEdit -------------------------------------------------------

func TestApplyCorrectionEdit_Idempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.md")
	require.NoError(t, os.WriteFile(p, []byte("the value is right-path/foo here\n"), 0o644))

	// old absent, new already present → idempotent no-op success.
	e := CorrectionEdit{File: "spec.md", Old: "wrong-path/foo", New: "right-path/foo"}

	before, err := os.ReadFile(p)
	require.NoError(t, err)
	changed, err := applyCorrectionEdit(p, e)
	require.NoError(t, err)
	assert.False(t, changed, "no-op: the replacement is already present")

	// Apply a second time — still a byte-identical no-op.
	changed2, err := applyCorrectionEdit(p, e)
	require.NoError(t, err)
	assert.False(t, changed2)
	after, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, before, after, "file is byte-identical after repeated applies")
}

func TestApplyCorrectionEdit_OldAndNewAbsentRefuses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.md")
	original := []byte("hello world\n")
	require.NoError(t, os.WriteFile(p, original, 0o644))

	// old not in file, new empty → cannot anchor, nothing to assert present: refuse.
	changed, err := applyCorrectionEdit(p, CorrectionEdit{File: "spec.md", Old: "missing", New: ""})
	require.Error(t, err, "refuse: old absent and new empty")
	assert.False(t, changed)

	// both old and new empty → refuse.
	changed2, err2 := applyCorrectionEdit(p, CorrectionEdit{File: "spec.md", Old: "", New: ""})
	require.Error(t, err2, "refuse: nothing to do")
	assert.False(t, changed2)

	after, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, original, after, "a refused edit performs no write")
}

// --- specArtifactPaths ---------------------------------------------------------

func TestSpecArtifactPaths_GoalMdAndDispatchSpecOnly(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Description: "t", Status: GoalRunning}}}
	writeGoals(t, dir, gf)

	// tasks.yaml carries one dispatch-spec context path; that path + goal.md are
	// the ONLY spec artifacts. A made-up source path must be excluded.
	tasksYaml := "tasks:\n  - id: t1\n    context: .tmux-cli/research/2026/spec-1.md\n"
	tp := filepath.Join(dir, ".tmux-cli", "tasks.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(tp), 0o755))
	require.NoError(t, os.WriteFile(tp, []byte(tasksYaml), 0o644))

	allow, err := d.specArtifactPaths(&gf.Goals[0])
	require.NoError(t, err)

	goalMd := filepath.Clean(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md"))
	dispatchSpec := filepath.Clean(filepath.Join(dir, ".tmux-cli", "research", "2026", "spec-1.md"))
	assert.True(t, allow[goalMd], "goal.md is a spec artifact")
	assert.True(t, allow[dispatchSpec], "the dispatch-spec context path is a spec artifact")

	other := filepath.Clean(filepath.Join(dir, "internal", "taskvisor", "goals.go"))
	assert.False(t, allow[other], "source files are NOT spec artifacts")

	// isSpecArtifact enforces membership AND workDir containment (reject `..`).
	assert.True(t, isSpecArtifact(goalMd, allow, dir))
	assert.False(t, isSpecArtifact(other, allow, dir), "non-member rejected")
	escape := filepath.Clean(filepath.Join(dir, "..", "etc", "passwd"))
	assert.False(t, isSpecArtifact(escape, allow, dir), "`..` escape rejected by containment guard")
}

func TestSpecArtifactPaths_PrefersPerGoalTasksYaml(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Description: "t", Status: GoalRunning}}}
	writeGoals(t, dir, gf)

	// BOTH files exist: the per-goal fan-out (this goal's own dispatch specs) and
	// a stale top-level planning-queue left behind by ANOTHER goal. The allowlist
	// must come from the per-goal file ONLY — the same source injectCorrections
	// reads — so a concurrent/stale goal's spec paths never leak into this goal's
	// editable set.
	perGoal := "tasks:\n  - id: t1\n    context: .tmux-cli/goals/goal-001/research/spec-own.md\n"
	pg := filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "tasks.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(pg), 0o755))
	require.NoError(t, os.WriteFile(pg, []byte(perGoal), 0o644))

	stale := "tasks:\n  - id: t9\n    context: .tmux-cli/goals/goal-999/research/spec-other.md\n"
	tp := filepath.Join(dir, ".tmux-cli", "tasks.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(tp), 0o755))
	require.NoError(t, os.WriteFile(tp, []byte(stale), 0o644))

	allow, err := d.specArtifactPaths(&gf.Goals[0])
	require.NoError(t, err)

	own := filepath.Clean(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "research", "spec-own.md"))
	other := filepath.Clean(filepath.Join(dir, ".tmux-cli", "goals", "goal-999", "research", "spec-other.md"))
	assert.True(t, allow[own], "per-goal dispatch-spec context path is a spec artifact")
	assert.False(t, allow[other], "stale top-level queue from another goal must NOT leak into the allowlist")
}

// --- revalidateAfterCorrection -------------------------------------------------

func TestRevalidateAfterCorrection_ChargesNoBudget(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{routeGoal("goal-001", 3, 2, 1, 0)}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	permissiveValidatorMocks(exec, testSession, "@5")
	d.SetWindowCreateFunc(mockCreateWindowFn("@5"))

	goal := &gf.Goals[0]
	require.NoError(t, d.revalidateAfterCorrection(goal, gf))

	assert.Equal(t, 3, goal.CodeRetries, "CodeRetries unchanged")
	assert.Equal(t, 2, goal.SpecRetries, "SpecRetries unchanged")
	assert.Equal(t, 1, goal.ValidationRetries, "ValidationRetries unchanged")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "phase advanced to validating")
}

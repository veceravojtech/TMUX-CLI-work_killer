package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- P7 GateTerminalPass unit tests --------------------------------------
//
// GateTerminalPass enforces that a goal which DECLARES validate steps cannot
// terminally `pass` on LLM judgment alone — without deterministic backing
// (validate.sh exit 0) a `pass` is downgraded to error/ops. Non-pass verdicts
// and no-validate goals pass through byte-identically.

// A goal that declares validate steps but whose deterministic script did not
// pass cannot terminally pass — downgrade to error/ops.
func TestGateTerminalPass_PassDeclaredValidateScriptNotPassed_DowngradesToErrorOps(t *testing.T) {
	v, o := GateTerminalPass(VerdictPass, "", PassGate{RequireValidate: true, ScriptPassed: false})
	assert.Equal(t, VerdictError, v, "declared-validate + script-not-passed downgrades pass to error")
	assert.Equal(t, "ops", o, "downgraded verdict is owned by ops (re-validate)")
}

// When the deterministic script passed, the LLM pass stands unchanged.
func TestGateTerminalPass_PassDeclaredValidateScriptPassed_StaysPass(t *testing.T) {
	v, o := GateTerminalPass(VerdictPass, "", PassGate{RequireValidate: true, ScriptPassed: true})
	assert.Equal(t, VerdictPass, v)
	assert.Equal(t, "", o)
}

// A goal with NO validate steps keeps current behavior: LLM pass is terminal.
func TestGateTerminalPass_PassNoValidateSteps_StaysPass(t *testing.T) {
	v, o := GateTerminalPass(VerdictPass, "", PassGate{RequireValidate: false, ScriptPassed: false})
	assert.Equal(t, VerdictPass, v, "no validate steps ⇒ no regression, pass stays terminal")
	assert.Equal(t, "", o)
}

// Non-pass verdicts are never touched by the gate — fail keeps implementer.
func TestGateTerminalPass_FailVerdict_Untouched(t *testing.T) {
	v, o := GateTerminalPass(VerdictFail, "implementer", PassGate{RequireValidate: true, ScriptPassed: false})
	assert.Equal(t, VerdictFail, v)
	assert.Equal(t, "implementer", o)
}

// Blocked verdict + its owner pass through unchanged (owner preserved).
func TestGateTerminalPass_BlockedVerdict_PreservesOwner(t *testing.T) {
	v, o := GateTerminalPass(VerdictBlocked, "planner", PassGate{RequireValidate: true, ScriptPassed: false})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "planner", o)
}

// Error verdict passes through unchanged.
func TestGateTerminalPass_ErrorVerdict_Untouched(t *testing.T) {
	v, o := GateTerminalPass(VerdictError, "ops", PassGate{RequireValidate: true, ScriptPassed: false})
	assert.Equal(t, VerdictError, v)
	assert.Equal(t, "ops", o)
}

// Regression guard: ClassifyVerdict's non-pass roll-up semantics must stay
// byte-identical (P7 leaves ClassifyVerdict untouched; the new rule lives in
// GateTerminalPass). Pins representative non-pass finding sets.
func TestClassifyVerdict_NonPassSemanticsUnchanged(t *testing.T) {
	cases := []struct {
		name        string
		findings    []ValidationFinding
		wantVerdict string
		wantOwner   string
	}{
		{
			name:        "code-defect fail → implementer",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictFail, FailureClass: "code-defect"}},
			wantVerdict: VerdictFail, wantOwner: "implementer",
		},
		{
			name:        "unknown class non-pass → leaf-4 catch-all fail/implementer",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictFail, FailureClass: ""}},
			wantVerdict: VerdictFail, wantOwner: "implementer",
		},
		{
			name:        "blocked env-config ops → blocked/ops",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"}},
			wantVerdict: VerdictBlocked, wantOwner: "ops",
		},
		{
			name:        "blocked planner-owned → blocked/planner (planner > ops)",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner"}},
			wantVerdict: VerdictBlocked, wantOwner: "planner",
		},
		{
			name:        "validator-error → error/ops",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictError, FailureClass: "validator-error"}},
			wantVerdict: VerdictError, wantOwner: "ops",
		},
		{
			name:        "all-pass → pass/empty-owner",
			findings:    []ValidationFinding{{Rule: "r1", Status: VerdictPass}},
			wantVerdict: VerdictPass, wantOwner: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, o := ClassifyVerdict(tc.findings)
			assert.Equal(t, tc.wantVerdict, v)
			assert.Equal(t, tc.wantOwner, o)
		})
	}
}

// --- P7 daemon-phase tests -----------------------------------------------

// A goal that DECLARES validate steps whose script did not pass: an LLM pass in
// the validating phase is downgraded to error/ops and routed through
// rerunValidationOnly (validator re-created, ValidationRetries decremented),
// and the goal is NOT marked GoalDone.
func TestCheckValidatingPhase_DeclaredValidate_LLMPassNoScript_DowngradesAndRevalidates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0

	goal := routeGoal("goal-001", 3, 2, 2, 0)
	goal.Validate = []string{"go test ./..."} // declares validate steps ⇒ gate armed
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{goal}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	d.runtime("goal-001").phase = phaseValidating

	// LLM validator returns pass; no validate.sh exists (script not passed).
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: "2026-06-05T14:35:00Z",
	}))

	const valID = "@2"
	val := []tmux.WindowInfo{{TmuxWindowID: valID, Name: "validator-001", CurrentCommand: "claude"}}
	exec.On("ListWindows", testSession).Return(val, nil)
	exec.On("CaptureWindowOutput", testSession, valID).Return("❯ ", nil)
	exec.On("KillWindow", testSession, valID).Return(nil)
	exec.On("SendMessage", testSession, valID, mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn(valID))

	g := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(g, gf))

	assert.NotEqual(t, GoalDone, g.Status, "a declared-validate LLM-only pass must NOT terminally pass")
	assert.Equal(t, GoalRunning, g.Status, "downgrade re-validates ⇒ goal stays running")
	assert.Equal(t, 1, g.ValidationRetries, "validation budget charged 2->1 via rerunValidationOnly")
	assert.Equal(t, 3, g.CodeRetries, "code budget untouched (not a code defect)")
	assert.Equal(t, 2, g.SpecRetries, "spec budget untouched (not a spec bounce)")
	assert.Equal(t, phaseValidating, d.runtime("goal-001").phase, "remains validating after re-create")
}

// A goal with NO validate steps: an LLM pass still reaches GoalDone (the gate is
// a no-op for no-validate goals — zero regression).
func TestCheckValidatingPhase_NoValidateSteps_LLMPass_GoalDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-06-05T10:00:00Z", MaxRetries: 3},
			{ID: "goal-002", Description: "next", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: "2026-06-05T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil).Once()
	exec.On("KillWindow", testSession, "@5").Return(nil)

	g := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(g, gf))
	assert.Equal(t, GoalDone, g.Status, "no validate steps ⇒ LLM pass is terminal (no regression)")
}

// A goal that declares validate steps but the LLM returns fail: routing is
// unchanged (code defect → handleFailedCycle, charges CodeRetries) — the gate
// only acts on a `pass`.
func TestCheckValidatingPhase_DeclaredValidate_LLMFail_RoutesUnchanged(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	goal := routeGoal("goal-001", 2, 2, 2, 0)
	goal.Validate = []string{"go test ./..."}
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{goal}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictFail, NextAction: "fix the off-by-one",
		Findings:  []ValidationFinding{{Rule: "r1", Status: VerdictFail, FailureClass: "code-defect"}},
		Timestamp: "2026-06-05T14:35:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)

	g := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(g, gf))

	assert.Equal(t, 1, g.CodeRetries, "fail/code-defect charges CodeRetries 2->1 (unchanged routing)")
	assert.Equal(t, 2, g.ValidationRetries, "validation budget untouched on a code defect")
	assert.NotEqual(t, GoalDone, g.Status, "a fail verdict never reaches done")
}

// Late-verdict salvage: a late PASS on a declared-validate goal at the salvage
// seam is gated — NOT salvaged to done.
func TestLateVerdictSalvage_DeclaredValidate_GatesLatePass(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	goal := routeGoal("goal-001", 3, 2, 2, 0)
	goal.Status = GoalFailed
	goal.FailedBy = "validation-timeout"
	goal.Validate = []string{"go test ./..."} // declares validate steps ⇒ gate armed
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{goal}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// A late LLM pass arrives after the timeout-synthesized failure.
	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: "2026-06-05T14:40:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)

	g := &gf.Goals[0]
	out := captureLog(t, func() {
		require.NoError(t, d.salvageLateVerdicts(gf))
	})

	assert.Equal(t, GoalFailed, g.Status, "a declared-validate late LLM pass is gated — failure stands")
	assert.NotEqual(t, GoalDone, g.Status)
	assert.Contains(t, out, "failure stands", "gated late pass takes the failure-stands log path")
}

// Late-verdict salvage regression: a late pass on a goal with NO validate steps
// IS still salvaged to done (gate is a no-op).
func TestLateVerdictSalvage_NoValidateSteps_LatePassSalvaged(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalFailed, FailedBy: "validation-timeout", StartedAt: "2026-06-05T10:00:00Z"},
		},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: "2026-06-05T14:40:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil)
	exec.On("KillWindow", testSession, "@5").Return(nil)

	g := &gf.Goals[0]
	require.NoError(t, d.salvageLateVerdicts(gf))
	assert.Equal(t, GoalDone, g.Status, "no validate steps ⇒ late pass still salvaged (no regression)")
}

// The supervising-phase deterministic anchor is unchanged: a declared-validate
// goal whose validate.sh exits 0 reaches GoalDone via the existing path. The
// gate never fires here (supervising short-circuits to a terminal pass).
func TestSupervisingPhase_ScriptExit0_DeclaredValidate_TerminalPass(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalRunning, StartedAt: "2026-06-05T10:00:00Z",
				Validate: []string{"go test ./..."}, MaxRetries: 3},
		},
	}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, writeExecScript(goalDir, "validate.sh"))

	require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
		Status: "done", Timestamp: "2026-06-05T14:30:00Z",
	}))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(2)
	setupDeactivateMocks(exec, testSession, "@9")
	d.SetWindowCreateFunc(mockCreateWindowFn("@9"))
	d.SetScriptRunnerFunc(func(_ context.Context, _, _ string, _ []string) (string, string, int, error) {
		return "", "", 0, nil // deterministic exit 0
	})

	g := &gf.Goals[0]
	require.NoError(t, d.checkSupervisingPhase(g, gf))
	assert.Equal(t, GoalDone, g.Status, "exit-0 supervising path is the unchanged deterministic anchor")
}

// writeExecScript writes a trivial executable shell script into goalDir.
func writeExecScript(goalDir, name string) error {
	return os.WriteFile(filepath.Join(goalDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

// p7 integration: a full daemon tick on a declared-validate goal whose
// validate.sh exits non-zero, with a fake validator signal of pass, must never
// land the goal at done — it re-validates instead.
func TestP7Integration_DeclaredValidate_ScriptNonZero_FakePass_NeverDone(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.validatorSendDelay = 0
	d.runtime("goal-001").phase = phaseValidating

	goal := routeGoal("goal-001", 3, 2, 2, 0)
	goal.Validate = []string{"go test ./..."}
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{goal}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	require.NoError(t, SaveValidatorSignal(dir, "goal-001", &ValidatorSignal{
		Verdict: VerdictPass, Timestamp: time.Now().UTC().Format(time.RFC3339),
	}))

	const valID = "@7"
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: valID, Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, valID).Return("❯ ", nil)
	exec.On("KillWindow", testSession, valID).Return(nil)
	exec.On("SendMessage", testSession, valID, mock.Anything).Return(nil)
	d.SetWindowCreateFunc(mockCreateWindowFn(valID))

	g := &gf.Goals[0]
	require.NoError(t, d.checkValidatingPhase(g, gf))

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	assert.NotEqual(t, GoalDone, reloaded.Goals[0].Status, "fake LLM pass never reaches done without deterministic backing")
}

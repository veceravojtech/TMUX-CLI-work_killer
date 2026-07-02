package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCycleReport is the happy-path FAIL report the mutation tests tweak.
func validCycleReport() CycleReport {
	return CycleReport{
		Scenario:        "scn",
		Cycle:           3,
		DrivenSummary:   "drove discovery → roadmap → taskvisor; goal-002 wedged in implement",
		FailurePoint:    "taskvisor phase, after goals-dispatched (600s silence fallback fired)",
		DefectSignature: "dispatch/hang/goal",
		FiledTask:       "task-281 / new",
		TimingTable:     "implement p90 300s; mean in-flight 1.2 of max_goals 3",
		Verdict:         VerdictFail,
		VerdictReason:   "goal-002 never reached validate",
		AppUp:           false,
	}
}

// inProgressState is the matching in-progress ledger at the report's cycle.
func inProgressState(cycle, maxCycles int) State {
	st := NewState("scn", maxCycles)
	st.Cycle = cycle
	return st
}

// --- RenderCycleReport ------------------------------------------------------

func TestRenderCycleReport_FixedSectionOrder(t *testing.T) {
	got := RenderCycleReport(validCycleReport())

	want := `# e2e-evaluator cycle report — scn cycle 3

## Driven Summary

drove discovery → roadmap → taskvisor; goal-002 wedged in implement

## Failure Point

taskvisor phase, after goals-dispatched (600s silence fallback fired)

## Defect Signature

dispatch/hang/goal

## Filed Task

task-281 / new

## Timing Table

implement p90 300s; mean in-flight 1.2 of max_goals 3

## Verdict

FAIL — goal-002 never reached validate
`
	assert.Equal(t, want, got, "rendering must be byte-stable with the six fixed-order sections")

	// The order is structural, not incidental: each heading index strictly grows.
	last := -1
	for _, h := range []string{"## Driven Summary", "## Failure Point", "## Defect Signature", "## Filed Task", "## Timing Table", "## Verdict"} {
		i := strings.Index(got, h)
		require.Greater(t, i, last, "%s must follow the previous section", h)
		last = i
	}
}

func TestRenderCycleReport_VerdictComposition(t *testing.T) {
	r := validCycleReport()
	r.Verdict = VerdictFail
	r.VerdictReason = "app-up probe failed"
	assert.Contains(t, RenderCycleReport(r), "\n## Verdict\n\nFAIL — app-up probe failed\n",
		"the Verdict body renders `<VERDICT> — <reason>` from the enum + reason")
}

func TestRenderCycleReport_TrimsProse(t *testing.T) {
	trimmed := RenderCycleReport(validCycleReport())

	padded := validCycleReport()
	padded.DrivenSummary = "  " + padded.DrivenSummary + "\n\n"
	padded.VerdictReason = "\t" + padded.VerdictReason + "  "
	assert.Equal(t, trimmed, RenderCycleReport(padded),
		"surrounding whitespace in section proses must not change the output bytes")
}

// --- ValidateCycleReport ----------------------------------------------------

func TestValidateCycleReport_AcceptsValid(t *testing.T) {
	assert.NoError(t, ValidateCycleReport(validCycleReport()))
}

func TestValidateCycleReport_RejectsEmptySection(t *testing.T) {
	blank := func(mutate func(*CycleReport)) error {
		r := validCycleReport()
		mutate(&r)
		return ValidateCycleReport(r)
	}
	cases := map[string]func(*CycleReport){
		"Driven Summary":   func(r *CycleReport) { r.DrivenSummary = "  " },
		"Failure Point":    func(r *CycleReport) { r.FailurePoint = "" },
		"Defect Signature": func(r *CycleReport) { r.DefectSignature = "\n" },
		"Filed Task":       func(r *CycleReport) { r.FiledTask = " " },
		"Timing Table":     func(r *CycleReport) { r.TimingTable = "" },
		"Verdict":          func(r *CycleReport) { r.VerdictReason = "  " },
	}
	for section, mutate := range cases {
		err := blank(mutate)
		require.Error(t, err, "an empty %s prose must be refused", section)
		assert.Contains(t, err.Error(), section, "the error must name the %s section", section)
	}
}

func TestValidateCycleReport_RejectsBadVerdict(t *testing.T) {
	r := validCycleReport()
	r.Verdict = "maybe"
	err := ValidateCycleReport(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PASS|FAIL|EXHAUSTED")
}

func TestValidateCycleReport_PassRequiresAppUp(t *testing.T) {
	r := validCycleReport()
	r.Verdict = VerdictPass
	r.VerdictReason = "all four criteria green"
	r.AppUp = false
	assert.Error(t, ValidateCycleReport(r), "PASS with app-up=false is the false-pass this guard refuses")

	r.AppUp = true
	assert.NoError(t, ValidateCycleReport(r), "PASS with app-up=true is valid")
}

func TestValidateCycleReport_RejectsBadShape(t *testing.T) {
	r := validCycleReport()
	r.Scenario = "  "
	assert.Error(t, ValidateCycleReport(r), "scenario must be non-empty")

	r = validCycleReport()
	r.Cycle = 0
	assert.Error(t, ValidateCycleReport(r), "cycle must be >= 1")
}

// --- ValidateReportForState -------------------------------------------------

func TestValidateReportForState_AcceptsInProgressMatch(t *testing.T) {
	assert.NoError(t, ValidateReportForState(validCycleReport(), inProgressState(3, 10)))
}

func TestValidateReportForState_RefusesTerminalLedger(t *testing.T) {
	for _, status := range []string{StatusPassed, StatusExhausted, StatusEscalated} {
		st := inProgressState(3, 10)
		st.Status = status
		err := ValidateReportForState(validCycleReport(), st)
		require.Error(t, err, "a %s ledger must refuse the report — report precedes record, so a terminal status means record already ran", status)
		assert.Contains(t, err.Error(), status)
	}
}

func TestValidateReportForState_RefusesCycleMismatch(t *testing.T) {
	r := validCycleReport() // cycle 3
	err := ValidateReportForState(r, inProgressState(4, 10))
	require.Error(t, err, "the report is always for the ledger's current in-progress cycle")
	assert.Contains(t, err.Error(), "3", "the error must name the report cycle")
	assert.Contains(t, err.Error(), "4", "the error must name the ledger cycle")
}

func TestValidateReportForState_ExhaustedOnlyAtMaxCycles(t *testing.T) {
	r := validCycleReport()
	r.Verdict = VerdictExhausted
	r.VerdictReason = "self-heal budget spent"
	r.Cycle = 2
	assert.Error(t, ValidateReportForState(r, inProgressState(2, 10)),
		"EXHAUSTED off the max_cycles boundary is refused")

	r.Cycle = 10
	assert.NoError(t, ValidateReportForState(r, inProgressState(10, 10)),
		"EXHAUSTED at cycle == max_cycles is accepted")
}

func TestValidateReportForState_RunsIntrinsicGateFirst(t *testing.T) {
	r := validCycleReport()
	r.Verdict = VerdictPass
	r.AppUp = false
	assert.Error(t, ValidateReportForState(r, inProgressState(3, 10)),
		"ValidateReportForState must include the intrinsic ValidateCycleReport gate")
}

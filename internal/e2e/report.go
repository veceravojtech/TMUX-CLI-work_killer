package e2e

import (
	"fmt"
	"strings"
)

// VerdictPass and friends are the three per-cycle report verdicts (the step-9
// Verdict section of e2e-evaluator.xml). The enum — not free prose — is what
// makes the false-pass guard (PASS requires app-up) machine-checkable.
const (
	VerdictPass      = "PASS"
	VerdictFail      = "FAIL"
	VerdictExhausted = "EXHAUSTED"
)

// CycleReport carries one finished cycle's report content. The six section
// proses are conductor-authored (LLM-supplied flag values); everything else —
// naming, section order, rendering, atomicity — is the deterministic writer's.
type CycleReport struct {
	Scenario        string
	Cycle           int
	DrivenSummary   string
	FailurePoint    string
	DefectSignature string
	FiledTask       string
	TimingTable     string
	Verdict         string
	VerdictReason   string
	AppUp           bool
}

// sections pairs the FIXED report section order with each section's rendered
// body — the single ordering authority ValidateCycleReport and
// RenderCycleReport both walk, so the two can never disagree on order.
func (r CycleReport) sections() []struct{ name, body string } {
	return []struct{ name, body string }{
		{"Driven Summary", r.DrivenSummary},
		{"Failure Point", r.FailurePoint},
		{"Defect Signature", r.DefectSignature},
		{"Filed Task", r.FiledTask},
		{"Timing Table", r.TimingTable},
		{"Verdict", r.Verdict + " — " + strings.TrimSpace(r.VerdictReason)},
	}
}

// ValidateCycleReport is the intrinsic gate over a report's own content:
// scenario/cycle shape, all six section proses non-empty (every section is
// present every cycle), the verdict enum, and the false-pass guard (a PASS
// verdict requires the app-up probe to have passed). This is the ONLY home of
// these checks — the CLI flag parser converts shapes and delegates here.
func ValidateCycleReport(r CycleReport) error {
	if strings.TrimSpace(r.Scenario) == "" {
		return fmt.Errorf("scenario must be non-empty")
	}
	if r.Cycle < 1 {
		return fmt.Errorf("cycle must be >= 1, got %d", r.Cycle)
	}
	for _, s := range []struct{ name, prose string }{
		{"Driven Summary", r.DrivenSummary},
		{"Failure Point", r.FailurePoint},
		{"Defect Signature", r.DefectSignature},
		{"Filed Task", r.FiledTask},
		{"Timing Table", r.TimingTable},
		{"Verdict", r.VerdictReason},
	} {
		if strings.TrimSpace(s.prose) == "" {
			return fmt.Errorf("the %s section prose must be non-empty — every section is present every cycle", s.name)
		}
	}
	switch r.Verdict {
	case VerdictPass, VerdictFail, VerdictExhausted:
	default:
		return fmt.Errorf("verdict must be %s|%s|%s, got %q", VerdictPass, VerdictFail, VerdictExhausted, r.Verdict)
	}
	if r.Verdict == VerdictPass && !r.AppUp {
		return fmt.Errorf("a %s verdict requires app-up=true — a green daemon with a dead app is a false pass", VerdictPass)
	}
	return nil
}

// ValidateReportForState cross-checks a report against the scenario's ledger
// (READ-ONLY — the report path never mutates state). Report-then-record is the
// XML contract: every report, including the final PASS one, is written while
// the ledger is still in-progress at the just-finished cycle, so a terminal
// status is refused with no carve-out (record already ran, or the run is dead).
// EXHAUSTED mirrors RecordCycleOutcome's boundary: only the max_cycles cycle's
// failed record flips the ledger exhausted.
func ValidateReportForState(r CycleReport, st State) error {
	if err := ValidateCycleReport(r); err != nil {
		return err
	}
	if err := ValidateState(st); err != nil {
		return err
	}
	if st.Status != StatusInProgress {
		return fmt.Errorf("ledger is already terminal (status=%s); the report is written BEFORE record, while the run is still in-progress", st.Status)
	}
	if r.Cycle != st.Cycle {
		return fmt.Errorf("report cycle %d does not match the ledger's in-progress cycle %d — the report is always for the ledger's current cycle", r.Cycle, st.Cycle)
	}
	if r.Verdict == VerdictExhausted && st.Cycle != st.MaxCycles {
		return fmt.Errorf("an %s verdict is only valid at the final cycle: ledger is at cycle %d of max_cycles %d", VerdictExhausted, st.Cycle, st.MaxCycles)
	}
	return nil
}

// RenderCycleReport renders the per-cycle report markdown: an H1 then the six
// sections in their fixed order, proses trimmed, one blank line between
// blocks, trailing newline. Pure text transform — byte-stable, no clock/fs.
func RenderCycleReport(r CycleReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# e2e-evaluator cycle report — %s cycle %d\n", r.Scenario, r.Cycle)
	for _, s := range r.sections() {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.name, strings.TrimSpace(s.body))
	}
	return b.String()
}

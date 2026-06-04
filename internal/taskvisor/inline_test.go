package taskvisor

import (
	"strings"
	"testing"
)

// cmdInv builds a pure-command investigator (explicit type:command + a command).
func cmdInv(name string) Investigator {
	return Investigator{Name: name, Type: "command", Commands: []string{"true"}, Pass: "exit 0"}
}

// findingFor mirrors the CLI's parseGoalFindings mapping: finding.Rule == inv.Name.
func findingFor(inv Investigator) ValidationFinding {
	return ValidationFinding{Rule: inv.Name}
}

func findingsFor(invs ...Investigator) []ValidationFinding {
	out := make([]ValidationFinding, 0, len(invs))
	for _, inv := range invs {
		out = append(out, findingFor(inv))
	}
	return out
}

func TestInlinePlan_AllCommandFirstCycle_Inline(t *testing.T) {
	invs := []Investigator{cmdInv("build"), cmdInv("lint"), cmdInv("unit")}
	mode, rerun, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if mode != "inline" {
		t.Fatalf("mode = %q, want inline (reason=%q)", mode, reason)
	}
	want := []string{"build", "lint", "unit"} // sorted
	if strings.Join(rerun, ",") != strings.Join(want, ",") {
		t.Fatalf("rerun = %v, want %v", rerun, want)
	}
}

func TestInlinePlan_MixedTypes_Fanout(t *testing.T) {
	review := Investigator{Name: "review", Type: "code-review", Pass: "matches expected"}
	invs := []Investigator{cmdInv("build"), review}
	mode, _, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if mode != "fanout" {
		t.Fatalf("mode = %q, want fanout", mode)
	}
	if !strings.Contains(reason, "review") {
		t.Fatalf("reason %q should cite the non-command investigator %q", reason, "review")
	}
}

func TestInlinePlan_RetryCycle_AllCommand_Inline(t *testing.T) {
	// Retry cycles are inline-eligible: inline runs AFTER C10 partitioning, so
	// the RERUN set on cycle 2 is the already-minimized remainder — pure-command
	// is exactly as safe as on cycle 1 (goal-061 post-mortem).
	invs := []Investigator{cmdInv("build"), cmdInv("lint")}
	mode, rerun, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 2, false, false)

	if mode != "inline" {
		t.Fatalf("mode = %q, want inline on cycle 2 for pure-command RERUN set (reason=%q)", mode, reason)
	}
	want := []string{"build", "lint"} // sorted
	if strings.Join(rerun, ",") != strings.Join(want, ",") {
		t.Fatalf("rerun = %v, want %v", rerun, want)
	}
}

func TestInlinePlan_RetryCycle_MixedTypes_Fanout(t *testing.T) {
	// A reasoning investigator in the retry-cycle RERUN set still fans the whole
	// set out (all-or-nothing) — removing the cycle gate does not weaken the
	// pure-command gate.
	review := Investigator{Name: "review", Type: "code-review", Pass: "matches expected"}
	invs := []Investigator{cmdInv("build"), review}
	mode, _, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 2, false, false)

	if mode != "fanout" {
		t.Fatalf("mode = %q, want fanout on cycle 2 with a reasoning investigator", mode)
	}
	if !strings.Contains(reason, "review") {
		t.Fatalf("reason %q should cite the reasoning investigator %q", reason, "review")
	}
}

func TestInlinePlan_StandaloneNoCycle_Inline(t *testing.T) {
	invs := []Investigator{cmdInv("build"), cmdInv("lint")}
	mode, _, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 0, false, false)

	if mode != "inline" {
		t.Fatalf("mode = %q, want inline for standalone (cycleN<=0) (reason=%q)", mode, reason)
	}
}

func TestInlinePlan_EmptyRerun_Fanout(t *testing.T) {
	// Every investigator REUSE: prior ledger all pass with matching fingerprints.
	invs := []Investigator{cmdInv("build"), cmdInv("lint")}
	findings := findingsFor(invs...)

	prev := &Results{Results: map[string]ResultEntry{}}
	for _, f := range findings {
		prev.Results[f.Rule] = ResultEntry{
			FindingID:        f.Rule,
			Status:           VerdictPass,
			InputFingerprint: ComputeInputFingerprint(f, nil),
			CycleNumber:      1,
		}
	}

	mode, rerun, reason := InlinePlan(invs, prev, findings, nil, 1, false, false)

	if mode != "fanout" {
		t.Fatalf("mode = %q, want fanout when nothing to rerun", mode)
	}
	if len(rerun) != 0 {
		t.Fatalf("rerun = %v, want empty", rerun)
	}
	if reason != "no RERUN investigators" {
		t.Fatalf("reason = %q, want %q", reason, "no RERUN investigators")
	}
}

func TestInlinePlan_SpecDefectRemovedRemainderCommand_Inline(t *testing.T) {
	// C8 already stripped the spec-defect investigator upstream; InlinePlan only
	// sees the remaining active set, which is all pure-command.
	invs := []Investigator{cmdInv("build"), cmdInv("phpstan")}
	mode, rerun, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if mode != "inline" {
		t.Fatalf("mode = %q, want inline for post-C8 all-command remainder (reason=%q)", mode, reason)
	}
	if len(rerun) != 2 {
		t.Fatalf("rerun = %v, want 2 entries", rerun)
	}
}

func TestInlinePlan_DeterministicSortedRerun(t *testing.T) {
	invs := []Investigator{cmdInv("zeta"), cmdInv("alpha"), cmdInv("mike")}
	findings := findingsFor(invs...)

	_, r1, _ := InlinePlan(invs, nil, findings, nil, 1, false, false)
	_, r2, _ := InlinePlan(invs, nil, findings, nil, 1, false, false)

	if strings.Join(r1, ",") != strings.Join(r2, ",") {
		t.Fatalf("non-deterministic rerun: %v vs %v", r1, r2)
	}
	want := []string{"alpha", "mike", "zeta"}
	if strings.Join(r1, ",") != strings.Join(want, ",") {
		t.Fatalf("rerun = %v, want sorted %v", r1, want)
	}
}

func TestInlinePlan_ForceFull_AllCommand_Inline(t *testing.T) {
	// forceFull forces every check to RERUN even when a prior pass exists; all are
	// command, so the full set inlines.
	invs := []Investigator{cmdInv("build"), cmdInv("lint")}
	findings := findingsFor(invs...)

	prev := &Results{Results: map[string]ResultEntry{}}
	for _, f := range findings {
		prev.Results[f.Rule] = ResultEntry{
			FindingID:        f.Rule,
			Status:           VerdictPass,
			InputFingerprint: ComputeInputFingerprint(f, nil),
			CycleNumber:      1,
		}
	}

	mode, rerun, reason := InlinePlan(invs, prev, findings, nil, 1, true /*forceFull*/, false)

	if mode != "inline" {
		t.Fatalf("mode = %q, want inline under forceFull (reason=%q)", mode, reason)
	}
	if len(rerun) != 2 {
		t.Fatalf("rerun = %v, want full set of 2", rerun)
	}
}

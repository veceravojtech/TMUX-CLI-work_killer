package taskvisor

import (
	"strings"
	"testing"
)

// cmdInv builds a pure-command investigator (explicit type:command + a command).
func cmdInv(name string) Investigator {
	return Investigator{Name: name, Type: "command", Commands: []string{"true"}, Pass: "exit 0"}
}

// reasoningInv builds a non-pure investigator (code-review with a semantic Pass)
// that must land in the spawn set.
func reasoningInv(name string) Investigator {
	return Investigator{Name: name, Type: "code-review", Pass: "matches expected"}
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

func joined(s []string) string { return strings.Join(s, ",") }

func TestInlinePlan_AllCommand_AllInline(t *testing.T) {
	invs := []Investigator{cmdInv("build"), cmdInv("lint"), cmdInv("unit")}
	inline, spawn, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if joined(inline) != "build,lint,unit" { // sorted
		t.Fatalf("inline = %v, want [build lint unit] (reason=%q)", inline, reason)
	}
	if len(spawn) != 0 {
		t.Fatalf("spawn = %v, want empty", spawn)
	}
}

func TestInlinePlan_MixedTypes_Split(t *testing.T) {
	// Core of the new behavior: a static check + a reasoning check no longer fan
	// out together. build runs inline; review spawns.
	invs := []Investigator{cmdInv("build"), reasoningInv("review")}
	inline, spawn, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if joined(inline) != "build" {
		t.Fatalf("inline = %v, want [build] (reason=%q)", inline, reason)
	}
	if joined(spawn) != "review" {
		t.Fatalf("spawn = %v, want [review] (reason=%q)", spawn, reason)
	}
	if !strings.Contains(reason, "split") {
		t.Fatalf("reason %q should describe a split", reason)
	}
}

func TestInlinePlan_AllReasoning_AllSpawn(t *testing.T) {
	// An e2e-test (Chrome) has a command and an exit-only Pass but its TYPE is not
	// pure-command, so it spawns — alongside the code-review.
	e2e := Investigator{Name: "e2e", Type: "e2e-test", Commands: []string{"npx playwright test"}, Pass: "all green (exit 0)"}
	invs := []Investigator{reasoningInv("review"), e2e}
	inline, spawn, _ := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if len(inline) != 0 {
		t.Fatalf("inline = %v, want empty (no pure-command checks)", inline)
	}
	if joined(spawn) != "e2e,review" { // sorted
		t.Fatalf("spawn = %v, want [e2e review]", spawn)
	}
}

func TestInlinePlan_RetryCycle_Split(t *testing.T) {
	// Retry cycles partition identically — there is no cycle gate (the RERUN set
	// is already the minimized remainder after C10 partitioning).
	invs := []Investigator{cmdInv("build"), reasoningInv("review")}
	inline, spawn, _ := InlinePlan(invs, nil, findingsFor(invs...), nil, 2, false, false)

	if joined(inline) != "build" || joined(spawn) != "review" {
		t.Fatalf("cycle-2 partition: inline=%v spawn=%v, want [build]/[review]", inline, spawn)
	}
}

func TestInlinePlan_StandaloneNoCycle_AllInline(t *testing.T) {
	invs := []Investigator{cmdInv("build"), cmdInv("lint")}
	inline, spawn, _ := InlinePlan(invs, nil, findingsFor(invs...), nil, 0, false, false)

	if joined(inline) != "build,lint" || len(spawn) != 0 {
		t.Fatalf("standalone (cycleN<=0): inline=%v spawn=%v, want all inline", inline, spawn)
	}
}

func TestInlinePlan_EmptyRerun_BothEmpty(t *testing.T) {
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

	inline, spawn, reason := InlinePlan(invs, prev, findings, nil, 1, false, false)

	if len(inline) != 0 || len(spawn) != 0 {
		t.Fatalf("all-REUSE: inline=%v spawn=%v, want both empty", inline, spawn)
	}
	if reason != "no RERUN investigators" {
		t.Fatalf("reason = %q, want %q", reason, "no RERUN investigators")
	}
}

func TestInlinePlan_MissingConfig_Spawns(t *testing.T) {
	// A RERUN finding with no investigator config cannot be proven pure-command,
	// so it falls to the spawn set (conservative).
	findings := []ValidationFinding{{Rule: "orphan"}}
	inline, spawn, _ := InlinePlan(nil, nil, findings, nil, 1, false, false)

	if len(inline) != 0 || joined(spawn) != "orphan" {
		t.Fatalf("orphan finding: inline=%v spawn=%v, want spawn=[orphan]", inline, spawn)
	}
}

func TestInlinePlan_SpecDefectRemovedRemainderCommand_AllInline(t *testing.T) {
	// C8 already stripped the spec-defect investigator upstream; InlinePlan only
	// sees the remaining active set, which is all pure-command.
	invs := []Investigator{cmdInv("build"), cmdInv("phpstan")}
	inline, spawn, reason := InlinePlan(invs, nil, findingsFor(invs...), nil, 1, false, false)

	if joined(inline) != "build,phpstan" || len(spawn) != 0 {
		t.Fatalf("post-C8 all-command remainder: inline=%v spawn=%v (reason=%q)", inline, spawn, reason)
	}
}

func TestInlinePlan_DeterministicSorted(t *testing.T) {
	invs := []Investigator{cmdInv("zeta"), cmdInv("alpha"), reasoningInv("mike"), reasoningInv("bravo")}
	findings := findingsFor(invs...)

	i1, s1, _ := InlinePlan(invs, nil, findings, nil, 1, false, false)
	i2, s2, _ := InlinePlan(invs, nil, findings, nil, 1, false, false)

	if joined(i1) != joined(i2) || joined(s1) != joined(s2) {
		t.Fatalf("non-deterministic: inline %v/%v spawn %v/%v", i1, i2, s1, s2)
	}
	if joined(i1) != "alpha,zeta" {
		t.Fatalf("inline = %v, want sorted [alpha zeta]", i1)
	}
	if joined(s1) != "bravo,mike" {
		t.Fatalf("spawn = %v, want sorted [bravo mike]", s1)
	}
}

func TestInlinePlan_ForceFull_AllCommand_AllInline(t *testing.T) {
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

	inline, spawn, _ := InlinePlan(invs, prev, findings, nil, 1, true /*forceFull*/, false)

	if joined(inline) != "build,lint" || len(spawn) != 0 {
		t.Fatalf("forceFull: inline=%v spawn=%v, want all inline", inline, spawn)
	}
}

//go:build c2_gate
// +build c2_gate

package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestC2Gate_Section51RequiresClassifyVerdict pins §5.1's contract on the C2
// routing wiring. It compiles ONLY when CascadeFailure has its 2-arg, C2-wired
// signature (failedGoalID, verdictClass) — a regression to the legacy 1-arg form
// fails the build under `go test -tags c2_gate ./internal/taskvisor/...`. It also
// asserts the verdict CLASS (the value C2's ClassifyVerdict feeds) actually drives
// the hard-vs-soft branch, so the gate guards behavior, not just the signature.
func TestC2Gate_Section51RequiresClassifyVerdict(t *testing.T) {
	// Hard class → dependents GoalBlocked. VerdictFail is the C1/C2 verdict const,
	// proving the gate binds to the shared enum, not a bare literal.
	hard := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalFailed},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	hard.CascadeFailure("goal-001", VerdictFail)
	dep, _ := hard.GoalByID("goal-002")
	assert.Equal(t, GoalBlocked, dep.Status, "hard verdict class blocks dependents")
	assert.Equal(t, "goal-001", dep.BlockedBy)

	// Soft class → dependents stay GoalPending with BlockedBy recorded.
	soft := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalBlocked},
		{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
	}}
	soft.CascadeFailure("goal-001", "env-config")
	dep2, _ := soft.GoalByID("goal-002")
	assert.Equal(t, GoalPending, dep2.Status, "soft verdict class leaves dependents pending")
	assert.Equal(t, "goal-001", dep2.BlockedBy)
}

// TestC2Gate_CallSitesPassExplicitClass asserts (at compile time, by reference)
// that the production CascadeFailure call sites compile against the 2-arg form —
// the C2 wiring threads an explicit verdictClass into handleFailedCycle. The
// reference to the method value below would not compile if the signature reverted.
func TestC2Gate_CallSitesPassExplicitClass(t *testing.T) {
	var gf *GoalsFile
	// Compile-time binding to the 2-arg signature.
	fn := gf.CascadeFailure
	_ = fn
	gf2 := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalFailed}}}
	gf2.CascadeFailure("goal-001", "code-defect") // no dependents — pure signature exercise
}

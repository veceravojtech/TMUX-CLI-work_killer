//go:build c1_gate
// +build c1_gate

package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyVerdict_M02_CodeDefect(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "unit tests", Status: VerdictFail, FailureClass: "code-defect"},
	})
	assert.Equal(t, VerdictFail, v)
	assert.Equal(t, "implementer", o)
}

func TestClassifyVerdict_M01_EnvConfig(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "secret present", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
	})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "ops", o)
}

func TestClassifyVerdict_M04b_InfraFlake(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "service reachable", Status: VerdictBlocked, FailureClass: "infra-flake", Owner: "ops"},
	})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "ops", o)
}

func TestClassifyVerdict_M06_SpecDefect(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "spec consistent", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner"},
	})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "planner", o)
}

func TestClassifyVerdict_OwnerPriority(t *testing.T) {
	// A planner-owned spec-defect block and an ops-owned env-config block:
	// planner wins (planner > ops).
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "env", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
		{Rule: "spec", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner"},
	})
	assert.Equal(t, VerdictBlocked, v)
	assert.Equal(t, "planner", o)
}

func TestClassifyVerdict_AllPass(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "a", Status: VerdictPass},
		{Rule: "b", Status: VerdictPass},
	})
	assert.Equal(t, VerdictPass, v)
	assert.Equal(t, "", o)
}

func TestClassifyVerdict_Empty(t *testing.T) {
	v, o := ClassifyVerdict(nil)
	assert.Equal(t, VerdictPass, v)
	assert.Equal(t, "", o)
}

func TestClassifyVerdict_Deterministic(t *testing.T) {
	findings := []ValidationFinding{
		{Rule: "zeta", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
		{Rule: "alpha", Status: VerdictBlocked, FailureClass: "spec-defect", Owner: "planner"},
		{Rule: "mid", Status: VerdictPass},
	}
	reversed := []ValidationFinding{findings[2], findings[1], findings[0]}

	v1, o1 := ClassifyVerdict(findings)
	v2, o2 := ClassifyVerdict(reversed)
	assert.Equal(t, v1, v2)
	assert.Equal(t, o1, o2)
	assert.Equal(t, VerdictBlocked, v1)
	assert.Equal(t, "planner", o1)
}

func TestClassifyVerdict_Leaf4Catchall_EmptyClass(t *testing.T) {
	// A non-pass finding with no class must never silently become pass.
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "mystery", Status: VerdictFail},
	})
	assert.Equal(t, VerdictFail, v)
	assert.Equal(t, "implementer", o)
}

func TestClassifyVerdict_Leaf4Catchall_UnknownClass(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "mystery", Status: VerdictBlocked, FailureClass: "gremlins"},
	})
	assert.Equal(t, VerdictFail, v)
	assert.Equal(t, "implementer", o)
}

func TestClassifyVerdict_ValidatorError(t *testing.T) {
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "validator", Status: VerdictError, FailureClass: "validator-error", Owner: "ops"},
	})
	assert.Equal(t, VerdictError, v)
	assert.Equal(t, "ops", o)
}

func TestClassifyVerdict_CodeDefectBeatsBlocked(t *testing.T) {
	// Precedence: a code defect outranks a concurrent block.
	v, o := ClassifyVerdict([]ValidationFinding{
		{Rule: "block", Status: VerdictBlocked, FailureClass: "env-config", Owner: "ops"},
		{Rule: "defect", Status: VerdictFail, FailureClass: "code-defect"},
	})
	assert.Equal(t, VerdictFail, v)
	assert.Equal(t, "implementer", o)
}

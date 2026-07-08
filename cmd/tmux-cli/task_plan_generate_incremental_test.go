package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// incrementalBranch extracts the spine's <step n="0a" ...> planning-mode /
// incremental section so assertions about the incremental contract don't
// accidentally pass on roadmap-mode text elsewhere in the file.
func incrementalBranch(t *testing.T) string {
	t.Helper()
	spine := readEmbeddedCommand(t, "task-plan-generate.xml")

	start := strings.Index(spine, `<step n="0a"`)
	require.NotEqual(t, -1, start,
		"spine must carry a <step n=\"0a\"> planning-mode gate (incremental vs roadmap)")
	end := strings.Index(spine[start:], "</step>")
	require.NotEqual(t, -1, end, "the mode-gate step must be well-formed")
	return spine[start : start+end]
}

// TestTaskPlanGenerateXml_IncrementalModeEntry: the daemon enters incremental
// mode via the invocation argument `/tmux:task-plan-generate incremental`
// (primary — robust across setting.yaml rewrites), with the
// `planning_mode: incremental` key in .tmux-cli/setting.yaml as the documented
// fallback. Roadmap mode stays the default when neither signal is present.
func TestTaskPlanGenerateXml_IncrementalModeEntry(t *testing.T) {
	branch := incrementalBranch(t)

	assert.Contains(t, branch, "$ARGUMENTS",
		"the mode gate must read the invocation argument (primary mode signal)")
	assert.Contains(t, branch, "planning_mode",
		"the mode gate must read the planning_mode key from setting.yaml (fallback signal)")
	assert.Contains(t, branch, ".tmux-cli/setting.yaml",
		"the fallback signal must be read from .tmux-cli/setting.yaml")
	assert.Contains(t, branch, "roadmap",
		"the mode gate must name roadmap as the default mode when no incremental signal is present")
}

// TestTaskPlanGenerateXml_IncrementalOneGoalCap: in incremental mode the
// command authors AT MOST ONE goal per invocation — never a batch, never the
// roadmap. The goal is CONCRETE (born pending, not a roadmap skeleton):
// description <=120 chars plus real acceptance/validate/scope/phase, via the
// existing goal-create authoring path.
func TestTaskPlanGenerateXml_IncrementalOneGoalCap(t *testing.T) {
	branch := incrementalBranch(t)

	assert.Contains(t, branch, "AT MOST ONE goal",
		"the incremental branch must state the hard one-goal cap")
	assert.Contains(t, branch, "EXACTLY ONE",
		"the incremental branch must mandate exactly one goal-create when the product is not complete")
	assert.Contains(t, branch, "goal-create",
		"the incremental branch must author through the existing goal-create path")
	assert.Contains(t, branch, "status=pending",
		"the incremental goal is born pending (concrete), never a roadmap skeleton")
	for _, field := range []string{"acceptance", "validate", "scope", "phase"} {
		assert.Contains(t, branch, field,
			"the incremental goal must carry a real %s", field)
	}
}

// TestTaskPlanGenerateXml_IncrementalProductCompleteMarker: when every product
// deliverable is met AND verified, the incremental branch writes the marker
// file .tmux-cli/taskvisor-product-complete (short one-line reason as body)
// INSTEAD of creating a goal. An already-present marker is a no-op.
func TestTaskPlanGenerateXml_IncrementalProductCompleteMarker(t *testing.T) {
	branch := incrementalBranch(t)

	assert.Contains(t, branch, ".tmux-cli/taskvisor-product-complete",
		"the incremental branch must write the exact product-complete marker path the daemon polls")
	assert.Contains(t, branch, "instead of creating a goal",
		"the marker write must replace goal creation, never accompany it")
	assert.Contains(t, branch, "already exists",
		"an already-present marker must be a no-op (product was already complete)")
}

// TestTaskPlanGenerateXml_IncrementalProductCompleteFrontend: the step-0a
// incremental product-complete predicate is gated on frontend coverage, not
// backend-only. When a frontend is in scope (has_frontend) the marker
// .tmux-cli/taskvisor-product-complete is NOT written until frontend goals are
// authored AND validated; while uncovered the branch authors ONE frontend goal
// instead (HARD ONE-GOAL CAP preserved) — parallel to the author-one-goal arm.
func TestTaskPlanGenerateXml_IncrementalProductCompleteFrontend(t *testing.T) {
	branch := incrementalBranch(t)

	assert.Contains(t, branch, "has_frontend",
		"the incremental product-complete predicate must reference the frontend-in-scope signal (has_frontend)")
	assert.Contains(t, branch, "FRONTEND COVERAGE GATE",
		"the incremental branch must carry the FRONTEND COVERAGE GATE arm (a completion gate inside step-0a, not roadmap text)")
	assert.Contains(t, branch, "authored AND validated",
		"the marker must be gated on frontend goals being both authored AND validated, not backend-only")
	assert.Contains(t, branch, "author ONE frontend goal",
		"the frontend arm must preserve the HARD ONE-GOAL CAP (author ONE frontend goal, never the whole frontend)")
	// The frontend gate must govern the SAME product-complete marker write —
	// proving it is a completion gate on the product-complete predicate, not a
	// stray mention elsewhere.
	assert.Contains(t, branch, ".tmux-cli/taskvisor-product-complete",
		"the frontend gate must co-locate with the exact product-complete marker path the daemon polls")
}

// TestTaskPlanGenerateXml_IncrementalReviewAndGrounding: each incremental
// invocation reviews ground truth first — the current repo tree, the
// goals.yaml ledger, and the last finished goal against the product spec —
// and authors a CORRECTIVE, smaller goal when the previous goal failed. The
// first invocation (empty ledger) authors goal-001, the env gate.
func TestTaskPlanGenerateXml_IncrementalReviewAndGrounding(t *testing.T) {
	branch := incrementalBranch(t)

	assert.Contains(t, branch, ".tmux-cli/goals.yaml",
		"the incremental branch must read the goals.yaml ledger")
	assert.Contains(t, branch, "docs/architecture",
		"the incremental branch must ground the decision in the product spec (docs/architecture/*)")
	assert.Contains(t, branch, "CORRECTIVE",
		"a failed previous goal must yield a corrective, re-scoped goal — never a verbatim retry")
	assert.Contains(t, branch, "goal-001",
		"the first invocation (empty ledger) must author goal-001 — the env gate")
	assert.Contains(t, branch, "step-1-gate0.xml",
		"the first-invocation env gate must reuse the existing Gate 0 shape")
}

// TestTaskPlanGenerateXml_RoadmapModeUntouched: the incremental addition must
// not disturb the roadmap flow — the roadmap steps, the roadmap-skeleton
// execution rule, and the mandatory <error-reporting> reference all stay.
func TestTaskPlanGenerateXml_RoadmapModeUntouched(t *testing.T) {
	spine := readEmbeddedCommand(t, "task-plan-generate.xml")

	assert.Contains(t, spine, "ROADMAP SKELETON on EVERY enumerated goal-create",
		"the roadmap-skeleton execution rule must survive")
	assert.Contains(t, spine, `<step n="0" title="Validate discovery outputs">`,
		"roadmap Step 0 must survive untouched")
	assert.Contains(t, spine, "error-reporting",
		"the spine must keep the mandatory shared error-reporting reference")

	rulesStart := strings.Index(spine, "<execution-rules>")
	rulesEnd := strings.Index(spine, "</execution-rules>")
	require.NotEqual(t, -1, rulesStart)
	require.NotEqual(t, -1, rulesEnd)
	assert.Contains(t, spine[rulesStart:rulesEnd], "<error-reporting>",
		"the error-reporting reference must live inside execution-rules")
}

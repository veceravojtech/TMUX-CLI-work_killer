package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReportFilePath_Shape: ReportFilePath is the single naming authority for
// per-cycle reports — `.tmux-cli/e2e-evaluator/e2e-report-<scenario>-cycle-<n>.md`
// under the conductor's owned dir, scenario-scoped so one scenario's fresh
// sweep can never name another scenario's reports.
func TestReportFilePath_Shape(t *testing.T) {
	assert.Equal(t,
		"/repo/.tmux-cli/e2e-evaluator/e2e-report-scn-cycle-3.md",
		ReportFilePath("/repo", "scn", 3))
	assert.Equal(t,
		"/repo/.tmux-cli/e2e-evaluator/e2e-report-symfony-dashboard-login-cycle-10.md",
		ReportFilePath("/repo", "symfony-dashboard-login", 10))
}

// TestIsScenarioReport_AnchoredSlug: the slug match is anchored by the
// following "-cycle-<digits>.md", so a sibling scenario whose slug extends
// this one ("scn" vs "scn-two") is never cross-matched in either direction.
func TestIsScenarioReport_AnchoredSlug(t *testing.T) {
	assert.True(t, IsScenarioReport("e2e-report-scn-cycle-1.md", "scn"))
	assert.True(t, IsScenarioReport("e2e-report-scn-cycle-12.md", "scn"))

	// Sibling slug extending ours is NOT ours.
	assert.False(t, IsScenarioReport("e2e-report-scn-two-cycle-1.md", "scn"))
	// And the reverse direction never matches either.
	assert.False(t, IsScenarioReport("e2e-report-scn-cycle-1.md", "scn-two"))

	// Legacy unscoped shape is not a scenario report.
	assert.False(t, IsScenarioReport("e2e-report-cycle-1.md", "scn"))
	// Non-report files never match.
	assert.False(t, IsScenarioReport("scn.state.json", "scn"))
	assert.False(t, IsScenarioReport("e2e-report-scn-cycle-.md", "scn"))
	assert.False(t, IsScenarioReport("e2e-report-scn-cycle-1.md.bak", "scn"))
}

// TestIsLegacyReport: the pre-scenario shape e2e-report-cycle-<n>.md — and
// ONLY that shape — is a legacy orphan. A scenario whose slug happens to start
// with "cycle-" must never be caught by the legacy matcher.
func TestIsLegacyReport(t *testing.T) {
	assert.True(t, IsLegacyReport("e2e-report-cycle-1.md"))
	assert.True(t, IsLegacyReport("e2e-report-cycle-10.md"))

	assert.False(t, IsLegacyReport("e2e-report-scn-cycle-1.md"))
	// A scenario slug starting with "cycle-" is scenario-scoped, not legacy.
	assert.False(t, IsLegacyReport("e2e-report-cycle-foo-cycle-1.md"))
	assert.False(t, IsLegacyReport("e2e-report-cycle-.md"))
	assert.False(t, IsLegacyReport("e2e-report-cycle-1.txt"))
}

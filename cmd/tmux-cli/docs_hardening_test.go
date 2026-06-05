package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", rel))
	require.NoError(t, err, "doc %s must be readable", rel)
	return string(b)
}

func TestDocsTaskvisorSpec_DriftGateDocumented(t *testing.T) {
	content := readDoc(t, "taskvisor-spec.md")
	assert.Contains(t, content, "### Dispatch-time spec-drift gate")
	assert.Contains(t, content, "goals.yaml is always the source of truth")
	assert.Contains(t, content, "spec repairs:")
	assert.Contains(t, content, "zero retry budget")
}

func TestDocsTaskvisorSpec_StaleBinaryGuardDocumented(t *testing.T) {
	content := readDoc(t, "taskvisor-spec.md")
	assert.Contains(t, content, "### Stale-binary guard")
	assert.Contains(t, content, "halt_on_stale_binary")
	assert.Contains(t, content, "BINARY STALE")
	assert.Contains(t, content, "vcs.revision")
	assert.Contains(t, content, "tmux-cli mcp is stale")
}

func TestDocsTaskvisorSpec_PlanApprovalGateDocumented(t *testing.T) {
	content := readDoc(t, "taskvisor-spec.md")
	assert.Contains(t, content, "RequirePlanApproval")
	assert.Contains(t, content, "require_plan_approval")
	assert.Contains(t, content, "plan-approval.md")
}

func TestDocsTaskvisorSpec_ConfigDefaultsDocumented(t *testing.T) {
	content := readDoc(t, "taskvisor-spec.md")

	lines := strings.Split(content, "\n")
	foundReqPlan := false
	foundHaltStale := false
	for _, line := range lines {
		if strings.Contains(line, "require_plan_approval") && strings.Contains(line, "false") {
			foundReqPlan = true
		}
		if strings.Contains(line, "halt_on_stale_binary") && strings.Contains(line, "false") {
			foundHaltStale = true
		}
	}
	assert.True(t, foundReqPlan, "require_plan_approval should appear on the same line as 'false'")
	assert.True(t, foundHaltStale, "halt_on_stale_binary should appear on the same line as 'false'")
}

func TestDocsAdvancedUsage_RestartProtocolDocumented(t *testing.T) {
	content := readDoc(t, "advanced-usage.md")
	assert.Contains(t, content, "## Restart protocol")
	assert.Contains(t, content, "make install")

	hasMCPRestart := false
	hasDaemonRestart := false
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "restart") && strings.Contains(lower, "mcp") {
			hasMCPRestart = true
		}
		if strings.Contains(lower, "restart") && strings.Contains(lower, "daemon") {
			hasDaemonRestart = true
		}
	}
	assert.True(t, hasMCPRestart, "restart protocol should mention restarting the MCP server")
	assert.True(t, hasDaemonRestart, "restart protocol should mention restarting the daemon")
}

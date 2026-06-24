package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are content-presence guards over the embedded task-plan-generate.xml
// prompt template. The template drives an LLM, not Go behavior, so the tests
// lock the D3 structural invariants (auth-bootstrap step exists, the soft
// "Register does it first" rule is gone, deps are wired to AUTH_BOOTSTRAP_GOAL_ID)
// so a future prompt edit can't silently regress the deterministic sequencing gate.

func readTaskPlanGenerateTemplate(t *testing.T) string {
	t.Helper()
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err)
	return string(data)
}

// sliceBetween returns the substring of s between the first occurrence of start
// and the first occurrence of end after it. It scopes an assertion to one block
// so a legitimate mention elsewhere (e.g. the bootstrap step) cannot cause a
// false pass/fail in a per-flow-block assertion.
func sliceBetween(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	require.GreaterOrEqual(t, i, 0, "start marker %q not found in template", start)
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	require.GreaterOrEqual(t, j, 0, "end marker %q not found after %q", end, start)
	return rest[:j]
}

func TestTemplate_AuthBootstrapStepPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "phase=auth-bootstrap",
		"template must declare a phase=auth-bootstrap goal")
	assert.Contains(t, content, "Generate Auth Bootstrap goal",
		"template must contain a step titled with 'Auth Bootstrap'")
}

func TestTemplate_AuthBootstrapConditionalOnFlows(t *testing.T) {
	content := readGenerateBundle(t)
	bootstrap := sliceBetween(t, content, `n="3.16a"`, `n="3.17"`)
	assert.Contains(t, bootstrap, "N_auth_flows == 0",
		"auth-bootstrap step must contain a skip branch keyed on N_auth_flows == 0")
	assert.Contains(t, strings.ToUpper(bootstrap), "SKIP",
		"auth-bootstrap step must SKIP when there are zero auth flows")
}

func TestTemplate_SoftFirstAuthGoalRuleRemoved(t *testing.T) {
	content := readGenerateBundle(t)
	assert.NotContains(t, content, "first auth goal (typically Register) includes security.yaml",
		"the non-deterministic soft rule must be removed entirely")
}

func TestTemplate_AuthDependsOnIncludesBootstrap(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.19.2"`, `n="3.19.3"`)
	assert.Contains(t, block, "AUTH_DEPENDS_ON",
		"substep 3.19.2 must define AUTH_DEPENDS_ON")
	assert.Contains(t, block, "AUTH_BOOTSTRAP_GOAL_ID",
		"AUTH_DEPENDS_ON in substep 3.19.2 must include AUTH_BOOTSTRAP_GOAL_ID")
}

func TestTemplate_ControllerActionDependsOnBootstrap(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.18.2"`, `n="3.18.3"`)
	assert.Contains(t, block, "AUTH_BOOTSTRAP_GOAL_ID",
		"substep 3.18.2 must add AUTH_BOOTSTRAP_GOAL_ID to depends_on for auth_required actions")
	assert.Contains(t, block, "auth_required",
		"substep 3.18.2 must gate the bootstrap dependency on auth_required actions")
}

// Two-tier (director redesign §5): per-flow auth goals are roadmap skeletons that
// declare no concrete deliverables at all — security.yaml/User entity are NEVER
// owned by a per-flow goal (they belong to auth-bootstrap), and that invariant
// still holds at Tier-1: the 3.19 step carries no User-entity/security deliverable
// and explicitly notes the security config comes from the auth-bootstrap
// dependency. Scope to the whole 3.19 auth step (3.19 → 3.19a).
func TestTemplate_PerFlowDeliverablesDropSecurityConfig(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.19"`, `n="3.19a"`)
	assert.NotContains(t, block, "User entity:",
		"per-flow auth goals must not declare a 'User entity:' deliverable")
	assert.NotContains(t, block, "<deliverable>Security config",
		"per-flow auth goals must not declare a security.yaml deliverable")
	assert.Contains(t, block, "auth-bootstrap dependency",
		"the 3.19 step must note that security config + User entity come from the auth-bootstrap dependency")
}

// Two-tier: the lexik JWT keypair generation is a concrete validate/command
// authored at dispatch by /tmux:elaborate against the real tree — the
// auth-bootstrap shard is now a roadmap skeleton and no longer authors the
// keygen inline. The skeleton still carries the security-config deliverable_area
// the elaborator seeds the JWT wiring from. Scope to the 3.16a step (3.16a → 3.17).
func TestTemplate_JwtKeygenGatedOnUsesJwt(t *testing.T) {
	content := readGenerateBundle(t)
	bootstrap := sliceBetween(t, content, `n="3.16a"`, `n="3.17"`)
	assert.Contains(t, bootstrap, `<param name="status">roadmap`,
		"auth-bootstrap is emitted as a roadmap skeleton")
	assert.Contains(t, bootstrap, `<param name="deliverable_area">`,
		"the auth-bootstrap skeleton carries a coarse deliverable_area (config/packages/) the elaborator seeds JWT wiring from")
	assert.NotContains(t, bootstrap, "lexik:jwt:generate-keypair",
		"the JWT keypair generation is a Tier-2 validate authored at dispatch by /tmux:elaborate, not inline here")
}

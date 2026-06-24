package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_AuthStateConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "E2E-AUTH-STATE-CONV",
		"template must declare an E2E-AUTH-STATE-CONV rule")
	assert.Contains(t, content, `id="E2E-AUTH-STATE-CONV"`,
		"E2E-AUTH-STATE-CONV must be a named rule with id attribute")
	assert.Contains(t, content, "N_auth_flows",
		"E2E-AUTH-STATE-CONV condition must reference N_auth_flows")
	assert.Contains(t, content, "HAS_FRONTEND",
		"E2E-AUTH-STATE-CONV condition must reference HAS_FRONTEND")
}

func TestTemplate_AuthStateConvIsCritical(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, `critical="true" id="E2E-AUTH-STATE-CONV"`,
		"E2E-AUTH-STATE-CONV must have critical=\"true\" attribute")
}

func TestTemplate_AuthStateConvDualState(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "admin.json",
		"E2E-AUTH-STATE-CONV must reference admin.json storageState path")
	assert.Contains(t, content, "user.json",
		"E2E-AUTH-STATE-CONV must reference user.json storageState path")
	assert.Contains(t, content, "playwright/.auth/",
		"E2E-AUTH-STATE-CONV must reference playwright/.auth/ directory")
}

func TestTemplate_AuthStateConvScaffoldSC21(t *testing.T) {
	content := readGenerateBundle(t)
	step2 := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, step2, "SC-21",
		"step-2 scaffold must contain SC-21 acceptance criterion")
	assert.Contains(t, step2, "playwright.config.ts",
		"SC-21 must reference playwright.config.ts")
	assert.Contains(t, step2, "setup project",
		"SC-21 must reference setup project")
}

// Two-tier (director redesign §5): AU-11 (the auth.setup.ts storageState setup
// deliverable) is authored at dispatch by /tmux:elaborate — the 3.16a
// auth-bootstrap shard is a roadmap skeleton.
func TestTemplate_AuthStateConvBootstrapAU11(t *testing.T) {
	content := readGenerateBundle(t)
	step316a := sliceBetween(t, content, `n="3.16a"`, `n="3.17"`)
	assert.Contains(t, step316a, `<param name="status">roadmap`,
		"the auth-bootstrap shard emits a roadmap skeleton")
	// the concrete auth.setup.ts storageState-setup deliverable (AU-11) is authored
	// at dispatch — it must NOT be authored inline by the generator. (The relocation
	// pointer comment legitimately NAMES AU-11, so we guard on the deliverable file.)
	assert.NotContains(t, step316a, "auth.setup.ts",
		"the auth.setup.ts deliverable is authored at dispatch by /tmux:elaborate")
}

func TestTemplate_AuthStateConvNoHardcodedCredentials(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "GM-05",
		"E2E-AUTH-STATE-CONV rule must reference GM-05 credential convention")
	assert.Contains(t, content, "test-environment.md",
		"E2E-AUTH-STATE-CONV rule must reference test-environment.md")
}

// Two-tier: the "auth-flow tests MUST NOT use storageState" preservation note is
// part of the E2E-AUTH-STATE-CONV convention, which still ships in the resolved
// bundle (rules catalogue) and is applied at dispatch by /tmux:elaborate. The
// 3.19 auth step itself is now a roadmap skeleton.
func TestTemplate_AuthStateConvPreservesAuthFlows(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "MUST NOT use storageState",
		"the E2E-AUTH-STATE-CONV convention (resolved into the bundle) must preserve the auth-flow no-storageState invariant")
	step319 := sliceBetween(t, content, `n="3.19"`, `n="3.19a"`)
	assert.Contains(t, step319, `<param name="status">roadmap`,
		"the auth shard emits a roadmap skeleton")
}

// Two-tier: the auth_required controller-action storageState/authenticated-project
// usage is authored at dispatch by /tmux:elaborate — the 3.18 action shard is a
// roadmap skeleton. The E2E-AUTH-STATE-CONV convention still ships in the bundle.
func TestTemplate_AuthStateConvControllerActionUsage(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "storageState",
		"the E2E-AUTH-STATE-CONV convention (resolved into the bundle) must define storageState reuse")
	step318 := sliceBetween(t, content, `n="3.18"`, `n="3.19"`)
	assert.Contains(t, step318, `<param name="status">roadmap`,
		"the action shard emits a roadmap skeleton")
	assert.NotContains(t, step318, "storageState",
		"the auth_required storageState usage is authored at dispatch by /tmux:elaborate, not inline here")
}

func TestTemplate_AuthStateConvStateFreshness(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "ENSURE-STACK-CONV",
		"E2E-AUTH-STATE-CONV must reference ENSURE-STACK-CONV for state freshness")
	assert.Contains(t, content, "re-runs on every",
		"E2E-AUTH-STATE-CONV must mandate re-authentication on every run")
}

func TestTemplate_AuthStateConvCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "E2E-AUTH-STATE-CONV",
		"companion doc must mention E2E-AUTH-STATE-CONV")
}

func TestTemplate_AuthStateConvGitignored(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "gitignored",
		"E2E-AUTH-STATE-CONV must mandate gitignored storageState path")
}

func TestTemplate_AuthStateTestStrategy(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/_base/test-strategy.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Authenticated E2E State Reuse",
		"_base/test-strategy.md must contain Authenticated E2E State Reuse section")
	assert.Contains(t, content, "storageState",
		"_base/test-strategy.md Authenticated E2E State Reuse section must reference storageState")
}

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These guards lock the B1 invariant: the controller deliverable path is
// produced by ONE shared resolver substep (3.17.0 / RESOLVE_CONTROLLER_PATH) in
// task-plan-generate.xml, so action goals (3.18) and auth goals (3.19) can never
// diverge on the controller-path shape again. They read the embedded prompt
// templates directly, the same single source the running planner consumes.

// canonicalControllerLiteral is the full canonical controller-path shape. It is
// the single source of truth and must appear in exactly one place in the XML.
// P2 monorepo: controllers live in the context app (framework) layer, never the
// retired flat Infrastructure/ tree.
const canonicalControllerLiteral = "contexts/{BC}/app/src/Http/Controller/{NAME}Controller.php"

func readApiEndpointsTemplate(t *testing.T) string {
	t.Helper()
	data, err := embeddedTemplates.ReadFile("embedded/templates/_base/api-endpoints.md")
	require.NoError(t, err)
	return string(data)
}

func TestTaskPlanGenerate_ResolverDefinedExactlyOnce(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Equal(t, 1, strings.Count(content, `n="3.17.0"`),
		"the shared resolver step id 3.17.0 must appear exactly once")
	assert.Equal(t, 1, strings.Count(content, canonicalControllerLiteral),
		"the full canonical controller-path shape must be stated in exactly one place (the resolver) — everywhere else references RESOLVE_CONTROLLER_PATH")
}

func TestTaskPlanGenerate_NoDivergentAuthControllerPath(t *testing.T) {
	content := readGenerateBundle(t)
	assert.NotContains(t, content, "Infrastructure/Http/{AuthAction}Controller.php",
		"the legacy hardcoded auth controller path (no /Controller/ segment) must be gone")
}

// Two-tier (director redesign §5): the auth shard is now a roadmap skeleton, but
// it still derives its coarse deliverable_area through the shared resolver, so the
// B1 single-source invariant survives at Tier-1 (the precise per-file controller
// deliverable is authored at dispatch by /tmux:elaborate). Scope to the whole 3.19
// auth step (3.19 → 3.19a).
func TestTaskPlanGenerate_AuthSectionUsesResolver(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.19"`, `n="3.19a"`)
	assert.Contains(t, block, "RESOLVE_CONTROLLER_PATH",
		"the auth shard must derive its controller deliverable_area via RESOLVE_CONTROLLER_PATH, not a literal path")
	assert.NotContains(t, block, "Infrastructure/Http/{AuthAction}Controller.php",
		"the auth block must not restate the legacy literal controller path")
}

// Two-tier: the action shard is a roadmap skeleton; it still routes its coarse
// controller deliverable_area through the shared resolver. The precise 5-file
// {controller_path} deliverable is authored at dispatch by /tmux:elaborate. Scope
// to the whole 3.18 action step (3.18 → 3.19).
func TestTaskPlanGenerate_ActionSectionUsesResolver(t *testing.T) {
	content := readGenerateBundle(t)
	block := sliceBetween(t, content, `n="3.18"`, `n="3.19"`)
	assert.Contains(t, block, "RESOLVE_CONTROLLER_PATH",
		"the action shard must bind the controller deliverable_area via RESOLVE_CONTROLLER_PATH")
	assert.NotContains(t, block, "Infrastructure/Http/{AuthAction}Controller.php",
		"the action block must not restate the legacy literal controller path")
}

func TestTaskPlanGenerate_CanonicalShapeIsControllerSubdir(t *testing.T) {
	content := readGenerateBundle(t)
	resolver := sliceBetween(t, content, `<resolver name="RESOLVE_CONTROLLER_PATH">`, `</resolver>`)
	produced := sliceBetween(t, resolver, "<produces>", "</produces>")
	assert.Equal(t, canonicalControllerLiteral, strings.TrimSpace(produced),
		"the resolver must produce the canonical /Controller/ subdir shape")
	assert.Contains(t, produced, "app/src/Http/Controller/",
		"the canonical shape must place the controller under the context app Http/Controller/ subdir")
}

func TestApiEndpointsTemplate_FanOutControllerShape(t *testing.T) {
	content := readApiEndpointsTemplate(t)
	require.Contains(t, content, "- Controller:",
		"the Fan-Out line must be labelled 'Controller'")
	line := sliceBetween(t, content, "- Controller:", "\n")
	assert.Contains(t, line, "app/src/Http/Controller/",
		"the Fan-Out Controller line must document the canonical context-app /Controller/ subdir shape")
	assert.Contains(t, line, "Controller.php",
		"the Fan-Out Controller line must document the {action_name}Controller.php suffix")
}

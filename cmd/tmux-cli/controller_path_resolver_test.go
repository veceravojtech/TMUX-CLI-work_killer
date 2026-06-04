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
const canonicalControllerLiteral = "src/{BC}/Infrastructure/Http/Controller/{NAME}Controller.php"

func readApiEndpointsTemplate(t *testing.T) string {
	t.Helper()
	data, err := embeddedTemplates.ReadFile("embedded/templates/_base/api-endpoints.md")
	require.NoError(t, err)
	return string(data)
}

func TestTaskPlanGenerate_ResolverDefinedExactlyOnce(t *testing.T) {
	content := readTaskPlanGenerateTemplate(t)
	assert.Equal(t, 1, strings.Count(content, `n="3.17.0"`),
		"the shared resolver step id 3.17.0 must appear exactly once")
	assert.Equal(t, 1, strings.Count(content, canonicalControllerLiteral),
		"the full canonical controller-path shape must be stated in exactly one place (the resolver) — everywhere else references RESOLVE_CONTROLLER_PATH")
}

func TestTaskPlanGenerate_NoDivergentAuthControllerPath(t *testing.T) {
	content := readTaskPlanGenerateTemplate(t)
	assert.NotContains(t, content, "Infrastructure/Http/{AuthAction}Controller.php",
		"the legacy hardcoded auth controller path (no /Controller/ segment) must be gone")
}

func TestTaskPlanGenerate_AuthSectionUsesResolver(t *testing.T) {
	content := readTaskPlanGenerateTemplate(t)
	block := sliceBetween(t, content, `n="3.19.4"`, `n="3.19.5"`)
	assert.Contains(t, block, "RESOLVE_CONTROLLER_PATH",
		"the auth controller deliverable/investigator (3.19.4) must reference RESOLVE_CONTROLLER_PATH, not a literal path")
	assert.NotContains(t, block, "Infrastructure/Http/{AuthAction}Controller.php",
		"the auth block must not restate the legacy literal controller path")
}

func TestTaskPlanGenerate_ActionSectionUsesResolver(t *testing.T) {
	content := readTaskPlanGenerateTemplate(t)
	block := sliceBetween(t, content, `n="3.18.5"`, `n="3.18.6"`)
	assert.Contains(t, block, "RESOLVE_CONTROLLER_PATH",
		"the action context block (3.18.5) must bind the controller path via RESOLVE_CONTROLLER_PATH")
	assert.Contains(t, block, "{controller_path}",
		"the action deliverable n=3 and investigator file list must use the bound {controller_path}")
	assert.NotContains(t, block, "{handler_path}",
		"the action block must no longer use the bare Fan-Out {handler_path} for the controller")
}

func TestTaskPlanGenerate_CanonicalShapeIsControllerSubdir(t *testing.T) {
	content := readTaskPlanGenerateTemplate(t)
	resolver := sliceBetween(t, content, `<resolver name="RESOLVE_CONTROLLER_PATH">`, `</resolver>`)
	produced := sliceBetween(t, resolver, "<produces>", "</produces>")
	assert.Equal(t, canonicalControllerLiteral, strings.TrimSpace(produced),
		"the resolver must produce the canonical /Controller/ subdir shape")
	assert.Contains(t, produced, "Infrastructure/Http/Controller/",
		"the canonical shape must place the controller under the Http/Controller/ subdir")
}

func TestApiEndpointsTemplate_FanOutControllerShape(t *testing.T) {
	content := readApiEndpointsTemplate(t)
	require.Contains(t, content, "- Controller:",
		"the Fan-Out line must be labelled 'Controller'")
	line := sliceBetween(t, content, "- Controller:", "\n")
	assert.Contains(t, line, "Infrastructure/Http/Controller/",
		"the Fan-Out Controller line must document the canonical /Controller/ subdir shape")
	assert.Contains(t, line, "Controller.php",
		"the Fan-Out Controller line must document the {action_name}Controller.php suffix")
}

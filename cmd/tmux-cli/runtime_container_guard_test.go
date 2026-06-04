package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readEmbeddedTemplate returns the text of a template shipped via the
// embeddedTemplates embed.FS (under embedded/templates/). The `//go:embed
// all:embedded/templates` directive includes the `_base` tier, so both the base
// and overlay tiers are reachable here (guarded by TestEmbeddedTemplates_*).
func readEmbeddedTemplate(t *testing.T, rel string) string {
	t.Helper()
	b, err := fs.ReadFile(embeddedTemplates, "embedded/templates/"+rel)
	require.NoError(t, err, "embedded template %s must be readable", rel)
	return string(b)
}

// TestInvestigateWorkerXML_HasRuntimeContainerPreflight verifies the investigator
// worker checks the runtime container is up (docker inspect / State.Running) and
// emits a failure_class=infra-flake finding BEFORE it executes any SCOPE command.
func TestInvestigateWorkerXML_HasRuntimeContainerPreflight(t *testing.T) {
	content := readEmbeddedCommand(t, "investigate-worker.xml")

	assert.Contains(t, content, "runtime-container-preflight",
		"step 3 must carry a runtime-container preflight block")
	assert.Contains(t, content, "docker inspect",
		"the preflight must verify the container via docker inspect")
	assert.Contains(t, content, "State.Running",
		"the preflight must read the container's State.Running field")
	assert.Contains(t, content, "failure_class=infra-flake",
		"a down container must be emitted as failure_class=infra-flake")

	// The preflight must run BEFORE command execution, otherwise a dead container
	// still gets php/vendor/bin/* commands fired at it.
	preflight := strings.Index(content, "runtime-container-preflight")
	exec := strings.Index(content, "For each command in SCOPE's commands field")
	require.NotEqual(t, -1, preflight, "preflight block must be present")
	require.NotEqual(t, -1, exec, "the command-execution step must be present")
	assert.Less(t, preflight, exec,
		"the runtime-container preflight must precede command execution")
}

// TestInvestigateWorkerXML_ForbidsCodeDefectForDeadContainer verifies the worker
// carries the critical rule that a dead/missing container is an environment fault
// (infra-flake / owner=ops), never a code defect — the footgun this guard fixes.
func TestInvestigateWorkerXML_ForbidsCodeDefectForDeadContainer(t *testing.T) {
	content := readEmbeddedCommand(t, "investigate-worker.xml")

	// Isolate the preflight block so the rule is asserted in context.
	start := strings.Index(content, "<runtime-container-preflight")
	end := strings.Index(content, "</runtime-container-preflight>")
	require.NotEqual(t, -1, start, "preflight block must open")
	require.NotEqual(t, -1, end, "preflight block must close")
	block := content[start:end]

	assert.Contains(t, block, "infra-flake",
		"the rule must classify a dead container as infra-flake")
	assert.Contains(t, block, "owner=ops",
		"the rule must assign ownership to ops")
	assert.Contains(t, block, "NEVER code-defect",
		"the rule must forbid classifying a dead container as a code defect")
}

// TestAgentsTemplates_HaveRuntimeContainerField verifies both agents.md tiers
// carry a `Runtime Container` field inside the {{#if is_docker}} branch of the
// GATE0 block, and retain the GATE0:BEGIN/END sentinels.
func TestAgentsTemplates_HaveRuntimeContainerField(t *testing.T) {
	for _, rel := range []string{"_base/agents.md", "php-symfony/agents.md"} {
		content := readEmbeddedTemplate(t, rel)

		assert.Contains(t, content, "<!-- GATE0:BEGIN -->",
			"%s must retain the GATE0:BEGIN sentinel", rel)
		assert.Contains(t, content, "<!-- GATE0:END -->",
			"%s must retain the GATE0:END sentinel", rel)

		// The Runtime Container field must live in the docker branch of the
		// ## Environment block (between ## Environment and ## Database).
		envStart := strings.Index(content, "## Environment")
		envEnd := strings.Index(content, "## Database")
		require.NotEqual(t, -1, envStart, "%s must have an ## Environment block", rel)
		require.NotEqual(t, -1, envEnd, "%s must have a ## Database block", rel)
		env := content[envStart:envEnd]

		docker := strings.Index(env, "{{#if is_docker}}")
		rc := strings.Index(env, "Runtime Container")
		require.NotEqual(t, -1, docker, "%s ## Environment must open an is_docker branch", rel)
		require.NotEqual(t, -1, rc, "%s must declare a Runtime Container field", rel)
		assert.Less(t, docker, rc,
			"%s Runtime Container field must sit inside the {{#if is_docker}} branch", rel)
	}
}

// TestEnvironmentGateTemplates_RecordContainerName verifies both environment-gate
// templates instruct Gate 0 to record the resolved runtime container name into the
// AGENTS.md ground-truth field, alongside the ## 0 Container Runtime content.
func TestEnvironmentGateTemplates_RecordContainerName(t *testing.T) {
	for _, rel := range []string{"_base/environment-gate.md", "php-symfony/environment-gate.md"} {
		content := readEmbeddedTemplate(t, rel)

		idx := strings.Index(content, "Container Runtime")
		require.NotEqual(t, -1, idx, "%s must reference the ## 0 Container Runtime section", rel)
		rest := content[idx:]

		assert.Contains(t, rest, "Runtime Container",
			"%s must name the AGENTS.md Runtime Container field", rel)
		assert.Contains(t, rest, "AGENTS.md",
			"%s must point at the AGENTS.md ground-truth sink", rel)
		assert.Contains(t, strings.ToLower(rest), "record",
			"%s must instruct Gate 0 to record the container name", rel)
	}
}

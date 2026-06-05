package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- D1b: generation emits a compose goal on RUN_TARGET=docker, fed real published ports ---
//
// These tests assert the prompt contract of step 3.26 in the embedded
// task-plan-generate.xml (+ companion .md). They follow the templates_share_test.go
// pattern: os.ReadFile the embedded asset + assert.Contains / assert.NotContains.

func readGenerateTemplate(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "commands", "tmux", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "%s must exist", name)
	return string(data)
}

func readGenerateBundle(t *testing.T) string {
	t.Helper()
	spine := readGenerateTemplate(t, "task-plan-generate.xml")
	stubRe := regexp.MustCompile(`(?s)<step n="[^"]+" title="[^"]+">\s*<load file="([^"]+)">[^<]*</load>\s*</step>`)
	result := stubRe.ReplaceAllStringFunc(spine, func(stub string) string {
		m := stubRe.FindStringSubmatch(stub)
		require.Len(t, m, 2, "stub must capture file path")
		rel := strings.Replace(m[1], ".claude/commands/tmux/", "", 1)
		shard := readGenerateTemplate(t, rel)
		return shard
	})
	return result
}

// Dev compose for a docker project is FRONT-LOADED into goal-002 (task-R), firing on
// RUN_TARGET=docker — so the container exists before any PHP command, not deferred to
// the late step-3.26 deployment goal (the ordering bug the front-load fixed).
func TestGenerateXML_DevComposeFrontloadedOnRunTargetDocker(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content, `id="DOCKER-RUNTIME-FRONTLOAD" condition="RUN_TARGET=docker"`)
	assert.Contains(t, content, "goal-002 MUST first MATERIALIZE AND START the dev runtime container")
	assert.Contains(t, content, `Prepend a fan-out task "task-R"`)
	assert.Contains(t, content, "docker compose up -d --build")
}

// Post-front-load, the late step-3.26 docker goal is PRODUCTION-DEPLOYMENT-ONLY: the
// dev runtime moved to goal-002 task-R, so RUN_TARGET=docker ALONE no longer fires
// 3.26 — only a Docker/deployment ADR does.
func TestGenerateXML_ProdDockerGoalIsAdrOnly(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content, "PRODUCTION-DEPLOYMENT-ONLY")
	assert.Contains(t, content, "skip unless a Docker/container/deployment ADR exists")
	assert.Contains(t, content, "COMPOSE_TRIGGER = ADR_PRESENT")
	assert.Contains(t, content, "RUN_TARGET=docker ALONE no longer fires this goal")
	// the old docker-firing trigger must be gone
	assert.NotContains(t, content, "COMPOSE_TRIGGER = (RUN_TARGET==docker) OR ADR_PRESENT")
}

// DK-02/DK-04 source port mappings from the Published Ports block, not DOCKER_ADR_SUMMARY.
func TestGenerateXML_ComposePortsFromPublishedPorts(t *testing.T) {
	content := readGenerateBundle(t)

	// DK-02: each service + host:container mapping from PUBLISHED_PORTS.
	assert.Contains(t, content, "host:container port mapping from the Published Ports block (PUBLISHED_PORTS)")
	// DK-04: accessible at the published host port APP_PORT from PUBLISHED_PORTS.
	assert.Contains(t, content, "published host port APP_PORT (from PUBLISHED_PORTS)")
	// The old ADR-sourced DK-02 wording must be gone.
	assert.NotContains(t, content, "includes all services from ADRs (DB, Redis, etc.) — read compose file, services match")
}

// 3.26.5 validate curls $APP_PORT (from published ports), with no hardcoded port literal or stale $PORT.
func TestGenerateXML_ValidateUsesPublishedAppPort(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content, "curl -sf http://localhost:$APP_PORT/health")
	assert.NotContains(t, content, "http://localhost:$PORT/health",
		"the stale hardcoded $PORT reference must be replaced by $APP_PORT")
}

// When RUN_TARGET=docker without an ADR, DOCKER_ADR_SUMMARY is optional and base image derives from architecture files.
func TestGenerateXML_DockerAdrSummaryOptional(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content, "DOCKER_ADR_SUMMARY is OPTIONAL")
	assert.Contains(t, content, "When RUN_TARGET=docker without an ADR, leave DOCKER_ADR_SUMMARY unset")
	assert.Contains(t, content, "BASE_IMAGE = from DOCKER_ADR_SUMMARY when ADR_PRESENT, else the runtime/framework default")
}

// 3.26.7 not_in_scope preserves the production-deployment exclusions verbatim.
func TestGenerateXML_ProdDeployExtrasPreserved(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content,
		"production deployment orchestration (Kubernetes, Swarm), cloud provider setup, SSL/TLS configuration.")
}

// 3.26.3 defines an APP_PORT fallback to the base-URL port plus a recorded context note when the block is absent.
func TestGenerateXML_PublishedPortsFallbackToBaseUrl(t *testing.T) {
	content := readGenerateBundle(t)

	assert.Contains(t, content, "FALLBACK when the Published Ports block is ABSENT: set APP_PORT = the port from the base URL")
	assert.Contains(t, content, "record a context note that ports were defaulted from the base URL")
}

// The companion cheat-sheet states the dev runtime is front-loaded to goal-002
// task-R on RUN_TARGET=docker, and that step 3.26 is production-deployment-only.
func TestGenerateMD_DockerTriggerNoteUpdated(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.md")

	assert.Contains(t, content, "production-deployment-only")
	assert.Contains(t, content, "task-R")
	assert.Contains(t, content, "Published Ports")
	assert.NotContains(t, content, "RUN_TARGET=docker OR a deployment ADR",
		"the stale docker-OR-ADR trigger note must be gone")
}

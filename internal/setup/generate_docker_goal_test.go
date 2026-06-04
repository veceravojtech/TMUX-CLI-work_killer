package setup

import (
	"os"
	"path/filepath"
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

// 3.26.1 fires the compose goal keyed on RUN_TARGET==docker, not ADR-presence alone.
func TestGenerateXML_ComposeGoalFiresOnRunTargetDocker(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content, "Determine compose trigger (RUN_TARGET=docker OR deployment ADR)")
	assert.Contains(t, content, "COMPOSE_TRIGGER = (RUN_TARGET==docker) OR ADR_PRESENT")
	// RUN_TARGET read rule must be byte-identical to substep 1.2 / step 0b.
	assert.Contains(t, content, `"Run Target" == docker → set RUN_TARGET=docker, else RUN_TARGET=local`)
}

// The critical skip rule now AND-s RUN_TARGET!=docker with no ADR — it is no longer ADR-only.
func TestGenerateXML_SkipRuleNoLongerAdrOnly(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content, "skip ONLY if RUN_TARGET!=docker AND no Docker/container/deployment ADR")
	assert.NotContains(t, content, "skip entirely if no Docker/container/deployment ADR exists",
		"the bare ADR-only skip gate must be gone")
}

// DK-02/DK-04 source port mappings from the Published Ports block, not DOCKER_ADR_SUMMARY.
func TestGenerateXML_ComposePortsFromPublishedPorts(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	// DK-02: each service + host:container mapping from PUBLISHED_PORTS.
	assert.Contains(t, content, "host:container port mapping from the Published Ports block (PUBLISHED_PORTS)")
	// DK-04: accessible at the published host port APP_PORT from PUBLISHED_PORTS.
	assert.Contains(t, content, "published host port APP_PORT (from PUBLISHED_PORTS)")
	// The old ADR-sourced DK-02 wording must be gone.
	assert.NotContains(t, content, "includes all services from ADRs (DB, Redis, etc.) — read compose file, services match")
}

// 3.26.5 validate curls $APP_PORT (from published ports), with no hardcoded port literal or stale $PORT.
func TestGenerateXML_ValidateUsesPublishedAppPort(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content, "curl -s http://localhost:$APP_PORT/health")
	assert.NotContains(t, content, "http://localhost:$PORT/health",
		"the stale hardcoded $PORT reference must be replaced by $APP_PORT")
}

// When RUN_TARGET=docker without an ADR, DOCKER_ADR_SUMMARY is optional and base image derives from architecture files.
func TestGenerateXML_DockerAdrSummaryOptional(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content, "DOCKER_ADR_SUMMARY is OPTIONAL")
	assert.Contains(t, content, "When RUN_TARGET=docker without an ADR, leave DOCKER_ADR_SUMMARY unset")
	assert.Contains(t, content, "BASE_IMAGE = from DOCKER_ADR_SUMMARY when ADR_PRESENT, else the runtime/framework default")
}

// 3.26.7 not_in_scope preserves the production-deployment exclusions verbatim.
func TestGenerateXML_ProdDeployExtrasPreserved(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content,
		"production deployment orchestration (Kubernetes, Swarm), cloud provider setup, SSL/TLS configuration.")
}

// 3.26.3 defines an APP_PORT fallback to the base-URL port plus a recorded context note when the block is absent.
func TestGenerateXML_PublishedPortsFallbackToBaseUrl(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.xml")

	assert.Contains(t, content, "FALLBACK when the Published Ports block is ABSENT: set APP_PORT = the port from the base URL")
	assert.Contains(t, content, "record a context note that ports were defaulted from the base URL")
}

// The companion cheat-sheet states the Docker goal fires on RUN_TARGET=docker OR a deployment ADR.
func TestGenerateMD_DockerTriggerNoteUpdated(t *testing.T) {
	content := readGenerateTemplate(t, "task-plan-generate.md")

	assert.Contains(t, content, "RUN_TARGET=docker OR a deployment ADR")
	assert.Contains(t, content, "Published Ports")
}

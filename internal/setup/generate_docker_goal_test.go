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

	// The <conventions> block loads the rule catalogue at runtime
	// (`tmux-cli rules resolve`); the bundle mirrors that by appending the
	// embedded rules files — the bundle is everything the planning agent loads.
	rulesDir := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "rules")
	err := filepath.WalkDir(rulesDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		result += "\n" + string(data)
		return nil
	})
	require.NoError(t, err, "embedded rules dir must be walkable")
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

// dockerShard reads the step-3.26 shard directly so skeleton/relocation
// assertions are scoped to the docker goal and not tripped by sibling steps.
func dockerShard(t *testing.T) string {
	t.Helper()
	return readGenerateTemplate(t, "task-plan-generate/step-3.26-docker.xml")
}

// assertRoadmapSkeleton pins the Tier-1 roadmap-skeleton contract (two-tier
// director redesign, docs/architecture/director-two-tier-design.md §5): a
// goal-emitting shard now passes status=roadmap + phase + a coarse
// deliverable_area + depends_on, and authors NO concrete validate/acceptance
// params (those are authored at dispatch by /tmux:elaborate, §6).
func assertRoadmapSkeleton(t *testing.T, shard, phase string) {
	t.Helper()
	assert.Contains(t, shard, `<param name="status">roadmap`,
		"goal-create must emit status=roadmap")
	assert.Contains(t, shard, `<param name="phase">`+phase,
		"skeleton must carry phase=%s", phase)
	assert.Contains(t, shard, `<param name="deliverable_area">`,
		"skeleton must carry a coarse deliverable_area")
	assert.Contains(t, shard, `<param name="depends_on">`,
		"skeleton must wire depends_on")
	assert.NotContains(t, shard, `<param name="validate">`,
		"roadmap skeleton must not author a validate param (Tier-2 / elaborate)")
	assert.NotContains(t, shard, `<param name="acceptance">`,
		"roadmap skeleton must not author an acceptance param (Tier-2 / elaborate)")
}

// Two-tier: the docker goal is now a Tier-1 roadmap skeleton; DK-02/DK-04
// published-port mappings are authored at dispatch by /tmux:elaborate, not here.
func TestGenerateXML_ComposePortsFromPublishedPorts(t *testing.T) {
	shard := dockerShard(t)

	assertRoadmapSkeleton(t, shard, "docker")
	// the concrete published-port acceptance wording must NOT be authored at Tier-1.
	assert.NotContains(t, shard, "host:container port mapping from the Published Ports block (PUBLISHED_PORTS)")
	assert.NotContains(t, shard, "published host port APP_PORT (from PUBLISHED_PORTS)")
}

// Two-tier: the $APP_PORT health-check curl is a Tier-2 validate, authored at
// dispatch — the roadmap skeleton carries no validate command at all.
func TestGenerateXML_ValidateUsesPublishedAppPort(t *testing.T) {
	shard := dockerShard(t)

	assertRoadmapSkeleton(t, shard, "docker")
	assert.NotContains(t, shard, "curl -sf http://localhost:$APP_PORT/health",
		"the health-check curl validate is authored at dispatch by /tmux:elaborate")
}

// The DOCKER_ADR_SUMMARY-optional wording survives at Tier-1 (it gates emission
// in the skip gate); the BASE_IMAGE derivation moved to /tmux:elaborate.
func TestGenerateXML_DockerAdrSummaryOptional(t *testing.T) {
	shard := dockerShard(t)

	// skeleton-layer assertions that STILL hold (the conditional skip gate).
	assert.Contains(t, shard, "DOCKER_ADR_SUMMARY is OPTIONAL")
	assert.Contains(t, shard, "When RUN_TARGET=docker without an ADR, leave DOCKER_ADR_SUMMARY unset")
	// BASE_IMAGE derivation is Tier-2 now (authored against the real tree).
	assert.NotContains(t, shard, "BASE_IMAGE = from DOCKER_ADR_SUMMARY when ADR_PRESENT")
}

// Two-tier: production-deployment not_in_scope exclusions are authored at
// dispatch now — the roadmap skeleton emits no not_in_scope param.
func TestGenerateXML_ProdDeployExtrasPreserved(t *testing.T) {
	shard := dockerShard(t)

	assertRoadmapSkeleton(t, shard, "docker")
	assert.NotContains(t, shard, `<param name="not_in_scope">`,
		"not_in_scope is authored at dispatch by /tmux:elaborate")
	assert.NotContains(t, shard,
		"production deployment orchestration (Kubernetes, Swarm), cloud provider setup, SSL/TLS configuration.")
}

// Two-tier: the APP_PORT fallback is part of validate authoring, now Tier-2.
func TestGenerateXML_PublishedPortsFallbackToBaseUrl(t *testing.T) {
	shard := dockerShard(t)

	assertRoadmapSkeleton(t, shard, "docker")
	assert.NotContains(t, shard, "FALLBACK when the Published Ports block is ABSENT: set APP_PORT = the port from the base URL")
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

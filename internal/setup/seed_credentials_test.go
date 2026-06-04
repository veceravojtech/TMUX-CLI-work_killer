package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- D2: bootstrap/seed default-admin goal ---
//
// These tests pin the discovery question, its persistence, the discovery
// template placeholders, and the generation step (Step 3.19a) that emits a
// single dev-env seed-admin goal. Credentials are ALWAYS referenced by
// env-var NAME only — never inlined — and the seed ALWAYS runs --env=dev,
// never --env=test, and is never folded into the test-only fixtures goal.

func readEmbeddedCommand(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "commands", "tmux", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "%s must exist", name)
	return string(data)
}

func readEmbeddedTemplate(t *testing.T, rel ...string) string {
	t.Helper()
	parts := append([]string{"..", "..", "cmd", "tmux-cli", "embedded", "templates"}, rel...)
	path := filepath.Join(parts...)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "%s must exist", filepath.Join(rel...))
	return string(data)
}

// step319a extracts the Step 3.19a block from task-plan-generate.xml so that
// "never" assertions are scoped to the seed step and not tripped by sibling
// steps (e.g. the fixtures goal legitimately uses --env=test).
func step319a(t *testing.T, content string) string {
	t.Helper()
	start := strings.Index(content, `<step n="3.19a"`)
	require.GreaterOrEqual(t, start, 0, "Step 3.19a must exist in task-plan-generate.xml")
	rest := content[start:]
	end := strings.Index(rest, `<step n="3.20"`)
	require.Greater(t, end, 0, "Step 3.20 must follow Step 3.19a")
	return rest[:end]
}

func TestDiscover_SeedAdminQuestionPresent(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-discover.xml")

	assert.Contains(t, content, `topic="seed-default-admin"`,
		"discovery must have a seed-default-admin question topic")
	assert.Contains(t, content, "default admin",
		"question must mention seeding a default admin")
	assert.Contains(t, content, "environment-variable NAME",
		"question must ask for env-var NAMES, not values")
	assert.Contains(t, content, "never the secret values",
		"question must instruct never to inline secret values")
}

func TestDiscover_SeedAdminGatedOnAuth(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-discover.xml")

	// The seed question must be guarded to ask only when auth flows exist.
	assert.Contains(t, content, "AUTH_FLOWS",
		"seed question must be gated on AUTH_FLOWS")
	assert.Contains(t, content, "Step 6.2",
		"gating must reference Step 6.2 where auth flows are captured")
}

func TestDiscover_SeedAdminPersistedToTestEnv(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-discover.xml")

	assert.Contains(t, content, "## Bootstrap / Seed Default Admin",
		"Step 7.3 must persist a Bootstrap / Seed Default Admin block")
	assert.Contains(t, content, "admin_identifier_env",
		"persisted block must record the admin identifier env-var NAME")
	assert.Contains(t, content, "admin_password_env",
		"persisted block must record the admin password env-var NAME")
	assert.Contains(t, content, "test-environment.md",
		"the block must be persisted into test-environment.md")
}

func TestDiscoveryTemplate_SeedAdminPlaceholders(t *testing.T) {
	content := readEmbeddedTemplate(t, "_base", "discovery.md")

	assert.Contains(t, content, "seed a default admin",
		"section 14 must contain the seed default admin question")
	assert.Contains(t, content, "{{seed_default_admin}}")
	assert.Contains(t, content, "{{admin_identifier_env}}")
	assert.Contains(t, content, "{{admin_password_env}}")
}

func TestGenerate_SeedStepEmitsDevEnvGoal(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")
	step := step319a(t, content)

	assert.Contains(t, step, "bin/console app:seed-admin --env=dev",
		"Step 3.19a must run the seed command against the dev env")
	assert.Contains(t, step, "phase=seed",
		"the seed goal must carry phase=seed")
	assert.Contains(t, step, "max_retries", "the seed goal must set max_retries")
	assert.Contains(t, step, "2", "the seed goal must use max_retries=2")
}

func TestGenerate_SeedStepNotEnvTest(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")
	step := step319a(t, content)

	// The seed command must NEVER be invoked against the test env.
	assert.NotContains(t, step, "app:seed-admin --env=test",
		"seed command must never run with --env=test")
	assert.Contains(t, step, "NEVER --env=test",
		"the step must explicitly prohibit --env=test")
	// And it must explicitly exclude folding into the test-only fixtures goal.
	assert.Contains(t, step, "fixtures goal",
		"the step must explicitly exclude the fixtures goal")
}

func TestGenerate_SeedDependsOnAuthBootstrap(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")
	step := step319a(t, content)

	assert.Contains(t, step, "phase=auth-bootstrap",
		"depends_on must resolve the auth-bootstrap goal")
	assert.Contains(t, step, "FALLBACK",
		"a documented fallback must exist when auth-bootstrap is absent")
	assert.Contains(t, step, "first phase=auth goal",
		"fallback must resolve the first auth (Register) goal")
}

func TestGenerate_SeedStepConditionalSkip(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")
	step := step319a(t, content)

	assert.Contains(t, step, "SKIP",
		"the step must skip when seeding is not requested / no auth flows")
	assert.Contains(t, step, "seed_default_admin != yes",
		"skip when seeding declined")
	assert.Contains(t, step, "N_auth_flows == 0",
		"skip (with warning) when no auth flows exist")
}

func TestGenerate_SeedNoInlinedSecret(t *testing.T) {
	content := readEmbeddedCommand(t, "task-plan-generate.xml")
	step := step319a(t, content)

	assert.Contains(t, step, "env-var name only",
		"credentials must be referenced by env-var name only")
	assert.Contains(t, step, "admin_identifier_env",
		"seed goal references the identifier env-var NAME")
	assert.Contains(t, step, "admin_password_env",
		"seed goal references the password env-var NAME")
	// No literal secret value assignment anywhere in the step.
	assert.NotContains(t, step, "APP_ADMIN_PASSWORD=",
		"no literal secret value may be inlined")
	assert.Contains(t, step, "GM-05",
		"reuse the GM-05 never-hardcode-credentials convention")
}

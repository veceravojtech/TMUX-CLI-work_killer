package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/testutil"
)

func TestGoalCreate_InfraGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Doctrine XML mapping exists for every aggregate root and entity",
		"XML mapping lives in src/{BC}/Infrastructure/Persistence/Doctrine/Mapping/",
		"No Doctrine annotations/attributes anywhere in Domain or Application layers",
		"Repository implementation implements Domain repository interface",
		"Repository implementation lives in src/{BC}/Infrastructure/Persistence/",
		"Write repository dispatches domain events after flush (flush-then-dispatch pattern)",
		"Read model repository implements Application read model interface",
		"Custom DBAL types created for value objects that need DB storage",
		"doctrine:schema:validate passes",
		"Migration generated and applies cleanly",
		"Integration tests use real test database, not mocks",
		"Integration tests reset DB state via fixtures before each test",
		"Integration tests cover: persist + retrieve aggregate, query methods, edge cases",
		"Integration tests run green",
		"Service configuration wires implementations to interfaces",
		"ACL adapters exist for each cross-BC dependency from context-map.md",
	}
	validate := []string{
		"bin/console doctrine:schema:validate",
		"vendor/bin/phpunit --filter=Booking\\Infrastructure",
	}
	ctx := "Booking BC infrastructure layer. flush-then-dispatch pattern required for domain event publishing. Custom DBAL types needed for Money and BookingStatus value objects. ACL adapters required for cross-BC dependencies with Pricing BC."

	output, err := server.GoalCreate(
		"Implement Booking infrastructure: Doctrine mappings, repos, migration, integration tests",
		acceptance, validate, ctx, "", "infrastructure", 0, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "flush-then-dispatch")
	assert.Contains(t, mdContent, "DBAL types")
	assert.Contains(t, mdContent, "ACL adapters")

	assert.Contains(t, mdContent, "- Write repository dispatches domain events after flush (flush-then-dispatch pattern)")
	assert.Contains(t, mdContent, "- Custom DBAL types created for value objects that need DB storage")
	assert.Contains(t, mdContent, "- ACL adapters exist for each cross-BC dependency from context-map.md")

	assert.Contains(t, mdContent, "- bin/console doctrine:schema:validate")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --filter=Booking\\Infrastructure")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_ActionGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Controller class has maximum ~20 lines in action method",
		"Controller action: deserialize → dispatch → serialize (nothing else)",
		"Request DTO has Symfony validation constraints matching business rules",
		"Response DTO serializes data, does not expose internal domain structure",
		"Route is configured with correct method + path",
		"Controller imports only from Application layer, never Domain directly",
		"Playwright E2E test exists at tests/E2E/Booking/CreateBookingTest.ts",
		"Playwright test resets fixtures before running",
		"Playwright test uses credentials from docs/architecture/test-environment.md",
		"Playwright test verifies: correct status code",
		"Playwright test verifies: response body structure and values",
		"Playwright test verifies: error cases (422 for invalid input, 401 for no auth)",
		"Playwright test verifies: state change in DB (if applicable)",
		"Playwright test passes when run individually",
		"PHPStan level 9 passes on controller file",
		"ECS passes on all new files",
		"Deptrac passes — controller imports Application layer only",
	}
	validate := []string{
		"bin/console debug:router | grep /api/bookings",
		"npx playwright test tests/E2E/Booking/CreateBookingTest.ts",
	}
	ctx := `Deliverables per GM-12:
- Request DTO: src/Booking/Infrastructure/Http/Dto/CreateBookingRequest.php
- Response DTO: src/Booking/Infrastructure/Http/Dto/CreateBookingResponse.php
- Controller: src/Booking/Infrastructure/Http/Action/CreateBookingAction.php
- Route: POST /api/bookings
- E2E test: tests/E2E/Booking/CreateBookingTest.ts`

	output, err := server.GoalCreate(
		"POST /api/bookings — Booking controller action",
		acceptance, validate, ctx, "", "action", 0, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "Request DTO")
	assert.Contains(t, mdContent, "Response DTO")
	assert.Contains(t, mdContent, "Controller")
	assert.Contains(t, mdContent, "Route")
	assert.Contains(t, mdContent, "E2E test")

	assert.Contains(t, mdContent, "CreateBookingAction.php")
	assert.Contains(t, mdContent, "CreateBookingTest.ts")
	assert.Contains(t, mdContent, "POST /api/bookings")

	assert.Contains(t, mdContent, "Playwright E2E test exists")
	assert.Contains(t, mdContent, "Playwright test passes when run individually")

	assert.Contains(t, mdContent, "- npx playwright test tests/E2E/Booking/CreateBookingTest.ts")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_ErrorHandlingGoalContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Symfony exception listener registered and catches all unhandled exceptions",
		"All error responses use RFC 7807 Problem Details format",
		"DomainException maps to 422",
		"EntityNotFoundException maps to 404",
		"AccessDeniedException maps to 403",
		"ValidationException maps to 422 with field-level errors",
		"Unexpected exceptions map to 500 with no internal details exposed",
		"PHPStan clean, ECS clean on error handling files",
	}
	validate := []string{
		"bin/console debug:event-dispatcher | grep ExceptionListener",
		"vendor/bin/phpunit --filter=ErrorHandling",
	}
	ctx := "Global error handling infrastructure. Symfony exception listener catches all unhandled exceptions. RFC 7807 Problem Details format for all error responses. DomainException maps to 422, EntityNotFoundException maps to 404, AccessDeniedException maps to 403, unexpected exceptions map to 500."

	output, err := server.GoalCreate(
		"Implement global error handling: exception listener, RFC 7807, status code mapping",
		acceptance, validate, ctx, "", "cross-cutting", 0, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Context")

	assert.Contains(t, mdContent, "exception listener")
	assert.Contains(t, mdContent, "RFC 7807")
	assert.Contains(t, mdContent, "DomainException")
	assert.Contains(t, mdContent, "EntityNotFoundException")
	assert.Contains(t, mdContent, "AccessDeniedException")

	assert.Contains(t, mdContent, "422")
	assert.Contains(t, mdContent, "404")
	assert.Contains(t, mdContent, "403")
	assert.Contains(t, mdContent, "500")

	assert.Contains(t, mdContent, "- bin/console debug:event-dispatcher | grep ExceptionListener")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --filter=ErrorHandling")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}
}

func TestGoalCreate_DeptracFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-01: Deptrac zero violations across entire codebase — vendor/bin/deptrac analyse exit code 0",
		"FG-02: Domain + Share layer depends on nothing (pure, no framework imports)",
		"FG-03: Application layer depends only on Domain + Share layer",
		"FG-04: Infrastructure layer depends on Domain + Share + Application only",
		"FG-05: Controllers (inbound adapters) import only Application layer",
		"FG-06: No cross-BC imports in Domain or Application layers",
		"FG-07: ACL adapters are the only cross-BC touchpoints in Infrastructure",
	}
	validate := []string{"vendor/bin/deptrac analyse"}
	ctx := `Final gate: validates that the entire codebase passes Deptrac layer dependency analysis after all BC goals, fixtures, actions, auth, and cross-cutting goals are complete.

## Investigation Config

### Investigator 1: Layer Structure Verifier
- Type: architecture-check
- Commands: vendor/bin/deptrac analyse, vendor/bin/deptrac analyse --formatter=json
- Pass: Exit 0, zero violations — all 4 DDD layers (Domain, Application, Infrastructure, Share) respect dependency rules
- Fail: Any layer violation detected — dependency flows upward or crosses BC boundary

### Investigator 2: Cross-BC Boundary Checker
- Type: architecture-check
- Commands: grep -rn 'use App\\' src/*/Domain/ | grep -v 'use App\\Share\\' | grep -v "$(basename $(dirname $f))" (per-BC), vendor/bin/deptrac analyse --filter=cross-bc
- Pass: Zero cross-BC imports in Domain/Application layers; only ACL adapters in Infrastructure cross BC boundaries
- Fail: Direct cross-BC import found outside ACL adapter`
	notInScope := "PHPStan, ECS, unit/integration tests, Playwright E2E, coverage, console boot, schema validation, migrations"

	output, err := server.GoalCreate(
		"Final gate: Deptrac full codebase layer dependency verification",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "## Not In Scope")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}

	assert.Contains(t, mdContent, "zero violations across entire codebase")
	assert.Contains(t, mdContent, "Domain + Share layer depends on nothing")
	assert.Contains(t, mdContent, "Application layer depends only on Domain + Share")
	assert.Contains(t, mdContent, "Infrastructure layer depends on Domain + Share + Application")
	assert.Contains(t, mdContent, "Controllers (inbound adapters) import only Application layer")
	assert.Contains(t, mdContent, "No cross-BC imports in Domain or Application")
	assert.Contains(t, mdContent, "ACL adapters are the only cross-BC touchpoints")

	assert.Contains(t, mdContent, "- vendor/bin/deptrac analyse")
	assert.Contains(t, mdContent, "Layer Structure Verifier")
	assert.Contains(t, mdContent, "Cross-BC Boundary Checker")
	assert.Contains(t, mdContent, "PHPStan, ECS")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_E2EFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-08: All Playwright E2E tests pass when run together (not just individually) — npx playwright test all green",
		"FG-09: No test isolation issues (order-dependent failures) — run Playwright tests in random order",
		"G-08: Last 3 goals in goals.yaml are final gates (Deptrac, E2E, Quality) — verify ordering",
	}
	validate := []string{"npx playwright test", "npx playwright test --shard=random"}
	ctx := `Final gate: validates that all Playwright E2E tests pass when run together as a full suite, detecting isolation issues that per-action test runs miss.

## Investigation Config

### Investigator 1: Full Suite Runner
- Type: test-execution
- Commands: npx playwright test
- Pass: Exit 0, all E2E tests green when run as a single suite — no failures from shared state or missing fixtures
- Fail: Any test failure when run together that passed in isolation

### Investigator 2: Isolation Checker
- Type: test-execution
- Commands: npx playwright test --shard=random
- Pass: Exit 0, all tests pass in randomized order — no order-dependent failures
- Fail: Test failure in randomized order indicates shared mutable state between tests`
	notInScope := "Individual endpoint tests, Deptrac, PHPStan, ECS, unit tests, schema validation"

	output, err := server.GoalCreate(
		"Final gate: Playwright E2E regression — all tests run together",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "FG-08: All Playwright E2E tests pass when run together (not just individually)")
	assert.Contains(t, mdContent, "FG-09: No test isolation issues (order-dependent failures)")
	assert.Contains(t, mdContent, "G-08: Last 3 goals in goals.yaml are final gates")
	assert.Contains(t, mdContent, "- npx playwright test")
	assert.Contains(t, mdContent, "- npx playwright test --shard=random")
	assert.Contains(t, mdContent, "Full Suite Runner")
	assert.Contains(t, mdContent, "Isolation Checker")
	assert.Contains(t, mdContent, "Individual endpoint tests, Deptrac, PHPStan")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_QualityFinalGateContent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"FG-10: PHPStan level 9 zero errors across entire codebase — vendor/bin/phpstan analyse exit code 0",
		"FG-11: ECS zero violations across entire codebase — vendor/bin/ecs check exit code 0",
		"FG-12: Unit test suite all green — vendor/bin/phpunit --testsuite=unit passes",
		"FG-13: Integration test suite all green — vendor/bin/phpunit --testsuite=integration passes",
		"FG-14: Test coverage meets threshold (configurable, default 80%) — coverage report generated, threshold met",
		"FG-15: No TODO/FIXME/HACK comments in codebase — grep check, zero results",
		"FG-16: bin/console boots without errors — exit code 0",
		"FG-17: doctrine:schema:validate passes — exit code 0",
		"FG-18: All migrations applied cleanly — doctrine:migrations:status shows no pending",
	}
	validate := []string{
		"vendor/bin/phpstan analyse src/ --level=9",
		"vendor/bin/ecs check src/",
		"vendor/bin/phpunit --testsuite=unit",
		"vendor/bin/phpunit --testsuite=integration",
		"vendor/bin/phpunit --coverage-text",
		"grep -rn 'TODO\\|FIXME\\|HACK' src/ | wc -l",
		"bin/console",
		"bin/console doctrine:schema:validate",
		"bin/console doctrine:migrations:status",
	}
	ctx := `Final gate: validates full codebase quality — PHPStan, ECS, all test suites, coverage threshold, and Doctrine schema/migration health after all goals complete.

## Investigation Config

### Investigator 1: Static Analysis Verifier
- Type: quality-gate
- Commands: vendor/bin/phpstan analyse src/ --level=9, vendor/bin/ecs check src/
- Pass: Both exit 0 — zero PHPStan errors at level 9, zero ECS violations
- Fail: Any static analysis error or coding standard violation

### Investigator 2: Test Suite Runner
- Type: test-execution
- Commands: vendor/bin/phpunit --testsuite=unit, vendor/bin/phpunit --testsuite=integration, vendor/bin/phpunit --coverage-text
- Pass: All suites green, coverage meets threshold (default 80%)
- Fail: Test failure or coverage below threshold

### Investigator 3: Runtime Health Checker
- Type: environment-check
- Commands: bin/console, bin/console doctrine:schema:validate, bin/console doctrine:migrations:status, grep -rn 'TODO\|FIXME\|HACK' src/ | wc -l
- Pass: Console boots, schema valid, no pending migrations, zero stale comment markers
- Fail: Boot failure, schema mismatch, pending migrations, or stale comments found`
	notInScope := "Deptrac analysis, Playwright E2E, new feature code, refactoring"

	output, err := server.GoalCreate(
		"Final gate: PHPStan, ECS, test suites, coverage, schema, migrations",
		acceptance, validate, ctx, notInScope, "final", 5, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "## Not In Scope")

	for _, ac := range acceptance {
		assert.Contains(t, mdContent, "- "+ac, "acceptance criterion missing: %s", ac)
	}

	assert.Contains(t, mdContent, "- vendor/bin/phpstan analyse src/ --level=9")
	assert.Contains(t, mdContent, "- vendor/bin/ecs check src/")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --testsuite=unit")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --testsuite=integration")
	assert.Contains(t, mdContent, "- vendor/bin/phpunit --coverage-text")
	assert.Contains(t, mdContent, "grep -rn")
	assert.Contains(t, mdContent, "- bin/console")
	assert.Contains(t, mdContent, "- bin/console doctrine:schema:validate")
	assert.Contains(t, mdContent, "- bin/console doctrine:migrations:status")

	assert.Contains(t, mdContent, "Static Analysis Verifier")
	assert.Contains(t, mdContent, "Test Suite Runner")
	assert.Contains(t, mdContent, "Runtime Health Checker")
	assert.Contains(t, mdContent, "Deptrac analysis, Playwright E2E")

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "final", gf.Goals[0].Phase)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_PlaywrightActionWithFlakeRetry(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	acceptance := []string{
		"Playwright E2E test exists at tests/E2E/Booking/CreateBookingTest.ts",
		"Playwright test passes when run individually",
	}
	validate := []string{
		"npx playwright test tests/E2E/Booking/CreateBookingTest.ts",
	}
	ctx := `Deliverables per GM-12:
- E2E test: tests/E2E/Booking/CreateBookingTest.ts

## Investigation Config

### Investigator 4: Playwright E2E Verifier
- Type: test-execution
- Commands: npx playwright test tests/E2E/Booking/CreateBookingTest.ts
- Retry: 3 total attempts (1 initial + 2 retries) for flake detection
- Pass: Test passes on any of 3 attempts — if first run fails but second/third passes, the test is flaky but acceptable
- Fail: Test fails all 3 attempts — genuine failure, not a flake`

	output, err := server.GoalCreate(
		"POST /api/bookings — Booking controller action",
		acceptance, validate, ctx, "", "action", 0, nil, nil, nil, nil,
		0,
	)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "3 total attempts")
	assert.Contains(t, mdContent, "1 initial + 2 retries")
	assert.Contains(t, mdContent, "flake detection")
	assert.Contains(t, mdContent, "Test passes on any of 3 attempts")
	assert.Contains(t, mdContent, "Test fails all 3 attempts")
	assert.Contains(t, mdContent, "Playwright E2E test exists")
	assert.Contains(t, mdContent, "Playwright test passes when run individually")
}

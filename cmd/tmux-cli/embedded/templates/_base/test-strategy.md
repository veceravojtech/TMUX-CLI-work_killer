# Test Strategy: {{project_name}}

## Test Layers

### Unit Tests — Domain Layer
- Scope: aggregates, value objects, domain services, domain events
- Directory: `{{test_dir}}/Unit/{{bc_name}}/Domain/`
- Dependencies: none — pure domain logic, no I/O
- Mocking: domain interfaces only (repository ports, event dispatcher)
- What to test:
  - Aggregate invariants hold on construction and mutation
  - Value object validation rejects invalid input
  - Domain service orchestration across aggregates
  - Domain events are raised for state transitions
  - Named constructors and factory methods produce correct state
- Isolation: no external dependencies; runs in-process with zero I/O

### Integration Tests — Infrastructure Layer
- Scope: repository implementations, DB mappings, external service adapters
- Directory: `{{test_dir}}/Integration/{{bc_name}}/Infrastructure/`
- Dependencies: real database, real filesystem, real external service stubs
- Mocking: external services behind ACL boundaries only
- What to test:
  - Repository persist and retrieve returns equivalent aggregate
  - DB schema mappings handle all value object types
  - Custom DB types serialize and deserialize correctly
  - Migration applies without error on clean and existing schema
  - Read model queries return expected projections
- Isolation: each test runs in a dedicated transaction rolled back after completion

### E2E Tests — API / Action Layer
- Scope: full HTTP request → response cycle through controller, application service, domain, infrastructure
- Directory: `{{test_dir}}/E2E/{{bc_name}}/`
- Dependencies: running application, test database, Playwright or {{e2e_runner}} for browser-driven tests
- Mocking: none — full stack
- What to test:
  - Request DTO validation rejects malformed input
  - Successful action returns correct response DTO and status code
  - Error cases return appropriate error response format
  - Auth-protected endpoints reject unauthenticated requests
  - Side effects persist (DB state, events dispatched)
- Isolation: full database reset between test suites via {{fixture_command}}

## Test Environment Reference

| Setting | Value |
|---------|-------|
| Base URL | `{{test_base_url}}` |
| Test database | `{{test_db_name}}` |
| Test user credentials | `{{test_user_credentials}}` |
| Fixture load command | `{{fixture_command}}` |
| E2E runner available | {{e2e_runner_available}} |

## Fixture Strategy

- **Seed data**: loaded before each test suite via `{{fixture_command}}`
- **Isolation**: each test runs in a transaction rolled back after completion (integration), or against a reset DB (E2E)
- **Fixture location**: `{{fixture_dir}}/`
- **Fixture per BC**: `{{fixture_dir}}/{{bc_name}}/` — each bounded context owns its fixtures
- **Cross-BC references**: shared fixture file defines foreign IDs used across BC boundaries
- **E2E fixtures**: loaded via API calls or CLI seed command, not direct DB inserts — matches full-stack testing philosophy

## Naming Conventions

- Test class: `{{entity_name}}Test` (unit/integration) or `{{action_name}}E2ETest` (E2E)
- Test method: `test_{{scenario_description}}` — describes the behavior, not the method under test
- Fixture file: `{{bc_name}}_fixtures` — one per bounded context
- Test directory mirrors source directory structure

## Layer Boundaries

| Layer | Tests | Mocks allowed | DB required | Network required |
|-------|-------|---------------|-------------|-----------------|
| Domain | Unit | Repository ports, event dispatcher | No | No |
| Infrastructure | Integration | External service ACL stubs | Yes | No |
| Action / API | E2E | None | Yes | Yes (HTTP) |

## CI Integration

- **Run order**: unit tests first (fastest feedback), then integration, then E2E
- **Parallelization**: unit tests may run in parallel across modules; integration tests may parallelize with isolated databases; E2E tests run sequentially to avoid shared-state conflicts
- **Environment**: unit tests need no external services; integration and E2E require a provisioned test environment (see `test-environment.md` for setup)
- **Failure strategy**: fail the pipeline on the first tier that breaks — do not run E2E if integration tests fail
- **Test database**: integration and E2E tiers each require a dedicated `{{test_db_name}}` instance; never share a database between parallel test jobs
- **Artifact collection**: capture test reports, coverage output, and E2E screenshots/traces on failure for post-mortem analysis. Playwright artifacts land in test-results/ (configured via playwright.config.ts: trace 'retain-on-failure', screenshot 'only-on-failure', reporter [['list'],['html',{open:'never'}]], outputDir 'test-results', retries: 0). The test-results/ directory must be gitignored.

## E2E Data Isolation

Fixtures load once per test suite (via ensure-stack before the first spec) and are treated as **read-only reference data**. E2E specs must not mutate (update, delete) fixture rows — mutations make later specs order-dependent.

- **Own data**: each E2E spec creates its own mutable entities through the app's UI or API, using unique keys (timestamp or UUID suffix) for identification
- **Filtered assertions**: count/list assertions filter to the spec's own data (e.g. search by unique key) — never assert raw totals against the full table
- **No mid-suite reset**: re-running the fixture load command mid-suite purges everything (including spec-created data) — the reset unit is the whole suite
- **No transaction rollback for E2E**: test-framework transaction rollback (e.g. Doctrine test bundles) does not work across HTTP process boundaries — the test runner and app server hold separate DB connections

## Runtime Stack Entrypoint

Every project with a database (HAS_DATABASE) provides a single `bin/ensure-test-stack.sh` script that guarantees the runtime stack is up, migrated, and fixture-loaded before any E2E or host-HTTP probe. The script runs three phases in order: stack up, test-env migrations, and test fixture load. It is invoked as a separate validate line immediately before each E2E probe — never &&-joined with the probe command, because the daemon runs each validate line independently and per-line error reporting requires clean separation. The language-specific template (e.g. php-symfony/fixtures.md) provides the concrete script body; the generator's ENSURE-STACK-CONV rule wires it into every goal that contains an E2E or host-HTTP probe.

### E2E Environment Contract

Every project with a database (HAS_DATABASE) enforces an environment-pinning invariant: the HTTP vhost that E2E probes hit MUST serve `APP_ENV=test` against the seeded test database. The scaffold pins `APP_ENV=test` in the compose spec (docker mode) or `.env.test` (local mode). The `/health` endpoint exposes `env` and `database` fields so the contract can be mechanically verified: `curl -s {{BASE_URL}}/health | jq -e '.env == "test" and (.database | test("test"))'`. Gate-0 re-asserts the pinning after scaffold completes. This cross-language pattern prevents the silent failure mode where `ensure-test-stack.sh` seeds the test database but the served env reads the dev database.

### Side-Effect Isolation

Every project with a database (HAS_DATABASE) enforces side-effect isolation in the E2E-serving test environment so assertions are deterministic and no external system is contacted:

- **Mail transport**: when a mailer component is present, the test env pins a null/discard transport (e.g. `MAILER_DSN=null://null` for Symfony). The scaffold asserts this value exists — it does not re-deliver it, since modern framework recipes default to null transport in test. E2E specs for mail-sending flows assert application-visible outcomes (HTTP status, flash message, API response), never actual email delivery.
- **Message bus**: when an async message bus is present, the test env forces synchronous transport (e.g. `sync://` for Symfony Messenger via a per-environment config file). This ensures message handlers complete before E2E assertions run. The scaffold creates the sync-transport config file.
- **External HTTP**: E2E specs assert application-visible outcomes (HTTP status codes, DB state changes, response bodies), never actual external delivery or third-party service responses. No env-var mandate — isolation is a spec-generation rule, not a runtime config.

### Authenticated E2E State Reuse

When the project has auth flows and Playwright is available, a Playwright `setup` project logs in once per role (admin and regular user) and writes storageState JSON to `playwright/.auth/admin.json` and `playwright/.auth/user.json`. The `authenticated` Playwright project declares the setup as a dependency and reuses the stored state — authenticated E2E tests skip per-test login entirely.

- **Setup project**: runs `playwright/auth.setup.ts` which reads credentials by env-var name from `test-environment.md` (never hardcoded), logs in against the `APP_ENV=test` stack, and writes dual storageState files
- **State freshness**: the setup project re-runs on every `npx playwright test` invocation; combined with ensure-stack DB reseed, sessions are always fresh
- **Auth-flow tests preserved**: auth-flow E2E specs (step 3.19) test the real login/register/logout UI and must NOT use storageState — storageState is a precondition shortcut for other suites only
- **Gitignored**: `playwright/.auth/` is added to `.gitignore` — storageState files are never committed

## Language-specific patterns
(See language template)

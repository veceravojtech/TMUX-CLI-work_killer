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
- **Artifact collection**: capture test reports, coverage output, and E2E screenshots/traces on failure for post-mortem analysis

## Language-specific patterns
(See language template)

# Quality Gates: {{project_name}}

## Discovery Phase Gates

### DG-01: Bounded Context Inventory Complete
- **Description:** All bounded contexts identified during discovery are documented
- **Pass condition:** `bounded-contexts.md` exists and lists at least one BC with purpose, core concepts, and aggregates
- **Fail action:** Re-run discovery — missing BC definitions block all downstream work

### DG-02: API Endpoints Mapped
- **Description:** Every BC has at least one action endpoint defined
- **Pass condition:** `api-endpoints.md` exists and every BC in `bounded-contexts.md` has at least one endpoint entry
- **Fail action:** Return to discovery and define missing endpoints

### DG-03: Domain Models Defined
- **Description:** Every BC has a domain model document with aggregates, entities, and value objects
- **Pass condition:** A `domain-model.md` exists for each BC; each contains at least one aggregate with root entity, value objects, and invariants
- **Fail action:** Generate missing domain model documents before proceeding

### DG-04: Architecture Decisions Recorded
- **Description:** Key design choices are captured as ADRs
- **Pass condition:** At least one ADR exists; all ADRs have status, context, decision, and consequences sections
- **Fail action:** Document pending architecture decisions before proceeding

### DG-05: Cross-File Consistency — BC Names
- **Description:** Bounded context names are consistent across all discovery documents
- **Pass condition:** Every BC name in `api-endpoints.md` exists in `bounded-contexts.md`; every BC in `bounded-contexts.md` has a matching `domain-model.md`
- **Fail action:** Reconcile naming — rename to the canonical name in `bounded-contexts.md`

### DG-06: Cross-File Consistency — Aggregate Names
- **Description:** Aggregate names used in endpoints and service contracts match domain model definitions
- **Pass condition:** Every aggregate referenced in `api-endpoints.md` and `service-contracts.md` is defined in the corresponding `domain-model.md`
- **Fail action:** Align aggregate names to the domain model as source of truth

### DG-07: Service Contracts Defined
- **Description:** Repository interfaces (write) and read model interfaces (query) are specified for every aggregate
- **Pass condition:** `service-contracts.md` exists for each BC; each aggregate has write repository and read model interfaces
- **Fail action:** Define missing service contracts before implementation phase

## Domain Modeling Phase Gates

### MG-01: Aggregate Boundary Integrity
- **Description:** Each aggregate has a single root entity and no shared entities across aggregates
- **Pass condition:** No entity class appears as a member of more than one aggregate within the same BC
- **Fail action:** Extract shared entities into their own aggregate or convert to value objects

### MG-02: No Cross-BC Entity Sharing
- **Description:** Aggregates do not reference entities from other bounded contexts directly
- **Pass condition:** No `domain-model.md` references entity types belonging to another BC; cross-BC references use foreign ID value objects only
- **Fail action:** Replace direct references with ACL interfaces and foreign ID value objects

### MG-03: Invariant Coverage
- **Description:** Every aggregate documents its business invariants
- **Pass condition:** Each aggregate in every `domain-model.md` has at least one invariant listed
- **Fail action:** Elicit missing invariants from domain requirements

### MG-04: Domain Event Completeness
- **Description:** State transitions that other BCs or processes depend on have corresponding domain events
- **Pass condition:** Each aggregate with cross-BC consumers has at least one domain event; events are listed in both `domain-model.md` and the Share namespace in `bounded-contexts.md`
- **Fail action:** Define missing domain events and register them in the Share namespace

### MG-05: Value Object Identification
- **Description:** Primitive-obsession is avoided — domain concepts with validation rules are modeled as value objects
- **Pass condition:** Each aggregate has at least the identity modeled as a value object; shared data types are listed in the Share namespace
- **Fail action:** Extract primitives that carry domain meaning into value objects

## Implementation Phase Gates

### IG-01: Unit Tests Pass
- **Description:** All domain layer unit tests pass
- **Pass condition:** Test runner exits with zero failures for domain and application layer tests
- **Fail action:** Fix failing tests before merging — domain logic regressions block the gate

### IG-02: Integration Tests Pass
- **Description:** Infrastructure layer tests (repository implementations, external service adapters) pass against real dependencies
- **Pass condition:** Test runner exits with zero failures for infrastructure layer tests
- **Fail action:** Fix infrastructure test failures — do not mock around them

### IG-03: Contract Satisfaction
- **Description:** Every repository interface and read model interface defined in `service-contracts.md` has a concrete implementation
- **Pass condition:** Each interface has exactly one implementation class; no interface is left unimplemented
- **Fail action:** Implement missing contracts before deployment

### IG-04: Endpoint Coverage
- **Description:** Every endpoint in `api-endpoints.md` has a corresponding controller, route, request DTO, and response DTO
- **Pass condition:** Each endpoint entry maps to an implemented controller with route registration, request validation, and response serialization
- **Fail action:** Implement missing endpoint components

### IG-05: E2E Test Coverage
- **Description:** Each action endpoint has at least one end-to-end test covering the happy path
- **Pass condition:** E2E test exists for each endpoint; test hits the real HTTP layer and verifies response status and body structure
- **Fail action:** Write missing E2E tests before deployment

### IG-06: No Cross-Layer Violations
- **Description:** Domain layer has no imports from Infrastructure or Application; Application has no imports from Infrastructure
- **Pass condition:** Static analysis or manual review confirms layer dependency rules hold
- **Fail action:** Move the violating code to the correct layer

## Deployment Phase Gates

### PG-01: Database Migrations Applied
- **Description:** All schema changes defined in `migration-plan.md` have corresponding migration files that execute without error
- **Pass condition:** Migrations run successfully against a clean database and against the current production schema
- **Fail action:** Fix migration errors — never deploy with unapplied or failing migrations

### PG-02: Endpoints Respond
- **Description:** Every deployed endpoint returns the expected HTTP status for a basic request
- **Pass condition:** Health check or smoke test confirms each endpoint returns 2xx (or expected 4xx for auth-required) with correct response structure
- **Fail action:** Investigate and fix non-responding endpoints before marking deployment complete

### PG-03: Rollback Plan Verified
- **Description:** A rollback path exists for the deployment
- **Pass condition:** Down migrations exist and have been tested; or a rollback strategy is documented in the deployment ADR
- **Fail action:** Create and test rollback migrations before proceeding

### PG-04: Configuration Validated
- **Description:** Environment-specific configuration (database connections, service URLs, feature flags) is correct for the target environment
- **Pass condition:** Application starts successfully and connects to all required external services
- **Fail action:** Fix configuration before routing traffic to the new deployment

## Cross-File Consistency Checks

These checks run across all phases and verify that the documentation set remains internally consistent.

| ID | Check | Files involved | Pass condition |
|----|-------|----------------|----------------|
| CC-01 | BC names match | `bounded-contexts.md`, `api-endpoints.md`, all `domain-model.md` | Identical BC name spelling across all files |
| CC-02 | Aggregate names match | `domain-model.md`, `service-contracts.md`, `api-endpoints.md` | Every aggregate reference resolves to a definition in `domain-model.md` |
| CC-03 | Service contract completeness | `domain-model.md`, `service-contracts.md` | Every aggregate has write repo + read model interfaces defined |
| CC-04 | Endpoint-to-controller mapping | `api-endpoints.md`, implementation files | Every endpoint has a corresponding controller |
| CC-05 | Migration-to-model alignment | `migration-plan.md`, `domain-model.md` | Every entity/VO requiring persistence has a migration entry |
| CC-06 | Share namespace consistency | `bounded-contexts.md` Share section, `domain-model.md` events | Published events in Share match domain events marked as cross-BC |
| CC-07 | ADR-to-implementation alignment | `adr.md` files, implementation | Accepted ADRs are reflected in the implementation; superseded ADRs have no lingering code |

## Goal.md Quality Gates (GM-01 through GM-14)

These gates validate the quality of individual goal specification files.

{{#each goal_files}}
### {{goal_id}}: {{goal_title}}

| Gate | Criterion | Pass condition | Fail action |
|------|-----------|----------------|-------------|
| GM-01 | Description section present | Goal has a Description section | Add Description section with problem statement and approach |
| GM-02 | Dependencies section present | Goal lists prior goals with status | Add Dependencies section referencing prerequisite goals |
| GM-03 | Deliverables with exact paths | Deliverables section lists exact file paths following DDD structure | Add file paths using `src/{{bc_name}}/Domain/`, `src/{{bc_name}}/Application/`, `src/{{bc_name}}/Infrastructure/` |
| GM-04 | Testable acceptance criteria | Acceptance Criteria section has machine-verifiable criteria | Rewrite criteria to be testable (given/when/then or pass/fail condition) |
| GM-05 | Test environment reference | E2E goals reference `docs/architecture/test-environment.md` | Add reference — do not duplicate credentials inline |
| GM-06 | Context section with doc refs | Context section references architecture documents | Add references to relevant `docs/architecture/*` files |
| GM-07 | Not In Scope section present | Goal has explicit Not In Scope section | Add section — prevents scope creep during implementation |
| GM-08 | Investigation Config (2-4 investigators) | Investigation Config has 2-4 investigators; conditional ones marked | Add investigators with type, paths, commands, pass/fail, conditions |
| GM-09 | Investigator field completeness | Each investigator has: type, file paths, commands, pass/fail criteria | Fill in missing fields per investigator |
| GM-10 | DDD directory structure | Deliverable paths follow `src/{BC}/Domain/`, `Application/`, `Infrastructure/` | Fix paths to match DDD layer conventions |
| GM-11 | No cross-BC references | Deliverables stay within own BC namespace (except cross-cutting goals) | Move cross-BC references behind ACL interfaces |
| GM-12 | Action goal completeness | Action goals include: request DTO, response DTO, controller, route, E2E test | Add missing deliverable types |
| GM-13 | Domain goal completeness | Domain goals include: aggregate root, VOs, events, services, repo interface, unit tests | Add missing deliverable types |
| GM-14 | Infrastructure goal completeness | Infra goals include: ORM mapping, repo impl, migration, custom database types, integration tests | Add missing deliverable types |

{{/each}}

## Gate Execution Protocol

1. **Before phase transition:** Run all gates for the completing phase
2. **On failure:** Stop — fix the failing gate before proceeding
3. **Cross-file checks:** Run CC-01 through CC-07 after any document modification
4. **Goal quality:** Run GM-01 through GM-14 after generating or modifying any goal.md
5. **Record results:** Log gate pass/fail with timestamp in {{gate_log_path}}


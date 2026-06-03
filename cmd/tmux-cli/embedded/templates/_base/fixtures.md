# Test Fixtures: {{bc_name}}

## Overview

- Bounded Context: {{bc_name}}
- Fixture Load Command: `{{fixture_command}}`
- Depends On: all migrations for {{bc_name}} applied first
- Idempotency: {{idempotency_strategy}}

## Entity Fixture List

Every aggregate and entity that E2E tests reference MUST have a corresponding fixture.

| Order | Entity / Aggregate | Fixture Identifier | Depends On | Record Count | Notes |
|-------|-------------------|-------------------|------------|--------------|-------|
| {{order}} | {{entity_name}} | {{fixture_id}} | {{depends_on_fixture}} | {{record_count}} | {{notes}} |

Ordering rules:
- Fixtures load in declared order — respect foreign key dependencies
- Parent entities load before children
- Cross-BC shared references load before BC-specific entities

## Dependency Graph

```
{{dependency_graph}}
```

Format: `FixtureA → FixtureB` means A must load before B.

## Idempotency Strategy

- **Approach:** {{idempotency_strategy}}
- **Collision handling:** {{collision_handling}}
- **Identifier stability:** fixture records use deterministic IDs (UUIDs or named constants) so repeated loads produce identical state
- **Verification:** running `{{fixture_command}}` twice in sequence produces zero errors and identical data state

## Data Volume Requirements

Fixtures must create enough records for pagination and list endpoint testing.

| Entity | Minimum Records | Pagination Tested | Reason |
|--------|----------------|-------------------|--------|
| {{entity_name}} | {{min_records}} | {{yes_no}} | {{reason}} |

Guidelines:
- List endpoints require at least `{{pagination_page_size}} + 1` records to verify pagination
- Filter/search endpoints require records with varying attribute values
- Edge cases: include at least one soft-deleted, one archived, or one inactive record per entity (if applicable)

## Test Users & Credentials

Fixtures create test users matching the credentials defined in the test environment.

| User Role | Username | Credential Reference | Purpose |
|-----------|----------|---------------------|---------|
| {{user_role}} | {{username}} | {{credential_ref}} | {{purpose}} |

- Credential values are NOT hardcoded in fixtures — reference `{{test_credentials_source}}` as the single source of truth
- Each role needed by E2E tests (admin, regular user, read-only, unauthenticated) must have a fixture user

## Cross-BC References

Shared fixture data required by other bounded contexts.

| This BC Entity | Referenced By BC | Foreign Entity | Shared Fixture ID |
|---------------|-----------------|---------------|-------------------|
| {{entity_name}} | {{foreign_bc}} | {{foreign_entity}} | {{shared_id}} |

- Cross-BC fixtures live in a shared fixture file: `{{fixture_dir}}/shared/`
- Shared fixtures load before any BC-specific fixtures

## Fixture Organization

```
{{fixture_dir}}/
  shared/              — cross-BC reference data
  {{bc_name}}/         — BC-specific fixtures
```

- One fixture file per aggregate root
- Fixture files are self-contained — each declares its own dependencies
- Directory mirrors bounded context structure

## Pre-conditions

- [ ] All schema migrations for {{bc_name}} have been applied
- [ ] Test database `{{test_db_name}}` exists and is accessible
- [ ] Custom column types registered (if any)
- [ ] Cross-BC shared fixtures loaded (if any dependencies)

## Verification

After fixture load, confirm correctness:

| Check | Query / Command | Expected |
|-------|----------------|----------|
| All fixtures loaded | `{{fixture_command}}` exits with code 0 | No errors |
| Idempotent reload | Run `{{fixture_command}}` twice | No errors, same record count |
| Record counts | Count rows per entity table | Match Data Volume table above |
| Cross-BC references | Verify foreign keys resolve | No orphaned references |
| Test user login | Authenticate with test credentials | Success for all fixture users |

## Language-specific patterns
(See language template)

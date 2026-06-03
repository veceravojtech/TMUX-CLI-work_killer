# Migration Plan: {{bc_name}}

## Overview

- Bounded Context: {{bc_name}}
- Related Goal: {{goal_reference}}
- Migration Count: {{migration_count}}

## Migration Ordering

Migrations MUST be executed in the order listed. Each migration declares its dependencies explicitly.

| Order | Migration ID | Description | Depends On |
|-------|-------------|-------------|------------|
| {{order}} | {{migration_id}} | {{description}} | {{depends_on_migration_id}} |

## Table Definitions

### {{table_name}}

- Purpose: {{table_purpose}}
- Bounded Context: {{bc_name}}

#### Columns

| Column | Type | Nullable | Default | Description |
|--------|------|----------|---------|-------------|
| {{column_name}} | {{column_type}} | {{nullable}} | {{default_value}} | {{column_description}} |

Supported generic types: `string`, `text`, `integer`, `bigint`, `smallint`, `decimal(precision,scale)`, `float`, `boolean`, `uuid`, `date`, `datetime`, `time`, `json`, `binary`.

#### Primary Key

- {{primary_key_columns}}

#### Indexes

| Index Name | Columns | Unique | Description |
|------------|---------|--------|-------------|
| {{index_name}} | {{index_columns}} | {{unique}} | {{index_description}} |

#### Foreign Keys

| Constraint Name | Columns | References | On Delete | On Update |
|----------------|---------|------------|-----------|-----------|
| {{fk_name}} | {{fk_columns}} | {{referenced_table}}({{referenced_columns}}) | {{on_delete_action}} | {{on_update_action}} |

#### Unique Constraints

| Constraint Name | Columns | Description |
|----------------|---------|-------------|
| {{unique_name}} | {{unique_columns}} | {{unique_description}} |

#### Check Constraints

| Constraint Name | Expression | Description |
|----------------|------------|-------------|
| {{check_name}} | {{check_expression}} | {{check_description}} |

## Column Alterations

### {{table_name}} — Changes

| Column | Change Type | Before | After | Data Migration Required |
|--------|------------|--------|-------|------------------------|
| {{column_name}} | {{add/modify/drop}} | {{before_state}} | {{after_state}} | {{yes/no}} |

## Data Migration

### Step {{step_number}}: {{data_migration_description}}

- **When:** {{before/after}} schema migration {{migration_id}}
- **Scope:** {{affected_row_estimate}} rows in {{table_name}}
- **Logic:** {{migration_logic_description}}
- **Idempotent:** {{yes/no}}
- **Batch Size:** {{batch_size}} (if applicable)

## Rollback Strategy

### Migration {{migration_id}} — Rollback

- **Reversible:** {{yes/no}}
- **Steps:**
  1. {{rollback_step}}
- **Data Loss Risk:** {{none/partial/full}} — {{risk_description}}
- **Point of No Return:** {{description_or_none}}

## Cross-BC Dependencies

| This BC Table | Foreign BC | Foreign Table | Relationship | Notes |
|--------------|-----------|---------------|-------------|-------|
| {{table_name}} | {{foreign_bc_name}} | {{foreign_table}} | {{relationship_type}} | {{dependency_notes}} |

## Verification Queries

Post-migration checks to confirm correctness:

- **Check:** {{verification_query_description}}
- **Expected:** {{expected_result}}

## Notes

- {{additional_notes}}

# Domain Model: {{bc_name}}

## Aggregates

### {{aggregate_name}}
- Root entity: {{root_entity}}
- Value objects: {{value_objects}}
- Domain events: {{domain_events}}
- Repository interface (write): {{write_repository_interface}}
- Read model interface (query): {{read_model_interface}} (lives in Application layer)
- Invariants: {{invariants}}

## Domain Services

### {{service_name}}
- Spans aggregates: {{aggregate_list}}
- Operation: {{operation_description}}

## Context Map

### Dependencies on other BCs
- {{dependency_bc}}: via ACL interface {{acl_interface}}

## Share Namespace

Shared types from `src/Share/` used by this BC's domain model. Share depends on nothing; all BCs may import from Share.

### Shared DataTypes
- {{shared_data_type}}: {{data_type_description}} (used by: {{consuming_bcs}})

### Shared Events (Published Language)
- {{shared_event}}: {{event_description}} (produced by: {{producer_bc}}, consumed by: {{consumer_bcs}})

## Language-specific patterns
(See language template)

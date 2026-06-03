# Bounded Context Map: {{project_name}}

## Bounded Context Inventory

{{#each bounded_contexts}}
### {{bc_name}}

- **Purpose:** {{bc_purpose}}
- **Core domain concepts:** {{core_concepts}}
- **Aggregates:** {{aggregate_list}}
- **Owner team:** {{owner_team}}

{{/each}}

## Context Map

{{#if single_bc}}
> Single bounded context — context map not applicable. No inter-BC relationships, ACL boundaries, or Published Language needed.
{{/if}}

{{#unless single_bc}}

### Relationships

{{#each bc_relationships}}
#### {{upstream_bc}} → {{downstream_bc}}

- **Relationship type:** {{relationship_type}}
- **Integration pattern:** {{integration_pattern}}
- **Communication:** {{communication_mechanism}}
- **Data flow:** {{data_flow_description}}

{{/each}}

### Relationship Types Reference

| Type | When to use |
|------|-------------|
| Upstream/Downstream | One BC produces data or events that another consumes |
| Shared Kernel | Two BCs share a small, explicitly defined model subset |
| Customer/Supplier | Downstream has negotiating power over upstream's API |
| Conformist | Downstream conforms to upstream's model without translation |
| Anti-Corruption Layer (ACL) | Downstream translates upstream's model to protect its own domain |
| Open Host Service | Upstream exposes a well-defined protocol for multiple consumers |
| Published Language | Shared event/message format used across BC boundaries |

### Anti-Corruption Layer Boundaries

{{#each acl_boundaries}}
#### {{consuming_bc}} ← {{providing_bc}}

- **ACL interface:** {{acl_interface_name}}
- **Translates:** {{external_concept}} → {{internal_concept}}
- **Location:** {{consuming_bc}} domain layer
- **Implementation:** infrastructure adapter in {{consuming_bc}}
- **Foreign ID value object:** {{foreign_id_vo}}

{{/each}}

### Communication Patterns

{{#each communication_patterns}}
#### {{pattern_name}}

- **Type:** {{communication_type}}
- **From:** {{source_bc}}
- **To:** {{target_bc}}
- **Trigger:** {{trigger_description}}
- **Payload:** {{payload_description}}

{{/each}}

{{/unless}}

## Share Namespace

Shared types that live outside any single BC. All BCs may import from Share; Share imports nothing from any BC.

### Categories

#### DataTypes
Reusable value objects used across multiple BCs.

{{#each shared_data_types}}
- **{{type_name}}:** {{type_description}}
{{/each}}

#### Events
Published Language event DTOs for cross-BC communication. These are NOT internal domain events — they are the public contract between BCs.

{{#each shared_events}}
- **{{event_name}}:** {{event_description}} (produced by {{producer_bc}}, consumed by {{consumer_bcs}})
{{/each}}

#### Exceptions
Shared domain exceptions used across BC boundaries.

{{#each shared_exceptions}}
- **{{exception_name}}:** {{exception_description}}
{{/each}}

{{#if single_bc}}
> Single-BC project: Share namespace may still contain DataTypes (e.g., Money, DateTime wrappers) even without cross-BC communication. Events and Exceptions sections are typically empty.
{{/if}}

## Dependency Rules

1. A BC's Domain layer depends only on its own domain types and Share namespace
2. Cross-BC references use foreign ID value objects, never direct entity references
3. ACL interfaces are defined in the consuming BC's domain layer
4. ACL implementations live in the consuming BC's infrastructure layer
5. Published Language events in Share are the only cross-BC message contract
6. Share namespace has zero dependencies on any BC

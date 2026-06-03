# Context Mapping Patterns: {{project_name}}

{{#if single_bc}}
> Single bounded context — context mapping patterns not applicable. Translation strategies, boundary error handling, event choreography, and data consistency patterns apply only to multi-BC projects.
{{/if}}

{{#unless single_bc}}

> This document describes HOW bounded contexts communicate — translation rules, error handling at boundaries, event choreography, and consistency strategies. For WHAT relationships exist, see bounded-contexts.md.

## Translation Strategies

{{#each translation_strategies}}
### {{boundary_name}}: {{upstream_bc}} → {{downstream_bc}}

- **Translation direction:** {{translation_direction}}
- **Field mapping:** {{field_mapping}}
- **Type coercion:** {{type_coercion_rules}}
- **Default value handling:** {{default_value_handling}}
- **Translator location:** {{translator_location}}
- **ACL interface:** {{acl_interface_ref}} (see service-contracts for method signatures)

{{/each}}

## Boundary Error Handling

{{#each boundary_error_patterns}}
### {{boundary_name}}

- **Error mapping:** {{error_mapping}}
- **Fallback behavior:** {{fallback_behavior}}
- **Timeout policy:** {{timeout_policy}}
- **Partial failure semantics:** {{partial_failure_semantics}}
- **Degraded mode:** {{degraded_mode_description}}

{{/each}}

## Event Flow Choreography

{{#each event_flows}}
### {{flow_name}}

- **Triggering event:** {{triggering_event}} (domain event in {{source_bc}})
- **Published Language event:** {{published_language_event}} (see bounded-contexts Share namespace)
- **Consuming BC(s):** {{consuming_bcs}}
- **Consumer reaction:** {{consumer_reaction}}
- **Ordering guarantee:** {{ordering_guarantee}}
- **Idempotency strategy:** {{idempotency_strategy}}

{{/each}}

## Data Consistency Patterns

{{#each consistency_patterns}}
### {{pattern_name}}: {{upstream_bc}} ↔ {{downstream_bc}}

- **Consistency model:** {{consistency_model}}
- **Synchronization mechanism:** {{sync_mechanism}}
- **Conflict resolution:** {{conflict_resolution}}
- **Staleness tolerance:** {{staleness_tolerance}}
- **Compensating action:** {{compensating_action}}

{{/each}}

{{#if has_shared_kernel}}

## Shared Kernel Governance

{{#each shared_kernel_rules}}
### {{kernel_name}}: {{participating_bcs}}

- **Change approval:** {{change_approval_process}}
- **Versioning strategy:** {{versioning_strategy}}
- **Breaking change policy:** {{breaking_change_policy}}
- **Testing requirements:** {{testing_requirements}}

{{/each}}

{{/if}}

## Cross-References

| Concern | Document | Section |
|---------|----------|---------|
| What relationships exist | bounded-contexts.md | Relationships, ACL Boundaries, Communication Patterns |
| ACL method signatures | service-contracts.md | Anti-Corruption Layer Interfaces |
| Shared event/type contracts | bounded-contexts.md | Share Namespace |
| Infrastructure adapter config | infrastructure.md | External Adapters (Anti-Corruption Layer) |
| Domain-level context dependencies | domain-model.md | Context Map |

{{/unless}}

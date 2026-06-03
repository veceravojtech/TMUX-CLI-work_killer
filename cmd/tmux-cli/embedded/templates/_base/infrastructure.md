# Infrastructure Layer: {{bc_name}}

## Persistence

### Repository Implementations

#### {{aggregate_name}}Repository
- Implements: {{write_repository_interface}} (from Domain layer)
- Mapping strategy: {{mapping_strategy}}
- Table: {{table_name}}
- Identity column: {{identity_column}}
- Embedded value objects: {{embedded_value_objects}}
- Concurrency control: {{concurrency_strategy}}

#### {{aggregate_name}}ReadRepository
- Implements: {{read_model_interface}} (from Application layer)
- Storage: {{read_storage_type}}
- Query optimizations: {{query_optimizations}}
- Filtering: {{supported_filters}}

### Persistence Mapping

#### {{aggregate_name}}
- Mapping format: {{mapping_format}}
- Mapping location: {{mapping_path}}
- Field mappings:
  - {{domain_field}} → {{db_column}} ({{column_type}})
- Value object mappings:
  - {{value_object}} → {{embedding_strategy}}
- Association mappings:
  - {{association_name}}: {{association_type}} → {{target_entity}}

### Custom Types

#### {{custom_type_name}}
- Wraps domain type: {{domain_value_object}}
- Database type: {{db_native_type}}
- Conversion: {{domain_to_db_description}}
- Reverse: {{db_to_domain_description}}

### Migrations

#### {{migration_name}}
- Purpose: {{migration_purpose}}
- Tables affected: {{tables_affected}}
- Reversible: {{is_reversible}}
- Data migration required: {{data_migration_notes}}

## Messaging

### Event Dispatching

#### {{event_dispatcher_name}}
- Implements: {{domain_event_dispatcher_interface}} (from Domain layer)
- Transport: {{transport_type}}
- Dispatch timing: {{dispatch_timing}}

### Message Bus Adapters

#### Command Bus
- Adapter: {{command_bus_adapter}}
- Middleware: {{command_middleware}}
- Routing: {{command_routing_strategy}}

#### Query Bus
- Adapter: {{query_bus_adapter}}
- Middleware: {{query_middleware}}
- Routing: {{query_routing_strategy}}

#### Event Bus
- Adapter: {{event_bus_adapter}}
- Async transport: {{async_transport}}
- Retry strategy: {{retry_strategy}}
- Failed message handling: {{failed_message_strategy}}

## External Adapters (Anti-Corruption Layer)

### {{external_system_name}} Adapter
- Implements: {{acl_interface}} (from Domain layer)
- External system: {{external_system_description}}
- Protocol: {{communication_protocol}}
- Authentication: {{auth_method}}
- Error mapping: {{external_to_domain_error_mapping}}
- Circuit breaker: {{circuit_breaker_config}}
- Data transformation: {{external_to_domain_dto_mapping}}

## Read Model Projections

### {{read_model_name}}
- Implements: {{read_model_interface}} (from Application layer)
- Storage: {{read_storage_type}}
- Projection source: {{projection_event_source}}
- Query optimizations: {{query_optimizations}}

## Integration Test Requirements

### Write Repository Tests
- {{write_repo_test_save_retrieve}}
- {{write_repo_test_concurrency}}
- {{write_repo_test_identity_uniqueness}}

### Read Model Tests
- {{read_model_test_query_filtering}}
- {{read_model_test_projection_accuracy}}
- {{read_model_test_empty_result}}

### Messaging Tests
- {{messaging_test_dispatch}}
- {{messaging_test_async_delivery}}
- {{messaging_test_retry_transient}}

### ACL Adapter Tests
- {{acl_test_external_domain_mapping}}
- {{acl_test_circuit_breaker}}
- {{acl_test_auth_failure}}

## Infrastructure Dependencies

### Internal (same BC)
- Domain contracts implemented: {{domain_contracts_list}}
- Application contracts implemented: {{application_contracts_list}}

### External
- {{external_dependency}}: {{dependency_purpose}}

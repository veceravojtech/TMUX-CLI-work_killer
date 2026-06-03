# Service Contracts: {{bc_name}}

## Write Repository Interfaces (Domain Layer)

### {{write_repo_interface_name}}
- File path: {{interface_file_path}}
- Aggregate served: {{aggregate_name}}
- Layer: Domain
- Purpose: {{write_repo_purpose}}

#### Methods
- {{method_name}}: {{method_purpose_description}}
  - Accepts: {{input_description}}
  - Returns: {{return_description}}
  - Invariants: {{invariants_enforced}}

### Conventions
- Return aggregate roots only, never child entities
- Use domain-specific query methods (not generic find-all)
- Accept and return domain objects, not primitives or DTOs

## Read Model Interfaces (Application Layer)

### {{read_model_interface_name}}
- File path: {{interface_file_path}}
- Read model served: {{read_model_name}}
- Layer: Application
- Purpose: {{read_model_purpose}}

#### Methods
- {{method_name}}: {{method_purpose_description}}
  - Accepts: {{input_description}}
  - Returns: {{return_description}} (DTO / read model projection)
  - Filtering: {{supported_filters}}

### Conventions
- Return lightweight DTOs, never full aggregates
- Keep Domain layer free of presentation concerns
- Define DTOs alongside the interface in the Application layer

## Domain Service Interfaces (Domain Layer)

### {{domain_service_interface_name}}
- File path: {{interface_file_path}}
- Aggregates spanned: {{aggregate_list}}
- Layer: Domain
- Purpose: {{service_purpose}}

#### Methods
- {{method_name}}: {{method_purpose_description}}
  - Accepts: {{input_description}}
  - Returns: {{return_description}}
  - Side effects: {{side_effects}}
  - Invariants: {{invariants_enforced}}

### Conventions
- Injected with repository interfaces (ports), not implementations
- No infrastructure dependencies
- Handles operations spanning multiple aggregates within same BC

## Anti-Corruption Layer Interfaces

### {{acl_interface_name}}
- File path: {{interface_file_path}}
- External BC: {{external_bc_name}}
- Direction: {{inbound_or_outbound}}
- Purpose: {{acl_purpose}}

#### Methods
- {{method_name}}: {{method_purpose_description}}
  - Accepts: {{input_description}} (own BC types)
  - Returns: {{return_description}} (own BC types)
  - Translates from/to: {{external_type_description}}
  - Condition: {{when_to_invoke}}

### Conventions
- Translate external types to own BC types at the boundary
- Never expose external BC's domain model to consuming code
- Use Published Language DTOs for cross-BC event contracts
- One ACL interface per external BC dependency

## Language-specific patterns
(See language template)

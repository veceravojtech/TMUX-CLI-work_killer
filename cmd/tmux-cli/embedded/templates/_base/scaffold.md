# Project Scaffold: {{project_name}}

## Source Directory Structure

{{#each bounded_contexts}}
### {{bc_name}}

```
src/{{bc_name}}/
├── Domain/
│   ├── Model/
│   │   ├── {{bc_name}}Id.{{ext}}
│   │   └── {{aggregate_name}}.{{ext}}
│   ├── Event/
│   ├── Exception/
│   ├── Repository/
│   │   └── {{aggregate_name}}RepositoryInterface.{{ext}}
│   └── Service/
├── Application/
│   ├── Command/
│   ├── Query/
│   ├── Handler/
│   ├── DTO/
│   └── ReadModel/
│       └── {{aggregate_name}}ReadModelInterface.{{ext}}
└── Infrastructure/
    ├── Persistence/
    │   ├── Mapping/
    │   ├── Migration/
    │   ├── Repository/
    │   │   └── {{aggregate_name}}Repository.{{ext}}
    │   └── Type/
    ├── Messaging/
    └── Adapter/
```

{{/each}}

## Share Namespace Structure

Shared types outside any single bounded context. All BCs may import from Share; Share imports nothing from any BC (see `bounded-contexts.md` Dependency Rules).

```
src/Share/
├── DataType/
│   └── {{shared_data_type}}.{{ext}}
├── Event/
│   └── {{shared_event}}.{{ext}}
├── Exception/
│   └── {{shared_exception}}.{{ext}}
└── Messaging/
    └── {{message_contract}}.{{ext}}
```

### Share Subdirectory Purposes

| Subdirectory | Contents | Example |
|-------------|----------|---------|
| `DataType/` | Reusable value objects used across multiple BCs | Money, DateRange, Locale |
| `Event/` | Published Language event DTOs for cross-BC communication | OrderPlaced, PaymentReceived |
| `Exception/` | Shared domain exceptions used across BC boundaries | InvalidCurrencyException |
| `Messaging/` | Message envelope contracts and bus interfaces | AsyncMessageEnvelope |

## Configuration Files

```
{{project_root}}/
├── config/
│   ├── {{env_config}}
│   ├── {{di_config}}
│   ├── {{routing_config}}
│   └── {{messaging_config}}
├── {{env_file}}
├── {{env_file}}.dist
└── {{project_config}}
```

### Configuration Categories

| Category | Purpose | Placement |
|----------|---------|-----------|
| Environment | Runtime variables (database, service URLs, secrets) | `{{env_file}}` at project root |
| Dependency injection | Service wiring and autowiring rules | `config/{{di_config}}` |
| Routing | HTTP endpoint-to-controller mapping | `config/{{routing_config}}` |
| Messaging | Event bus transports and routing | `config/{{messaging_config}}` |
| BC-specific | Per-BC service overrides | `config/{{bc_name}}/` subdirectory |

## Quality Tool Configuration

Abstract tool categories — concrete tool names are language-specific and belong in the language overlay template.

```
{{project_root}}/
├── {{linter_config}}
├── {{static_analyzer_config}}
├── {{architecture_enforcer_config}}
└── {{test_runner_config}}
```

### Quality Tool Categories

| Category | Purpose | IG-06 Role |
|----------|---------|------------|
| Linter | Code style enforcement and auto-fixing | — |
| Static analyzer | Type safety, dead code, bug detection | Detects cross-layer type leaks |
| Architecture enforcer | Layer dependency validation | **Primary IG-06 gate** — enforces no Domain→Infrastructure imports |
| Test runner | Unit, integration, and E2E test execution | Runs gate IG-01 through IG-05 checks |

### Architecture Enforcer Rules

The architecture enforcer validates layer dependencies per `quality-gates.md` IG-06:

{{#each bounded_contexts}}
#### {{bc_name}}

| Source layer | Allowed dependencies | Forbidden dependencies |
|-------------|---------------------|----------------------|
| `src/{{bc_name}}/Domain/` | Own domain types, `src/Share/` | Application, Infrastructure, other BCs |
| `src/{{bc_name}}/Application/` | Own domain layer, `src/Share/` | Infrastructure, other BCs |
| `src/{{bc_name}}/Infrastructure/` | Own domain layer, own application layer, `src/Share/`, external libraries | Other BCs |

{{/each}}

## Dependency Rules

Cross-reference: see `bounded-contexts.md` Dependency Rules for the full specification.

1. **Domain layer isolation** — a BC's Domain layer depends only on its own domain types and `src/Share/`
2. **No cross-BC entity sharing** — cross-BC references use foreign ID value objects, never direct entity references
3. **ACL at domain boundary** — ACL interfaces are defined in the consuming BC's Domain layer; implementations live in Infrastructure
4. **Share imports nothing** — the Share namespace has zero dependencies on any bounded context
5. **Published Language only** — cross-BC communication uses Share event DTOs as the only message contract

## Scaffold Verification

### SC-03: DDD Directory Structure per BC

Each bounded context must contain the three DDD layers:

{{#each bounded_contexts}}
- [ ] `src/{{bc_name}}/Domain/` exists with Model, Event, Exception, Repository, Service subdirectories
- [ ] `src/{{bc_name}}/Application/` exists with Command, Query, Handler, DTO, ReadModel subdirectories
- [ ] `src/{{bc_name}}/Infrastructure/` exists with Persistence, Messaging, Adapter subdirectories
{{/each}}

### SC-04: Share Namespace Structure

- [ ] `src/Share/DataType/` exists for cross-BC value objects
- [ ] `src/Share/Event/` exists for Published Language event DTOs
- [ ] `src/Share/Exception/` exists for shared domain exceptions
- [ ] `src/Share/Messaging/` exists for message contracts

### Configuration Verification

- [ ] Environment config file exists at project root
- [ ] Dependency injection config exists in `config/`
- [ ] Routing config exists in `config/`
- [ ] Quality tool configs exist at project root

### Architecture Enforcement Verification

- [ ] Architecture enforcer config defines layer rules for each BC per IG-06
- [ ] No Domain layer imports from Infrastructure or Application
- [ ] No Application layer imports from Infrastructure
- [ ] Share namespace has zero imports from any BC

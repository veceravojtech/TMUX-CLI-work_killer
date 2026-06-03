# Bounded Context Structure: {{project_name}} (Symfony)

> Extends: `_base/bounded-contexts.md` — context map, relationships, ACL, Share categories, dependency rules.
> This template adds Symfony namespace conventions, directory layout, and Deptrac enforcement.

## Namespace Convention

Each bounded context maps to a top-level PHP namespace under `App\`:

```
App\{BC}\Domain\        — aggregates, value objects, repository interfaces, domain services, events
App\{BC}\Application\   — command/query handlers, read models, DTOs
App\{BC}\Infrastructure\ — Doctrine repos, Messenger adapters, HTTP controllers
```

Share namespace: `App\Share\` — cross-BC shared types (see base template Share Namespace section).

## Per-BC Directory Tree

```
src/{BC}/
├── Domain/
│   ├── Model/          # Aggregate roots, entities, value objects
│   ├── Repository/     # Repository interfaces (ports)
│   ├── Service/        # Domain services (multi-aggregate operations)
│   └── Event/          # Internal domain events
├── Application/
│   ├── Command/        # Command DTOs + handlers
│   ├── Query/          # Query DTOs + handlers
│   └── ReadModel/      # Read-model projections and DTOs
└── Infrastructure/
    ├── Persistence/
    │   └── Doctrine/
    │       ├── Repository/  # Doctrine repository implementations
    │       └── Mapping/     # XML mapping files ({Entity}.orm.xml)
    └── Adapter/        # ACL adapters, Messenger handlers, external service clients
```

Canonical paths (9 DDD directories):

- `src/{BC}/Domain/Model/` — aggregate roots, entities, value objects
- `src/{BC}/Domain/Repository/` — repository interfaces (ports)
- `src/{BC}/Domain/Service/` — domain services
- `src/{BC}/Domain/Event/` — internal domain events
- `src/{BC}/Application/Command/` — command DTOs + handlers
- `src/{BC}/Application/Query/` — query DTOs + handlers
- `src/{BC}/Application/ReadModel/` — read-model projections and DTOs
- `src/{BC}/Infrastructure/Persistence/Doctrine/Repository/` — Doctrine implementations
- `src/{BC}/Infrastructure/Adapter/` — ACL adapters, external service clients

## Share Namespace Directory Tree

```
src/Share/
├── DataType/           # Money, PhoneNumber, DateTime wrappers
├── Event/              # Published Language event DTOs (cross-BC contract)
├── Exception/          # Shared domain exceptions
└── Messaging/          # Message bus abstractions
```

### Composer Autoload

```json
{
  "autoload": {
    "psr-4": {
      "App\\": "src/"
    }
  }
}
```

All BCs and Share resolve under the single `App\\` prefix. No per-BC autoload entries needed.

## Deptrac Layer Definitions

### Layers per BC

```yaml
# deptrac.yaml
deptrac:
  layers:
    {{#each bounded_contexts}}
    - name: {{bc_name}}Domain
      collectors:
        - type: classLike
          value: App\\{{bc_name}}\\Domain\\.*
    - name: {{bc_name}}Application
      collectors:
        - type: classLike
          value: App\\{{bc_name}}\\Application\\.*
    - name: {{bc_name}}Infrastructure
      collectors:
        - type: classLike
          value: App\\{{bc_name}}\\Infrastructure\\.*
    - name: {{bc_name}}Controller
      collectors:
        - type: classLike
          value: App\\{{bc_name}}\\Infrastructure\\.*Controller
    {{/each}}
    - name: Share
      collectors:
        - type: classLike
          value: App\\Share\\.*
```

### DomainAndShare Composite Layer (previo2 pattern)

Each BC's domain layer is grouped with Share into a composite layer. This allows domain code to use shared types without Deptrac violations:

```yaml
    {{#each bounded_contexts}}
    - name: {{bc_name}}DomainAndShare
      collectors:
        - type: bool
          must:
            - type: classLike
              value: App\\{{bc_name}}\\Domain\\.*
          must_not: []
        - type: classLike
          value: App\\Share\\.*
    {{/each}}
```

### Ruleset

```yaml
  ruleset:
    {{#each bounded_contexts}}
    {{bc_name}}Domain:
      - Share
    {{bc_name}}Application:
      - {{bc_name}}DomainAndShare
    {{bc_name}}Infrastructure:
      - {{bc_name}}DomainAndShare
      - {{bc_name}}Application
    {{bc_name}}Controller:
      - {{bc_name}}Application
    {{/each}}
    Share: ~
```

**Layer dependency rules:**
- **Domain** → Share only (no Application, no Infrastructure, no other BC)
- **Application** → own Domain + Share (via DomainAndShare composite)
- **Infrastructure** → own Domain + Share + own Application
- **Controller** (Infrastructure sublayer) → own Application only (cannot bypass to Domain directly)
- **Share** → nothing (zero dependencies)
- **Cross-BC** → never direct; always via ACL adapter in Infrastructure + Published Language events in Share

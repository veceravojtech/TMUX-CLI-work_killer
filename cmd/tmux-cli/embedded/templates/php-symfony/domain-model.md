# PHP/Symfony Domain Patterns

> Extends: `_base/domain-model.md` — adds Doctrine entity patterns, aggregate root conventions, value object implementations, and Share namespace structure.

## Aggregate Root (PD-01, PD-02, PD-17, PD-20)
- Pure POPO: extends nothing, no framework base class
- Private constructor — all creation through named constructors
- Named constructors: `public static function create(...)` returning `self`
- Domain events collected via `DomainEventTrait` (collect-only, never dispatch from the aggregate)
- Invariants enforced in constructor and command methods
- One aggregate per transaction — prefer small aggregates referencing by ID
- Location: `src/{BC}/Domain/`

## Value Object (PD-03, PD-04, PD-05)
- `final class` — immutable, no setters
- Constructor validates input, throws `DomainException` on invalid state
- Implements `equals()` method for comparison: `equals(self $other): bool`
- Location: `src/{BC}/Domain/ValueObject/`
- Custom DBAL type in Infrastructure for DB persistence (see Doctrine Mapping)

## Repository Interface — Write (PD-07, PD-09)
- Location: `src/{BC}/Domain/Repository/`
- Returns aggregate root only, never child entities
- Domain-specific methods: `findByEmail()`, `nextIdentity()` — not generic `find($id)`
- Zero imports from Infrastructure or Application namespace

## Read Model Interface — Query (PD-08)
- Location: `src/{BC}/Application/ReadModel/`
- Returns read model DTOs defined alongside the interface
- Avoids hydrating full aggregates for reads
- Keeps Domain layer free of presentation concerns

## Domain Service (PD-14, PD-09)
- Location: `src/{BC}/Domain/Service/`
- Injected with repository interfaces (ports), no concrete infrastructure
- Handles operations spanning multiple aggregates within same BC
- Zero infrastructure dependencies

## Domain Event Dispatching (PD-06, PD-17)
- Events named in past tense: `{{aggregate_name}}Created`, `{{aggregate_name}}Updated`
- Events collected via `DomainEventTrait` in aggregate root
- Infrastructure repository dispatches after flush (flush-then-dispatch)
- `DomainEventDispatcherInterface` defined in Domain layer
- Symfony Messenger adapter in `src/{BC}/Infrastructure/Messaging/` implements the interface

## Anti-Corruption Layer (PD-15, PD-16)
- ACL interface in consuming BC's Domain layer: `src/{BC}/Domain/ACL/`
- Symfony Messenger adapter in `src/{BC}/Infrastructure/ACL/`
- Published Language event DTOs for cross-BC events in `src/Share/Event/`
- Foreign ID value objects for cross-BC references (e.g., `{{foreign_bc}}Id`)

## Doctrine Mapping (PD-13)
- XML mapping files in `src/{BC}/Infrastructure/Persistence/Doctrine/Mapping/`
- No annotations or attributes on domain objects (`@ORM\` and `#[ORM\` forbidden)
- Custom DBAL types for value objects in `src/{BC}/Infrastructure/Persistence/Doctrine/Type/`
- One XML file per entity/embeddable: `{{AggregateRoot}}.orm.xml`

## Share Namespace — previo2 Pattern (PD-18, PD-19)
- `src/Share/DataType/` — Money, PhoneNumber, DateTime wrappers, common VOs
- `src/Share/Event/` — Published Language event DTOs for cross-BC communication
- `src/Share/Exception/` — shared domain exceptions
- `src/Share/Messaging/` — message bus abstractions (Symfony Messenger contracts)
- Deptrac groups `{BC}Domain` + `Share` into `{BC}DomainAndShare` layer
- All BCs can import from Share; Share imports nothing
- Cross-BC shared types live here, NOT duplicated per BC

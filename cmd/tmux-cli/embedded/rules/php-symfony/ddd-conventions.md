# Symfony DDD conventions (pack: php-symfony)

Universal layer contract for generated Symfony DDD projects. Audience:
spec/implementation/review agents. The point-wise enforceable subset lives in
`code-rules.yaml` (same pack); this document is the connective tissue —
read it before writing code in any `src/<BC>/` tree.

Adapted from a production Symfony DDD monorepo's layer contract, generalized
to the `src/<BC>/<Layer>/` skeleton this planner generates.

## Layering

- Three layers per bounded context: `src/<BC>/Domain/` → `src/<BC>/Application/` → `src/<BC>/Infrastructure/`.
- Dependency direction is strictly downward-free: a lower layer never uses anything from a higher layer (Domain knows nothing of Application; neither knows Infrastructure). Enforced with deptrac.
- Namespace mirrors path: `App\<BC>\<Layer>\<Module>\...`.
- Only Infrastructure (and the framework entry points — controllers, CLI commands, message consumers) may use Symfony framework components, Doctrine, or any concrete technology.

## Domain layer (`src/<BC>/Domain/`)

- Pure business logic — no Doctrine, no HTTP, no framework types.
- Structured by domain modules; each module owns:
  - its **Aggregate** as the main carrier of business logic;
  - its **repository interface** (in the module folder, e.g. `Domain/Reservation/ReservationRepositoryInterface.php`);
  - a **Command folder** for input objects passed into aggregate business methods;
  - entities and value objects grouped in folders named after their most important entity.
- **Aggregate contract:**
  - a static `create(...)` factory constructs the aggregate and fires the Created event;
  - business methods (e.g. `lock()`, `changeSource()`) mutate state and fire domain events — verify every state-changing method publishes its event;
  - child entities are never exposed whole — return DTOs, or aggregate encapsulation is broken.
- **Repository interface:** prefer intent-named save methods (`saveContactPerson(...)`, `lockFolio(...)`) over one generic `save()` — the persistence intent stays explicit and accidental full-aggregate saves are impossible.
- Domain events carry lightweight readonly snapshots of aggregate state — never the full aggregate. Events are published language: only additive, non-breaking changes (add fields; never remove or rename).

## Application layer (`src/<BC>/Application/`)

- Still technology-free: no Doctrine, no HTTP types.
- **CQRS-lite split:**
  - **Query services** (`Query/` folders): each has its OWN read-only repository interface in the Application layer — never the Domain repository. They return readonly DTOs, do reading logic only, and never call each other. Naming: `XxxQueryService` + `XxxQueryRepositoryInterface`.
  - **Command handlers** (`Command/` folders): orchestrate Domain logic via the domain repository interface (dependency inversion). Standard shape: load aggregate → authorization/ownership check on the actor → call aggregate business method → save via domain repository. Single entry method `handle()`; keep handlers straight and small.
- Application repositories are read-only — no save/update/delete on a Query repository interface.
- `handle()` is atomic: wrap in a transaction (Symfony messenger/Doctrine middleware) so persistence and event publication commit together or not at all.
- Every command handler enforces the actor's authorization/ownership before mutating — and its tests cover the deny path (PHP-TEST-001).
- Exceptions thrown to callers come from the Application layer's own `Exception/` folders — domain exceptions never cross the boundary raw.

## Infrastructure layer (`src/<BC>/Infrastructure/`)

- Concrete implementations of Domain and Application interfaces; the only layer that may use Doctrine/SQL/external SDKs.
- Naming states the technology: `DoctrineXxxRepository`, `MysqlXxxQueryRepository`.

## Shared namespace (`src/Share/`)

- Cross-BC value objects, shared aggregate IDs, and base types live here (the planner's discovery captures these in the Share Namespace step).
- Always prefer a shared value object (`Money`, `Email`, `DateRange`) over a raw primitive (PHP-TYPE-002).
- Never redefine an ID/value object in a BC when `src/Share/` already has it.

## Cross-BC communication

- One BC never imports another BC's concrete classes. Cross-BC reads go through published contract interfaces (query contract returning contract-shaped DTOs); cross-BC effects go through domain events.
- A consumer depends on the contract interface only; the owning BC's query service implements it.

## Tests

- Mirror the source structure under `tests/`; use the `#[Test]` attribute, not the `test*` prefix; keep tests small and readable.
- **Unit tests** (`tests/Unit/`): pure in-memory, no kernel, no database. `Unit/Domain` tests Domain behavior only; `Unit/Application` tests handlers along the longest happy path using spies and in-memory repositories.
- **Integration tests** (`tests/Integration/`): real Symfony container + real (test-env) database.
- **Test mothers** (`tests/Resources/`): static factories with descriptive names (`TestReservation::singleConfirmed()`), pre-set ID constants (`SINGLE_CONFIRMED_ID = 1`, `NONEXISTENT_ID = 99`), building REAL domain objects (not mocks) and composing each other. In-memory `TestXxxRepository` classes implement the production interfaces. Reuse mothers across unit and integration tests — never duplicate fixture data.

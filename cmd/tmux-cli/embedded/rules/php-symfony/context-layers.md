# Context layers (pack: php-symfony)

A bounded context (`contexts/<bc>/src`) is layered Domain ← Application ← Bundle.
Domain is the lowest and purest; Application is the entry point for use cases;
Bundle is infrastructure. A lower layer never imports a higher one. A class's
namespace mirrors its directory path under the project's **resolved root
namespace** — the vendor prefix discovered from the project's `composer.json`
PSR-4 autoload (or the `docs/architecture/layout.md` `## Layers` doc), falling
back to `App\` only for a greenfield project, and ASK when ambiguous. So
namespaces follow `<RootNs>\<Bc>\<Layer>\<Module>\...` (shown here with the
greenfield `App\` default; substitute the discovered vendor, e.g. `Previo2\`).

## Domain — the aggregate triad

Each domain module (`src/Domain/<Module>/`) is built around one **Aggregate** and
a three-part seam that keeps the aggregate Doctrine-free:

- **`<Aggregate>`** — the business object. Its constructor takes the aggregate's
  typed `Id`, any owning ids, and an **`<Aggregate>Data`** DAO. A static
  `create(<Aggregate>Id, OwnerId, <Aggregate>DTO): self` factory builds it and
  fires the `Created` event. Business methods (`lock()`, `addItem()`, …) mutate
  the DAO and publish domain events. The aggregate exposes getters consumed by
  handlers for ACL and decisions; entities are returned **only as DTOs**, never
  raw, so encapsulation holds.
- **`<Aggregate>Data` (DAO)** — the Doctrine entity. It carries `Collection<>`
  fields for child entities of the same aggregate, and references **foreign**
  aggregates by their typed `Id` plus a `#[PersistentReference]` back-reference —
  never by a concrete foreign-entity association (this is the seam PHP-PERS-005
  guards). Named with a `Data` suffix (`OrderData`, `OrderLineData`).
- **`<Aggregate>DTO`** — a `readonly` immutable input value object passed to
  `create()`. Constructor-promoted `readonly` properties; `DTO` suffix.

```php
final class Order
{
    public function __construct(
        private readonly OrderId $id,
        #[AggregateDAO] private OrderData $data,   // DAO, not a bag of scalars
    ) {}

    public static function create(OrderId $id, CustomerId $customer, OrderDTO $dto): self
    {
        $order = new self($id, OrderData::fromDto($customer, $dto));
        EventPublisher::publish(new OrderCreated($order->snapshot()));
        return $order;
    }
}
```

The domain `RepositoryInterface` lives directly in `Domain/<Module>/`. Its save
methods are **named per operation** (`saveContact(Order $o)`, `lockOrder(Order $o)`)
— never a generic `save()` — so persistence intent is explicit and accidental
full-aggregate writes are impossible.

## Application — read vs write split

`src/Application/<Module>/` holds the use cases and **must not** contain a
repository with save/delete/update methods (those belong to Domain).

**Place each `Application/<Module>` in the context that owns its concept.** A
module's read models, query services and repositories belong in the bounded context
that ALREADY holds the `Application/<Concept>` for its dominant returned aggregate /
`*Id` / DTO — author `Guest` under customer, `Invoice` under invoicing, `Partner`
under crm, never under whatever context is convenient. A green deptrac slice does
not authorize a misplaced concept (deptrac checks dependency direction only); the
catalogue rule PHP-ARCH-017 carries this ownership check.

- **Query services** (`Query/`) are read-only. Each query service has its **own**
  query-repository interface declared in the Application layer — it does **not**
  reuse the Domain repository. Query services are standalone (they never call each
  other) and return `readonly` result DTOs. Naming: `<X>QueryService` implementing
  the shared `<X>QueryContract` (the contract lives in `contexts/previo/src` — see
  shared-kernel.md).
- **Command handlers** (`Command/`) do the writing. The handler entry method is
  `handle()` and everything inside it is **atomic** — Symfony wraps `handle()` in a
  transaction, so even event publication is deferred until a successful return.
  The canonical body is small and straight:

```php
final class LockOrderHandler
{
    public function handle(LockOrder $command): Result
    {
        $this->acl->ensureAccessToProperty($command->actor, $command->propertyId); // ACL first
        $order = $this->orders->get($command->orderId);   // load via DOMAIN repo
        $order->lock();                                    // aggregate business method
        $this->orders->lockOrder($order);                 // intent-named save
        return Result::ok();
    }
}
```

Every handler starts with the actor ACL check
(`$this->acl->ensureAccessToProperty($actor, $command->propertyId)`) so access is
verified before any state changes. Handlers throw only exceptions defined in this
module's `Application/<Module>/Exception/` — never raw domain exceptions.

## Bundle — concrete implementations

`src/Bundle/Domain/<Module>/` implements the domain `RepositoryInterface`
(`DoctrineOrderRepository`, `MysqlOrderRepository`); `src/Bundle/Application/<Module>/`
implements the query-repository interfaces (`MysqlOrdersQueryRepository`). These
are the only classes that may touch Doctrine, MySQL, or other storage. Because
Domain and Application depend only on their interfaces, both are unit-tested with
in-memory test repositories that implement the same contract.

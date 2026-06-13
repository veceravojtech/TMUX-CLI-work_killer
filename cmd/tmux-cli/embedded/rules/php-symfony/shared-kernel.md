# Shared kernel (pack: php-symfony)

`contexts/previo/src` is the **shared-kernel context**: the one published language
that every other context and project may depend on. Keep this directory name
literal — it is the canonical shared-kernel context for this pack. Its namespace
is `App\Previo\<Layer>\<Module>\...`. Nothing here may depend on any other
context; everything here is hard to change, so changes must stay **non-breaking**
(additive only).

## Published language (Domain layer)

The shared kernel's Domain layer is the vocabulary other contexts speak:

- **Shared aggregate Ids** — the typed identity of a concept that crosses
  contexts (`OrderId`, `CustomerId`, `PropertyId`) lives here and is imported by
  every context that references it. Never mint your own id for a concept that
  already has one here.
- **`AbstractIntId`** (in `Share/Model/`) — the base for integer-backed ids,
  providing `from(int)`, `new()`, `equals()`, `getValue()`, `fromArray()`,
  `toArray()`. Concrete ids extend it.
- **`EventRecord`** — each aggregate module defines an `<Aggregate>EventRecord`
  (e.g. `OrderEventRecord`), a lightweight `readonly` snapshot of aggregate state
  embedded inside domain events. Never pass a whole aggregate into an event.
- **`EventInterface`** — each module defines an `<Aggregate>EventInterface` that
  all of its events implement; listeners type-hint against the interface, not the
  concrete event.

```php
namespace App\Previo\Domain\Order;

final class OrderId extends AbstractIntId {}                 // shared identity

interface OrderEventInterface extends EventInterface {}      // listeners hint this

final class OrderCreated implements OrderEventInterface
{
    public function __construct(public readonly OrderEventRecord $order) {} // snapshot, not aggregate
}
```

## Contracts (Application layer)

Contracts are the **only** way one context may consume another. They live in
`contexts/previo/src/Application/` and exist in two shapes used side by side:

- **`<X>QueryContract`** — what a query service can do (read methods returning
  `<X>Contract` objects), e.g. `OrdersQueryContract`, `CustomerQueryContract`.
- **`<X>Contract`** — the getter interface of a single result DTO returned by a
  query, e.g. `OrderContract`, `CustomerContract`.

Rules that hold across contexts:

- A `<X>QueryContract` always returns `<X>Contract` objects, never concrete
  classes.
- The producing context's concrete DTO (`App\Sales\Application\Order\Order`)
  *implements* the kernel `OrderContract`; its query service implements
  `OrdersQueryContract`.
- A consumer injects the **contract interface** into its Application layer — it
  never names the producing context's namespace, and never injects a foreign
  concrete service or DTO (this is what PHP-ARCH-008/009 enforce).

```php
// consumer context — depends ONLY on the shared contract
public function __construct(private OrdersQueryContract $orders) {}

$order = $this->orders->byId($orderId);   // returns an OrderContract, not a concrete class
```

A command-side `<X>Contract` (a write service callable cross-context, e.g.
`OrdersContract::checkIn()`) exists too, but is used sparingly.

## Share layer

`contexts/previo/src/Share/` holds cross-cutting base classes and value objects
reused everywhere — always prefer these over raw primitives:

- **`Share/Model/AbstractIntId`** — base for integer aggregate ids (above).
- **`Share/Query/AbstractQuery`** with **`Paging`** and **`Ordering`** — the
  filter/paging/ordering objects passed to `<X>QueryRepositoryInterface`.
- **`Share/DataType/`** — shared value objects: `Money`, `Email`, `PhoneNumber`,
  `DateRange`, `UserString`, … Use these instead of scalars.
- **`Share/Command/`** — `CommandInterface`, `ResultInterface`, `Result`,
  `AbstractResult`: the base types for commands and results crossing the command
  bus.

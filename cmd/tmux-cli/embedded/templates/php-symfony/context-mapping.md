# PHP/Symfony Context Mapping: {{project_name}}

> Extends: `_base/context-mapping.md` — translation strategies, boundary error handling, event choreography, consistency patterns.
> This template adds Messenger-based ACL implementation, transport configuration, and Published Language event patterns.

## Messenger ACL Adapter (Infrastructure Layer)

The ACL adapter implements the interface defined in service-contracts.md and uses Symfony Messenger to dispatch queries to the upstream BC.

```
File: src/{{bc_name}}/Infrastructure/ACL/{{external_bc_name}}ACLAdapter.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Infrastructure\ACL;

use App\{{bc_name}}\Domain\ACL\{{external_bc_name}}ACLInterface;
use App\{{bc_name}}\Domain\Model\{{foreign_id_vo}};
use App\{{bc_name}}\Domain\Model\{{own_bc_dto}};
use Symfony\Component\Messenger\MessageBusInterface;
use Symfony\Component\Messenger\Stamp\HandledStamp;

final class {{external_bc_name}}ACLAdapter implements {{external_bc_name}}ACLInterface
{
    public function __construct(
        private readonly MessageBusInterface $queryBus,
    ) {}

    public function {{method_name}}({{foreign_id_vo}} $id): ?{{own_bc_dto}}
    {
        $envelope = $this->queryBus->dispatch(
            new Fetch{{external_bc_name}}Query($id)
        );

        $handledStamp = $envelope->last(HandledStamp::class);

        if ($handledStamp === null) {
            return null;
        }

        $result = $handledStamp->getResult();

        return $result !== null
            ? {{own_bc_dto}}::fromACLResult($result)
            : null;
    }
}
```

The adapter translates external data into the consuming BC's own domain model — no upstream types leak past this boundary.

## Published Language Event DTO

Published Language events live in the Share namespace and serve as the sole cross-BC data contract. They are immutable value objects with no behavior.

```
File: src/Share/Event/{{event_name}}.php
```

```php
<?php

declare(strict_types=1);

namespace App\Share\Event;

final readonly class {{event_name}}
{
    public function __construct(
        public string $aggregateId,
        public string $occurredAt,
        public array $payload,
    ) {}
}
```

Rules:
- One class per cross-BC event, named after the domain fact (e.g. `ReservationConfirmed`, `GuestCheckedIn`)
- Constructor-promoted `readonly` properties only (PHP 8.2+)
- No methods beyond the constructor — consuming BCs interpret payload via their own ACL
- Lives in `src/Share/Event/`, never inside a BC namespace

## Messenger Event Handler

Handlers receive Published Language events and translate them into the consuming BC's domain commands via the ACL adapter.

```
File: src/{{bc_name}}/Infrastructure/Adapter/{{event_name}}Handler.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Infrastructure\Adapter;

use App\Share\Event\{{event_name}};
use App\{{bc_name}}\Domain\ACL\{{external_bc_name}}ACLInterface;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;

#[AsMessageHandler]
final class {{event_name}}Handler
{
    public function __construct(
        private readonly {{external_bc_name}}ACLInterface $acl,
    ) {}

    public function __invoke({{event_name}} $event): void
    {
        $translated = $this->acl->{{method_name}}(
            new \App\{{bc_name}}\Domain\Model\{{foreign_id_vo}}($event->aggregateId)
        );

        if ($translated === null) {
            return;
        }

        // Dispatch domain command or call domain service with translated data
    }
}
```

Rules:
- One handler per Published Language event per consuming BC
- Handler injects the ACL interface (not the adapter directly) — constructor injection per service-contracts.md
- Handler lives in `Infrastructure/Adapter/` (not `Infrastructure/ACL/`) — adapters and Messenger handlers share this location
- No direct imports from other BCs — only `App\Share\Event\` and own BC namespaces

## Messenger Transport Configuration

```
File: config/packages/messenger.yaml
```

```yaml
framework:
    messenger:
        transports:
            async:
                dsn: 'doctrine://default'
                options:
                    queue_name: '{{bc_name}}'
                retry_strategy:
                    max_retries: 3
                    delay: 1000
                    multiplier: 2
            # For production with dedicated message broker:
            # async:
            #     dsn: '%env(MESSENGER_TRANSPORT_DSN)%'
            #     options:
            #         exchange:
            #             name: '{{project_name}}'
            #             type: topic

        routing:
            'App\Share\Event\{{event_name}}': async
```

Notes:
- Default transport uses `doctrine://default` for simplicity — switch to AMQP (`amqp://guest:guest@localhost:5672/%2f`) for production workloads
- Each BC should define its own queue name to enable independent consumer scaling
- Route Published Language events (from `App\Share\Event\`) to async transport — never handle cross-BC events synchronously
- Retry strategy with exponential backoff handles transient failures

## Message Routing Pattern

Cross-BC communication follows this flow:

```
Source BC                    Share Namespace               Consuming BC
─────────                    ──────────────               ─────────────
Domain Event                 Published Language            Handler + ACL
(internal)                   Event (contract)              (translation)

AggregateRoot       ──emit──▸ {{event_name}}      ──route──▸ {{event_name}}Handler
  raises domain               (Share/Event/)                (Infrastructure/Adapter/)
  event internally            immutable DTO                 receives event
                              on Messenger                  │
                              transport                     ▼
                                                    {{external_bc_name}}ACLInterface
                                                            (Domain/ACL/)
                                                            translates to own model
                                                            │
                                                            ▼
                                                    Domain Command / Service
                                                            (own BC only)
```

Key constraints:
- Source BC dispatches only to Share namespace events — never directly to another BC's handler
- Consuming BC handler depends on ACL interface (Domain layer), not ACL adapter (Infrastructure)
- No synchronous cross-BC calls — all communication is async via Messenger transport
- Each BC can evolve its internal domain model independently; the Published Language event is the stable contract

## Cross-References

| Concern | Document | Section |
|---------|----------|---------|
| ACL interface definition | service-contracts.md | ACL Interface (Domain Layer) |
| ACL adapter path convention | service-contracts.md | Infrastructure implementation location |
| Share namespace structure | bounded-contexts.md | Share Namespace Directory Tree |
| Deptrac layer enforcement | bounded-contexts.md | Deptrac Configuration |
| Cross-BC dependency rule | bounded-contexts.md | Layer dependency rules |
| Abstract translation strategies | _base/context-mapping.md | Translation Strategies |
| Event choreography patterns | _base/context-mapping.md | Event Flow Choreography |
| Consistency patterns | _base/context-mapping.md | Data Consistency Patterns |

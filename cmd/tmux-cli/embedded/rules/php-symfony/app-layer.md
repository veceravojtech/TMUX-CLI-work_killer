# App layer (pack: php-symfony)

The `app/` folder of a context (`contexts/<bc>/app`) is the **framework
entry-point layer**. It wires HTTP, CLI, and messaging to the context's
Application layer. It is architecturally identical to `projects/<app>/src` — both
are thin framework layers that delegate into Application handlers and own no
business logic. Namespace: `App\<Bc>App\<Module>\...`.

## What lives here

The app layer uses a **feature-based** folder structure: each root folder is a
module (`Order/`, `Billing/`, …). Inside, it may contain only framework-bound
adapters:

- **Controllers** — Symfony controllers (extending `AbstractController`) that
  parse the HTTP request, build a Command/Query, hand it to an Application
  handler or query service, and map the result/DTO to a response. No business
  branching.
- **CLI commands** — `#[AsCommand]` console commands (typically `<X>CliCommand`)
  for batch jobs and maintenance, delegating to handlers exactly like a
  controller.
- **Message processors / event listeners** — consume domain events or
  integration messages off the bus and call Application handlers.
- **Adapters** — integrations with external systems implementing an interface
  declared in the context's Application or Bundle layer.
- **`Application/Security/`** — the `User` / `UserBuilder` that bridge the
  framework's security component to the context.

The app layer is the **only** layer permitted to use Symfony framework
attributes (`#[Route]`, `#[AsCommand]`, `#[AsMessageProcessor]`, …). Domain,
Application, and Bundle stay framework-free.

## Delegation, not logic

A controller's job is translation: HTTP in → Command/Query → handler → response.
The decision lives in the handler (the atomic `handle()` with its actor ACL check
— see context-layers.md), never in the controller.

```php
#[Route('/orders/{id}/lock', methods: ['POST'])]
final class LockOrderController extends AbstractController
{
    public function __construct(private readonly CommandBus $bus) {}

    public function __invoke(string $id, #[CurrentUser] User $actor): JsonResponse
    {
        // build the command and delegate — no business rules here
        $result = $this->bus->handle(new LockOrder($actor->id(), OrderId::from((int) $id)));

        return $this->json(['status' => $result->isOk() ? 'locked' : 'rejected']);
    }
}
```

## Cross-context and cross-layer boundaries

- The app layer calls **only** its own context's Application layer
  (`contexts/<bc>/src/Application`) — never another context's `src` directly.
- Cross-context needs go through a shared-kernel contract from
  `contexts/previo/src` (see shared-kernel.md), the same as any other layer.
- A project module root (`projects/<app>/src/<Module>/`) holds only
  `*Controller.php`; each REST resource gets its own thin controller, and that
  resource's Request/Response/Command DTOs live in a matching subfolder (this is
  what PHP-ARCH-006 enforces).

## Tests

App-layer code is tested under `contexts/<bc>/app/tests`: `tests/Application/`
for lightweight unit-style tests of processors/adapters/controllers without
booting the kernel, and `tests/Integration/` for kernel-booting tests of the app
code. Context **library** code (`contexts/<bc>/src`) is tested separately under
`contexts/<bc>/tests/` — keep the two suites distinct, and reuse the shared test
mother objects from `contexts/<bc>/tests/Resources/`.

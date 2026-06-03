# PHP/Symfony Error Handling: {{project_name}}

Extends `_base/error-handling.md` with a concrete Symfony exception listener, domain-to-HTTP mappings, and service wiring.

## ApiExceptionListener

`src/{{bc_name}}/Infrastructure/Http/ApiExceptionListener.php`:

```php
<?php
declare(strict_types=1);

namespace App\{{bc_name}}\Infrastructure\Http;

use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpKernel\Event\ExceptionEvent;
use Symfony\Component\HttpKernel\Exception\HttpExceptionInterface;
use Symfony\Component\HttpKernel\KernelEvents;
final class ApiExceptionListener implements EventSubscriberInterface
{
    public static function getSubscribedEvents(): array
    {
        return [KernelEvents::EXCEPTION => 'onKernelException'];
    }
    public function onKernelException(ExceptionEvent $event): void
    {
        $e = $event->getThrowable();
        if ($e instanceof HttpExceptionInterface) {
            return;
        }
        [$status, $type, $title] = $this->mapException($e);
        $payload = ['type' => $type, 'title' => $title, 'status' => $status,
            'detail' => $status === 500 ? 'An unexpected error occurred.' : $e->getMessage()];
        if ($e instanceof ValidationException) {
            $payload['violations'] = $this->normalizeValidation($e);
        }
        $event->setResponse(new JsonResponse($payload, $status, ['Content-Type' => 'application/problem+json']));
    }

    private function mapException(\Throwable $e): array
    {
        return match (true) {
            $e instanceof ValidationException => [422, '{{error_type_uri_prefix}}/validation-failed', 'Validation Failed'],
            $e instanceof EntityNotFoundException => [404, '{{error_type_uri_prefix}}/not-found', 'Not Found'],
            $e instanceof AccessDeniedException => [403, '{{error_type_uri_prefix}}/access-denied', 'Access Denied'],
            $e instanceof DomainException => [422, '{{error_type_uri_prefix}}/domain-error', 'Domain Error'],
            default => [500, '{{error_type_uri_prefix}}/internal-error', 'Internal Server Error'],
        };
    }
    private function normalizeValidation(ValidationException $e): array
    {
        $violations = [];
        foreach ($e->getViolations() as $v) {
            $violations[] = ['field' => $v->getPropertyPath(), 'message' => $v->getMessage(), 'code' => $v->getCode() ?? ''];
        }
        return $violations;
    }
}
```

## Domain Exception Mapping

| Exception | Status | Type URI |
|-----------|-------:|----------|
| `ValidationException` | 422 | `{{error_type_uri_prefix}}/validation-failed` |
| `EntityNotFoundException` | 404 | `{{error_type_uri_prefix}}/not-found` |
| `AccessDeniedException` | 403 | `{{error_type_uri_prefix}}/access-denied` |
| `DomainException` | 422 | `{{error_type_uri_prefix}}/domain-error` |
| Unexpected | 500 | `{{error_type_uri_prefix}}/internal-error` |

Match order matters: `ValidationException` before `DomainException` (it typically extends it). Symfony `HttpExceptionInterface` passes through to the framework. 500 responses use generic detail — no stack traces, class names, or SQL.

## Symfony Service Configuration
```yaml
services:
    App\{{bc_name}}\Infrastructure\Http\ApiExceptionListener:
        autoconfigure: true
```
`autoconfigure: true` auto-tags as `kernel.event_subscriber` via `EventSubscriberInterface`.

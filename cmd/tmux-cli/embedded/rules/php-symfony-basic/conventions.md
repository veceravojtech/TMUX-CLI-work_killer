# Basic Symfony app (pack: php-symfony-basic)

Binding planning conventions for a **flat, single-package Symfony application**
(architecture=basic) — the profile for an app that deliberately does NOT adopt
the DDD monorepo topology. These conventions carry the practices that transfer
from the full-grade DDD profile; the php and php-symfony-common code-rule packs
(types, controllers, dates, naming, tests, debug hygiene, PHPStan/ECS gates,
persistence hygiene, env/config, i18n) load alongside and enforce the
point-wise rules. Namespace root is the project's vendor namespace (shown as
`App\`; substitute the discovered vendor).

## Layout

```
src/Controller/<Area>/      thin HTTP controllers (one per resource)
src/Entity/                 Doctrine entities (attribute mapping is acceptable here)
src/Repository/             one repository per entity, query methods intent-named
src/Service/<Area>/         application services — ALL business logic lives here
src/Form/                   form types + request DTOs
src/Security/               voters, authenticators, the user provider
src/Twig/                   twig extensions (presentation helpers only)
config/                     framework + package config; env vars declared in .env
migrations/                 doctrine migrations — the only schema-change path
templates/                  twig templates (no business logic — PHP-ARCH-001)
tests/                      mirrors src/ (PHP-TEST-005)
```

A class's namespace mirrors its path under the resolved root (PHP-ARCH-010).
The profile divergence from the DDD pack: attribute mapping on entities and a
single flat package are ACCEPTED here — there is no Bundle/infra layer split
and no shared-kernel context. Everything else transfers.

<rule critical="true" id="SF-BASIC-SERVICES">BUSINESS LOGIC LIVES IN SERVICES.
Controllers translate HTTP to a typed call and back — parse/validate input into
a typed value (form, DTO, or value object), call ONE service method, map the
result to a response. No entity construction, no persistence calls, no business
branching in controllers (PHP-CTRL-003 enforces the persistence half). Services
are constructor-injected, final, and stateless; one service owns one area of
behaviour.</rule>

<rule critical="true" id="SF-BASIC-VALIDATION">VALIDATE AT THE BOUNDARY. All
external input (request, CLI arg, message payload) is coerced into typed values
via the Validator/Form component BEFORE it reaches a service — services assume
well-typed input and never re-parse raw request data. Malformed input yields a
4xx, never a 500.</rule>

<rule critical="true" id="SF-BASIC-AUTHZ">AUTHORIZATION VIA VOTERS. Every
non-public action asserts the actor's permission for THIS operation — a Symfony
voter (`#[IsGranted(...)]` or `denyAccessUnlessGranted`) on the controller plus
ownership scoping in the service query (fetch by id AND owner, never fetch-then-
trust). An ownership check alone is not authorization; the role/permission for
the operation must be asserted. CSRF protection stays on for every state-changing
browser endpoint; SQL goes through the ORM/DBAL with bound parameters only.</rule>

<rule critical="true" id="SF-BASIC-MIGRATIONS">SCHEMA CHANGES ONLY VIA
MIGRATIONS. Every schema change is a generated + reviewed Doctrine migration
(final class, strict types, reversible where possible — PHP-MIG-001);
`doctrine:schema:validate` stays green. Never `schema:update --force`.</rule>

<rule id="SF-BASIC-GATES">ONE ENTRYPOINT PER QUALITY GATE. The project exposes
composer scripts for its gates — illustrative: `composer app:phpstan` (PHPStan at
the configured max level), `composer app:cs-check` / `app:cs-fix` (ECS),
`composer app:test` (PHPUnit unit + functional suites) — and CI runs exactly
those entrypoints, so a local run and CI can never diverge (PHP-STAN-001 /
PHP-STYLE-002 bind the diff to them).</rule>

# Security conventions (pack: php-symfony)

Binding planning conventions distilled from a production Symfony DDD monorepo's
security handbook. Each `<rule>` below is BINDING on
every later planning and implementation step for backend code, and on review.
These hold IN ADDITION to ownership coverage — never as a substitute for it.

<rule critical="true" id="SEC-AUTHZ">AUTHORIZATION BEYOND OWNERSHIP. Every Command/Query use case must verify the actor is *authorized for the action*, not merely that the record exists or is owned. The actor ACL check is the FIRST statement of every `handle()` (Application layer), e.g. `$this->acl->ensureAccessToProperty($command->actor, $command->propertyId)` — before any load or state change (see context-layers.md). Controllers (app layer) NEVER make the authorization decision; they pass the authenticated actor into the command and delegate. Read-side query services apply the same actor scoping. An ownership check alone (record belongs to user) is NOT authorization — the actor's role/permission for THIS operation must be asserted.</rule>

<rule critical="true" id="SEC-INPUT">INPUT VALIDATION AT THE BOUNDARY. All external input (HTTP request, CLI arg, message payload) is untrusted. Validate and coerce it into typed domain values at the app-layer boundary BEFORE it reaches a handler — typed ids (`OrderId::from(...)`), value objects, and `readonly` DTOs, never raw scalars passed downstream. Reject malformed input with a 4xx/validation error, never a 500. Domain/Application code may assume inputs are already well-typed; the boundary is the only place that parses untrusted data.</rule>

<rule critical="true" id="SEC-INJECTION">INJECTION & XSS/CSRF DEFENSE. SQL: only the Bundle (infrastructure) layer touches the database, and it MUST use parameterized queries / the DBAL query builder / Doctrine bound parameters — never string-concatenated SQL or interpolated identifiers from user input. XSS: template output is auto-escaped (Twig) or framework-encoded; never emit `|raw` / unescaped user data and never build HTML by concatenation. CSRF: all state-changing browser-facing endpoints require the framework's CSRF protection (stateless API tokens excepted). Any deviation must be justified in the spec and is a review blocker.</rule>

<rule critical="true" id="SEC-SECRETS">SECRET HANDLING. Secrets (credentials, API keys, tokens, signing keys) come from environment/secret store config, NEVER hardcoded in source, fixtures, or committed config, and are NEVER logged, echoed in errors, or returned in responses/DTOs. Do not place secrets in `.env` files that are committed; reference them via the framework's secrets/parameter mechanism. Generated code and tests must use placeholders or injected test doubles, never real secret values.</rule>

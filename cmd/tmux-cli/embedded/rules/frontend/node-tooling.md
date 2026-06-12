# Node tooling convention (pack: frontend — HAS_FRONTEND)

Binding planning convention, loaded when the project has a frontend /
Playwright toolchain. Extracted verbatim from the planner's `<conventions>`
block. (The rule governs WHETHER Node commands may be emitted at all, so it
loads with the frontend pack but its body also covers the API-only negative
case.)

<rule critical="true" id="NODE-TOOL-CONV">NODE-TOOLING CONVENTION — BINDING (governs WHETHER a Node command is emitted; the daemon wraps a Node command into {{NODE_SVC}} but never REMOVES one). A Node-runtime tool (node, npm, npx, playwright, swagger-cli) may be emitted ONLY when test-environment.md indicates a frontend/Playwright toolchain (HAS_FRONTEND==true or Playwright installed) — then task-R provisions {{NODE_SVC}} and the daemon routes the command there. In a PHP project that is API-only / Playwright-N/A, NEVER emit a Node-tool command — use the PHP-native equivalent (e.g. validate OpenAPI via `bin/console nelmio:apidoc:dump`, not `npx swagger-cli validate`; never emit `npx playwright` — see the steps 3.18/3.19 guards). The Node-runtime requirement, when present, is recorded in the goal-002 deliverables.</rule>

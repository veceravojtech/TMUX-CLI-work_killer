# Discovery Questions — PHP/Symfony Supplement

<!-- INSTRUCTIONS FOR CLAUDE:
     This template supplements `_base/discovery.md`. Load it after section 1
     (Language Detection) confirms PHP/Symfony. Ask these questions IN ADDITION
     TO (not instead of) the base discovery questions.
     Sub-section numbers (1a, 1b, etc.) reference the parent base section they
     extend. Record answers in the {{placeholder}} positions using snake_case
     mustache variable syntax. -->

---

## 1a. Symfony Framework Detection
<!-- Extends base section 1 — Language Detection -->
<!-- PURPOSE: Identify exact Symfony version, Flex usage, and environment mode
     so downstream templates can adapt to framework capabilities. -->

1. Scan `composer.json` and `symfony.lock` for the Symfony framework version:
   - Symfony version: {{symfony_version}}

2. Is Symfony Flex installed (`symfony/flex` in require or require-dev)?
   - Flex enabled: {{symfony_flex}}

3. What environment mode is configured in `.env` or `.env.local`?
   - APP_ENV value: {{symfony_env_mode}}

---

## 1b. Composer Package Analysis
<!-- Extends base section 1 — Language Detection -->
<!-- PURPOSE: Identify key Symfony ecosystem packages to determine which
     framework features are in use and which follow-up questions apply. -->

1. Scan `composer.json` for key Symfony and ecosystem packages (e.g., `doctrine/orm`, `symfony/messenger`, `symfony/security-bundle`, `api-platform/core`, `symfony/mailer`). List all significant packages found:
   > {{composer_key_packages}}

2. What PSR-4 autoload namespaces are declared in `composer.json`?
   > {{psr4_namespaces}}

3. Are there custom Composer scripts defined (e.g., `auto-scripts`, `post-install-cmd`)?
   > {{composer_scripts}}

4. **Source layout (code-rule routing).** Where does source code actually live,
   and what is the infrastructure-layer directory called? The code-rules engine
   resolves `{src}`/`{infra}` globs from this (see `.tmux-cli/rules/SCHEMA.md`).
   Derive the source roots from the PSR-4 dirs above when possible; **ASK** when
   the topology is a monorepo the root `composer.json` can't express (per-context
   `contexts/*/src`, framework `contexts/*/app/src`, `projects/*/src`,
   `packages/*/src`) or the infra layer is not the default `Infrastructure`.
   Record the answer in `docs/architecture/layout.md` under a `## Layers` section
   with exactly these two lines (authoritative over PSR-4 detection):

   ```markdown
   ## Layers

   - Source roots: {{layout_source_roots}}
   - Infrastructure layer: {{layout_infra_layer}}
   ```

   - Source roots (comma-separated globs): {{layout_source_roots}}
   - Infrastructure layer directory: {{layout_infra_layer}}

---

## 1c. PHP Extension Requirements
<!-- Extends base section 1 — Language Detection -->
<!-- PURPOSE: Capture PHP runtime requirements so infrastructure and CI
     environments can be configured correctly. -->

1. What PHP version constraint is specified in `composer.json` `require.php`?
   - PHP version constraint: {{php_version_constraint}}

2. Scan `composer.json` for `ext-*` entries in `require` and `require-dev`. List all required PHP extensions:
   > {{required_extensions}}

3. Are there any runtime PHP extensions used in production that are NOT declared in `composer.json`? (e.g., `ext-apcu`, `ext-redis`, `ext-opcache`)
   > {{runtime_extensions}}

---

## 3a. Symfony Bundle Detection
<!-- Extends base section 3 — Bounded Contexts -->
<!-- PURPOSE: Map bounded contexts to their Symfony bundle structure and identify
     third-party bundles that shape the domain implementation. -->

1. For each bounded context, what Symfony bundles correspond to it? Check `config/bundles.php` and `src/` directory structure:

| Bounded Context | Bundle(s) | Location |
|-----------------|-----------|----------|
| {{bc_name}} | {{bc_bundles}} | {{bundle_location}} |

2. What third-party bundles are registered in `config/bundles.php`? (e.g., `StofDoctrineExtensionsBundle`, `NelmioApiDocBundle`, `LexikJWTAuthenticationBundle`)
   > {{third_party_bundles}}

3. Are there any custom bundles defined within the project (under `src/`)?
   > {{custom_bundles}}

---

## 4a. Doctrine ORM/DBAL Configuration
<!-- Extends base section 4 — Aggregate Roots and Domain Events -->
<!-- PURPOSE: Capture Doctrine-specific persistence decisions that affect how
     aggregates, value objects, and domain events are stored and mapped. -->

1. What mapping strategy does Doctrine use for entities? (PHP attributes, annotations, XML, YAML)
   - Mapping strategy: {{doctrine_mapping_strategy}}

2. Are there custom DBAL types registered for value objects? (e.g., custom types for Money, Email, UUID)
   > {{custom_dbal_types}}

3. What is the Doctrine Migrations configuration? Check `config/packages/doctrine_migrations.yaml`:
   - Migrations namespace: {{doctrine_migrations_config}}

4. How many entity managers and DBAL connections are configured? Is there a read/write split or separate managers per bounded context?
   - Entity manager count: {{entity_manager_count}}

---

## 4b. Aggregate Implementation Pattern (aggregate triad)
<!-- Extends base section 4 — Aggregate Roots and Domain Events -->
<!-- PURPOSE: Capture HOW aggregates are implemented (not just which exist) so
     domain-model.md can describe each aggregate as a triad and downstream
     generation emits aggregate-triad specs instead of classic aggregates.
     Default to the aggregate triad for the php-symfony monorepo pack and RECORD
     the decision so the user can confirm or override. -->

1. DAO/DTO triad — does each aggregate use the root + a mutable `<X>Data` DAO (`#[AggregateDAO]`) + a readonly `<X>DTO`? (monorepo default: YES — root holds no scalars directly; state lives in `<X>Data`, reads project to `<X>DTO`.)
   - Triad pattern: {{aggregate_triad_pattern}}

2. Event payloads — do domain events carry `<X>EventRecord` snapshots implementing a per-aggregate `<Aggregate>EventInterface` (rather than scalar/flat events)? (monorepo default: YES.)
   - EventRecord pattern: {{event_record_pattern}}

3. Shared Id placement — where do shared aggregate Id value objects live? (monorepo default: `shared/src/Domain/<Module>` — i.e. `contexts/shared/src/Domain/<Module>` — not a flat per-BC `DataType/`.)
   - Shared aggregate Id placement: {{shared_aggregate_id_placement}}

4. Record the decision so it can be confirmed or overridden by the user (default: `aggregate triad`):
   - Aggregate pattern decision: {{aggregate_pattern_decision}}

---

## 6a. Symfony Architecture Decisions
<!-- Extends base section 6 — Architecture Decisions -->
<!-- PURPOSE: Capture Symfony-specific architectural patterns that affect code
     organisation, layer enforcement, and message bus design. -->

1. Is Deptrac (or another layer enforcement tool) configured? If so, what layers and rules are defined?
   > {{deptrac_config}}

2. How are CQRS buses separated? (single bus, separate command/query/event buses, custom middleware)
   - Bus separation strategy: {{bus_separation}}

3. What is the domain event dispatch strategy? (dispatch after flush, dispatch before flush, manual dispatch, Doctrine lifecycle events)
   - Event dispatch strategy: {{event_dispatch_strategy}}

---

## 10a. Symfony Security Configuration
<!-- Extends base section 10 — Auth Flows -->
<!-- PURPOSE: Capture Symfony Security component specifics — firewall config,
     voter/access decision strategy, and guard authenticator setup. -->

1. What firewalls are configured in `config/packages/security.yaml`? List each firewall and its authentication method:

| Firewall | Pattern | Authenticator |
|----------|---------|---------------|
| {{firewall_name}} | {{firewall_pattern}} | {{firewall_authenticator}} |

2. Are custom voters used for authorization decisions? List any registered voters:
   > {{security_voters}}

3. What access decision strategy is configured? (affirmative, consensus, unanimous)
   - Access decision strategy: {{access_decision_strategy}}

---

## 11a. Messenger Transport Configuration
<!-- Extends base section 11 — Event Listeners -->
<!-- PURPOSE: Map the async messaging infrastructure so message routing, failure
     handling, and retry behaviour are documented explicitly. -->

1. What Messenger transports are configured? Check `config/packages/messenger.yaml`:

| Transport | DSN Type | Purpose |
|-----------|----------|---------|
| {{transport_name}} | {{messenger_transports}} | {{transport_purpose}} |

2. How are message types routed to transports?

| Message Class | Target Transport |
|---------------|-----------------|
| {{message_class}} | {{message_routing}} |

3. What retry strategy is configured for failed messages? (fixed delay, multiplier, max retries)
   > {{retry_strategy}}

4. How are permanently failed messages handled? (dead-letter queue, logging, manual review)
   > {{dead_letter_handling}}

---

## 14a. Symfony Test Environment
<!-- Extends base section 14 — Test Environment -->
<!-- PURPOSE: Capture Symfony-specific testing infrastructure so generated tests
     use the correct base classes, database strategy, and configuration. -->

1. What PHPUnit configuration is in use? Check for `phpunit.xml.dist` or `phpunit.xml`:
   > {{phpunit_config}}

2. What database strategy is used for tests? (separate test database, SQLite in-memory, transaction rollback per test, fixtures)
   - Test database strategy: {{test_db_strategy}}

3. What Symfony test base classes are used? (e.g., `KernelTestCase`, `WebTestCase`, `ApiTestCase`, custom base class)
   > {{symfony_test_patterns}}

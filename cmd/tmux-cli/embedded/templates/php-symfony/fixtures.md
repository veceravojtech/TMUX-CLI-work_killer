# PHP/Symfony Fixture Patterns: {{bc_name}}

This template extends `_base/fixtures.md` with Doctrine Fixtures Bundle class structure, dependency ordering, and CLI integration. Requires `composer require --no-interaction --dev doctrine/doctrine-fixtures-bundle`.

## Fixture Class with Dependencies and References

File: `src/{{bc_name}}/Infrastructure/DataFixtures/{{entity_name}}Fixtures.php`

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Infrastructure\DataFixtures;

use Doctrine\Bundle\FixturesBundle\Fixture;
use Doctrine\Common\DataFixtures\DependentFixtureInterface;
use Doctrine\Persistence\ObjectManager;
use Symfony\Component\DependencyInjection\Attribute\AsFixture;

#[AsFixture(group: 'test')]
final class {{entity_name}}Fixtures extends Fixture implements DependentFixtureInterface
{
    public const REF_DEFAULT = '{{entity_name}}-default';

    public function load(ObjectManager $manager): void
    {
        for ($i = 0; $i < {{pagination_page_size}} + 1; $i++) {
            $entity = {{entity_name}}::create(/* ... */);
            $manager->persist($entity);
            if ($i === 0) {
                $this->addReference(self::REF_DEFAULT, $entity);
            }
        }
        $manager->flush();
    }

    public function getDependencies(): array
    {
        return [/* dependent fixture classes */];
    }
}
```

- Cross-fixture lookup: `$this->getReference({{entity_name}}Fixtures::REF_DEFAULT, {{entity_name}}::class)`
- Data volume: create `{{pagination_page_size}} + 1` records per entity to verify pagination; include varied attribute values for filter/search testing

## Test User Fixture

File: `src/{{bc_name}}/Infrastructure/DataFixtures/UserFixtures.php`

Follows the same class pattern. Inject `UserPasswordHasherInterface` via constructor to hash passwords. Create one fixture user per role required by E2E tests. Credential values from `docs/architecture/test-environment.md` — never hardcode usernames or passwords in the fixture class.

## Idempotency

- Default: `--purge-with-truncate` clears tables before loading (fast, may fail with FK constraints)
- Alternative: `--purge-with-delete` respects FK ordering (slower, always safe)
- Append mode: `$manager->getRepository({{entity_name}}::class)->findOneBy(...)` to skip existing

## E2E Data Isolation

Fixtures are read-only reference data for E2E specs. Specs create their own mutable test data via the API, never by mutating fixture rows.

- **Unique keys**: use `uniqid('test-')` or `Uuid::v4()->toRfc4122()` suffix on entity names/identifiers to avoid collision across specs
- **Filtered assertions**: use API query parameters or repository `findBy(['name' => $uniqueName])` to scope list/count checks to own data
- **WARNING**: `dama/doctrine-test-bundle` wraps each test in a transaction rollback — this does NOT work for E2E/Playwright tests where the HTTP server runs in a separate process with its own DB connection. Do NOT configure it for E2E test suites.
- **No mid-suite reload**: `doctrine:fixtures:load -n` purges and reloads — calling it between specs destroys all spec-created data. The load runs once in ensure-stack (phase 3) before the suite starts.

## CLI Integration

```bash
bin/console doctrine:fixtures:load --env=test
bin/console doctrine:fixtures:load --env=test --group=test
```

## Ensure-stack script

File: `bin/ensure-test-stack.sh`

The ensure-test-stack.sh script guarantees the runtime stack is up, migrated, and fixture-loaded before any E2E or host-HTTP probe runs. This script assumes APP_ENV=test is pinned by the compose spec or .env.test (E2E-ENV-CONV). Three phases, executed in order:

```bash
#!/bin/sh -e

# Phase 1: Stack up
docker compose up -d

# Phase 2: Test-env migrations
bin/console doctrine:migrations:migrate -n --env=test

# Phase 3: Test fixtures
bin/console doctrine:fixtures:load -n --env=test
```

- The script MUST be executable (`chmod +x bin/ensure-test-stack.sh`)
- Each phase runs independently — if any fails, the script exits non-zero immediately (`set -e`)
- In docker mode, the daemon wraps `bin/console` commands into the app container (CMD-CONV) — emit bare host-style commands
- In local mode without docker, omit the `docker compose up -d` phase (the stack is already the host)
- The generator references this template for the Symfony-specific script body when HAS_DATABASE is true

# PHP/Symfony Fixture Patterns: {{bc_name}}

This template extends `_base/fixtures.md` with Doctrine Fixtures Bundle class structure, dependency ordering, and CLI integration. Requires `composer require --dev doctrine/doctrine-fixtures-bundle`.

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

## CLI Integration

```bash
bin/console doctrine:fixtures:load --env=test
bin/console doctrine:fixtures:load --env=test --group=test
```

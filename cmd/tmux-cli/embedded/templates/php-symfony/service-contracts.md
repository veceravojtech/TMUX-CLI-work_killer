# PHP/Symfony Service Contract Patterns: {{bc_name}}

This template extends `_base/service-contracts.md` with concrete PHP interface syntax, file path conventions, and Symfony autowiring configuration.

## Write Repository Interface (Domain Layer)

```
File: src/{{bc_name}}/Domain/Repository/{{aggregate_name}}RepositoryInterface.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Domain\Repository;

use App\{{bc_name}}\Domain\Model\{{aggregate_name}};
use App\{{bc_name}}\Domain\Model\{{aggregate_name}}Id;

interface {{aggregate_name}}RepositoryInterface
{
    public function save({{aggregate_name}} ${{aggregate_var}}): void;

    public function remove({{aggregate_name}} ${{aggregate_var}}): void;

    public function ofId({{aggregate_name}}Id $id): ?{{aggregate_name}};
}
```

Infrastructure implementation location:
```
src/{{bc_name}}/Infrastructure/Persistence/Doctrine/Doctrine{{aggregate_name}}Repository.php
```

## Read Model Interface (Application Layer)

```
File: src/{{bc_name}}/Application/ReadModel/{{read_model_name}}ReadModelInterface.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Application\ReadModel;

interface {{read_model_name}}ReadModelInterface
{
    /** @return {{read_model_name}}DTO[] */
    public function findByCriteria({{read_model_name}}Criteria $criteria): array;

    public function ofId(string $id): ?{{read_model_name}}DTO;
}
```

DTO defined alongside interface:
```
src/{{bc_name}}/Application/ReadModel/{{read_model_name}}DTO.php
src/{{bc_name}}/Application/ReadModel/{{read_model_name}}Criteria.php
```

Infrastructure implementation location:
```
src/{{bc_name}}/Infrastructure/Persistence/Doctrine/Doctrine{{read_model_name}}ReadModel.php
```

## Domain Service Interface (Domain Layer)

```
File: src/{{bc_name}}/Domain/Service/{{domain_service_name}}Interface.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Domain\Service;

interface {{domain_service_name}}Interface
{
    public function {{method_name}}({{input_type}} $input): {{return_type}};
}
```

## ACL Interface (Domain Layer — Consuming BC)

```
File: src/{{bc_name}}/Domain/ACL/{{external_bc_name}}ACLInterface.php
```

```php
<?php

declare(strict_types=1);

namespace App\{{bc_name}}\Domain\ACL;

use App\{{bc_name}}\Domain\Model\{{foreign_id_vo}};

interface {{external_bc_name}}ACLInterface
{
    public function {{method_name}}({{foreign_id_vo}} $id): ?{{own_bc_dto}};
}
```

All parameter and return types belong to the consuming BC — never expose external BC domain model.

Infrastructure implementation location:
```
src/{{bc_name}}/Infrastructure/ACL/{{external_bc_name}}ACLAdapter.php
```

## Constructor Injection Pattern (PA-02)

Handlers inject interfaces, never implementations:

```php
final class {{handler_name}}Handler
{
    public function __construct(
        private readonly {{aggregate_name}}RepositoryInterface ${{aggregate_var}}Repository,
        private readonly {{read_model_name}}ReadModelInterface ${{read_model_var}}ReadModel,
    ) {}
}
```

## Symfony Autowiring Configuration

```yaml
# config/services.yaml
services:
    _defaults:
        autowire: true
        autoconfigure: true

    # Auto-register Application + Domain services
    App\{{bc_name}}\Application\:
        resource: '../src/{{bc_name}}/Application/'

    App\{{bc_name}}\Domain\:
        resource: '../src/{{bc_name}}/Domain/'

    # Bind interfaces to Infrastructure implementations
    App\{{bc_name}}\Domain\Repository\{{aggregate_name}}RepositoryInterface:
        alias: App\{{bc_name}}\Infrastructure\Persistence\Doctrine\Doctrine{{aggregate_name}}Repository

    App\{{bc_name}}\Application\ReadModel\{{read_model_name}}ReadModelInterface:
        alias: App\{{bc_name}}\Infrastructure\Persistence\Doctrine\Doctrine{{read_model_name}}ReadModel

    App\{{bc_name}}\Domain\ACL\{{external_bc_name}}ACLInterface:
        alias: App\{{bc_name}}\Infrastructure\ACL\{{external_bc_name}}ACLAdapter
```

Alternative — parameter binding for multiple implementations:
```yaml
services:
    _defaults:
        bind:
            ${{aggregate_var}}Repository: '@App\{{bc_name}}\Infrastructure\Persistence\Doctrine\Doctrine{{aggregate_name}}Repository'
```

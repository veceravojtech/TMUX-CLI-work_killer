# Preferred Composer Packages — PHP/Symfony DDD Stack

> This template is PHP-specific. No corresponding `_base/` template exists.
> The scaffold generator reads this list to emit `composer require --no-interaction` commands.
> Package names only — never pin versions here.

## Framework Core
- symfony/framework-bundle
- symfony/console
- symfony/runtime
- symfony/dotenv
- symfony/yaml
- symfony/flex

## ORM & Persistence
- doctrine/orm
- doctrine/doctrine-bundle
- doctrine/doctrine-migrations-bundle
- doctrine/doctrine-fixtures-bundle

## CQRS & Messaging
- symfony/messenger
- symfony/serializer

## Validation
- symfony/validator

## Security
- symfony/security-bundle
{{#uses_jwt}}
- lexik/jwt-authentication-bundle
{{/uses_jwt}}

## HTTP & API
- symfony/http-client
{{#uses_api_platform}}
- api-platform/core
{{/uses_api_platform}}
- nelmio/cors-bundle

## Templating
{{#frontend_twig}}
- symfony/twig-bundle
{{/frontend_twig}}

## Static Analysis (require-dev)
- phpstan/phpstan
- phpstan/phpstan-symfony
- phpstan/phpstan-doctrine
- symplify/easy-coding-standard
- qossmic/deptrac

## Testing (require-dev)
- phpunit/phpunit
- symfony/test-pack
- dama/doctrine-test-bundle
- zenstruck/foundry

## E2E Testing (require-dev)
{{#uses_e2e}}
- playwright (npm — not Composer)
{{/uses_e2e}}

## Debug (require-dev)
- symfony/debug-bundle
- symfony/web-profiler-bundle
- symfony/maker-bundle

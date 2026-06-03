# Quality Gates: {{project_name}} — PHP/Symfony

> Extends: `_base/quality-gates.md` — phase gates (DG/MG/IG/PG/CC/GM) are defined there.
> This template adds PHP-specific tooling: PHPStan, ECS, and Deptrac enforcement.

## PHPStan Configuration

Config: `phpstan.neon`

```neon
parameters:
    level: 9
    paths:
        - src/
```

```bash
vendor/bin/phpstan analyse --configuration=phpstan.neon  # exit 0 required
```

Implements SC-07, enforced by FG-10.

## ECS Configuration

Config: `ecs.php`

```php
use Symplify\EasyCodingStandard\Config\ECSConfig;

return ECSConfig::configure()
    ->withPaths([__DIR__ . '/src'])
    ->withPreparedSets(psr12: true)
    ->withPhpCsFixerSets(symfony: true);
```

```bash
vendor/bin/ecs check src/         # exit 0 required
vendor/bin/ecs check src/ --fix   # auto-fix
```

Implements SC-08, enforced by FG-11.

## Deptrac Layer Enforcement

Layer definitions, `{{bc_name}}DomainAndShare` composites, and rulesets are in `bounded-contexts.md` — not duplicated here.

**Dependency rules enforced (SC-12, SC-13):**
- **Domain** → Share only (pure, no framework imports)
- **Application** → own `{{bc_name}}DomainAndShare` composite (Domain + Share)
- **Infrastructure** → own DomainAndShare + own Application
- **Share** → nothing (zero dependencies)
- **Cross-BC** → never direct; only via ACL adapters (`{{external_bc_name}}ACLInterface`)

### FG-05: Controller Sublayer

Controllers are Infrastructure but restricted to Application-only imports. Add to `deptrac.yaml`:

```yaml
    {{#each bounded_contexts}}
    - name: {{bc_name}}Controller
      collectors:
        - type: classLike
          value: App\\{{bc_name}}\\Infrastructure\\.*Controller
    {{/each}}
```

```yaml
    {{#each bounded_contexts}}
    {{bc_name}}Controller:
      - {{bc_name}}Application
    {{/each}}
```

```bash
vendor/bin/deptrac analyse  # exit 0 required
```

Implements SC-09.

## PHP Implementation Gate Mapping

Extends base IG gates with concrete PHP commands:

| Base Gate | PHP Pass Condition | Command |
|-----------|--------------------|---------|
| IG-01 | Unit tests pass | `vendor/bin/phpunit --testsuite=unit` exit 0 |
| IG-02 | Integration tests pass | `vendor/bin/phpunit --testsuite=integration` exit 0 |
| IG-06 | No cross-layer violations | `vendor/bin/deptrac analyse` exit 0 |

## Final Gate Checks

| Gate | Tool | Criterion | Pass Condition |
|------|------|-----------|----------------|
| FG-01 | Deptrac | Zero violations codebase-wide | `vendor/bin/deptrac analyse` exit 0 |
| FG-02 | Deptrac | Domain depends on Share only | No framework imports in Domain layer |
| FG-03 | Deptrac | Application depends on DomainAndShare only | `{{bc_name}}Application → {{bc_name}}DomainAndShare` |
| FG-04 | Deptrac | Infrastructure depends on DomainAndShare + Application | Full ruleset enforced |
| FG-05 | Deptrac | Controllers import Application only | `{{bc_name}}Controller → {{bc_name}}Application` sublayer |
| FG-06 | Deptrac | No cross-BC imports in Domain/Application | No `App\{OtherBC}\` in Domain or Application |
| FG-07 | Deptrac | ACL adapters only cross-BC touchpoints | Cross-BC imports limited to `Adapter/` ACL classes |
| FG-10 | PHPStan | Level 9 zero errors | `vendor/bin/phpstan analyse` exit 0 |
| FG-11 | ECS | Zero violations | `vendor/bin/ecs check src/` exit 0 |

FG-08, FG-09 are Playwright (non-PHP) — see base template.

## Gate Execution

```bash
vendor/bin/phpunit --testsuite=unit \
  && vendor/bin/phpunit --testsuite=integration \
  && vendor/bin/phpstan analyse --configuration=phpstan.neon \
  && vendor/bin/ecs check src/ \
  && vendor/bin/deptrac analyse
```

Results logged to `{{gate_log_path}}`.

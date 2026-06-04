# PHP/Symfony Environment Gate (Gate 0): {{project_name}}

This template extends `_base/environment-gate.md` with concrete PHP/Symfony check commands, extension lists, and version constraints. It populates the base gate's mustache sections — it does NOT duplicate the base structure or execution protocol.

## Variable Sources

- `{{php_version}}` — major.minor from `composer.json` `require.php` (e.g., `8.2`)
- `{{version_constraint}}` — full constraint string from `composer.json` `require.php` (e.g., `>=8.2`)
- `{{db_host}}`, `{{db_port}}`, `{{db_user}}`, `{{db_password}}`, `{{db_name}}` — from `docs/architecture/test-environment.md`

---

<!-- Container Runtime (## 0) values: in docker mode the base template's `## 0. Container Runtime`
     section is filled from RUN_TARGET; this PHP overlay adds no PHP-image-specific guidance here.
     In docker mode, Gate 0 also records the resolved primary runtime container name into the
     AGENTS.md GATE0 `Runtime Container` ground-truth field (the investigator preflight reads it to
     verify the container is up before running any php/vendor/bin/* command).
     Host value blocks (## 1–## 3) below are wrapped in {{#unless is_docker}} — in docker mode the
     PHP runtime/extensions live inside the image and are deferred to the in-container scaffold. -->

{{#unless is_docker}}
## 1. Runtime Environment — `runtime_checks` values

{{#each runtime_checks}}
<!-- runtime_checks entry: PHP -->
- **check_id:** `ENV-R01`
- **runtime_name:** PHP
- **version_constraint:** `{{version_constraint}}`
- **check_command:** `php -r "echo PHP_VERSION;"`
- **expected_output:** Version satisfying `{{version_constraint}}`
- **install_command:**
  {{#if is_debian}}`sudo apt install php{{php_version}}-cli`{{/if}}
  {{#if is_macos}}`brew install php@{{php_version}}`{{/if}}
- **check_status:** {{check_status}}
{{/each}}

---

## 2. System Packages — `system_packages` values

{{#each system_packages}}

<!-- system_packages entry: Composer -->
- **check_id:** `ENV-P01`
- **package_name:** Composer
- **package_purpose:** PHP dependency manager — required before any extension or library install
- **check_command:** `composer --version`
- **expected_output:** `Composer version 2.x.x`
- **install_command:** `curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer`
- **check_status:** {{check_status}}

{{#if symfony_cli_detected}}
<!-- system_packages entry: Symfony CLI (optional) -->
- **check_id:** `ENV-P02`
- **package_name:** Symfony CLI
- **package_purpose:** Symfony local development server and project helper (optional — gate does not fail if absent)
- **check_command:** `symfony version`
- **expected_output:** `Symfony CLI version x.x.x`
- **install_command:**
  {{#if is_debian}}`curl -1sLf https://dl.cloudsmith.io/public/symfony/stable/setup.deb.sh | sudo bash && sudo apt install symfony-cli`{{/if}}
  {{#if is_macos}}`brew install symfony-cli/tap/symfony-cli`{{/if}}
- **check_status:** optional_pass | optional_missing
{{/if}}

{{/each}}

---

## 3. Required Extensions — `required_extensions` values

{{#each required_extensions}}

<!-- required_extensions entry: pdo_pgsql -->
- **check_id:** `ENV-X01`
- **extension_name:** pdo_pgsql
- **extension_purpose:** PostgreSQL database driver for PDO
- **check_command:** `php -m | grep -i pdo_pgsql`
- **install_command:** `sudo apt install php{{php_version}}-pgsql`
- **check_status:** {{check_status}}

<!-- required_extensions entry: intl -->
- **check_id:** `ENV-X02`
- **extension_name:** intl
- **extension_purpose:** Internationalization — required by Symfony Validator and locale handling
- **check_command:** `php -m | grep -i intl`
- **install_command:** `sudo apt install php{{php_version}}-intl`
- **check_status:** {{check_status}}

<!-- required_extensions entry: mbstring -->
- **check_id:** `ENV-X03`
- **extension_name:** mbstring
- **extension_purpose:** Multibyte string handling — required by Symfony core
- **check_command:** `php -m | grep -i mbstring`
- **install_command:** `sudo apt install php{{php_version}}-mbstring`
- **check_status:** {{check_status}}

{{#each extra_extensions}}
<!-- required_extensions entry: project-specific -->
- **check_id:** `ENV-X{{index}}`
- **extension_name:** {{extension_name}}
- **extension_purpose:** {{extension_purpose}}
- **check_command:** `php -m | grep -i {{extension_name}}`
- **install_command:** `sudo apt install php{{php_version}}-{{extension_name}}`
- **check_status:** {{check_status}}
{{/each}}

{{/each}}
{{/unless}}

---

## 4. Database Connection — conditional `{{#if has_database}}`

{{#if has_database}}

- **db_type:** PostgreSQL
<!-- db_host/db_port are the Gate-0 connection target only; the full published host:container port mapping lives in docs/architecture/test-environment.md (Published Ports). -->
- **db_host:** `{{db_host}}`
- **db_port:** `{{db_port}}`
- **db_name:** `{{db_name}}`
- **db_credentials_source:** `docs/architecture/test-environment.md`

### Reachability check

- **check_id:** `ENV-D01`
- **check_command:** `pg_isready -h {{db_host}} -p {{db_port}}`
- **expected_output:** `accepting connections`
- **check_status:** {{check_status}}

### Connection test

- **check_id:** `ENV-D02`
- **check_command:** `PGPASSWORD={{db_password}} psql -h {{db_host}} -p {{db_port}} -U {{db_user}} -d {{db_name}} -c "SELECT 1"`
- **expected_output:** Row with value `1`
- **check_status:** {{check_status}}

{{/if}}

---

## 5. Frontend Prerequisites — conditional `{{#if playwright_detected}}`

{{#if playwright_detected}}

{{#each frontend_runtime_checks}}

<!-- Node.js presence check (E-12: presence only, not Playwright package) -->
- **check_id:** `ENV-F01`
- **runtime_name:** Node.js
- **version_constraint:** any
- **check_command:** `node --version`
- **expected_output:** `v*.*.*`
- **install_command:**
  {{#if is_debian}}`curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - && sudo apt install -y nodejs`{{/if}}
  {{#if is_macos}}`brew install node`{{/if}}
- **check_status:** {{check_status}}

<!-- npm presence check (E-12: presence only) -->
- **check_id:** `ENV-F02`
- **runtime_name:** npm
- **version_constraint:** any
- **check_command:** `npm --version`
- **expected_output:** `*.*.*`
- **install_command:** Bundled with Node.js — install Node first
- **check_status:** {{check_status}}

{{/each}}

{{/if}}

---

## 6. Service Dependencies — conditional per service

{{#if has_services}}

{{#if has_redis}}
- **check_id:** `ENV-S01`
- **service_name:** Redis
- **check_command:** `redis-cli -h {{redis_host}} -p {{redis_port}} ping`
- **start_command:** `sudo systemctl start redis-server`
- **check_status:** {{check_status}}
{{/if}}

{{#if has_rabbitmq}}
- **check_id:** `ENV-S02`
- **service_name:** RabbitMQ
- **check_command:** `rabbitmqctl -n {{rabbitmq_node}} status`
- **start_command:** `sudo systemctl start rabbitmq-server`
- **check_status:** {{check_status}}
{{/if}}

{{/if}}

---

## Correction Ordering (E-13)

When Gate 0 detects failures, apply corrections in this order:

0. **Container runtime** (docker mode only) — Docker engine, daemon, Compose (ENV-C01..C04)
1. **System packages** — PHP CLI, Composer (ENV-R01, ENV-P01, ENV-P02)
2. **PHP extensions** — pdo_pgsql, intl, mbstring (ENV-X01..X03)
3. **Services** — Redis, RabbitMQ (ENV-S01, ENV-S02)
4. **Connections** — Database reachability and auth (ENV-D01, ENV-D02)
5. **Frontend** — Node.js, npm (ENV-F01, ENV-F02)

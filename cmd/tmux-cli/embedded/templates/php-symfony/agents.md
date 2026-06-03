# AGENTS.md — {{project_name}}

<!-- PHP/Symfony overlay for the AGENTS.md project-context file. Resolved by Gate 0 the same way
     as environment-gate.md (overlay → _base fallback). It mirrors the base GATE0 skeleton and adds
     PHP/Symfony-specific environment facts. Goal 0 fills ONLY the verified environment values inside
     the sentinels; framework build/test conventions (below GATE0:END) are appended by later goals
     once known. The GATE0 sentinels and contract headings MUST match _base/agents.md verbatim. -->

<!-- GATE0:BEGIN -->
<!-- Goal 0 (environment gate) owns ONLY the content between GATE0:BEGIN/END.
     Replace this whole block on retry; never touch content outside it.
     Later goals append their own sections AFTER GATE0:END. -->

## Run Target
- **Mode:** {{run_target}}            <!-- docker | local -->
{{#if is_docker}}- **DB/services:** {{services_location}}   <!-- compose | external -->{{/if}}

## Environment
{{#if is_docker}}
- **Container runtime:** Docker {{docker_version}}, Compose {{compose_version}}
- **PHP image:** {{php_image}}        <!-- e.g. php:8.2-fpm — verified inside the container by scaffold -->
{{/if}}
{{#unless is_docker}}
- **Runtime:** PHP {{php_version}} ({{version_constraint}})
- **Extensions:** {{verified_extensions}}   <!-- pdo_pgsql, intl, mbstring, … -->
- **Package manager:** Composer {{composer_version}}
{{/unless}}

## Database
- {{db_type}} @ {{db_host}}:{{db_port}} / {{db_name}} — credentials: {{db_credentials_source}}

## Services
- {{services_summary}}            <!-- Redis/RabbitMQ/none -->

## Test Environment
- **Base URL:** {{base_url}}

## Gate 0 Status
- **Status:** {{gate_status}} ({{passed_count}}/{{total_count}})
<!-- GATE0:END -->

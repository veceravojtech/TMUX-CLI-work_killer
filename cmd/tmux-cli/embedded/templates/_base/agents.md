# AGENTS.md — {{project_name}}

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
{{/if}}
{{#unless is_docker}}
- **Runtime:** {{runtime_name}} {{runtime_version}}
- **Extensions:** {{verified_extensions}}
- **Package manager:** {{composer_version}}
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

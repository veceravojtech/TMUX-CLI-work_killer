# Environment Gate (Gate 0): {{project_name}}

<!-- INSTRUCTIONS FOR CLAUDE:
     This template defines Gate 0 — environment prerequisite checks that must pass
     before any code generation or scaffold work begins. It is language-agnostic;
     language-specific checks come from overlay templates.
     Fill {{variables}} from project analysis. Skip conditional sections when
     their flag is false. Each check is independently executable. -->

---

## Gate Status

- **Status:** {{gate_status}}
- **Passed:** {{passed_count}} / {{total_count}}

---

## 0. Container Runtime

{{#if is_docker}}
### {{check_id}}: Docker Engine
- **Check command:** `docker --version`  /  `docker info`  (daemon up)
- **Install command:** `{{docker_install_command}}`
- **Result:** {{check_status}}

### {{check_id}}: Docker Compose
- **Check command:** `docker compose version`
- **Result:** {{check_status}}
{{/if}}
{{#unless is_docker}}
> Local run target — host runtime checked directly (sections 1–3).
{{/unless}}

---

{{#unless is_docker}}
## 1. Runtime Environment

{{#each runtime_checks}}
### {{check_id}}: {{runtime_name}} {{version_constraint}}

- **Check command:** `{{check_command}}`
- **Expected output:** {{expected_output}}
- **Install command:** `{{install_command}}`
- **Result:** {{check_status}}

{{/each}}

## 2. System Packages

{{#each system_packages}}
### {{check_id}}: {{package_name}}

- **Purpose:** {{package_purpose}}
- **Check command:** `{{check_command}}`
- **Expected output:** {{expected_output}}
- **Install command:** `{{install_command}}`
- **Result:** {{check_status}}

{{/each}}

## 3. Required Extensions

{{#each required_extensions}}
### {{check_id}}: {{extension_name}}

- **Purpose:** {{extension_purpose}}
- **Check command:** `{{check_command}}`
- **Install command:** `{{install_command}}`
- **Result:** {{check_status}}

{{/each}}
{{/unless}}

## 4. Database Connection

{{#if has_database}}

- **Type:** {{db_type}}
- **Host:** {{db_host}}
- **Port:** {{db_port}}
- **Database:** {{db_name}}
- **Check command:** `{{db_check_command}}`
- **Credentials source:** {{db_credentials_source}}
- **Result:** {{db_check_status}}

{{/if}}
{{#unless has_database}}
> No database — section skipped.
{{/unless}}

## 5. Frontend Prerequisites

{{#if has_frontend}}

{{#each frontend_runtime_checks}}
### {{check_id}}: {{runtime_name}} {{version_constraint}}

- **Check command:** `{{check_command}}`
- **Expected output:** {{expected_output}}
- **Install command:** `{{install_command}}`
- **Result:** {{check_status}}

{{/each}}

{{/if}}
{{#unless has_frontend}}
> No frontend — section skipped.
{{/unless}}

## 6. Service Dependencies

{{#if has_services}}

{{#each service_dependencies}}
### {{check_id}}: {{service_name}}

- **Check command:** `{{check_command}}`
- **Start command:** `{{start_command}}`
- **Result:** {{check_status}}

{{/each}}

{{/if}}
{{#unless has_services}}
> No external services — section skipped.
{{/unless}}

## 7. Gate Execution Protocol

1. Run every check independently — do not short-circuit on first failure
2. Record PASS or FAIL for each check in the results table below
3. The gate passes ONLY when all checks report PASS (all green)
4. After correction, Gate 0 re-runs failed checks and any checks whose inputs changed; on the final cycle before overall pass, re-run all checks for end-to-end verification.
5. Apply corrections in this order: container runtime (docker mode only) → system packages → extensions → services → connections

---

## 8. Check Results

| # | Check | Status | Output | Correction |
|---|-------|--------|--------|------------|
{{#each check_results}}
| {{check_number}} | {{check_name}} | {{check_status}} | {{check_output}} | `{{correction_command}}` |
{{/each}}

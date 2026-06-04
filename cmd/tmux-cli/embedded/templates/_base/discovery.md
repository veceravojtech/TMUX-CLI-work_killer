# Discovery Questions — Universal Template

<!-- INSTRUCTIONS FOR CLAUDE:
     Read each section, ask the user the numbered questions, and record answers
     in the {{placeholder}} positions. Sections marked with SKIP IF comments
     should be evaluated and skipped when the condition is true.
     This template is language-agnostic — language-specific follow-ups come
     from the language template after detection. -->

---

## 1. Language Detection
<!-- D-01, D-02, D-03 -->
<!-- PURPOSE: Identify the project's primary language and framework so the
     correct language template can be loaded for later phases. -->

1. Scan the project root for language indicators (e.g., dependency manifests, config files, source directories). What language and framework are detected?
   - Detected language: {{detected_language}}
   - Framework: {{framework}}
   - Detection source: {{detection_source}}

2. If no project files are found, ask the user:
   > What programming language and framework will this project use?

3. Confirm detection with the user:
   > I detected **{{detected_language}}** with **{{framework}}** (from {{detection_source}}). Is this correct?

<!-- After confirmation, load the corresponding language template from
     templates/<language>/ for use in later phases. -->

---

## 2. Product Brief
<!-- D-04 -->
<!-- PURPOSE: Capture the core product definition so all subsequent domain
     modelling has clear business context. -->

1. What problem does this project solve?
   > {{problem_statement}}

2. Who are the target users or personas?
   > {{target_users}}

3. What is the MVP scope — the minimum set of features for initial release?
   > {{mvp_scope}}

4. What is explicitly out of scope for the MVP?
   > {{out_of_scope}}

5. What are the success metrics — how will you know the MVP is working?
   > {{success_metrics}}

---

## 3. Bounded Contexts
<!-- D-05, D-06, D-07, D-27, D-28 -->
<!-- PURPOSE: Identify the domain boundaries that will shape code organisation,
     module structure, and inter-module contracts. -->

<!-- GUARD: If the user identifies zero bounded contexts, stop discovery and
     display an error: "At least one bounded context is required to proceed.
     Please describe the main domain area of your project." (D-28) -->

1. What are the major domain areas (bounded contexts) in this project? List each with a short description.

| # | Bounded Context | Description |
|---|-----------------|-------------|
| 1 | {{bc_name_1}} | {{bc_description_1}} |
| 2 | {{bc_name_2}} | {{bc_description_2}} |
| N | {{bc_name_N}} | {{bc_description_N}} |

<!-- SHORTCUT: If only one BC is identified, note it as a single-BC project.
     Sections that require multiple BCs (Context Map, some Share questions)
     will be skipped automatically. (D-27) -->

- Total bounded contexts: {{bc_count}}
- Single-BC project: {{is_single_bc}} (yes/no)

2. For each bounded context, confirm that no entity is shared across BC boundaries (D-07):
   > Are there any entities that live in more than one bounded context? If so, which ones, and how should they be separated?

---

## 4. Aggregate Roots and Domain Events
<!-- D-06, D-08 -->
<!-- PURPOSE: For each BC, identify the aggregate roots (consistency boundaries),
     their entities, value objects, and the domain events they emit. -->

For each bounded context listed above, ask:

### BC: {{bc_name}}

1. What are the aggregate roots in this bounded context? An aggregate root is the main entity that controls a cluster of related objects.

| Aggregate | Root Entity | Value Objects | Invariants |
|-----------|-------------|---------------|------------|
| {{aggregate_name}} | {{root_entity}} | {{value_objects}} | {{invariants}} |

2. What domain events does each aggregate produce? (Events = facts about things that happened.)

| Aggregate | Domain Event | Trigger |
|-----------|--------------|---------|
| {{aggregate_name}} | {{domain_event}} | {{event_trigger}} |

---

## 5. Domain Services
<!-- D-22 -->
<!-- PURPOSE: Identify operations that span multiple aggregates within a single BC
     but don't belong to any one aggregate. -->

For each bounded context, ask:

1. Are there any operations that span multiple aggregates within **{{bc_name}}**? These are operations that coordinate between aggregates but don't naturally belong to either.

| Service | Spans Aggregates | Operation |
|---------|------------------|-----------|
| {{service_name}} | {{aggregate_list}} | {{operation_description}} |

<!-- If no cross-aggregate operations exist in a BC, record "none" and move on. -->

---

## 6. Architecture Decisions
<!-- D-09, D-10 -->
<!-- PURPOSE: Capture key technology and design decisions with their context
     and alternatives, so ADRs can be generated from this input. -->

1. What key architecture decisions have been made (or need to be made) for this project?

| # | Decision | Context | Alternatives Considered | Rationale |
|---|----------|---------|------------------------|-----------|
| 1 | {{decision_name}} | {{decision_context}} | {{decision_alternatives}} | {{decision_rationale}} |

2. Are there any architecture decisions that are still open or need further investigation?
   > {{open_decisions}}

- Total decisions: {{decision_count}}

---

## 7. Context Map
<!-- D-21, D-27 -->
<!-- SKIP IF: single-BC project ({{is_single_bc}} = yes) — a single BC has no
     inter-BC relationships to map. -->
<!-- PURPOSE: Map relationships between bounded contexts — who depends on whom,
     and where anti-corruption layers are needed. -->

1. For each pair of bounded contexts that communicate, describe the relationship:

| Upstream BC | Downstream BC | Relationship Type | ACL Interface |
|-------------|---------------|-------------------|---------------|
| {{upstream_bc}} | {{downstream_bc}} | {{relationship_type}} | {{acl_interface}} |

Relationship types: Conformist, Customer-Supplier, Published Language, Shared Kernel, Anti-Corruption Layer, Open Host Service.

2. Are there any bounded contexts that should be completely isolated (no direct communication)?
   > {{isolated_bcs}}

---

## 8. Share Namespace
<!-- D-30 -->
<!-- PURPOSE: Identify shared types, value objects, and data types that are used
     across multiple bounded contexts and belong in a shared namespace. -->

1. Are there any value objects, data types, or enums that are used across multiple bounded contexts? (e.g., Money, DateRange, Address, Email, Status enums)

| Shared Type | Kind | Used By BCs |
|-------------|------|-------------|
| {{shared_type}} | {{type_kind}} | {{used_by_bcs}} |

2. For each shared type, confirm it is truly shared and not a candidate for duplication within each BC:
   > Should **{{shared_type}}** be a single shared definition, or should each BC have its own version?

---

## 9. API Endpoint Inventory
<!-- D-11 -->
<!-- PURPOSE: Build a complete list of every API action the system exposes.
     This inventory feeds directly into goal generation. -->

1. List every API endpoint the system needs, grouped by bounded context:

### {{bc_name}} Endpoints

| # | HTTP Method | Path | Description | Auth Required |
|---|-------------|------|-------------|---------------|
| 1 | {{http_method}} | {{endpoint_path}} | {{endpoint_description}} | {{auth_required}} |

2. Are there any batch or bulk endpoints needed?
   > {{batch_endpoints}}

3. Are there any webhook or callback endpoints?
   > {{webhook_endpoints}}

---

## 10. Auth Flows
<!-- D-12 -->
<!-- PURPOSE: Explicitly document every authentication and authorisation step
     so auth is treated as a first-class domain, not an afterthought. -->

1. What authentication method does this project use? (e.g., session-based, JWT, OAuth2, API keys, none)
   > {{auth_method}}

2. List every auth-related flow and its steps:

| Flow | Steps |
|------|-------|
| Register | {{register_steps}} |
| Login | {{login_steps}} |
| Logout | {{logout_steps}} |
| Token Refresh | {{token_refresh_steps}} |
| Password Reset | {{password_reset_steps}} |

3. Are there different user roles or permission levels? If so, list them:
   > {{user_roles}}

4. How are permissions enforced? (middleware, per-handler checks, policy objects, etc.)
   > {{permission_enforcement}}

---

## 11. Event Listeners
<!-- D-13 -->
<!-- PURPOSE: Map async/event-driven behaviour — which events trigger which
     handlers, and where cross-cutting concerns live. -->

1. Beyond the domain events listed in section 4, are there system-level events that trigger handlers? (e.g., user registered triggers welcome email, order placed triggers inventory check)

| Trigger Event | Handler | Side Effect |
|---------------|---------|-------------|
| {{trigger_event}} | {{handler_name}} | {{side_effect}} |

2. Are any of these handlers asynchronous (queued) vs synchronous?
   > {{async_handlers}}

---

## 12. Cross-Cutting Concerns
<!-- D-23, D-24 -->
<!-- PURPOSE: Capture patterns that apply across the entire application so they
     are designed once and applied consistently. -->

1. **Pagination**: How should list endpoints paginate results? (cursor-based, offset-limit, page-number)
   > {{pagination_strategy}}

2. **Soft-delete**: Should any entities support soft-delete (mark as deleted but retain in DB)?
   > {{soft_delete_strategy}}

3. **Timezone handling**: What timezone strategy does the project use? (UTC storage + user-local display, single timezone, etc.)
   > {{timezone_strategy}}

4. **Internationalisation (i18n)**: Does the project need multi-language support? If so, which languages and what content is translated?
   > {{i18n_strategy}}

5. **Error response format**: What standard format should API errors follow? (e.g., RFC 7807 Problem Details, custom envelope, HTTP status only)
   > {{error_format}}

6. **Middleware / filters**: What middleware or request/response filters are needed? (logging, CORS, rate limiting, request ID, etc.)
   > {{middleware_list}}

---

## 13. Frontend Presence
<!-- D-20 -->
<!-- PURPOSE: Determine whether a frontend exists and what type, so frontend-
     specific questions can be included or skipped. -->

1. Does this project have a frontend? What type?
   - [ ] Server-rendered (SSR / templates)
   - [ ] Single-page application (SPA)
   - [ ] Mobile app (native or cross-platform)
   - [ ] API-only (no frontend)
   - [ ] Other: {{frontend_other}}

   > Frontend type: {{frontend_type}}

<!-- SKIP IF: frontend_type = "API-only" — skip all remaining frontend questions.
     Record {{frontend_type}} = "API-only" and proceed to Test Environment. -->

2. What frontend framework or technology is used?
   > {{frontend_framework}}

3. How does the frontend communicate with the backend? (REST, GraphQL, gRPC, WebSocket, etc.)
   > {{frontend_api_protocol}}

4. Are there any shared types or contracts between frontend and backend? (e.g., OpenAPI spec, generated types, shared schema)
   > {{frontend_shared_contracts}}

---

## 14. Test Environment
<!-- D-14, D-15 -->
<!-- PURPOSE: Capture environment details so generated tests can run against
     a real backend from the start. -->
<!-- NOTE: The run target (docker | local) is captured in Step 4 Architecture
     Decisions (decision topic="run-target") and persisted into test-environment.md
     alongside the fields below; D-14 also gates it as non-empty. It is recorded
     here only, never as an ADR. -->
<!-- NOTE: When the run target is docker with compose-hosted services, the published
     host:container port mappings (PUBLISHED_PORTS — service, host port, container
     port, purpose) are ALSO captured in Step 4 and persisted as a Published Ports
     block in test-environment.md (omitted entirely for local/external). These are
     distinct from db_host/db_port, which is only the Gate-0 connection target. -->

1. What is the base URL for the test/development environment?
   > {{test_base_url}}

2. What test user credentials are available? (username, password, or token)
   > {{test_user_credentials}}

3. What is the test database name?
   > {{test_db_name}}

4. What command loads test fixtures or seeds the database?
   > {{fixture_command}}

5. Is Playwright installed and available for E2E testing?
   - [ ] Yes, installed and configured
   - [ ] No, needs Gate 0 setup
   - [ ] Not applicable (API-only, no browser tests needed)

   > Playwright status: {{playwright_status}}

6. Should the app seed a default admin user on first run (the dev environment)?
   <!-- GATED: only relevant when at least one auth flow exists (see Auth Flows /
        discover Step 6.2). Skip entirely when there are no auth flows — there is
        no User entity to seed. -->
   <!-- Provide environment-variable NAMES only (e.g. APP_ADMIN_EMAIL,
        APP_ADMIN_PASSWORD). NEVER record the secret values — they live only in
        the environment. -->
   > Seed default admin (dev): {{seed_default_admin}}
   > Admin identifier env-var NAME: {{admin_identifier_env}}
   > Admin password env-var NAME: {{admin_password_env}}

---

## 15. Inventory Confirmation Gate
<!-- D-16, D-17, D-25, D-26 -->
<!-- PURPOSE: Present the computed goal count and get explicit user confirmation
     before goal generation begins. This is a workflow control point. -->

1. Present the inventory summary to the user:

   | Category | Count |
   |----------|-------|
   | Bounded contexts | {{bc_count}} |
   | Aggregates | {{aggregate_count}} |
   | API endpoints | {{endpoint_count}} |
   | Auth flows | {{auth_flow_count}} |
   | Event listeners | {{listener_count}} |
   | Cross-cutting items | {{crosscutting_count}} |
   | Architecture decisions | {{decision_count}} |
   | **Estimated total goals** | **{{estimated_goal_count}}** |

   Goal count formula: `endpoints + auth_flows + listeners + cross_cutting + decisions + (aggregates * 2) + infrastructure`

<!-- WARNING: If {{estimated_goal_count}} > 100, display:
     "This inventory produces over 100 goals. Consider reducing MVP scope
     or splitting into phases before proceeding." (D-26) -->

2. Ask for explicit confirmation:
   > The discovery phase identified **{{estimated_goal_count}}** goals. Do you want to proceed with goal generation, or would you like to adjust the scope first?

<!-- NOTE: The confirmation gate logic (blocking on user input) lives in the
     discovery skill, not in this template. This template only defines the
     questions and presentation format. (D-17) -->

<!-- CHECKPOINT: Before goal generation begins, all discovery outputs must be
     saved to disk under docs/architecture/. The discovery skill handles
     this persistence — this template defines what to persist. (D-25) -->

<!-- CANCELLATION: If the user cancels mid-discovery, all outputs produced by
     completed sections must be preserved on disk. The discovery skill handles
     partial-save logic. (D-29) -->

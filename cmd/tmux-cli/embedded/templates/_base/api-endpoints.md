# API Endpoints: {{bc_name}}

## Endpoint Inventory

### {{action_name}}

| Field | Value |
|-------|-------|
| Method | {{http_method}} |
| Path | {{endpoint_path}} |
| Bounded Context | {{bc_name}} |
| Description | {{action_description}} |
| Auth Required | {{auth_required}} |
| Auth Roles | {{auth_roles}} |

#### Request

- Content-Type: {{request_content_type}}
- Validation rules:

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| {{field_name}} | {{field_type}} | {{required}} | {{constraints}} |

#### Response

- Success status: {{success_status_code}}
- Content-Type: {{response_content_type}}

```json
{{success_response_example}}
```

#### Error Responses

| Status | Condition | Body Format |
|--------|-----------|-------------|
| 400 | Malformed request body | {{error_format}} |
| 401 | Missing or invalid authentication | {{error_format}} |
| 403 | Insufficient permissions | {{error_format}} |
| 404 | Resource not found | {{error_format}} |
| 422 | Validation failure | {{error_format}} |

#### Fan-Out (per-action deliverables)

- Controller: {{handler_path}} <!-- canonical shape: contexts/{{bc_name}}/app/src/Http/Controller/{{action_name}}Controller.php -->
- Request DTO: {{request_dto_path}}
- Response DTO: {{response_dto_path}}
- Route: {{route_definition_path}}
- E2E Test: {{e2e_test_path}}

---

(Repeat ### {{action_name}} block for each endpoint in this BC)

---

## Auth Flows

### {{auth_flow_name}}

| Step | Method | Path | Description |
|------|--------|------|-------------|
| {{step_number}} | {{http_method}} | {{step_path}} | {{step_description}} |

(Repeat rows for multi-step flows)

---

(Repeat ### {{auth_flow_name}} block for each auth flow)

---

## Cross-Cutting: Event Listeners

| Trigger Event | Listener | BC | Description |
|---------------|----------|----|-------------|
| {{event_name}} | {{listener_name}} | {{listener_bc}} | {{listener_description}} |

---

## API Conventions

- **Error format**: {{error_format}} (e.g., RFC 7807 Problem Details, custom envelope)
- **Pagination**: {{pagination_strategy}} (e.g., cursor-based, offset-limit)
- **Versioning**: {{versioning_strategy}} (e.g., URL prefix /v1, header-based)
- **Rate limiting**: {{rate_limiting_strategy}}
- **Content negotiation**: {{content_negotiation}}

## Language-specific patterns
(See language template)

# Error Handling: {{project_name}}

## Exception Categories

| Category | Description | Default HTTP Status | Example Scenario |
|----------|-------------|--------------------:|------------------|
| {{category_name}} | {{category_description}} | {{http_status}} | {{example_scenario}} |

### Standard Categories

| Category | Status | When to use |
|----------|-------:|-------------|
| Validation | 422 | Input fails business rules or format constraints |
| Not Found | 404 | Requested resource does not exist |
| Authorization | 403 | Authenticated user lacks required permission |
| Authentication | 401 | Missing or invalid credentials |
| Domain Conflict | 409 | Operation violates domain invariant or state precondition |
| Domain Error | 422 | Business logic rejects the operation |
| Infrastructure | 502 | Upstream service or dependency failure |
| Unexpected | 500 | Unhandled error — no internal details exposed |

## Error Response Format (RFC 7807 Problem Details)

All error responses MUST use [RFC 7807](https://datatracker.ietf.org/doc/html/rfc7807) Problem Details structure:

```json
{
  "type": "{{error_type_uri}}",
  "title": "{{short_human_readable_summary}}",
  "status": {{http_status_code}},
  "detail": "{{specific_explanation}}",
  "instance": "{{request_uri}}"
}
```

| Field | Required | Description |
|-------|:--------:|-------------|
| type | yes | URI identifying the error type (e.g., `/errors/validation-failed`) |
| title | yes | Short summary — same for all instances of this type |
| status | yes | HTTP status code (integer) |
| detail | yes | Human-readable explanation specific to this occurrence |
| instance | no | URI of the request that caused the error |

Content-Type: `application/problem+json`

## Validation Error Format

Validation failures extend Problem Details with a `violations` array:

```json
{
  "type": "/errors/validation-failed",
  "title": "Validation Failed",
  "status": 422,
  "detail": "{{validation_error_count}} validation error(s)",
  "violations": [
    {
      "field": "{{field_path}}",
      "message": "{{constraint_message}}",
      "code": "{{constraint_code}}"
    }
  ]
}
```

## Error Logging Strategy

| Category | Log Level | Include Stack Trace | Include Request Context |
|----------|-----------|:-------------------:|:-----------------------:|
| Validation | info | no | field list only |
| Not Found | info | no | resource identifier |
| Authorization | warning | no | user ID, resource |
| Authentication | warning | no | redacted credentials indicator |
| Domain Conflict | warning | no | aggregate state summary |
| Domain Error | warning | no | operation context |
| Infrastructure | error | yes | upstream service, timeout |
| Unexpected | critical | yes | full request context |

### Logging Rules

- NEVER log sensitive data: passwords, tokens, PII, credit card numbers
- ALWAYS include a correlation ID (from request header or generated) in log entries
- Stack traces only for 5xx errors — never in the response body
- Log the error type URI for traceability across services

## Language-specific patterns
(See language template)

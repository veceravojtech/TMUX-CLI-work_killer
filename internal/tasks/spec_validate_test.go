package tasks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSpec(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

const fullValidSpec = `## Intent

**Problem:** The auth module has no rate limiting, allowing brute-force attacks.

**Approach:** Add token-bucket rate limiter middleware before the auth handler.

## Boundaries & Constraints

**Always:** All rate limiter config must be loaded from environment variables.
**Ask First:** Whether to apply rate limiting to internal service-to-service calls.
**Never:** Never modify the existing auth handler signature or break backwards compatibility.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Normal request | Valid token, under limit | 200 OK, pass through | N/A |
| Rate exceeded | Valid token, over limit | 429 Too Many Requests | Return retry-after header |
| No token | Missing auth header | 401 Unauthorized | Standard auth error |

## Dependencies

- Module auth-core: provides the AuthHandler interface and session store.

## Code Map

- ` + "`internal/auth/handler.go:42`" + ` — AuthHandler.ServeHTTP, the insertion point for middleware
- ` + "`internal/auth/middleware.go:1`" + ` — new file for rate limiter
- ` + "`internal/config/env.go:15`" + ` — LoadEnvConfig, add rate limiter fields

## Implementation Plan

### Files to Create/Modify

- ` + "`internal/auth/middleware.go`" + ` — new rate limiter middleware
- ` + "`internal/auth/handler.go`" + ` — wire middleware into handler chain
- ` + "`internal/config/env.go`" + ` — add RATE_LIMIT_* env vars

### Key Classes/Functions

- RateLimiter struct with Allow(key string) bool method
- NewRateLimiter(rate int, burst int) constructor

## Test Plan

- TestRateLimiter_AllowsUnderLimit: 5 requests at rate=10/s should all pass
- TestRateLimiter_BlocksOverLimit: 11 requests at rate=10/s, 11th should be blocked
- TestRateLimiter_DifferentKeys: separate buckets per IP
- TestMiddleware_Returns429: integration test with HTTP test server

## Acceptance Criteria

- [ ] Given a client under the rate limit, when they send a request, then it passes through to the auth handler
- [ ] Given a client over the rate limit, when they send a request, then they receive 429 with retry-after header
- [ ] Given rate limiter config in env vars, when the service starts, then the limiter uses those values

## Implementation Notes

Watch for race conditions in the token bucket — use sync.Mutex, not atomic operations, because the refill logic is multi-step.

## Verification

- ` + "`go test ./internal/auth/ -run TestRateLimiter -v`" + ` — expected: all pass
- ` + "`curl -X POST localhost:8080/auth -H 'X-Forwarded-For: 1.2.3.4'`" + ` x20 — expected: 429 after 10
`

func TestValidateSpecFile_FullValid(t *testing.T) {
	dir := t.TempDir()
	path := writeSpec(t, dir, "spec.md", fullValidSpec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Gaps)
	assert.GreaterOrEqual(t, result.Stats.TestCases, 4)
	assert.GreaterOrEqual(t, result.Stats.AcceptanceCriteria, 3)
	assert.GreaterOrEqual(t, result.Stats.CodeMapEntries, 3)
}

func TestValidateSpecFile_MissingIntent(t *testing.T) {
	dir := t.TempDir()
	spec := `## Boundaries & Constraints

**Always:** Do stuff.
**Never:** Don't do stuff.

## Dependencies

none

## Code Map

- ` + "`file.go:10`" + ` — thing

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestSomething: does a thing

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.False(t, result.Valid)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S0")
}

func TestValidateSpecFile_S1_EmptyCodeMap(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something broken.
**Approach:** Fix it.

## Boundaries & Constraints

**Always:** Be good.
**Never:** Be bad.

## Dependencies

none

## Code Map

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestThing: verifies the thing works

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.False(t, result.Valid)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S1")
}

func TestValidateSpecFile_S2_NoFilesToCreate(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something.
**Approach:** Fix.

## Boundaries & Constraints

**Always:** Do.
**Never:** Don't.

## Dependencies

none

## Code Map

- ` + "`file.go:1`" + ` — thing

## Implementation Plan

Some vague plan without file structure.

## Test Plan

- TestThing: checks it

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S2")
}

func TestValidateSpecFile_S4_NoGivenWhenThen(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something.
**Approach:** Fix.

## Boundaries & Constraints

**Always:** Do.
**Never:** Don't.

## Dependencies

none

## Code Map

- ` + "`file.go:1`" + ` — thing

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestThing: checks it

## Acceptance Criteria

- [ ] System should work correctly
- [ ] No errors should occur
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S4")
}

func TestValidateSpecFile_S7_NoBoundaries(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something.
**Approach:** Fix.

## Dependencies

none

## Code Map

- ` + "`file.go:1`" + ` — thing

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestThing: checks it

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S7")
}

func TestValidateSpecFile_S7_BoundariesNoNever(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something.
**Approach:** Fix.

## Boundaries & Constraints

**Always:** Do stuff.

## Dependencies

none

## Code Map

- ` + "`file.go:1`" + ` — thing

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestThing: checks it

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S7")
}

func TestValidateSpecFile_S8_PlaceholdersTBD(t *testing.T) {
	dir := t.TempDir()
	spec := `## Intent

**Problem:** Something.
**Approach:** TBD

## Boundaries & Constraints

**Always:** Do.
**Never:** Don't.

## Dependencies

none

## Code Map

- ` + "`file.go:1`" + ` — thing

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

- TestThing: checks it

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)

	var gapIDs []string
	for _, g := range result.Gaps {
		gapIDs = append(gapIDs, g.ID)
	}
	assert.Contains(t, gapIDs, "S8")
}

func TestValidateSpecFile_NotFound(t *testing.T) {
	_, err := ValidateSpecFile("/nonexistent/spec.md")
	require.Error(t, err)
}

func TestValidateSpecFile_Stats(t *testing.T) {
	dir := t.TempDir()
	path := writeSpec(t, dir, "spec.md", fullValidSpec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Equal(t, 4, result.Stats.TestCases)
	assert.Equal(t, 3, result.Stats.AcceptanceCriteria)
	assert.Equal(t, 3, result.Stats.CodeMapEntries)
}

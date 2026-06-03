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

// gapIDsOf extracts the gap IDs from a validation result (helper for the
// false-positive-remediation tests below; existing tests inline their own loop).
func gapIDsOf(r *SpecValidationResult) []string {
	var ids []string
	for _, g := range r.Gaps {
		ids = append(ids, g.ID)
	}
	return ids
}

// specWith builds an otherwise-valid spec skeleton with the Code Map and Test
// Plan section bodies swapped in, so a test can exercise one section in
// isolation without tripping the other S0-S8 checks.
func specWith(codeMap, testPlan string) string {
	return `## Intent

**Problem:** Something is broken.
**Approach:** Fix it cleanly.

## Boundaries & Constraints

**Always:** Do good.
**Never:** Do bad.

## Dependencies

none

## Code Map

` + codeMap + `

## Implementation Plan

### Files to Create/Modify

- ` + "`file.go`" + ` — modify

## Test Plan

` + testPlan + `

## Acceptance Criteria

- [ ] Given X, when Y, then Z
`
}

const defaultCodeMap = "- `file.go:1` — thing"

// --- S3 testCaseRe: regex-level forms ---------------------------------------

func TestTestCaseRe_AcceptedForms(t *testing.T) {
	accepted := []string{
		"- `TestCreateUser` succeeds",
		"- testCreateUser",
		"  - TestNested",
		"| TestFoo | returns X |",
		"- TC-1: grep returns zero",
	}
	for _, in := range accepted {
		assert.True(t, testCaseRe.MatchString(in), "expected match: %q", in)
	}
}

func TestTestCaseRe_RejectedForms(t *testing.T) {
	rejected := []string{
		"- testing the cache",
		"- tests should pass",
		"| Normal request | valid | 200 | n/a |",
		"| Scenario | Input | Output |",
	}
	for _, in := range rejected {
		assert.False(t, testCaseRe.MatchString(in), "expected NO match: %q", in)
	}
}

// --- S1 codeRefRe: regex-level forms ----------------------------------------

func TestCodeRefRe_AcceptedForms(t *testing.T) {
	accepted := []string{
		"internal/foo.go:42",
		"`base.md:1-168`",
		"`internal/auth/handler.go:42`",
		"file.go:1",
	}
	for _, in := range accepted {
		assert.True(t, codeRefRe.MatchString(in), "expected match: %q", in)
	}
}

func TestCodeRefRe_RejectedForms(t *testing.T) {
	rejected := []string{
		"http://host:80",
		"time 12:30",
		"noextensionhere:42",
	}
	for _, in := range rejected {
		assert.False(t, codeRefRe.MatchString(in), "expected NO match: %q", in)
	}
}

// --- S8 tdbRe: regex-level forms --------------------------------------------

func TestTdbRe_ValuePositionFires(t *testing.T) {
	fires := []string{
		"**Approach:** TBD",
		"- TODO",
		"Status: PLACEHOLDER",
		"field: to be determined",
	}
	for _, in := range fires {
		assert.True(t, tdbRe.MatchString(in), "expected match: %q", in)
	}
}

func TestTdbRe_ProseAndTemplateDoesNotFire(t *testing.T) {
	noFire := []string{
		"Use {{placeholder}} in body",
		"{{placeholder}}",
		"**Never:** never create placeholder code.",
		"**Never:** leave a TODO comment behind",
		"lowercase placeholder word in prose",
	}
	for _, in := range noFire {
		assert.False(t, tdbRe.MatchString(in), "expected NO match: %q", in)
	}
}

// --- S3 end-to-end: ValidateSpecFile -----------------------------------------

func TestValidateSpecFile_S3_TableTestPlan(t *testing.T) {
	dir := t.TempDir()
	tp := "| Test Case | Expected |\n|---|---|\n| TestFoo | returns X |\n| TestBar | returns Y |"
	path := writeSpec(t, dir, "spec.md", specWith(defaultCodeMap, tp))

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S3")
	assert.GreaterOrEqual(t, result.Stats.TestCases, 1)
}

func TestValidateSpecFile_S3_CamelCaseTestPlan(t *testing.T) {
	dir := t.TempDir()
	tp := "- testCreatesUser: builds a user\n- testRejectsDuplicate: returns an error"
	path := writeSpec(t, dir, "spec.md", specWith(defaultCodeMap, tp))

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S3")
	assert.GreaterOrEqual(t, result.Stats.TestCases, 1)
}

func TestValidateSpecFile_S3_EmptyTestPlanStillFails(t *testing.T) {
	dir := t.TempDir()
	tp := "- testing the cache layer\n- general notes about how tests should pass"
	path := writeSpec(t, dir, "spec.md", specWith(defaultCodeMap, tp))

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Contains(t, gapIDsOf(result), "S3")
	assert.Equal(t, 0, result.Stats.TestCases)
}

// --- S1 end-to-end: ValidateSpecFile -----------------------------------------

func TestValidateSpecFile_S1_BareAndRangeRefs(t *testing.T) {
	dir := t.TempDir()
	cm := "- internal/x.go:10 — current behavior\n- `y.md:1-20` — range reference"
	path := writeSpec(t, dir, "spec.md", specWith(cm, "- TestThing: checks it"))

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S1")
	assert.GreaterOrEqual(t, result.Stats.CodeMapEntries, 2)
}

func TestValidateSpecFile_S1_EmptyCodeMapStillFails(t *testing.T) {
	dir := t.TempDir()
	cm := "Just prose describing the area, no file references at all."
	path := writeSpec(t, dir, "spec.md", specWith(cm, "- TestThing: checks it"))

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Contains(t, gapIDsOf(result), "S1")
	assert.Equal(t, 0, result.Stats.CodeMapEntries)
}

// --- S8 end-to-end: ValidateSpecFile -----------------------------------------

func TestValidateSpecFile_S8_MustacheNoFire(t *testing.T) {
	dir := t.TempDir()
	spec := specWith(defaultCodeMap, "- TestThing: checks it") +
		"\n## Implementation Notes\n\nUse {{placeholder}} in the body. Never create placeholder code by hand.\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S8")
	assert.True(t, result.Valid)
}

func TestValidateSpecFile_S8_ValuePositionStillFires(t *testing.T) {
	dir := t.TempDir()
	spec := specWith(defaultCodeMap, "- TestThing: checks it") +
		"\n## Implementation Notes\n\n**Approach:** TBD\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Contains(t, gapIDsOf(result), "S8")
}

// --- S9 subjectiveGateRe / coverageClauseRe: regex-level forms ---------------

func TestSubjectiveGateRe_AcceptedForms(t *testing.T) {
	accepted := []string{
		"tests demonstrably cover the feature",
		"the handler is properly wired",
		"input is appropriately validated",
		"appropriate error handling",
		"sufficient coverage of edge cases",
		"the buffer is adequately sized",
		"retries as needed until success",
		"applies where applicable",
		"a reasonable number of attempts",
	}
	for _, in := range accepted {
		assert.True(t, subjectiveGateRe.MatchString(in), "expected match: %q", in)
	}
}

func TestSubjectiveGateRe_RejectedForms(t *testing.T) {
	rejected := []string{
		"vendor/bin/phpunit --filter=Foo exits 0",
		"grep -rl 'class X' src/ returns >=1",
		"this is an objective gate, not a judgment call",
		"the property of the system is invariant", // must NOT match "properly"
		"retries as expected",                     // must NOT match "as needed"
	}
	for _, in := range rejected {
		assert.False(t, subjectiveGateRe.MatchString(in), "expected NO match: %q", in)
	}
}

func TestCoverageClauseRe_AcceptedForms(t *testing.T) {
	accepted := []string{
		"pass exit 0 with creation/mutation/invariant/event coverage",
		"missing test coverage for aggregate operations",
		"insufficient test coverage",
		"coverage of the domain layer",
		"covers creation via named constructor",
		"covers each invariant violation",
	}
	for _, in := range accepted {
		assert.True(t, coverageClauseRe.MatchString(in), "expected match: %q", in)
	}
}

func TestCoverageClauseRe_RejectedForms(t *testing.T) {
	rejected := []string{
		"this is an objective gate, not a coverage judgment call",
		"a green-but-uncovered suite FAILS",
		"line coverage >= 80%",
		"code coverage threshold is enforced at 80%",
	}
	for _, in := range rejected {
		assert.False(t, coverageClauseRe.MatchString(in), "expected NO match: %q", in)
	}
}

// --- S9 end-to-end: ValidateSpecFile -----------------------------------------

func TestValidateSpecFile_S9_SubjectiveCoverageGateFires(t *testing.T) {
	dir := t.TempDir()
	spec := fullValidSpec +
		"\n## Investigation Config\n\nINV-1 Unit tests (test-execution): vendor/bin/phpunit; pass exit 0 with creation/mutation/invariant/event coverage; fail otherwise.\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Contains(t, gapIDsOf(result), "S9")
}

func TestValidateSpecFile_S9_SubjectiveAdjectiveFires(t *testing.T) {
	dir := t.TempDir()
	spec := fullValidSpec +
		"\n## Validation Rules\n\n- the feature is properly validated end to end\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.Contains(t, gapIDsOf(result), "S9")
}

func TestValidateSpecFile_S9_ObjectiveGatesNoFire(t *testing.T) {
	dir := t.TempDir()
	spec := fullValidSpec +
		"\n## Validation Rules\n\n" +
		"- vendor/bin/phpunit --filter=Foo (exit 0)\n" +
		"- grep -rl 'class SkuUniquenessChecker' src/ (must return >=1)\n" +
		"- line coverage >= 80%\n" +
		"\n## Investigation Config\n\n" +
		"INV-1 (test-execution): phpunit; pass exit 0 AND grep -rl 'class X' src/ returns >=1; fail otherwise.\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S9")
	assert.True(t, result.Valid)
}

func TestValidateSpecFile_S9_MetaReferenceNoFire(t *testing.T) {
	// Guards the goal-003 INV-1 wording: an objective gate that mentions the
	// word "coverage"/"uncovered" only to DESCRIBE itself must not be flagged.
	dir := t.TempDir()
	spec := fullValidSpec +
		"\n## Investigation Config\n\n" +
		"INV-1 (test-execution): phpunit --filter=Foo MUST exit 0 AND grep -rl 'class X' src/ returns >=1; " +
		"a green-but-uncovered suite FAILS. This is an objective gate, not a coverage judgment call.\n"
	path := writeSpec(t, dir, "spec.md", spec)

	result, err := ValidateSpecFile(path)
	require.NoError(t, err)
	assert.NotContains(t, gapIDsOf(result), "S9")
	assert.True(t, result.Valid)
}

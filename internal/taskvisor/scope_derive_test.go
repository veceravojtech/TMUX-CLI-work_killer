package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DeriveScopeFromDeliverables token hygiene (F5) ---------------------------

// TestDeriveScope_StripsDotSlash guards the falsely-disjoint stem bug: a
// './internal/x' token and a bare 'internal/x' token must normalize to ONE
// stem. Raw './'-prefixed stems never path-prefix-match their bare twins in
// globsOverlap, so the gate would co-schedule two goals editing the same tree.
func TestDeriveScope_StripsDotSlash(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Edit ./internal/x/file.go and wire it up",
		"Also touch internal/x/file.go for the same change",
		"Create ./cmd/tool/main.go",
	})
	assert.Equal(t, []string{"internal/x/file.go", "cmd/tool/main.go"}, got,
		"leading ./ must be stripped and the bare/dotted twins deduped to one stem")
}

// TestDeriveScope_StripsDotSlash_SingleSegment: './x' is path-like (the
// leading ./ is what carries the slash) and must survive normalization as the
// bare segment, not be dropped.
func TestDeriveScope_StripsDotSlash_SingleSegment(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{"Update ./Makefile targets"})
	assert.Equal(t, []string{"Makefile"}, got)
}

// TestDeriveScope_DropsPunctuationOnlyTokens: './...' (the go package
// wildcard) and other letterless tokens derive a garbage scope stem ("..."
// path-prefixes nothing real but poisons the set) and must be dropped.
func TestDeriveScope_DropsPunctuationOnlyTokens(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Run go vet over ./... before merging",
		"Keep internal/taskvisor/goals.go green",
	})
	assert.Equal(t, []string{"internal/taskvisor/goals.go"}, got)
}

func TestDeriveScope_DropsLetterlessTokens(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Score 1/2 in pkg/mod and note 3/4 ratio",
	})
	assert.Equal(t, []string{"pkg/mod"}, got, "digit-only path-like tokens carry no file footprint")
}

func TestDeriveScope_NilWhenNothingSurvivesHygiene(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{"Run go test ./... and ship it"})
	assert.Nil(t, got, "a deliverable whose only path-like token is './...' has UNKNOWN scope")
}

func TestDeriveScope_KeepsFirstSeenOrder(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Touch b/second.go then a/first.go",
		"Then b/second.go again",
	})
	assert.Equal(t, []string{"b/second.go", "a/first.go"}, got)
}

// TestDeriveScope_AcceptanceOnlySemantics documents the CALLERS' contract:
// CreateGoal derives scope from Acceptance ONLY — validate commands are too
// noisy (runner flags, ./... wildcards, tool paths) to mine for a footprint.
// A goal whose only path tokens live in Validate stays UNKNOWN (serialized).
func TestDeriveScope_AcceptanceOnlySemantics(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Validate-only paths goal",
		Acceptance:  []string{"Behavior is correct end to end"},
		Validate:    []string{"go test ./internal/taskvisor/ -count=1"},
	})
	require.NoError(t, err)
	assert.False(t, derived)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Nil(t, gf.Goals[0].Scope, "validate paths must never leak into derived scope")
}

// --- DeriveScopeWithCompleteness (per-line completeness) ----------------------

// TestDeriveScopeWithCompleteness_AllLinesCovered: every non-empty line names a
// path → complete; scope equals the wrapper's output exactly.
func TestDeriveScopeWithCompleteness_AllLinesCovered(t *testing.T) {
	in := []string{"update cors.go in internal/x/cors.go", "edit internal/x/ratelimit.go"}
	scope, incomplete, uncovered := DeriveScopeWithCompleteness(in)
	assert.Equal(t, []string{"internal/x/cors.go", "internal/x/ratelimit.go"}, scope)
	assert.False(t, incomplete)
	assert.Nil(t, uncovered)
	assert.Equal(t, DeriveScopeFromDeliverables(in), scope, "wrapper must return identical scope")
}

// TestDeriveScopeWithCompleteness_PartialIsIncomplete: one bare line (no path
// token) downgrades the derivation to incomplete and is reported verbatim.
func TestDeriveScopeWithCompleteness_PartialIsIncomplete(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"return request id header",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.True(t, incomplete)
	assert.Equal(t, []string{"return request id header"}, uncovered)
}

// TestDeriveScopeWithCompleteness_NoLineCovered: zero path tokens anywhere →
// nil scope (same observable as today's UNKNOWN) but incomplete with both
// lines reported.
func TestDeriveScopeWithCompleteness_NoLineCovered(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{"do X", "do Y"})
	assert.Nil(t, scope)
	assert.True(t, incomplete)
	assert.Equal(t, []string{"do X", "do Y"}, uncovered)
}

// TestDeriveScopeWithCompleteness_BlankLinesNotCounted: blank/whitespace lines
// are NOT criteria — they never trigger a downgrade.
func TestDeriveScopeWithCompleteness_BlankLinesNotCounted(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"",
		"   ",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.False(t, incomplete)
	assert.Empty(t, uncovered)
}

// TestDeriveScopeWithCompleteness_DuplicatePathStillCoversLine: coverage is
// per-line and decided BEFORE dedup — a line whose only token was already
// emitted by an earlier line is still covered.
func TestDeriveScopeWithCompleteness_DuplicatePathStillCoversLine(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"also touch internal/x/cors.go",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.False(t, incomplete)
	assert.Empty(t, uncovered)
}

// TestDeriveScopeWithCompleteness_NoAcceptanceIsComplete: an empty input has no
// criteria to cover, so it is NOT incomplete (existing UNKNOWN default branch).
func TestDeriveScopeWithCompleteness_NoAcceptanceIsComplete(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness(nil)
	assert.Nil(t, scope)
	assert.False(t, incomplete)
	assert.Nil(t, uncovered)
}

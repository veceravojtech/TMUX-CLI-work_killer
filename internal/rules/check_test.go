package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes content to dir/rel, creating parent dirs — the on-disk
// changed-file the signal grep runs against.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// automatedRule is a minimal validate_kind=automated rule carrying a signal —
// the violated path's fixture.
func automatedRule(id, signal string, applies ...string) CodeRule {
	r := CodeRule{ID: id, Severity: "should", Phase: "application", ValidateKind: "automated", AppliesTo: applies, Signal: signal}
	r.Examples.Bad = "a forbidden token here" // makes the signal runnable-gate pass
	return r
}

func TestRulesCheck_ViolationWhenSignalMatchesChangedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/x.go", "package x\nvar y = forbidden()\n")
	r := automatedRule("X", "forbidden", "src/**")
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)
	require.Len(t, res.Rules, 1)
	assert.True(t, res.Rules[0].Violated)
	assert.False(t, res.Rules[0].AgentReview)
	assert.Equal(t, []string{"src/x.go"}, res.Rules[0].Matched)
	assert.Equal(t, "forbidden", res.Rules[0].Signal)
}

func TestRulesCheck_CleanWhenSignalAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/x.go", "package x\nvar y = compliant()\n")
	r := automatedRule("X", "forbidden", "src/**")
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)
	require.Len(t, res.Rules, 1)           // applicable...
	assert.False(t, res.Rules[0].Violated) // ...but clean
}

func TestRulesCheck_UnmatchedFileNotApplicable(t *testing.T) {
	dir := t.TempDir()
	r := automatedRule("X", "forbidden", "src/**")
	res := Check([]CodeRule{r}, []string{"docs/readme.md"}, "", dir)
	assert.Empty(t, res.Rules) // changed file outside applies_to → rule omitted
}

func TestRulesCheck_ReviewRuleNoSignalIsAgentReview(t *testing.T) {
	dir := t.TempDir()
	r := CodeRule{ID: "R", Severity: "should", Phase: "application", ValidateKind: "review", AppliesTo: []string{"src/**"}}
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)
	require.Len(t, res.Rules, 1)
	assert.True(t, res.Rules[0].AgentReview)
	assert.False(t, res.Rules[0].Violated)
}

func TestRulesCheck_MixedRuleRunsSignalAndFlagsAgentReview(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/x.go", "package x\nvar y = forbidden()\n")
	r := CodeRule{ID: "M", Severity: "should", Phase: "application", ValidateKind: "mixed", AppliesTo: []string{"src/**"}, Signal: "forbidden"}
	r.Examples.Bad = "forbidden token"
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)
	require.Len(t, res.Rules, 1)
	assert.True(t, res.Rules[0].Violated)    // signal half ran
	assert.True(t, res.Rules[0].AgentReview) // review half is the agent's
}

func TestRulesCheck_NonRunnableSignalDroppedWithWarning(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/x.go", "package x\n")
	r := CodeRule{ID: "BAD", Severity: "should", Phase: "application", ValidateKind: "automated", AppliesTo: []string{"src/**"}, Signal: "["}
	r.Examples.Bad = "anything"
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)
	assert.Empty(t, res.Rules)
	require.NotEmpty(t, res.Warnings)
	assert.Contains(t, res.Warnings[0], "not runnable")
	assert.Contains(t, res.Warnings[0], "BAD")
}

func TestRulesCheck_PhaseFilterExcludesOtherPhases(t *testing.T) {
	dir := t.TempDir()
	r := CodeRule{ID: "T", Severity: "should", Phase: "test", ValidateKind: "review", AppliesTo: []string{"src/**"}}
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "impl", dir)
	assert.Empty(t, res.Rules)
}

func TestRulesCheck_JSONResultSchemaStable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/x.go", "package x\nvar y = forbidden()\n")
	r := automatedRule("X", "forbidden", "src/**")
	res := Check([]CodeRule{r}, []string{"src/x.go"}, "", dir)

	b, err := json.Marshal(res)
	require.NoError(t, err)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &top))
	_, hasRules := top["rules"]
	_, hasWarnings := top["warnings"]
	assert.True(t, hasRules)
	assert.True(t, hasWarnings)

	var payloads []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["rules"], &payloads))
	require.Len(t, payloads, 1)
	for _, key := range []string{"id", "severity", "validate_kind", "phase", "paths", "matched", "violated", "agent_review", "signal"} {
		_, ok := payloads[0][key]
		assert.Truef(t, ok, "payload missing key %q", key)
	}
}

func TestRulesCheck_EmptyRulesYieldsEmptyResult(t *testing.T) {
	dir := t.TempDir()
	res := Check(nil, nil, "", dir)
	assert.NotNil(t, res.Rules) // [] not null — JSON-stable
	assert.NotNil(t, res.Warnings)
	assert.Empty(t, res.Rules)
	assert.Empty(t, res.Warnings)
}

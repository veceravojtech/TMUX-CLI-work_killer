package taskvisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvestigatorConfigParity locks the render→parse round trip: for every
// inferInvestigatorType emission, the investigator the inline-plan evaluates
// (ParseInvestigators of goal.md) must classify IsPureCommand identically to the
// one deriveInvestigators stored. This is the "single source" guarantee — the
// renderer (renderInvestigationConfig) and the in-package parser
// (ParseInvestigators) can never drift on type/command/Pass without this failing.
func TestInvestigatorConfigParity(t *testing.T) {
	// One rule per inferInvestigatorType branch.
	rules := []string{
		"vendor/bin/phpstan analyse",   // quality-gate
		"php bin/phpunit",              // test-execution
		"vendor/bin/deptrac analyse",   // architecture-check
		"vendor/bin/ecs check",         // quality-gate (lint family)
		"grep -q X f.go",               // static-analysis, exit-only (pure)
		"php bin/console debug:router", // static-analysis, semantic (NOT pure)
		"go build ./...",               // default → static-analysis, "command succeeds"
	}

	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			stored := deriveInvestigators(t.TempDir(), []string{rule}, nil)
			require.NotEmpty(t, stored)

			var b strings.Builder
			renderInvestigationConfig(&b, stored, ResolveExecRuntime(t.TempDir()))
			parsed := ParseInvestigators(b.String())

			require.Len(t, parsed, len(stored), "render→parse must reproduce every investigator")
			for i := range stored {
				assert.Equalf(t, IsPureCommand(stored[i]), IsPureCommand(parsed[i]),
					"IsPureCommand parity for %q (stored type=%q pass=%q ⇄ parsed type=%q pass=%q)",
					rule, stored[i].Type, stored[i].Pass, parsed[i].Type, parsed[i].Pass)
				// Byte-faithful inverse for the IsPureCommand-relevant fields.
				assert.Equal(t, stored[i].Type, parsed[i].Type, "type round-trips")
				assert.Equal(t, stored[i].Pass, parsed[i].Pass, "Pass round-trips")
				assert.Equal(t, stored[i].Commands, parsed[i].Commands, "commands round-trip")
			}
		})
	}
}

// TestInvestigatorConfigParity_GrepEmissionPureBothDirections proves acceptance #4
// for the grep emission: the derived investigator is pure-command with Pass
// "command succeeds (exit 0)", and the parsed-from-goal.md copy is pure too.
func TestInvestigatorConfigParity_GrepEmissionPureBothDirections(t *testing.T) {
	stored := deriveInvestigators(t.TempDir(), []string{"grep -q X f.go"}, nil)
	require.NotEmpty(t, stored)
	require.Equal(t, "static-analysis", stored[0].Type)
	require.Equal(t, "command succeeds (exit 0)", stored[0].Pass)
	require.True(t, IsPureCommand(stored[0]), "derived grep is pure-command")

	var b strings.Builder
	renderInvestigationConfig(&b, stored, ResolveExecRuntime(t.TempDir()))
	parsed := ParseInvestigators(b.String())
	require.NotEmpty(t, parsed)
	assert.True(t, IsPureCommand(parsed[0]), "parsed grep is pure-command (other direction)")
}

// TestInvestigatorConfigParity_DebugRouterSemanticBothDirections proves acceptance
// #4 for the semantic console emission: debug:router stays Pass "matches expected"
// and is NEVER judged pure-command, derived or parsed.
func TestInvestigatorConfigParity_DebugRouterSemanticBothDirections(t *testing.T) {
	stored := deriveInvestigators(t.TempDir(), []string{"php bin/console debug:router"}, nil)
	require.NotEmpty(t, stored)
	require.Equal(t, "static-analysis", stored[0].Type)
	require.Equal(t, "matches expected", stored[0].Pass)
	require.False(t, IsPureCommand(stored[0]), "derived debug:router is semantic, not pure")

	var b strings.Builder
	renderInvestigationConfig(&b, stored, ResolveExecRuntime(t.TempDir()))
	parsed := ParseInvestigators(b.String())
	require.NotEmpty(t, parsed)
	assert.False(t, IsPureCommand(parsed[0]), "parsed debug:router is semantic (other direction)")
}

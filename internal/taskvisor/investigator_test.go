package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapInvestigators_PreservesUnder4(t *testing.T) {
	in := []Investigator{
		{Name: "A", Type: "static-analysis"},
		{Name: "B", Type: "test-execution"},
		{Name: "C", Type: "quality-gate"},
	}
	out := capInvestigators(in)
	require.Len(t, out, 3)
	assert.Equal(t, "A", out[0].Name)
	assert.Equal(t, "B", out[1].Name)
	assert.Equal(t, "C", out[2].Name)
}

func TestDeriveInvestigators_StillCapsAt4(t *testing.T) {
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
		"npx eslint .",
	}
	out := deriveInvestigators(t.TempDir(), validate)
	assert.Len(t, out, 4)
}

// --- B2b: own-suite-green mandatory gate -----------------------------------

func TestProducesAppCode_SrcToken(t *testing.T) {
	got := producesAppCode("domain", []string{"AC1"}, []string{"phpunit covers `src/Catalog`"}, "")
	assert.True(t, got, "a validate rule citing src/Catalog produces app code")
}

func TestProducesAppCode_NoSrcToken(t *testing.T) {
	got := producesAppCode("domain",
		[]string{"docs updated", "the source of truth is the README"},
		[]string{"markdownlint docs/"}, "purely a documentation goal, app behaviour unchanged")
	assert.False(t, got, "prose mentioning 'source'/'app' without a src/|app/ path token is not app code")
}

func TestProducesAppCode_GatePhase(t *testing.T) {
	got := producesAppCode("gate", []string{"build green"}, []string{"compile app/Kernel.php"}, "touches app/")
	assert.False(t, got, "a gate-phase goal never produces app code even with an app/ token")
}

func TestOwnSuiteGateInvestigator_FailIsCodeDefect(t *testing.T) {
	inv := ownSuiteGateInvestigator([]string{"tests/Integration/Catalog", "tests/Functional/Catalog"})
	assert.Equal(t, "own-suite-green", inv.Type)
	assert.Contains(t, inv.Fail, "code-defect")
	assert.Contains(t, inv.Fail, "implementer")
	require.Len(t, inv.Commands, 1)
	assert.Equal(t, "vendor/bin/phpunit tests/Integration/Catalog tests/Functional/Catalog", inv.Commands[0])
	assert.NotContains(t, inv.Commands[0], "--filter")
}

// --- IsPureCommand / isExitOnlyPass (B9a) ----------------------------------

func TestIsPureCommand_ExplicitCommandType(t *testing.T) {
	inv := Investigator{Type: "command", Commands: []string{"test -f composer.json"}, Pass: "anything"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExplicitCommandTypeNoCommands(t *testing.T) {
	inv := Investigator{Type: "command", Commands: nil, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyStaticAnalysis(t *testing.T) {
	inv := Investigator{Type: "static-analysis", Commands: []string{"vendor/bin/phpstan analyse"}, Pass: "exit 0, no errors"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyTestExecution(t *testing.T) {
	inv := Investigator{Type: "test-execution", Commands: []string{"php bin/phpunit"}, Pass: "all green (exit 0)"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitOnlyArchitectureCheck(t *testing.T) {
	inv := Investigator{Type: "architecture-check", Commands: []string{"vendor/bin/deptrac analyse"}, Pass: "exit 0, no layer violations"}
	assert.True(t, IsPureCommand(inv))
}

func TestIsPureCommand_SemanticPassVetoesExitType(t *testing.T) {
	inv := Investigator{Type: "static-analysis", Commands: []string{"grep -r Foo src/"}, Pass: "matches expected"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_CodeReviewRejected(t *testing.T) {
	inv := Investigator{Type: "code-review", Commands: []string{"true"}, Pass: "design correct"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ConventionAuditRejected(t *testing.T) {
	inv := Investigator{Type: "convention-audit", Commands: []string{"true"}, Pass: "DDD compliance holds"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_E2ETestRejected(t *testing.T) {
	inv := Investigator{Type: "e2e-test", Commands: []string{"npx playwright test"}, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_ExitTypeNoCommandRejected(t *testing.T) {
	inv := Investigator{Type: "quality-gate", Commands: nil, Pass: "exit 0"}
	assert.False(t, IsPureCommand(inv))
}

func TestIsPureCommand_DerivedExitInvestigatorsAreClassified(t *testing.T) {
	derived := deriveInvestigators(t.TempDir(), []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
	})
	require.Len(t, derived, 3)
	for _, inv := range derived {
		assert.Truef(t, IsPureCommand(inv), "derived %s/%q should be pure-command", inv.Type, inv.Pass)
	}
}

func TestIsPureCommand_DerivedGrepInvestigatorNotPure(t *testing.T) {
	derived := deriveInvestigators(t.TempDir(), []string{"grep -r Foo src/"})
	require.NotEmpty(t, derived)
	grep := derived[0]
	require.Equal(t, "static-analysis", grep.Type)
	require.Equal(t, "matches expected", grep.Pass)
	assert.False(t, IsPureCommand(grep))
}

func TestIsExitOnlyPass_MarkerTable(t *testing.T) {
	cases := []struct {
		pass string
		want bool
	}{
		{"exit 0", true},
		{"command succeeds", true},
		{"matches expected", false},
		{"", false},
		{"review passes", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isExitOnlyPass(c.pass), "isExitOnlyPass(%q)", c.pass)
	}
}

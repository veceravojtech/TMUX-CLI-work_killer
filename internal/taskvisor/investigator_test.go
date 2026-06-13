package taskvisor

import (
	"strings"
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
	out := deriveInvestigators(t.TempDir(), validate, nil)
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
	}, nil)
	require.Len(t, derived, 3)
	for _, inv := range derived {
		assert.Truef(t, IsPureCommand(inv), "derived %s/%q should be pure-command", inv.Type, inv.Pass)
	}
}

func TestIsPureCommand_DerivedGrepInvestigatorIsPure(t *testing.T) {
	derived := deriveInvestigators(t.TempDir(), []string{"grep -r Foo src/"}, nil)
	require.NotEmpty(t, derived)
	grep := derived[0]
	require.Equal(t, "static-analysis", grep.Type)
	require.Equal(t, "command succeeds (exit 0)", grep.Pass)
	assert.True(t, IsPureCommand(grep))
}

func TestIsExitOnlyPass_MarkerTable(t *testing.T) {
	cases := []struct {
		pass string
		want bool
	}{
		{"exit 0", true},
		{"command succeeds", true},
		{"command succeeds (exit 0)", true},
		{"matches expected", false},
		{"", false},
		{"review passes", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isExitOnlyPass(c.pass), "isExitOnlyPass(%q)", c.pass)
	}
}

// --- Scope-derived investigators (classifyScope + deriveInvestigators scope fallback) ---

func TestClassifyScope_GoFiles(t *testing.T) {
	profile := classifyScope([]string{"internal/taskvisor/investigator.go", "internal/taskvisor/goals.go"})
	assert.Equal(t, []string{"internal/taskvisor/investigator.go", "internal/taskvisor/goals.go"}, profile.Go)
	assert.Empty(t, profile.Shell)
	assert.Empty(t, profile.XML)
	assert.Empty(t, profile.Unknown)
}

func TestClassifyScope_ShellFiles(t *testing.T) {
	profile := classifyScope([]string{"scripts/deploy.sh"})
	assert.Equal(t, []string{"scripts/deploy.sh"}, profile.Shell)
	assert.Empty(t, profile.Go)
	assert.Empty(t, profile.XML)
	assert.Empty(t, profile.Unknown)
}

func TestClassifyScope_MixedFiles(t *testing.T) {
	profile := classifyScope([]string{"internal/foo.go", "scripts/run.sh", "templates/plan.xml"})
	assert.Equal(t, []string{"internal/foo.go"}, profile.Go)
	assert.Equal(t, []string{"scripts/run.sh"}, profile.Shell)
	assert.Equal(t, []string{"templates/plan.xml"}, profile.XML)
	assert.Empty(t, profile.Unknown)
}

func TestClassifyScope_GlobPatterns(t *testing.T) {
	profile := classifyScope([]string{"internal/taskvisor/**"})
	assert.Empty(t, profile.Go)
	assert.Empty(t, profile.Shell)
	assert.Empty(t, profile.XML)
	assert.Equal(t, []string{"internal/taskvisor/**"}, profile.Unknown)
}

func TestClassifyScope_EmptyScope(t *testing.T) {
	profile := classifyScope(nil)
	assert.Empty(t, profile.Go)
	assert.Empty(t, profile.Shell)
	assert.Empty(t, profile.XML)
	assert.Empty(t, profile.Unknown)
}

func TestDeriveInvestigators_ScopeGoFiles_NoValidate(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, []string{"internal/taskvisor/investigator.go"})

	require.GreaterOrEqual(t, len(list), 2)
	hasTest := false
	hasBuild := false
	for _, inv := range list {
		if inv.Type == "test-execution" && strings.Contains(strings.Join(inv.Commands, " "), "go test") {
			hasTest = true
		}
		if inv.Type == "quality-gate" && strings.Contains(strings.Join(inv.Commands, " "), "go build") {
			hasBuild = true
		}
	}
	assert.True(t, hasTest, "scope Go files should produce a go test investigator")
	assert.True(t, hasBuild, "scope Go files should produce a go build investigator")
}

func TestDeriveInvestigators_ScopeShellFiles_NoValidate(t *testing.T) {
	root := padRoot(t)
	list := deriveInvestigators(root, nil, []string{"scripts/deploy.sh"})

	hasBashN := false
	for _, inv := range list {
		if strings.Contains(strings.Join(inv.Commands, " "), "bash -n") {
			hasBashN = true
		}
	}
	assert.True(t, hasBashN, "scope .sh files should produce a bash -n investigator")
}

func TestDeriveInvestigators_ScopeMixed_NoValidate(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, []string{"internal/foo.go", "scripts/run.sh"})

	require.LessOrEqual(t, len(list), 4, "capped at 4")
	cmds := padCommands(list)
	assert.Contains(t, cmds, "go test", "mixed scope includes go test")
	assert.Contains(t, cmds, "bash -n", "mixed scope includes bash -n")
}

func TestDeriveInvestigators_ValidateRulesSufficient_ScopeIgnored(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit --testsuite unit"}, []string{"internal/foo.go"})

	require.Len(t, list, 2)
	assert.Equal(t, "quality-gate", list[0].Type)
	assert.Equal(t, "test-execution", list[1].Type)
	cmds := padCommands(list)
	assert.NotContains(t, cmds, "go test", "scope-derived should not fire when validate rules sufficient")
}

func TestDeriveInvestigators_ValidateRulesPartial_ScopeSupplements(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, []string{"go vet ./..."}, []string{"internal/taskvisor/investigator.go"})

	require.GreaterOrEqual(t, len(list), 2)
	hasGoTest := false
	for _, inv := range list {
		if inv.Type == "test-execution" && strings.Contains(strings.Join(inv.Commands, " "), "go test") {
			hasGoTest = true
		}
	}
	assert.True(t, hasGoTest, "scope-derived go test should supplement the single validate rule")
}

func TestDeriveInvestigators_EmptyScopeEmptyValidate_GenericPad(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2)
	assert.Contains(t, padCommands(list), "go build ./...", "generic pad should fire with go.mod present")
}

func TestDeriveInvestigators_UnknownExtensionsInScope_FallsThrough(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, []string{"data/config.yaml", "docs/README.md"})

	require.GreaterOrEqual(t, len(list), 2)
	cmds := padCommands(list)
	assert.NotContains(t, cmds, "go test", "unknown extensions should not produce scope-derived investigators")
	assert.Contains(t, cmds, "go build ./...", "should fall through to generic pad")
}

func TestDeriveInvestigators_GoScopeAdaptiveDepth_Simple(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, []string{"internal/taskvisor/investigator.go", "internal/taskvisor/goals.go"})

	scopeInvs := 0
	hasVet := false
	for _, inv := range list {
		if inv.Type == "test-execution" || inv.Type == "quality-gate" {
			scopeInvs++
		}
		if strings.Contains(strings.Join(inv.Commands, " "), "go vet") {
			hasVet = true
		}
	}
	assert.LessOrEqual(t, scopeInvs, 2, "simple scope (<=3 files, single pkg) should produce at most 2 investigators")
	assert.False(t, hasVet, "simple scope should not include go vet")
}

func TestDeriveInvestigators_GoScopeAdaptiveDepth_Complex(t *testing.T) {
	root := padRoot(t, "go.mod")
	scope := []string{
		"internal/taskvisor/investigator.go",
		"internal/taskvisor/goals.go",
		"internal/taskvisor/daemon.go",
		"internal/taskvisor/statemachine.go",
		"internal/mcp/server.go",
	}
	list := deriveInvestigators(root, nil, scope)

	hasVet := false
	for _, inv := range list {
		if strings.Contains(strings.Join(inv.Commands, " "), "go vet") {
			hasVet = true
		}
	}
	assert.True(t, hasVet, "complex scope (>3 files, multiple packages) should include go vet")
	require.LessOrEqual(t, len(list), 4, "capped at 4")
}

func TestDeriveInvestigators_XMLScope_FoldsIntoGoBuild(t *testing.T) {
	root := padRoot(t, "go.mod")
	list := deriveInvestigators(root, nil, []string{"cmd/tmux-cli/embedded/commands/tmux/plan.xml"})

	hasBuild := false
	for _, inv := range list {
		if strings.Contains(strings.Join(inv.Commands, " "), "go build") {
			hasBuild = true
		}
	}
	assert.True(t, hasBuild, "XML scope in Go project should produce go build investigator")
}

func TestGoTestPaths_SinglePackage(t *testing.T) {
	result := goTestPaths([]string{"internal/taskvisor/investigator.go"})
	assert.Equal(t, "./internal/taskvisor/...", result)
}

func TestGoTestPaths_MultiplePackages(t *testing.T) {
	result := goTestPaths([]string{"internal/taskvisor/a.go", "internal/mcp/b.go"})
	assert.Equal(t, "./...", result)
}

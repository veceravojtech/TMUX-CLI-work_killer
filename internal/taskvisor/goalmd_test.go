package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteGoalMD_AllSections(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Fix prices", "", []string{"Price matches API", "No rounding errors"}, []string{"go test ./...", "curl check"}, nil, "We need accurate pricing", "UI redesign", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Fix prices")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "- Price matches API")
	assert.Contains(t, content, "- No rounding errors")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "- go test ./...")
	assert.Contains(t, content, "- curl check")
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "We need accurate pricing")
	assert.Contains(t, content, "## Not In Scope")
	assert.Contains(t, content, "UI redesign")
	assert.NotContains(t, content, "## Phase")
}

func TestWriteGoalMD_WithPhase(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Setup DB", "infrastructure", []string{"Tables exist"}, []string{"check"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Phase")
	assert.Contains(t, content, "infrastructure")
	lines := strings.Split(content, "\n")
	phaseIdx := -1
	acIdx := -1
	for i, l := range lines {
		if l == "## Phase" {
			phaseIdx = i
		}
		if l == "## Acceptance Criteria" {
			acIdx = i
		}
	}
	assert.Greater(t, phaseIdx, 0, "Phase section should exist")
	assert.Greater(t, acIdx, phaseIdx, "Phase must appear before Acceptance Criteria")
}

func TestWriteGoalMD_EmptyPhaseOmitted(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "No phase goal", "", []string{"AC1"}, []string{"check"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "## Phase")
}

func TestWriteGoalMD_AcceptanceOnly(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Build API", "", []string{"Returns 200"}, nil, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Build API")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "- Returns 200")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_NoCriteria(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Simple goal", "", nil, nil, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Simple goal")
	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
	assert.NotContains(t, content, "## Context")
	assert.NotContains(t, content, "## Not In Scope")
}

func TestWriteGoalMD_ContextAndNotInScope(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Refactor module", "", nil, nil, nil, "Legacy code needs cleanup", "Performance tuning", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Acceptance Criteria")
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "Legacy code needs cleanup")
	assert.Contains(t, content, "## Not In Scope")
	assert.Contains(t, content, "Performance tuning")
	assert.Contains(t, content, "## Validation Rules")
	assert.Contains(t, content, "(none)")
}

func TestWriteGoalMD_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Test atomic", "", []string{"A1"}, nil, nil, "", "", nil)
	require.NoError(t, err)

	tmpPath := filepath.Join(dir, "goal.md.tmp")
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not remain after write")
}

func TestWriteGoalMD_MarkdownFormat(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Format check", "", []string{"Criterion A", "Criterion B"}, []string{"validate cmd"}, nil, "Some context", "Out of scope", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")

	assert.Equal(t, "# Format check", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "## Acceptance Criteria", lines[2])
	assert.Equal(t, "", lines[3])
	assert.Equal(t, "- Criterion A", lines[4])
	assert.Equal(t, "- Criterion B", lines[5])
	assert.Equal(t, "", lines[6])
	assert.Equal(t, "## Validation Rules", lines[7])
	assert.Equal(t, "", lines[8])
	assert.Equal(t, "- validate cmd", lines[9])
	assert.Equal(t, "", lines[10])
	assert.Equal(t, "## Context", lines[11])
	assert.Equal(t, "", lines[12])
	assert.Equal(t, "Some context", lines[13])
	assert.Equal(t, "", lines[14])
	assert.Equal(t, "## Not In Scope", lines[15])
	assert.Equal(t, "", lines[16])
	assert.Equal(t, "Out of scope", lines[17])
}

func TestWriteGoalMD_MarkdownFormatWithPhase(t *testing.T) {
	dir := t.TempDir()
	err := WriteGoalMD(dir, "Phase check", "domain", []string{"Criterion A"}, []string{"validate cmd"}, nil, "", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")

	assert.Equal(t, "# Phase check", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "## Phase", lines[2])
	assert.Equal(t, "", lines[3])
	assert.Equal(t, "domain", lines[4])
	assert.Equal(t, "", lines[5])
	assert.Equal(t, "## Acceptance Criteria", lines[6])
}

func TestWriteGoalMD_PreconditionsSection(t *testing.T) {
	dir := t.TempDir()
	preconds := []Precondition{
		{Kind: "env", Spec: "DB_USER", Remedy: "export DB_USER"},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	err := WriteGoalMD(dir, "With preconds", "", []string{"AC1"}, []string{"check"}, preconds, "ctx", "", nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "## Preconditions")
	assert.Contains(t, content, "- [env] DB_USER — export DB_USER")
	assert.Contains(t, content, "- [service] localhost:5432 — start postgres")

	// ## Preconditions must sit between ## Validation Rules and ## Context.
	valIdx := strings.Index(content, "## Validation Rules")
	preIdx := strings.Index(content, "## Preconditions")
	ctxIdx := strings.Index(content, "## Context")
	require.NotEqual(t, -1, valIdx)
	require.NotEqual(t, -1, preIdx)
	require.NotEqual(t, -1, ctxIdx)
	assert.Greater(t, preIdx, valIdx, "Preconditions must come after Validation Rules")
	assert.Greater(t, ctxIdx, preIdx, "Context must come after Preconditions")

	// Empty slice => section omitted (legacy goal.md byte-unchanged).
	dir2 := t.TempDir()
	require.NoError(t, WriteGoalMD(dir2, "No preconds", "", []string{"AC1"}, []string{"check"}, nil, "ctx", "", nil))
	data2, err := os.ReadFile(filepath.Join(dir2, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data2), "## Preconditions")
}

func TestWriteGoalMD_RendersProvidedInvestigators(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "Quality gate", Type: "quality-gate", Commands: []string{"phpstan analyse"}, Pass: "exit 0", Fail: "errors"},
		{Name: "Test execution", Type: "test-execution", Commands: []string{"phpunit"}, Pass: "green", Fail: "red"},
		{Name: "Architecture check", Type: "architecture-check", Commands: []string{"deptrac"}, Pass: "no violations", Fail: "violation"},
	}
	require.NoError(t, WriteGoalMD(dir, "Provided", "", []string{"AC1"}, []string{"x"}, nil, "", "", invs))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Equal(t, 1, strings.Count(content, "## Investigation Config"))
	assert.Contains(t, content, "### Investigator 1: Quality gate")
	assert.Contains(t, content, "### Investigator 2: Test execution")
	assert.Contains(t, content, "### Investigator 3: Architecture check")
	assert.Contains(t, content, "- type: quality-gate")
	assert.Contains(t, content, "- command: phpstan analyse")
	assert.Contains(t, content, "- Pass: exit 0")
	assert.Contains(t, content, "- Fail: errors")

	cfgIdx := strings.Index(content, "## Investigation Config")
	revalIdx := strings.Index(content, "## Re-validation")
	assert.Greater(t, cfgIdx, 0)
	assert.Greater(t, revalIdx, cfgIdx, "Investigation Config must come before Re-validation")

	// In-order rendering
	assert.Less(t, strings.Index(content, "Investigator 1:"), strings.Index(content, "Investigator 2:"))
	assert.Less(t, strings.Index(content, "Investigator 2:"), strings.Index(content, "Investigator 3:"))
}

func TestWriteGoalMD_DerivesFallbackFromValidate(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse --level=9",
		"php bin/phpunit --testsuite=unit",
		"vendor/bin/deptrac analyse",
	}
	require.NoError(t, WriteGoalMD(dir, "Fallback", "", []string{"AC1"}, validate, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	qg := strings.Index(content, "- type: quality-gate")
	te := strings.Index(content, "- type: test-execution")
	ac := strings.Index(content, "- type: architecture-check")
	assert.Greater(t, qg, 0, "quality-gate present")
	assert.Greater(t, te, 0, "test-execution present")
	assert.Greater(t, ac, 0, "architecture-check present")
	assert.Less(t, qg, te, "quality-gate before test-execution")
	assert.Less(t, te, ac, "test-execution before architecture-check")
}

func TestWriteGoalMD_FallbackGuaranteesAtLeastTwo(t *testing.T) {
	for _, validate := range [][]string{
		{"vendor/bin/phpstan analyse"},
		{},
	} {
		dir := t.TempDir()
		require.NoError(t, WriteGoalMD(dir, "Few", "", []string{"AC1"}, validate, nil, "", "", nil))
		data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, strings.Count(string(data), "### Investigator "), 2,
			"validate=%v should yield >=2 investigators", validate)
	}
}

func TestWriteGoalMD_CapsAtFourInvestigators(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
		"npx eslint .",
		"npx playwright test",
	}
	require.NoError(t, WriteGoalMD(dir, "Many", "", []string{"AC1"}, validate, nil, "", "", nil))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(string(data), "### Investigator "))
}

func TestWriteGoalMD_SingleInvestigationConfigSection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Single", "", []string{"AC1"}, []string{"go test ./..."}, nil, "", "", nil))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "## Investigation Config"))
}

func TestWriteGoalMD_OptionalConditionRendered(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "With cond", Type: "static-analysis", Pass: "p", Fail: "f", Condition: "only when X"},
		{Name: "No cond", Type: "static-analysis", Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Cond", "", []string{"AC1"}, []string{"x"}, nil, "", "", invs))
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "- condition: only when X")
	assert.Equal(t, 1, strings.Count(content, "- condition:"), "exactly one condition line (the empty one omitted)")
}

// --- B3: emission / dead-choreography investigator -------------------------

func TestWriteGoalMD_EventGoal_RendersEmissionInvestigator(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Catalog reserves stock", "event",
		[]string{`src/Catalog/ constructs App\Share\Event\StockReserved`},
		[]string{"go build ./..."}, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Event emission")
	assert.Contains(t, content, "- type: emission-check")
}

func TestWriteGoalMD_EventGoal_EmissionSurvivesCap(t *testing.T) {
	dir := t.TempDir()
	validate := []string{
		"vendor/bin/phpstan analyse",
		"php bin/phpunit",
		"vendor/bin/deptrac analyse",
		"vendor/bin/ecs check",
	}
	require.NoError(t, WriteGoalMD(dir, `Catalog emits App\Share\Event\StockReserved`, "event",
		[]string{"AC1"}, validate, nil, "", "", nil))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)
	assert.LessOrEqual(t, strings.Count(content, "### Investigator "), 4)
	assert.Contains(t, content, "- type: emission-check")
}

func TestWriteGoalMD_ExplicitInvestigators_NoEmissionAppended(t *testing.T) {
	dir := t.TempDir()
	invs := []Investigator{
		{Name: "Q", Type: "quality-gate", Pass: "p", Fail: "f"},
		{Name: "T", Type: "test-execution", Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, `Catalog emits App\Share\Event\StockReserved`, "event",
		[]string{"AC1"}, []string{"x"}, nil, "", "", invs))

	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "emission-check")
}

// newOwnSuiteGoalDir lays out <root>/.tmux-cli/goals/goal-001 (the canonical
// goalDir shape ownSuiteFSRoot climbs back from) plus any given test-suite dirs
// under root. The selector resolves existence against root, so the suites listed
// here are exactly the ones the gate's phpunit scope will reference.
func newOwnSuiteGoalDir(t *testing.T, suites ...string) string {
	t.Helper()
	root := t.TempDir()
	goalDir := filepath.Join(root, ".tmux-cli", "goals", "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	for _, s := range suites {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(s)), 0o755))
	}
	return goalDir
}

func TestWriteGoalMD_SrcDeliverableGetsOwnSuiteGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "Own-suite green")
	assert.Contains(t, content, "- type: own-suite-green")
}

func TestWriteGoalMD_OwnSuiteGateUsesSelectorScope(t *testing.T) {
	// Only the Integration suite exists, so the selector scope is exactly it.
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "- type: own-suite-green")
	assert.Contains(t, content, "vendor/bin/phpunit tests/Integration/Catalog")
	assert.NotContains(t, content, "--filter")
}

func TestWriteGoalMD_OwnSuiteGateNeverUsesUnitFilter(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", nil))

	cmd := ownSuiteGateCommand(t, readGoalMD(t, dir))
	assert.NotContains(t, cmd, "--filter", "the gate must not run the unit --filter slice")
	assert.NotContains(t, cmd, "Domain", "the gate must not target the unit \\Domain regex")
	assert.Contains(t, cmd, "vendor/bin/phpunit tests/")
}

func TestWriteGoalMD_DocsGoalNoOwnSuiteGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t)
	require.NoError(t, WriteGoalMD(dir, "Update docs", "domain",
		[]string{"README explains the flow"}, []string{"markdownlint docs/"}, nil, "", "", nil))

	assert.NotContains(t, readGoalMD(t, dir), "own-suite-green",
		"a docs-only goal (no src/app deliverable) gets no own-suite gate")
}

func TestWriteGoalMD_ExplicitConfigStillGetsMandatoryGate(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "Stan", Type: "quality-gate", Commands: []string{"phpstan"}, Pass: "p", Fail: "f"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"phpunit"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Contains(t, content, "- type: own-suite-green",
		"the gate is appended even when the planner supplied an explicit config")
}

func TestWriteGoalMD_OwnSuiteGateNotDuplicated(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "Own", Type: "own-suite-green", Commands: []string{"vendor/bin/phpunit tests/Integration/Catalog"}, Pass: "p", Fail: "f"},
		{Name: "Stan", Type: "quality-gate", Commands: []string{"phpstan"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Equal(t, 1, strings.Count(content, "- type: own-suite-green"),
		"an explicit own-suite-green config must not be duplicated by the auto-append")
}

func TestWriteGoalMD_GatePinnedWhenCapExceeded(t *testing.T) {
	dir := newOwnSuiteGoalDir(t, "tests/Integration/Catalog", "tests/Functional/Catalog")
	invs := []Investigator{
		{Name: "A", Type: "test-execution", Commands: []string{"a"}, Pass: "p", Fail: "f"},
		{Name: "B", Type: "quality-gate", Commands: []string{"b"}, Pass: "p", Fail: "f"},
		{Name: "C", Type: "architecture-check", Commands: []string{"c"}, Pass: "p", Fail: "f"},
		{Name: "D", Type: "static-analysis", Commands: []string{"d"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(dir, "Reserve stock", "domain",
		[]string{"src/Catalog reserves stock"}, []string{"go build ./..."}, nil, "", "", invs))

	content := readGoalMD(t, dir)
	assert.Equal(t, 4, strings.Count(content, "### Investigator "),
		"section must hold exactly 4 investigators after the cap")
	assert.Contains(t, content, "- type: own-suite-green",
		"the mandatory gate survives the cap (lowest-priority explicit dropped)")
}

// readGoalMD reads the rendered goal.md from a goalDir.
func readGoalMD(t *testing.T, goalDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	return string(data)
}

// ownSuiteGateCommand extracts the own-suite-green investigator's command line
// from a rendered goal.md (the first `- command:` line following the
// `- type: own-suite-green` marker).
func ownSuiteGateCommand(t *testing.T, content string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "- type: own-suite-green" {
			continue
		}
		for _, after := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(after), "- command:") {
				return after
			}
			if strings.HasPrefix(after, "### Investigator ") {
				break
			}
		}
	}
	t.Fatalf("no own-suite-green command found in:\n%s", content)
	return ""
}

// strippedGoalMD is a goal.md whose `## Investigation Config` section was
// removed post-creation (the planner-re-write failure mode B4 repairs).
const strippedGoalMD = `# Reserve stock

## Acceptance Criteria

- Stock decrements on reserve
- No oversell

## Validation Rules

- vendor/bin/phpstan analyse
- vendor/bin/phpunit

## Not In Scope

UI redesign

## Re-validation

Incremental: only failed checks and checks whose inputs changed are re-run on retry.
`

// countInvestigatorsLikeParser mirrors parseGoalFindings (cmd/tmux-cli/session.go,
// package main — not importable here): it counts `### Investigator ` headings
// scoped to the `## Investigation Config` section, the same round-trip the
// validator relies on. Used to prove a repaired goal.md parses to >=2 findings.
func countInvestigatorsLikeParser(md string) int {
	n := 0
	section := ""
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if section == "Investigation Config" && strings.HasPrefix(line, "### ") {
			n++
		}
	}
	return n
}

func writeGoalMDRaw(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "goal.md"), []byte(content), 0o644))
}

func TestEnsureInvestigationConfig_NoopWhenValidSectionPresent(t *testing.T) {
	dir := t.TempDir()
	// A creation-time goal.md always carries a valid (>=2) section.
	require.NoError(t, WriteGoalMD(dir, "Valid goal", "", []string{"AC1"},
		[]string{"go test ./...", "go vet ./..."}, nil, "", "", nil))
	before, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)

	repaired, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired, "valid section must be a no-op")

	after, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "file bytes must be unchanged")
}

func TestEnsureInvestigationConfig_RepairsWhenSectionMissing(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, dir, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit"})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "## Investigation Config")
	hasSection, n := countInvestigators(string(out))
	assert.True(t, hasSection)
	assert.GreaterOrEqual(t, n, 2, "must end with >=2 investigators")
}

func TestEnsureInvestigationConfig_ReplacesMalformedSingleInvestigator(t *testing.T) {
	dir := t.TempDir()
	malformed := `# Goal

## Acceptance Criteria

- AC1

## Investigation Config

### Investigator 1: Lonely
- type: static-analysis
- command: go build ./...
- Pass: ok
- Fail: no

## Re-validation

Incremental.
`
	writeGoalMDRaw(t, dir, malformed)

	repaired, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./...", "go vet ./..."})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	hasSection, n := countInvestigators(string(out))
	assert.True(t, hasSection)
	assert.GreaterOrEqual(t, n, 2)
	assert.Equal(t, 1, strings.Count(string(out), "## Investigation Config"),
		"exactly one Investigation Config heading after replace")
	assert.NotContains(t, string(out), "Lonely", "the malformed section must be replaced, not kept")
}

func TestEnsureInvestigationConfig_DoesNotDuplicateSection(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out), "## Investigation Config"))

	// Idempotent: a second run is a no-op and still leaves exactly one.
	repaired2, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired2)
	out2, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out2), "## Investigation Config"))
}

func TestEnsureInvestigationConfig_PreservesSurroundingSections(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "# Reserve stock")
	assert.Contains(t, s, "- Stock decrements on reserve")
	assert.Contains(t, s, "- No oversell")
	assert.Contains(t, s, "## Not In Scope")
	assert.Contains(t, s, "UI redesign")
	assert.Contains(t, s, "## Re-validation")
}

func TestEnsureInvestigationConfig_InsertsBeforeReValidation(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	s := string(out)
	icIdx := strings.Index(s, "## Investigation Config")
	rvIdx := strings.Index(s, "## Re-validation")
	require.GreaterOrEqual(t, icIdx, 0)
	require.GreaterOrEqual(t, rvIdx, 0)
	assert.Less(t, icIdx, rvIdx, "section must be spliced BEFORE Re-validation")
}

func TestEnsureInvestigationConfig_EmptyValidateStillYieldsTwo(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	repaired, err := EnsureInvestigationConfig(dir, dir, nil)
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	_, n := countInvestigators(string(out))
	assert.GreaterOrEqual(t, n, 2, "Build-sanity padding guarantees >=2")
}

func TestEnsureInvestigationConfig_MissingFileIsNoop(t *testing.T) {
	dir := t.TempDir() // no goal.md written
	repaired, err := EnsureInvestigationConfig(dir, dir, []string{"go test ./..."})
	require.NoError(t, err)
	assert.False(t, repaired)
	_, statErr := os.Stat(filepath.Join(dir, "goal.md"))
	assert.True(t, os.IsNotExist(statErr), "must not create a goal.md when none existed")
}

func TestEnsureInvestigationConfig_RepairSurvivesParseGoalFindings(t *testing.T) {
	dir := t.TempDir()
	writeGoalMDRaw(t, dir, strippedGoalMD)

	_, err := EnsureInvestigationConfig(dir, dir, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit"})
	require.NoError(t, err)

	out, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, countInvestigatorsLikeParser(string(out)), 2,
		"the goal.md parser must see >=2 findings after repair")
}

func TestRenderInvestigationConfig_MatchesWriteGoalMDOutput(t *testing.T) {
	invs := []Investigator{
		{Name: "Quality gate", Type: "quality-gate", Paths: []string{"src/Foo"},
			Commands: []string{"phpstan analyse"}, Pass: "exit 0", Fail: "errors"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"phpunit"},
			Pass: "all green", Fail: "red", Condition: "when changed"},
	}
	var b strings.Builder
	renderInvestigationConfig(&b, invs, LocalExecRuntime())
	section := b.String()

	// WriteGoalMD with these explicit investigators (no src/ deliverable, no
	// event phase) embeds exactly this list — its file must CONTAIN the helper's
	// byte-for-byte output, proving the extraction introduced no drift.
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Parity", "", []string{"AC1"},
		[]string{"x"}, nil, "", "", invs))
	full, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(full), section,
		"WriteGoalMD must embed renderInvestigationConfig output verbatim")
}

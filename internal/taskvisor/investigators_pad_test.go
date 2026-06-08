package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- F1 (RC-B): project-aware fallback investigators ------------------------
//
// deriveInvestigators pads the Investigation Config to >=2. The pad must be
// PROJECT-AWARE (marker files at projectRoot, first match wins) and the two
// pad entries must never be identical — in a PHP project a hardcoded
// `go build ./...` pad manufactured a guaranteed validation failure every
// cycle (test-project goal-051: budget exhausted + cascade).

// padRoot creates an isolated project root containing the given marker files.
func padRoot(t *testing.T, markers ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, m := range markers {
		require.NoError(t, os.WriteFile(filepath.Join(root, m), []byte("x"), 0o644))
	}
	return root
}

// padCommands flattens all investigator commands into one string for
// contains-assertions on the padded entries.
func padCommands(list []Investigator) string {
	var all []string
	for _, inv := range list {
		all = append(all, inv.Commands...)
	}
	return strings.Join(all, "\n")
}

func TestDeriveInvestigators_GoProject(t *testing.T) {
	root := padRoot(t, "go.mod")

	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2, "empty validate must still pad to >=2")
	assert.Contains(t, padCommands(list), "go build ./...",
		"a go.mod project pads with the go build sanity check")
}

func TestDeriveInvestigators_PHPProject(t *testing.T) {
	root := padRoot(t, "composer.json")

	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2)
	cmds := padCommands(list)
	assert.Contains(t, cmds, "composer validate --no-check-publish --no-check-all",
		"a composer.json project pads with the composer sanity check")
	assert.NotContains(t, cmds, "go build",
		"a PHP project must NEVER receive the go build pad (RC-B)")
}

func TestDeriveInvestigators_NodeProject(t *testing.T) {
	root := padRoot(t, "package.json")

	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2)
	cmds := padCommands(list)
	assert.Contains(t, cmds, "npm ls --depth=0",
		"a package.json project pads with the node/npm sanity check")
	assert.NotContains(t, cmds, "go build")
}

func TestDeriveInvestigators_MakefileOnly(t *testing.T) {
	root := padRoot(t, "Makefile")

	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2)
	cmds := padCommands(list)
	assert.Contains(t, cmds, "make -n test",
		"a Makefile-only project pads with the make dry-run check")
	assert.NotContains(t, cmds, "go build")
}

func TestDeriveInvestigators_UnknownStack(t *testing.T) {
	root := padRoot(t) // no marker files at all

	list := deriveInvestigators(root, nil, nil)

	require.GreaterOrEqual(t, len(list), 2)
	assert.Equal(t, "Workspace sanity", list[0].Name,
		"unknown stack pads with the always-pass workspace check")
	require.NotEmpty(t, list[0].Commands)
	assert.Equal(t, "test -d .", list[0].Commands[0],
		"the unknown-stack pad must be an always-pass existence check, never a fake failure")
	assert.Equal(t, "static-analysis", list[0].Type)
}

func TestDeriveInvestigators_NoDuplicatePad(t *testing.T) {
	root := padRoot(t, "go.mod")

	list := deriveInvestigators(root, nil, nil) // empty validate → BOTH entries are pads

	require.Len(t, list, 2)
	assert.NotEqual(t, list[0].Name, list[1].Name,
		"two pad entries must carry DIFFERENT names")
	assert.NotEqual(t, list[0].Commands, list[1].Commands,
		"two pad entries must run DIFFERENT commands — never two identical pads")
}

func TestDeriveInvestigators_ExplicitRulesUnchanged(t *testing.T) {
	root := padRoot(t, "composer.json")
	validate := []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit --testsuite unit"}

	list := deriveInvestigators(root, validate, nil)

	// Non-empty validate maps 1:1 via inferInvestigatorType — no pad appended
	// when the rules already satisfy the >=2 floor.
	require.Len(t, list, 2)
	assert.Equal(t, "quality-gate", list[0].Type)
	assert.Equal(t, []string{"vendor/bin/phpstan analyse"}, list[0].Commands)
	assert.Equal(t, "test-execution", list[1].Type)
	assert.Equal(t, []string{"vendor/bin/phpunit --testsuite unit"}, list[1].Commands)

	// A single rule still maps 1:1; the pad only FILLS to 2 (project-aware).
	single := deriveInvestigators(root, []string{"vendor/bin/phpstan analyse"}, nil)
	require.Len(t, single, 2)
	assert.Equal(t, []string{"vendor/bin/phpstan analyse"}, single[0].Commands)
	assert.Contains(t, strings.Join(single[1].Commands, " "), "composer validate",
		"the fill-to-2 pad is project-aware, not hardcoded go build")
}

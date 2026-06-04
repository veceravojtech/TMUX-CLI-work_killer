package taskvisor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stageDirs creates the given slash-separated relative dirs under root.
func stageDirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
}

func TestSelectOwnSuiteScope_SingleBC_BothSuitesExist(t *testing.T) {
	root := t.TempDir()
	stageDirs(t, root, "tests/Integration/Catalog", "tests/Functional/Catalog")

	scope := SelectOwnSuiteScope([]string{"src/Catalog/Domain/Sku.php"}, root)

	assert.False(t, scope.Empty)
	assert.Equal(t, []string{"Catalog"}, scope.BoundedContexts)
	assert.Equal(t, []string{"tests/Functional/Catalog", "tests/Integration/Catalog"}, scope.Paths)
	assert.Equal(t, "vendor/bin/phpunit tests/Functional/Catalog tests/Integration/Catalog", scope.Command)
}

func TestSelectOwnSuiteScope_MultiBC_UnionSorted(t *testing.T) {
	root := t.TempDir()
	stageDirs(t, root,
		"tests/Integration/Catalog", "tests/Functional/Catalog",
		"tests/Integration/Order", "tests/Functional/Order",
	)

	scope := SelectOwnSuiteScope([]string{
		"src/Order/Domain/Order.php",
		"src/Catalog/Domain/Sku.php",
	}, root)

	assert.False(t, scope.Empty)
	assert.Equal(t, []string{"Catalog", "Order"}, scope.BoundedContexts)
	assert.Equal(t, []string{
		"tests/Functional/Catalog",
		"tests/Functional/Order",
		"tests/Integration/Catalog",
		"tests/Integration/Order",
	}, scope.Paths)
}

func TestSelectOwnSuiteScope_OnlyIntegrationDirExists(t *testing.T) {
	root := t.TempDir()
	stageDirs(t, root, "tests/Integration/Catalog") // no Functional/Catalog

	scope := SelectOwnSuiteScope([]string{"src/Catalog/Domain/Sku.php"}, root)

	assert.False(t, scope.Empty)
	assert.Equal(t, []string{"tests/Integration/Catalog"}, scope.Paths)
	assert.Equal(t, "vendor/bin/phpunit tests/Integration/Catalog", scope.Command)
}

func TestSelectOwnSuiteScope_NoTestDirs_EmptyScope(t *testing.T) {
	root := t.TempDir() // nothing staged

	scope := SelectOwnSuiteScope([]string{"src/Catalog/Domain/Sku.php"}, root)

	assert.True(t, scope.Empty)
	assert.Equal(t, "", scope.Command)
	assert.Empty(t, scope.Paths)
}

func TestSelectOwnSuiteScope_NoSrcDeliverables_EmptyScope(t *testing.T) {
	root := t.TempDir()

	scope := SelectOwnSuiteScope([]string{"docs/x.md", "config/y.yaml"}, root)

	assert.True(t, scope.Empty)
	assert.Empty(t, scope.BoundedContexts)
	assert.Equal(t, "", scope.Command)
}

func TestSelectOwnSuiteScope_ShareKernelSkipped(t *testing.T) {
	root := t.TempDir()
	// Even if a Share suite dir existed it must be skipped; stage one to prove it.
	stageDirs(t, root, "tests/Integration/Share", "tests/Functional/Share")

	scope := SelectOwnSuiteScope([]string{"src/Share/Event/Foo.php"}, root)

	assert.True(t, scope.Empty)
	assert.Empty(t, scope.BoundedContexts)
	assert.Equal(t, "", scope.Command)
}

func TestSelectOwnSuiteScope_DeterministicAndDeduped(t *testing.T) {
	root := t.TempDir()
	stageDirs(t, root, "tests/Integration/Catalog", "tests/Functional/Catalog")

	deliverables := []string{
		"`src/Catalog/Domain/Sku.php`",
		"src/Catalog/Domain/Sku.php",
		"src/Catalog/Application/Handler.php",
	}

	first := SelectOwnSuiteScope(deliverables, root)
	second := SelectOwnSuiteScope(deliverables, root)

	assert.True(t, reflect.DeepEqual(first, second), "selector output must be deterministic")
	assert.Equal(t, []string{"Catalog"}, first.BoundedContexts, "BCs must be deduped")
	assert.Equal(t, []string{"tests/Functional/Catalog", "tests/Integration/Catalog"}, first.Paths)
}

func TestSelectOwnSuiteScope_UsesDirectoryPathsNotFilter(t *testing.T) {
	root := t.TempDir()
	stageDirs(t, root, "tests/Integration/Catalog", "tests/Functional/Catalog")

	scope := SelectOwnSuiteScope([]string{"src/Catalog/Domain/Sku.php"}, root)

	assert.NotContains(t, scope.Command, "--filter")
	assert.NotContains(t, scope.Command, `\Domain`)
	assert.True(t, strings.HasPrefix(scope.Command, "vendor/bin/phpunit "))
}

func TestDeliverablesFromGoal_ExtractsSrcTokensFromAcceptanceAndValidate(t *testing.T) {
	g := Goal{
		Acceptance: []string{"file `src/Catalog/Domain/Sku.php` exists and is typed"},
		Validate:   []string{"phpstan analyse src/Catalog/Domain --level=max"},
	}

	got := DeliverablesFromGoal(g)

	assert.Contains(t, got, "src/Catalog/Domain/Sku.php")
	assert.Contains(t, got, "src/Catalog/Domain")
	assert.Len(t, got, 2, "distinct src tokens from both fields")
}

func TestDeliverablesFromGoal_IgnoresNonSrcTokens(t *testing.T) {
	g := Goal{
		Validate: []string{"vendor/bin/phpunit --filter=CatalogDomain"},
	}

	got := DeliverablesFromGoal(g)

	assert.Empty(t, got)
}

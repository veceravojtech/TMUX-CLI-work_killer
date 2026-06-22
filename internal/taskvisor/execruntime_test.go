package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestEnvMD(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "docs", "architecture")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-environment.md"), []byte(body), 0o644))
}

func TestResolveExecRuntime_DockerWithFrontend(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `# Test Environment
**Run Target:** docker
**Playwright Status:** installed and configured

| Service | Host Port | Container Port | Purpose |
|---------|-----------|----------------|---------|
| app | 8080 | 80 | HTTP app |
| db  | 5432 | 5432 | Postgres |
`)
	er := ResolveExecRuntime(root)
	assert.Equal(t, "docker", er.RunTarget)
	assert.Equal(t, "app", er.AppSvc)
	assert.Equal(t, "e2e", er.NodeSvc)
}

func TestResolveExecRuntime_DockerApiOnly_NoNodeSvc(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**Playwright Status:** not applicable (API-only)
`)
	er := ResolveExecRuntime(root)
	assert.Equal(t, "docker", er.RunTarget)
	assert.Equal(t, "app", er.AppSvc)
	assert.Equal(t, "", er.NodeSvc)
}

func TestResolveExecRuntime_Local(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, "**Run Target:** local\n")
	assert.Equal(t, "local", ResolveExecRuntime(root).RunTarget)
}

func TestResolveExecRuntime_MissingFile_DefaultsLocal(t *testing.T) {
	assert.Equal(t, "local", ResolveExecRuntime(t.TempDir()).RunTarget)
}

func TestResolveExecRuntime_AppServiceFromPublishedPorts(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**Playwright Status:** not applicable

| Service | Host Port | Container Port | Purpose |
|---------|-----------|----------------|---------|
| php | 9000 | 9000 | php-fpm |
| nginx | 8080 | 80 | web |
`)
	assert.Equal(t, "php", ResolveExecRuntime(root).AppSvc)
}

func TestResolveExecRuntime_AppServiceFromDocumentedField(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**Runtime Container:** php
**Playwright Status:** not applicable
`)
	assert.Equal(t, "php", ResolveExecRuntime(root).AppSvc)
}

func TestResolveExecRuntime_DocumentedFieldArbitraryName(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**APP service:** web-app
**Playwright Status:** not applicable
`)
	assert.Equal(t, "web-app", ResolveExecRuntime(root).AppSvc)
}

func TestResolveExecRuntime_DocumentedFieldWinsOverPublishedPorts(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**Runtime Container:** php
**Playwright Status:** not applicable

| Service | Host Port | Container Port | Purpose |
|---------|-----------|----------------|---------|
| app | 8080 | 80 | HTTP app |
`)
	assert.Equal(t, "php", ResolveExecRuntime(root).AppSvc)
}

func TestResolveExecRuntime_ComposeProjectFromBasename(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, "**Run Target:** docker\n**Playwright Status:** not applicable\n")
	er := ResolveExecRuntime(root)
	want := normalizeComposeName(filepath.Base(root))
	assert.NotEmpty(t, want)
	assert.Equal(t, want, er.ComposeProject)
}

func TestResolveExecRuntime_ComposeProjectFromEnvFile(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, "**Run Target:** docker\n**Playwright Status:** not applicable\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("COMPOSE_PROJECT_NAME=previo2\n"), 0o644))
	assert.Equal(t, "previo2", ResolveExecRuntime(root).ComposeProject)
}

func TestResolveExecRuntime_ComposeProjectFromDocumentedField(t *testing.T) {
	root := t.TempDir()
	// Documented field wins over the (differing) basename heuristic.
	writeTestEnvMD(t, root, "**Run Target:** docker\n**Compose Project:** previo2\n**Playwright Status:** not applicable\n")
	er := ResolveExecRuntime(root)
	assert.Equal(t, "previo2", er.ComposeProject)
	assert.NotEqual(t, normalizeComposeName(filepath.Base(root)), er.ComposeProject)
}

func TestResolveComposeProject_StableAcrossWorktree(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, ".tmux-cli-worktrees", "goal-005")
	got := resolveComposeProject(projectRoot, "")
	assert.Equal(t, normalizeComposeName(filepath.Base(base)), got)
	assert.NotEqual(t, "goal-005", got)
}

func TestResolveExecRuntime_LocalHasNoComposeProject(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, "**Run Target:** local\n")
	er := ResolveExecRuntime(root)
	assert.Equal(t, "local", er.RunTarget)
	assert.Equal(t, "", er.ComposeProject)
}

func TestNormalizeComposeName_Lowercases(t *testing.T) {
	assert.Equal(t, "myapp", normalizeComposeName("My.App"))
	assert.Equal(t, "foo-1", normalizeComposeName("_foo-1"))
}

func TestResolveExecRuntime_NoServiceDefaultsApp(t *testing.T) {
	root := t.TempDir()
	writeTestEnvMD(t, root, `**Run Target:** docker
**Playwright Status:** not applicable

| Service | Host Port | Container Port | Purpose |
|---------|-----------|----------------|---------|
| nginx | 8080 | 80 | web |
`)
	assert.Equal(t, "app", ResolveExecRuntime(root).AppSvc)
}

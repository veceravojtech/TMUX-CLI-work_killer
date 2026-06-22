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

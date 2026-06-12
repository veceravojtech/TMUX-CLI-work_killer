package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRulesTree materializes a minimal on-disk catalogue so Resolve's
// existence checks pass.
func writeRulesTree(t *testing.T, root string, manifest string, files []string) {
	t.Helper()
	dir := filepath.Join(root, RulesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const testManifest = `
version: 1
packs:
  - id: _base
    conventions: [a.md]
  - id: docker
    when: { run_target: docker }
    conventions: [d.md]
  - id: database
    when: { has_database: true }
    conventions: [db.md]
  - id: frontend
    when: { has_frontend: true }
    conventions: [fe.md]
  - id: frontend-auth
    when: { has_frontend: true, min_auth_flows: 1 }
    conventions: [auth.md]
  - id: php
    when: { lang: php }
    code_rules: [code-rules.yaml]
  - id: php-symfony
    when: { lang: php, framework: symfony }
    code_rules: [code-rules.yaml]
`

var testFiles = []string{
	"_base/a.md", "docker/d.md", "database/db.md", "frontend/fe.md",
	"frontend-auth/auth.md", "php/code-rules.yaml", "php-symfony/code-rules.yaml",
}

func resolvePacks(t *testing.T, sig Signals) (packs []string, warnings []string) {
	t.Helper()
	root := t.TempDir()
	writeRulesTree(t, root, testManifest, testFiles)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	files, warns, err := Resolve(root, m, sig)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !seen[f.Pack] {
			seen[f.Pack] = true
			packs = append(packs, f.Pack)
		}
	}
	return packs, warns
}

func assertPacks(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("packs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("packs = %v, want %v", got, want)
		}
	}
}

func TestResolve_FullStackSymfony(t *testing.T) {
	packs, warns := resolvePacks(t, Signals{
		Lang: "php", Framework: "symfony", RunTarget: "docker",
		HasDatabase: TriYes, HasFrontend: TriYes, NAuthFlows: 2,
	})
	assertPacks(t, packs, []string{"_base", "docker", "database", "frontend", "frontend-auth", "php", "php-symfony"})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
}

func TestResolve_PhpApiOnlyLocal(t *testing.T) {
	packs, _ := resolvePacks(t, Signals{
		Lang: "php", Framework: "symfony", RunTarget: "local",
		HasDatabase: TriYes, HasFrontend: TriNo, NAuthFlows: 1,
	})
	assertPacks(t, packs, []string{"_base", "database", "php", "php-symfony"})
}

func TestResolve_GoProjectNoStackRules(t *testing.T) {
	packs, _ := resolvePacks(t, Signals{
		Lang: "go", RunTarget: "local",
		HasDatabase: TriYes, HasFrontend: TriNo, NAuthFlows: 0,
	})
	assertPacks(t, packs, []string{"_base", "database"})
}

func TestResolve_UnknownCapabilitiesConservative(t *testing.T) {
	// No discovery docs: capability packs load conservatively WITH warnings;
	// stack packs stay out (unknown lang must not pull php rules).
	packs, warns := resolvePacks(t, Signals{NAuthFlows: -1})
	assertPacks(t, packs, []string{"_base", "docker", "database", "frontend", "frontend-auth"})
	if len(warns) == 0 {
		t.Fatal("conservative inclusion must warn")
	}
}

func TestResolve_AuthFlowsZeroExcludesFrontendAuth(t *testing.T) {
	packs, _ := resolvePacks(t, Signals{
		Lang: "php", RunTarget: "local",
		HasDatabase: TriNo, HasFrontend: TriYes, NAuthFlows: 0,
	})
	assertPacks(t, packs, []string{"_base", "frontend", "php"})
}

func TestResolve_LocalRulesAlwaysIncluded(t *testing.T) {
	root := t.TempDir()
	writeRulesTree(t, root, testManifest, append(testFiles,
		"local/conventions/house.md", "local/code-rules/team.yaml"))
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := Resolve(root, m, Signals{Lang: "go", RunTarget: "local", HasDatabase: TriNo, HasFrontend: TriNo, NAuthFlows: 0})
	if err != nil {
		t.Fatal(err)
	}
	var gotLocal []string
	for _, f := range files {
		if f.Pack == "local" {
			gotLocal = append(gotLocal, f.Path+"|"+f.Kind)
		}
	}
	want := []string{
		".tmux-cli/rules/local/code-rules/team.yaml|" + KindCodeRules,
		".tmux-cli/rules/local/conventions/house.md|" + KindConvention,
	}
	if len(gotLocal) != 2 || gotLocal[0] != want[0] || gotLocal[1] != want[1] {
		t.Fatalf("local files = %v, want %v", gotLocal, want)
	}
}

func TestResolve_MissingFileErrors(t *testing.T) {
	root := t.TempDir()
	writeRulesTree(t, root, testManifest, testFiles[:len(testFiles)-1]) // drop one file
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Resolve(root, m, Signals{Lang: "php", Framework: "symfony", RunTarget: "local", HasDatabase: TriNo, HasFrontend: TriNo, NAuthFlows: 0})
	if err == nil {
		t.Fatal("missing materialized file must error")
	}
}

func TestDetect_SymfonyComposer(t *testing.T) {
	root := t.TempDir()
	composer := `{"require": {"php": ">=8.2", "symfony/framework-bundle": "^7.0"}}`
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(composer), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := Detect(root)
	if sig.Lang != "php" || sig.Framework != "symfony" {
		t.Fatalf("got %s/%s, want php/symfony", sig.Lang, sig.Framework)
	}
	if sig.HasDatabase != TriUnknown || sig.RunTarget != "" || sig.NAuthFlows != -1 {
		t.Fatalf("capabilities must be unknown without discovery docs: %+v", sig)
	}
}

func TestDetect_DiscoveryDocs(t *testing.T) {
	root := t.TempDir()
	arch := filepath.Join(root, "docs", "architecture")
	if err := os.MkdirAll(arch, 0o755); err != nil {
		t.Fatal(err)
	}
	testEnv := `# Test Environment
**Base URL:** http://localhost:8080
**Run Target:** docker
**Test Database:** app_test
**Playwright Status:** installed and configured

**Symfony-specific:**
- PHPUnit config: phpunit.xml.dist
`
	crossCutting := `# Cross-cutting
## Frontend Presence
Yes, server-rendered Twig.
## Auth Flows
- Register
- Login
- Logout
## Event Listeners
- none
`
	if err := os.WriteFile(filepath.Join(arch, "test-environment.md"), []byte(testEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "cross-cutting.md"), []byte(crossCutting), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := Detect(root)
	if sig.RunTarget != "docker" {
		t.Fatalf("RunTarget = %q, want docker", sig.RunTarget)
	}
	if sig.HasDatabase != TriYes {
		t.Fatalf("HasDatabase = %v, want yes", sig.HasDatabase)
	}
	if sig.HasFrontend != TriYes {
		t.Fatalf("HasFrontend = %v, want yes", sig.HasFrontend)
	}
	if sig.NAuthFlows != 3 {
		t.Fatalf("NAuthFlows = %d, want 3", sig.NAuthFlows)
	}
	// Greenfield: no composer.json yet, but test-environment.md names Symfony.
	if sig.Lang != "php" || sig.Framework != "symfony" {
		t.Fatalf("greenfield symfony fallback failed: %s/%s", sig.Lang, sig.Framework)
	}
}

func TestDetect_PlaywrightNotApplicable(t *testing.T) {
	root := t.TempDir()
	arch := filepath.Join(root, "docs", "architecture")
	if err := os.MkdirAll(arch, 0o755); err != nil {
		t.Fatal(err)
	}
	testEnv := "**Run Target:** local\n**Test Database:** none\n**Playwright Status:** not applicable (API-only)\n"
	if err := os.WriteFile(filepath.Join(arch, "test-environment.md"), []byte(testEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := Detect(root)
	if sig.RunTarget != "local" || sig.HasDatabase != TriNo || sig.HasFrontend != TriNo {
		t.Fatalf("unexpected signals: %+v", sig)
	}
}

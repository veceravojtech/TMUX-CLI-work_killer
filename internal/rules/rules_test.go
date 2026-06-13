package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProjectFile materializes one file (creating parent dirs) under root.
func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

func TestDetect_StackLineOverridesManifest(t *testing.T) {
	// An explicit **Stack:** line beats project-manifest detection: a php
	// composer.json present, but the doc declares go-gin.
	root := t.TempDir()
	writeProjectFile(t, root, "composer.json", `{"require":{"symfony/framework-bundle":"^7.0"}}`)
	writeProjectFile(t, root, "docs/architecture/test-environment.md",
		"# Test Environment\n**Stack:** go-gin\n**Run Target:** local\n")
	sig := Detect(root)
	if sig.Lang != "go" || sig.Framework != "gin" {
		t.Fatalf("Stack line must win: got %s/%s, want go/gin", sig.Lang, sig.Framework)
	}
}

func TestDetect_StackLineParsesLangFramework(t *testing.T) {
	// Split on the FIRST hyphen.
	root := t.TempDir()
	writeProjectFile(t, root, "docs/architecture/test-environment.md", "**Stack:** php-symfony\n")
	if sig := Detect(root); sig.Lang != "php" || sig.Framework != "symfony" {
		t.Fatalf("got %s/%s, want php/symfony", sig.Lang, sig.Framework)
	}
	// No hyphen → framework empty.
	root2 := t.TempDir()
	writeProjectFile(t, root2, "docs/architecture/test-environment.md", "**Stack:** go\n")
	if sig := Detect(root2); sig.Lang != "go" || sig.Framework != "" {
		t.Fatalf("got %s/%s, want go/<empty>", sig.Lang, sig.Framework)
	}
}

func TestDetect_StackLineAbsentFallsThroughToDetectStack(t *testing.T) {
	// No Stack line: manifest detection drives lang/framework.
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/x\n\ngo 1.25\n")
	writeProjectFile(t, root, "docs/architecture/test-environment.md", "**Run Target:** local\n")
	if sig := Detect(root); sig.Lang != "go" {
		t.Fatalf("manifest fall-through failed: got lang=%q, want go", sig.Lang)
	}
	// No Stack line, no manifest, but a symfony mention: the last-tier
	// symfony fallback stays intact.
	root2 := t.TempDir()
	writeProjectFile(t, root2, "docs/architecture/test-environment.md",
		"**Run Target:** local\n\n**Symfony-specific:**\n- PHPUnit config: phpunit.xml.dist\n")
	if sig := Detect(root2); sig.Lang != "php" || sig.Framework != "symfony" {
		t.Fatalf("symfony fallback broken: got %s/%s, want php/symfony", sig.Lang, sig.Framework)
	}
}

func TestDetect_UsesJWTFromComposerLexikOrFirebase(t *testing.T) {
	for _, pkg := range []string{"lexik/jwt-authentication-bundle", "firebase/php-jwt"} {
		root := t.TempDir()
		writeProjectFile(t, root, "composer.json", `{"require":{"`+pkg+`":"^2.0"}}`)
		if sig := Detect(root); sig.UsesJWT != TriYes {
			t.Fatalf("composer %s must set UsesJWT=yes, got %v", pkg, sig.UsesJWT)
		}
	}
}

func TestDetect_UsesJWTFromCrossCuttingSecurity(t *testing.T) {
	// Security section names JWT → yes.
	root := t.TempDir()
	writeProjectFile(t, root, "docs/architecture/cross-cutting.md",
		"## Security\n- Auth via JWT bearer tokens\n")
	if sig := Detect(root); sig.UsesJWT != TriYes {
		t.Fatalf("JWT in security section must set yes, got %v", sig.UsesJWT)
	}
	// Security section present without JWT → no.
	root2 := t.TempDir()
	writeProjectFile(t, root2, "docs/architecture/cross-cutting.md",
		"## Security\n- Session cookies only, no tokens\n")
	if sig := Detect(root2); sig.UsesJWT != TriNo {
		t.Fatalf("security section without JWT must set no, got %v", sig.UsesJWT)
	}
	// Neither composer nor security section → unknown.
	root3 := t.TempDir()
	writeProjectFile(t, root3, "docs/architecture/cross-cutting.md",
		"## Auth Flows\n- Login\n- Logout\n")
	if sig := Detect(root3); sig.UsesJWT != TriUnknown {
		t.Fatalf("no JWT signal must stay unknown, got %v", sig.UsesJWT)
	}
}

func TestDetect_HasMailerMessengerHTTPClientFromComposer(t *testing.T) {
	// All three present → yes.
	root := t.TempDir()
	writeProjectFile(t, root, "composer.json",
		`{"require":{"symfony/mailer":"^7","symfony/messenger":"^7","symfony/http-client":"^7"}}`)
	sig := Detect(root)
	if sig.HasMailer != TriYes || sig.HasMessenger != TriYes || sig.HasHTTPClient != TriYes {
		t.Fatalf("want all yes, got mailer=%v messenger=%v http=%v", sig.HasMailer, sig.HasMessenger, sig.HasHTTPClient)
	}
	// composer present without the keys → no.
	root2 := t.TempDir()
	writeProjectFile(t, root2, "composer.json", `{"require":{"symfony/framework-bundle":"^7"}}`)
	sig2 := Detect(root2)
	if sig2.HasMailer != TriNo || sig2.HasMessenger != TriNo || sig2.HasHTTPClient != TriNo {
		t.Fatalf("want all no, got mailer=%v messenger=%v http=%v", sig2.HasMailer, sig2.HasMessenger, sig2.HasHTTPClient)
	}
	// no composer → unknown.
	root3 := t.TempDir()
	writeProjectFile(t, root3, "go.mod", "module example.com/x\n")
	sig3 := Detect(root3)
	if sig3.HasMailer != TriUnknown || sig3.HasMessenger != TriUnknown || sig3.HasHTTPClient != TriUnknown {
		t.Fatalf("want all unknown, got mailer=%v messenger=%v http=%v", sig3.HasMailer, sig3.HasMessenger, sig3.HasHTTPClient)
	}
}

func TestDetect_NBoundedContextsCounts(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "docs/architecture/bounded-contexts.md",
		"# Bounded Contexts\n## Bounded Context Inventory\n### Booking\nblah\n### Billing\nblah\n### Guest\nblah\n## Relationships\n### Booking -> Billing\n")
	if sig := Detect(root); sig.NBoundedContexts != 3 {
		t.Fatalf("NBoundedContexts = %d, want 3", sig.NBoundedContexts)
	}
	// Absent file → -1 (unknown).
	root2 := t.TempDir()
	if sig := Detect(root2); sig.NBoundedContexts != -1 {
		t.Fatalf("absent bounded-contexts.md must be -1, got %d", sig.NBoundedContexts)
	}
}

func TestDetect_FrontendModeExplicitLine(t *testing.T) {
	// An explicit **Frontend:** line is authoritative for each accepted mode.
	for _, mode := range []string{"vue", "twig", "none"} {
		root := t.TempDir()
		writeProjectFile(t, root, "docs/architecture/test-environment.md",
			"# Test Environment\n**Stack:** php-symfony\n**Frontend:** "+mode+"\n**Run Target:** local\n")
		if sig := Detect(root); sig.FrontendMode != mode {
			t.Fatalf("explicit **Frontend:** %s → FrontendMode=%q, want %q", mode, sig.FrontendMode, mode)
		}
	}
}

func TestParseFrontendMode_DerivesFromHasFrontend(t *testing.T) {
	// No explicit line: derive from frontend presence (parseHasFrontend).
	// Present + no "twig" mention → generic-Vue default.
	if got := parseFrontendMode("**Playwright Status:** installed and configured\n"); got != "vue" {
		t.Fatalf("present frontend, no twig → %q, want vue", got)
	}
	// Present AND body mentions twig → twig.
	if got := parseFrontendMode("**Playwright Status:** installed\nServer-rendered Twig templates.\n"); got != "twig" {
		t.Fatalf("present frontend, twig mention → %q, want twig", got)
	}
	// Absent frontend → none.
	if got := parseFrontendMode("**Playwright Status:** not applicable (API-only)\n"); got != "none" {
		t.Fatalf("absent frontend → %q, want none", got)
	}
	// No Playwright line, no explicit line → unknown.
	if got := parseFrontendMode("**Run Target:** local\n"); got != "" {
		t.Fatalf("no frontend signal → %q, want empty", got)
	}
}

func TestParseFrontendMode_JunkValueFallsThrough(t *testing.T) {
	// An unrecognized explicit value is ignored; with no frontend signal the
	// derivation yields unknown (empty).
	if got := parseFrontendMode("**Frontend:** angular\n"); got != "" {
		t.Fatalf("junk value → %q, want empty (line ignored, derivation unknown)", got)
	}
}

func TestResolve_FrontendModeConditionMatches(t *testing.T) {
	const manifest = `
version: 1
packs:
  - id: _base
    conventions: [a.md]
  - id: frontend-vue
    when: { frontend_mode: vue }
    conventions: [fe-vue.md]
`
	files := []string{"_base/a.md", "frontend-vue/fe-vue.md"}
	resolve := func(sig Signals) (packs, warns []string) {
		root := t.TempDir()
		writeRulesTree(t, root, manifest, files)
		m, err := LoadManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		rfiles, w, err := Resolve(root, m, sig)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, f := range rfiles {
			if !seen[f.Pack] {
				seen[f.Pack] = true
				packs = append(packs, f.Pack)
			}
		}
		return packs, w
	}

	// Known match (vue) includes the pack with NO conservative warning.
	packs, warns := resolve(Signals{FrontendMode: "vue"})
	assertPacks(t, packs, []string{"_base", "frontend-vue"})
	if len(warns) != 0 {
		t.Fatalf("known stack-signal match must not warn, got %v", warns)
	}
	// Mismatched mode (twig) excludes the pack, NO warning.
	packs, warns = resolve(Signals{FrontendMode: "twig"})
	assertPacks(t, packs, []string{"_base"})
	if len(warns) != 0 {
		t.Fatalf("mismatched stack signal must exclude with no warning, got %v", warns)
	}
	// Unknown mode ("") excludes the pack with NO conservative warning
	// (stack-style, not capability-style include-with-warning).
	packs, warns = resolve(Signals{FrontendMode: ""})
	assertPacks(t, packs, []string{"_base"})
	if len(warns) != 0 {
		t.Fatalf("unknown frontend_mode must exclude with NO conservative warning, got %v", warns)
	}
}

func TestSignals_MarshalJSON_FrontendModeKey(t *testing.T) {
	data, err := json.Marshal(Signals{FrontendMode: "vue"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"frontend_mode":"vue"`) {
		t.Fatalf("marshaled signals %s missing \"frontend_mode\":\"vue\"", data)
	}
	// An empty mode must still marshal the key (zero value = unknown).
	empty, err := json.Marshal(Signals{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"frontend_mode":""`) {
		t.Fatalf("empty frontend_mode must marshal the key, got %s", empty)
	}
}

func TestSignals_MarshalJSON_UsesJWTKeyAndTriStrings(t *testing.T) {
	sig := Signals{UsesJWT: TriUnknown, HasDatabase: TriYes, HasFrontend: TriNo, NAuthFlows: -1, NBoundedContexts: -1}
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"uses_jwt":"unknown"`, `"has_database":"yes"`, `"has_frontend":"no"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("marshaled signals %s missing %s", s, want)
		}
	}
	// Unknown must stay distinguishable from No.
	u, _ := TriUnknown.MarshalJSON()
	n, _ := TriNo.MarshalJSON()
	y, _ := TriYes.MarshalJSON()
	if string(u) != `"unknown"` || string(n) != `"no"` || string(y) != `"yes"` {
		t.Fatalf("Tri JSON forms wrong: %s/%s/%s", u, n, y)
	}
	if string(u) == string(n) {
		t.Fatal("unknown and no must not marshal identically")
	}
}

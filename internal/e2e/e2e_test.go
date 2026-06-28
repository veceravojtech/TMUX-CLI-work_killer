package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSlugifyScenario(t *testing.T) {
	cases := map[string]string{
		"Symfony DDD app with dashboard - no login": "symfony-ddd-app-with-dashboard-no-login",
		"  trailing/leading  ":                      "trailing-leading",
		"":                                          "symfony-dashboard-login", // blank → default
		"!!!":                                       "symfony-dashboard-login", // unusable → default
		"already-a-slug":                            "already-a-slug",
		"MiXeD_Case 123":                            "mixed-case-123",
	}
	for in, want := range cases {
		if got := SlugifyScenario(in); got != want {
			t.Errorf("SlugifyScenario(%q) = %q, want %q", in, got, want)
		}
	}
	// Long brief is bounded and trimmed of trailing dashes.
	long := SlugifyScenario(strings.Repeat("ab cd ", 40))
	if len(long) > 48 {
		t.Errorf("slug not bounded: len=%d (%q)", len(long), long)
	}
	if strings.HasPrefix(long, "-") || strings.HasSuffix(long, "-") {
		t.Errorf("slug has stray dash: %q", long)
	}
}

func TestResolveTargetDir(t *testing.T) {
	// Default: /tmp/<scenario>-<stamp>.
	got, err := ResolveTargetDir("foo-scenario", "", "20260628T120000Z")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/foo-scenario-20260628T120000Z" {
		t.Errorf("default dir = %q", got)
	}

	// --project pins the dir (made absolute).
	got, err = ResolveTargetDir("foo", "/var/tmp/pinned", "stamp")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/var/tmp/pinned" {
		t.Errorf("pinned dir = %q", got)
	}

	// Missing stamp on the default path is an error.
	if _, err := ResolveTargetDir("foo", "", ""); err == nil {
		t.Error("expected error for missing stamp")
	}
}

func TestSelectStaleSessions(t *testing.T) {
	in := []string{
		"tmux-cli-tmp-symfony-ddd-dashboard-x-y", // disposable target → reap
		"tmux-cli-tmp-other-run-a-b",             // disposable target → reap
		"tmux-cli-home-console-projects-cli-z",   // real project → keep
		"test-id",                                // unrelated → keep
	}
	got := SelectStaleSessions(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 stale, got %d: %v", len(got), got)
	}
	for _, n := range got {
		if !strings.HasPrefix(n, StaleSessionPrefix) {
			t.Errorf("selected non-stale session %q", n)
		}
	}
}

func TestSeedTrustConfig_FreshAndMerge(t *testing.T) {
	// From empty: must create the keys.
	out, err := SeedTrustConfig(nil, "/tmp/t1")
	if err != nil {
		t.Fatal(err)
	}
	assertTrusted(t, out, "/tmp/t1")

	// Merge into an existing config WITHOUT clobbering unrelated keys/projects.
	existing := []byte(`{
		"numStartups": 7,
		"bypassPermissionsModeAccepted": false,
		"projects": {
			"/home/u/proj": {"hasTrustDialogAccepted": true, "history": [1,2,3]}
		}
	}`)
	out, err = SeedTrustConfig(existing, "/tmp/t2")
	if err != nil {
		t.Fatal(err)
	}
	assertTrusted(t, out, "/tmp/t2")

	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if string(root["numStartups"]) != "7" {
		t.Errorf("unrelated top-level key lost: %s", root["numStartups"])
	}
	var projects map[string]map[string]json.RawMessage
	json.Unmarshal(root["projects"], &projects)
	if _, ok := projects["/home/u/proj"]; !ok {
		t.Error("pre-existing project entry was dropped")
	}
	// Compare semantically — pretty-printing re-indents the nested value, so a
	// byte-equal check would spuriously fail even though the data is preserved.
	var hist []int
	if err := json.Unmarshal(projects["/home/u/proj"]["history"], &hist); err != nil {
		t.Fatalf("pre-existing project sub-key not preserved: %v", err)
	}
	if len(hist) != 3 || hist[0] != 1 || hist[2] != 3 {
		t.Errorf("pre-existing project sub-key clobbered: %v", hist)
	}

	// Empty target dir is rejected.
	if _, err := SeedTrustConfig(nil, ""); err == nil {
		t.Error("expected error for empty targetDir")
	}
}

func assertTrusted(t *testing.T, out []byte, dir string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if string(root["bypassPermissionsModeAccepted"]) != "true" {
		t.Errorf("bypassPermissionsModeAccepted not true: %s", root["bypassPermissionsModeAccepted"])
	}
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["projects"], &projects); err != nil {
		t.Fatal(err)
	}
	p, ok := projects[dir]
	if !ok {
		t.Fatalf("project %q not seeded", dir)
	}
	if string(p["hasTrustDialogAccepted"]) != "true" {
		t.Errorf("hasTrustDialogAccepted not true for %q", dir)
	}
	if string(p["hasCompletedProjectOnboarding"]) != "true" {
		t.Errorf("hasCompletedProjectOnboarding not true for %q", dir)
	}
}

func TestStateRoundTrip(t *testing.T) {
	s := NewState("scn", 0) // 0 → DefaultMaxCycles
	if s.MaxCycles != DefaultMaxCycles || s.Cycle != 1 || s.Status != StatusInProgress {
		t.Fatalf("fresh state wrong: %+v", s)
	}
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseState(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scenario != "scn" || got.Cycle != 1 || got.MaxCycles != DefaultMaxCycles {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// History serializes as [] not null.
	if !strings.Contains(string(b), `"history": []`) {
		t.Errorf("history should marshal as []: %s", b)
	}
}

func TestBootstrapResultJSON(t *testing.T) {
	r := BootstrapResult{Ok: true, Scenario: "scn", Cycle: 1, Session: "tmux-cli-tmp-x"}
	js := r.JSON()
	if !strings.HasPrefix(js, "{") || !strings.Contains(js, `"ok":true`) {
		t.Errorf("bad json: %s", js)
	}
	// Failure shape carries stage+error.
	f := BootstrapResult{Ok: false, Stage: "handshake", Error: "channel dead"}
	if !strings.Contains(f.JSON(), `"stage":"handshake"`) {
		t.Errorf("failure json missing stage: %s", f.JSON())
	}
}

func TestHandshakeToken(t *testing.T) {
	if HandshakeToken("sess-1") != "E2E-HANDSHAKE-OK sess-1" {
		t.Errorf("bad token: %q", HandshakeToken("sess-1"))
	}
}

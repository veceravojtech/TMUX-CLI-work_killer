package e2e

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ── Fix 1: exhausted-resume marks the ledger ────────────────────────────────

func TestMarkExhausted_FlipsStatusPreservesRest(t *testing.T) {
	s := NewState("scn", 3)
	s.Cycle = 4 // budget blown
	got := s.MarkExhausted()
	if got.Status != StatusExhausted {
		t.Errorf("Status = %q, want %q", got.Status, StatusExhausted)
	}
	if got.Scenario != "scn" || got.Cycle != 4 || got.MaxCycles != 3 {
		t.Errorf("other fields must be preserved: %+v", got)
	}
	if s.Status != StatusInProgress {
		t.Errorf("value receiver must not mutate the original: %+v", s)
	}
}

// ── Fix 2: disposable-dir marker (pure content + reap selection) ────────────

func TestNewMarker_ParseRoundTrip(t *testing.T) {
	c := NewMarker("scn-x", "20260702T080000Z")
	m := ParseMarker(c)
	if m.Scenario != "scn-x" {
		t.Errorf("Scenario = %q", m.Scenario)
	}
	if m.Stamp != "20260702T080000Z" {
		t.Errorf("Stamp = %q", m.Stamp)
	}
	if m.Session != "" {
		t.Errorf("fresh marker must have no session, got %q", m.Session)
	}
}

func TestMarkerWithSession_AppendsAndReplaces(t *testing.T) {
	c := MarkerWithSession(NewMarker("scn", "stamp"), "tmux-cli-tmp-a-1")
	m := ParseMarker(c)
	if m.Session != "tmux-cli-tmp-a-1" {
		t.Errorf("Session = %q", m.Session)
	}
	if m.Scenario != "scn" || m.Stamp != "stamp" {
		t.Errorf("scenario/stamp lost: %+v", m)
	}
	// Re-recording replaces, never duplicates the session line.
	c2 := MarkerWithSession(c, "tmux-cli-tmp-a-2")
	if got := ParseMarker(c2).Session; got != "tmux-cli-tmp-a-2" {
		t.Errorf("replaced Session = %q", got)
	}
	if n := strings.Count(c2, "session:"); n != 1 {
		t.Errorf("session line duplicated %d times:\n%s", n, c2)
	}
}

func TestParseMarker_GarbageIsZeroMarker(t *testing.T) {
	for _, in := range []string{"", "not a marker", "scenario\nstamp"} {
		m := ParseMarker(in)
		if m.Scenario != "" || m.Stamp != "" || m.Session != "" {
			t.Errorf("ParseMarker(%q) = %+v, want zero", in, m)
		}
	}
}

func TestShouldReapDisposable(t *testing.T) {
	live := []string{"tmux-cli-tmp-live-1", "real-project-session"}
	cases := []struct {
		name string
		m    Marker
		want bool
	}{
		{"no session line (crashed before start)", Marker{Scenario: "s", Stamp: "t"}, true},
		{"recorded session gone", Marker{Scenario: "s", Stamp: "t", Session: "tmux-cli-tmp-dead"}, true},
		{"recorded session live", Marker{Scenario: "s", Stamp: "t", Session: "tmux-cli-tmp-live-1"}, false},
		{"empty marker (unparseable content)", Marker{}, true},
	}
	for _, tc := range cases {
		if got := ShouldReapDisposable(tc.m, live); got != tc.want {
			t.Errorf("%s: ShouldReapDisposable = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Nil live list (tmux server down): every marked dir is orphaned.
	if !ShouldReapDisposable(Marker{Session: "tmux-cli-tmp-x"}, nil) {
		t.Error("nil live list must reap")
	}
}

// ── Fix 3: start --print-json parse ─────────────────────────────────────────

func TestParseStartOutput_ValidLine(t *testing.T) {
	out, err := ParseStartOutput(`{"session":"tmux-cli-tmp-x-20260702t080000","created":true}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if out.Session != "tmux-cli-tmp-x-20260702t080000" || !out.Created {
		t.Errorf("parsed = %+v", out)
	}
	// created:false (attached to an existing session) is also valid.
	out, err = ParseStartOutput(`{"session":"s2","created":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Session != "s2" || out.Created {
		t.Errorf("parsed = %+v", out)
	}
}

func TestParseStartOutput_Rejects(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"whitespace only": " \n\t",
		"not json":        "Created session 'x'",
		"missing session": `{"created":true}`,
		"multi line":      `{"session":"a","created":true}` + "\n" + `{"session":"b","created":true}`,
	}
	for name, in := range cases {
		if _, err := ParseStartOutput(in); err == nil {
			t.Errorf("%s: expected error for %q", name, in)
		}
	}
}

// ── Fix 4: receipt path + result field ──────────────────────────────────────

func TestReceiptPath(t *testing.T) {
	got := ReceiptPath("/repo", "scn-20260702T080000Z")
	want := "/repo/.tmux-cli/e2e-evaluator/logs/scn-20260702T080000Z.receipt"
	if got != want {
		t.Errorf("ReceiptPath = %q, want %q", got, want)
	}
}

func TestBootstrapResultJSON_CarriesReceiptPath(t *testing.T) {
	r := BootstrapResult{Ok: true, Scenario: "s", ReceiptPath: "/r/.tmux-cli/e2e-evaluator/logs/x.receipt"}
	if !strings.Contains(r.JSON(), `"receipt_path":"/r/.tmux-cli/e2e-evaluator/logs/x.receipt"`) {
		t.Errorf("receipt_path missing from JSON: %s", r.JSON())
	}
}

// ── Fix 6: trust transform idempotency + seeded verify ──────────────────────

func TestSeedTrustConfig_Idempotent(t *testing.T) {
	once, err := SeedTrustConfig(nil, "/tmp/t-idem")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := SeedTrustConfig(once, "/tmp/t-idem")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("transform not idempotent:\nonce:  %s\ntwice: %s", once, twice)
	}
}

func TestTrustSeeded(t *testing.T) {
	out, err := SeedTrustConfig(nil, "/tmp/t-ver")
	if err != nil {
		t.Fatal(err)
	}
	if !TrustSeeded(out, "/tmp/t-ver") {
		t.Error("freshly seeded config must verify")
	}
	if TrustSeeded(out, "/tmp/other-dir") {
		t.Error("different target dir must not verify")
	}
	if TrustSeeded(nil, "/tmp/t-ver") {
		t.Error("nil config must not verify")
	}
	if TrustSeeded([]byte("{not json"), "/tmp/t-ver") {
		t.Error("invalid JSON must not verify")
	}
	// Any one of the three keys missing → not seeded.
	partial := []byte(`{"bypassPermissionsModeAccepted":true,"projects":{"/tmp/t-ver":{"hasTrustDialogAccepted":true}}}`)
	if TrustSeeded(partial, "/tmp/t-ver") {
		t.Error("config missing hasCompletedProjectOnboarding must not verify")
	}
}

// ── Fix: version-seen seed so an auto-updated Claude skips the release dialog ──

// withClaudeVersion overrides the injectable resolver for the duration of a test.
func withClaudeVersion(t *testing.T, v string) {
	t.Helper()
	prev := claudeVersion
	claudeVersion = func() string { return v }
	t.Cleanup(func() { claudeVersion = prev })
}

func TestSeedTrustConfig_SeedsVersionFields(t *testing.T) {
	withClaudeVersion(t, "2.1.201")
	out, err := SeedTrustConfig(nil, "/tmp/t-ver")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if got := string(root["lastReleaseNotesSeen"]); got != `"2.1.201"` {
		t.Errorf("top-level lastReleaseNotesSeen = %s, want %q", got, "2.1.201")
	}
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["projects"], &projects); err != nil {
		t.Fatal(err)
	}
	if got := string(projects["/tmp/t-ver"]["lastVersionBase"]); got != `"2.1.201"` {
		t.Errorf("projects[dir].lastVersionBase = %s, want %q", got, "2.1.201")
	}
	// The three trust keys must remain present.
	if !TrustSeeded(out, "/tmp/t-ver") {
		t.Error("trust keys must survive the version seed")
	}
}

func TestSeedTrustConfig_VersionUnresolvedSkips(t *testing.T) {
	withClaudeVersion(t, "")
	out, err := SeedTrustConfig(nil, "/tmp/t-none")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["lastReleaseNotesSeen"]; ok {
		t.Error("lastReleaseNotesSeen must be absent when version unresolved")
	}
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["projects"], &projects); err != nil {
		t.Fatal(err)
	}
	if _, ok := projects["/tmp/t-none"]["lastVersionBase"]; ok {
		t.Error("lastVersionBase must be absent when version unresolved")
	}
	// Best-effort skip preserves the exact prior three-key behavior.
	if !TrustSeeded(out, "/tmp/t-none") {
		t.Error("trust keys must still be written when version unresolved")
	}
}

func TestSeedTrustConfig_VersionIdempotent(t *testing.T) {
	withClaudeVersion(t, "2.1.201")
	once, err := SeedTrustConfig(nil, "/tmp/t-ver-idem")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := SeedTrustConfig(once, "/tmp/t-ver-idem")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("version seed not idempotent:\nonce:  %s\ntwice: %s", once, twice)
	}
}

func TestParseClaudeVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"2.1.201 (Claude Code)", "2.1.201"},
		{"2.1.201\n", "2.1.201"},
		{"v1.0.0-beta", "1.0.0"},
		{"unknown", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseClaudeVersion(c.in); got != c.want {
			t.Errorf("parseClaudeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/e2e"
)

// ── Fix 1: --resume past budget must flip the ledger to exhausted ───────────

func TestResolveCycle_ExhaustedResumeMarksLedger(t *testing.T) {
	dir := t.TempDir()
	stateFile := e2e.StateFilePath(dir, "scn")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	st := e2e.NewState("scn", 2)
	st.Cycle = 3 // budget blown, but still in-progress on disk
	b, err := st.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, b, 0o644); err != nil {
		t.Fatal(err)
	}

	oldResume := e2eResume
	e2eResume = true
	defer func() { e2eResume = oldResume }()

	if _, err := resolveCycle(dir, "scn"); err == nil {
		t.Fatal("expected exhausted-budget error")
	}
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e2e.ParseState(raw)
	if err != nil {
		t.Fatalf("ledger unreadable after exhausted resume: %v", err)
	}
	if got.Status != e2e.StatusExhausted {
		t.Errorf("ledger status = %q, want %q (must be terminal before erroring)", got.Status, e2e.StatusExhausted)
	}
	if got.Cycle != 3 || got.MaxCycles != 2 || got.Scenario != "scn" {
		t.Errorf("ledger fields clobbered: %+v", got)
	}
}

func TestResolveCycle_TerminalStatusLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	stateFile := e2e.StateFilePath(dir, "scn")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	st := e2e.NewState("scn", 5)
	st.Status = e2e.StatusPassed
	b, err := st.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, b, 0o644); err != nil {
		t.Fatal(err)
	}

	oldResume := e2eResume
	e2eResume = true
	defer func() { e2eResume = oldResume }()

	if _, err := resolveCycle(dir, "scn"); err == nil {
		t.Fatal("expected already-terminal error")
	}
	after, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(b) {
		t.Errorf("already-terminal ledger must be left byte-identical:\nbefore: %s\nafter:  %s", b, after)
	}
}

// ── Fix 2: marker write/record + orphan-dir reaping ─────────────────────────

func TestDisposableMarker_WriteThenRecordSession(t *testing.T) {
	dir := t.TempDir()
	if err := writeDisposableMarker(dir, "scn", "20260702T080000Z"); err != nil {
		t.Fatal(err)
	}
	if err := recordDisposableSession(dir, "tmux-cli-tmp-x-1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, e2e.DisposableMarkerName))
	if err != nil {
		t.Fatalf("marker file missing: %v", err)
	}
	m := e2e.ParseMarker(string(raw))
	if m.Scenario != "scn" || m.Stamp != "20260702T080000Z" || m.Session != "tmux-cli-tmp-x-1" {
		t.Errorf("marker round-trip = %+v", m)
	}
}

func TestRecordDisposableSession_MissingMarkerErrors(t *testing.T) {
	if err := recordDisposableSession(t.TempDir(), "s"); err == nil {
		t.Error("recording a session into a dir without a marker must error")
	}
}

func TestReapOrphanDisposables_SelectsByMarkerAndLiveness(t *testing.T) {
	root := t.TempDir()
	mk := func(name, content string, withMarker bool) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if withMarker {
			if err := os.WriteFile(filepath.Join(d, e2e.DisposableMarkerName), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	deadDir := mk("scn-a", e2e.MarkerWithSession(e2e.NewMarker("scn", "s1"), "tmux-cli-tmp-dead"), true)
	liveDir := mk("scn-b", e2e.MarkerWithSession(e2e.NewMarker("scn", "s2"), "tmux-cli-tmp-live"), true)
	crashedDir := mk("scn-c", e2e.NewMarker("scn", "s3"), true) // no session line
	unmarkedDir := mk("unrelated", "", false)                   // no marker: NEVER touched

	reaped := reapOrphanDisposables(root, []string{"tmux-cli-tmp-live", "other"})

	if len(reaped) != 2 {
		t.Fatalf("reaped = %v, want exactly dead+crashed", reaped)
	}
	for _, d := range []string{deadDir, crashedDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("orphan dir %s must be removed", d)
		}
	}
	for _, d := range []string{liveDir, unmarkedDir} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("dir %s must survive: %v", d, err)
		}
	}
}

// ── Fix 4: handshake verify — receipt primary, normalized log fallback ──────

func TestHandshakeSeen_ReceiptIsPrimary(t *testing.T) {
	d := t.TempDir()
	receipt := filepath.Join(d, "s.receipt")
	log := filepath.Join(d, "s.log") // never written — receipt alone must prove it
	token := e2e.HandshakeToken("tmux-cli-tmp-x")
	if err := os.WriteFile(receipt, []byte("earlier message\n"+token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !handshakeSeen(receipt, log, token) {
		t.Error("exact token line in the receipt file must verify without any log")
	}
}

func TestHandshakeSeen_LogFallbackSurvivesTUIWrapping(t *testing.T) {
	d := t.TempDir()
	receipt := filepath.Join(d, "s.receipt") // missing (old binary in target)
	log := filepath.Join(d, "s.log")
	token := e2e.HandshakeToken("tmux-cli-tmp-demo")
	raw := "\x1b]0;run\x07Notifi\x1b[2Ced\x1b[3A orches\x1b[Gtrator\x1b[K pane\n" +
		"E2E-HANDSHAKE-OK\x1b[K tmux-cli-tmp-demo\n"
	if err := os.WriteFile(log, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if !handshakeSeen(receipt, log, token) {
		t.Error("normalized-log fallback must still verify when the receipt is missing")
	}
}

func TestHandshakeSeen_NeitherProof(t *testing.T) {
	d := t.TempDir()
	if handshakeSeen(filepath.Join(d, "no.receipt"), filepath.Join(d, "no.log"), "TOKEN") {
		t.Error("no receipt and no log must not verify")
	}
	// A receipt with other content but not the token line must not verify.
	receipt := filepath.Join(d, "s.receipt")
	if err := os.WriteFile(receipt, []byte("goals-dispatched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if handshakeSeen(receipt, filepath.Join(d, "no.log"), "TOKEN") {
		t.Error("receipt without the token line must not verify")
	}
}

// ── Fix 5: TUI probe constants pinned in one block ──────────────────────────

func TestIdleProbeConstants_Pinned(t *testing.T) {
	if idlePromptMarker != "❯" {
		t.Errorf("idlePromptMarker = %q, want the Claude Code input marker ❯", idlePromptMarker)
	}
	if len(busyHintWords) == 0 {
		t.Fatal("busyHintWords must not be empty")
	}
	found := false
	for _, w := range busyHintWords {
		if w == "esc to interrupt" {
			found = true
		}
	}
	if !found {
		t.Error(`busyHintWords must include "esc to interrupt" (the stable busy affordance)`)
	}
	if idleStableInterval < time.Second || idleStableInterval > 5*time.Second {
		t.Errorf("idleStableInterval = %v, want ~2s between stability snapshots", idleStableInterval)
	}
}

// ── Fix 6: seedTrust writes, verifies, and preserves unrelated config ───────

func TestSeedTrust_WritesVerifiedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := "/tmp/e2e-seed-trust-target"

	if err := seedTrust(target); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !e2e.TrustSeeded(raw, target) {
		t.Errorf("seedTrust output must pass TrustSeeded: %s", raw)
	}
}

func TestSeedTrust_PreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	existing := []byte(`{"numStartups": 9, "projects": {"/home/u/keep": {"hasTrustDialogAccepted": true}}}`)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedTrust("/tmp/e2e-seed-trust-merge"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !e2e.TrustSeeded(raw, "/tmp/e2e-seed-trust-merge") {
		t.Errorf("target not seeded: %s", raw)
	}
	for _, needle := range []string{`"numStartups": 9`, `"/home/u/keep"`} {
		if !strings.Contains(string(raw), needle) {
			t.Errorf("pre-existing config lost %q: %s", needle, raw)
		}
	}
}

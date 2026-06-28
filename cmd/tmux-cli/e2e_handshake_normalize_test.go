package main

import (
	"strings"
	"testing"
)

// TestHandshakeNormalizeSurvivesTUIWrapping pins the e2e-evaluator handshake
// false-negative: the target's Claude Code TUI wraps the notify-orchestrator
// success line with ANSI cursor/colour escapes, so the literal
// "Notified orchestrator pane" (with spaces) never matches a raw substring
// search even though the channel proved live. normalizeLog must strip the
// escapes + whitespace so both proofs are detected.
func TestHandshakeNormalizeSurvivesTUIWrapping(t *testing.T) {
	// A faithful slice of a real pipe-pane transcript: the success line is
	// split across columns by CSI escapes and the token carries an OSC title
	// sequence — exactly what defeated the old strings.Contains check.
	raw := "\x1b]0;✳ run\x07Notifi\x1b[2Ced\x1b[3A orches\x1b[Gtrator\x1b[K pane \x1b[38;5;246m%260\x1b[39m\n" +
		"\x1b[2D some noise \x1b[K\n" +
		"E2E-HANDSHAKE-OK\x1b[K tmux-cli-tmp-demo-20260628t125920z\x1b[1B\n"

	token := stripWS("E2E-HANDSHAKE-OK tmux-cli-tmp-demo-20260628t125920z")
	n := normalizeLog(raw)

	if strings.Contains(raw, "Notified orchestrator pane") {
		t.Fatal("precondition: raw log must NOT contain the literal spaced success line")
	}
	if !strings.Contains(n, "Notifiedorchestratorpane") {
		t.Errorf("normalized log lost the success line: %q", n)
	}
	if !strings.Contains(n, token) {
		t.Errorf("normalized log lost the handshake token: %q", n)
	}
}

// TestNormalizeLogStripsWhitespaceAndEscapes guards the helper primitives.
func TestNormalizeLogStripsWhitespaceAndEscapes(t *testing.T) {
	if got := stripWS(" a\tb\nc\x00d "); got != "abcd" {
		t.Errorf("stripWS = %q, want abcd", got)
	}
	if got := normalizeLog("\x1b[31mred\x1b[0m \x1b]0;title\x07x"); got != "redx" {
		t.Errorf("normalizeLog = %q, want redx", got)
	}
}

// Package redact implements the P3 transcript redaction pass (design
// docs/architecture/session-log-streaming-design.md §6, frozen contract
// .tmux-cli/research/2026-07-23-00/p3-transcripts-contract.md §Redaction).
//
// Two layers, applied in order before any transcript text ships:
//
//  1. Mask — the built-in secret masker. ALWAYS runs, cannot be disabled,
//     never fails. Each catalogue match is replaced with the literal token
//     «REDACTED:<label>» (labels: bearer, apikey, privatekey, kv, urlcreds).
//  2. Hook — the optional user hook .tmux-cli/hooks/redact-transcript.sh,
//     consulted only when present AND executable: built-in-masked text on
//     stdin → redacted text on stdout, exit 0, bounded by a 10s timeout.
//     The hook is FAIL-CLOSED at the call site: any error/non-zero/timeout
//     from a present hook means the caller must hold the segment local and
//     ship nothing from it. An ABSENT hook is normal, not a failure.
package redact

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// maskRules is the ordered built-in catalogue (contract §Redaction "minimum
// catalogue"). Order matters: the multi-line private-key block first (so its
// base64 body is not partially eaten by narrower rules), header-bound bearer
// tokens before bare JWTs, and the generic kv rule last so specific labels win.
var maskRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// privatekey: -----BEGIN … PRIVATE KEY----- … -----END … PRIVATE KEY-----
	{regexp.MustCompile(`(?s)-----BEGIN[^-]*PRIVATE KEY-----.*?-----END[^-]*PRIVATE KEY-----`),
		"«REDACTED:privatekey»"},
	// bearer: Authorization: Bearer <tok> (also the quoted JSON header form).
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?bearer\s+)[A-Za-z0-9._~+/=-]+`),
		"${1}«REDACTED:bearer»"},
	// bearer: bare JWTs — three dot-joined base64url segments starting eyJ.
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		"«REDACTED:bearer»"},
	// apikey: sk-…, AKIA…, AIza…, gh[pousr]_… (case-defined prefixes).
	{regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|gh[pousr]_[0-9A-Za-z]{36})\b`),
		"«REDACTED:apikey»"},
	// urlcreds: scheme://user:pass@host → mask the userinfo only.
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@:]+:[^/\s@]+@`),
		"${1}«REDACTED:urlcreds»@"},
	// kv: sensitive key followed by = or : → mask the VALUE only. The optional
	// quotes absorb the JSON `"key": "value"` form; the key and separator are
	// preserved so the surrounding text stays readable.
	{regexp.MustCompile(`(?i)\b((?:secret|token|password|passwd|api[_-]?key|access[_-]?key|private[_-]?key)["']?\s*[:=]\s*["']?)[^\s"']+`),
		"${1}«REDACTED:kv»"},
}

// Mask applies the built-in catalogue to s and returns the masked text. It is
// pure and total: no input can make it fail (contract: the built-in masker
// never "fails").
func Mask(s string) string {
	for _, r := range maskRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// DefaultHookTimeout bounds one user-hook invocation (contract: "wrapped
// `timeout 10s`" — enforced here via the exec context deadline, which kills
// the hook process without depending on a coreutils timeout binary).
const DefaultHookTimeout = 10 * time.Second

// Hook is the optional Layer-2 user redaction hook.
type Hook struct {
	// Path is the hook script location (.tmux-cli/hooks/redact-transcript.sh).
	Path string
	// Timeout overrides DefaultHookTimeout when > 0 (tests use a short one).
	Timeout time.Duration
}

// Runnable reports whether the hook is present AND executable — the contract's
// sole condition for Layer 2 to run. A missing, non-regular, or non-executable
// file means "absent" (normal, masker alone suffices).
func (h Hook) Runnable() bool {
	fi, err := os.Stat(h.Path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// Run executes the hook with stdin on its standard input and returns its
// standard output. Any spawn failure, non-zero exit, or timeout is an error —
// the caller applies the fail-closed contract (segment held local). Run does
// not check Runnable; callers gate on it first.
func (h Hook) Run(ctx context.Context, stdin string) (string, error) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultHookTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.Path)
	cmd.Stdin = bytes.NewReader([]byte(stdin))
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	// If the hook ignores the kill long enough to matter, give up on waiting
	// rather than hanging the ship pass.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("redact hook timed out after %s: %w", timeout, ctx.Err())
		}
		return "", fmt.Errorf("redact hook failed: %w (stderr: %s)", err, errBuf.String())
	}
	return out.String(), nil
}

package taskvisor

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteCorrectionFile_LogsEveryFail locks RC-4: every validator fail is
// LOGGED, and the log line points to what ran (the command), what failed (the
// rule), and how (the output excerpt) — never a silent fail.
func TestWriteCorrectionFile_LogsEveryFail(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-007")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	sig := &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{{
			Rule: "phpstan", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
			FailingCommand: "vendor/bin/phpstan analyse",
			OutputExcerpt:  "Type error on line 12",
		}},
	}
	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig, true))

	logged := buf.String()
	assert.Contains(t, logged, "validator fail", "every fail must emit a log line")
	assert.Contains(t, logged, "phpstan", "log must name what failed (rule)")
	assert.Contains(t, logged, "vendor/bin/phpstan analyse", "log must name what ran (command)")
	assert.Contains(t, logged, "Type error on line 12", "log must show how it failed (output)")
}

func TestWriteCorrectionFile_DoneHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	// No structured findings → fallback writes NextAction verbatim (the call site
	// primes it with the daemon framing header).
	sig := &ValidatorSignal{NextAction: "Implementation completed but failed acceptance criteria.\n\nfix the pricing"}
	err := d.writeCorrectionFile(goalDir, 1, sig, false)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "Implementation completed but failed acceptance criteria."))
	assert.Contains(t, content, "fix the pricing")
}

func TestWriteCorrectionFile_StoppedHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	header := "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup."
	sig := &ValidatorSignal{NextAction: header + "\n\nfinish booking page"}
	err := d.writeCorrectionFile(goalDir, 2, sig, false)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-2.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "Previous cycle hit the supervisor cycle limit"))
	assert.Contains(t, content, "finish booking page")
}

func TestWriteCorrectionFile_CreatesDirectory(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")

	err := d.writeCorrectionFile(goalDir, 1, &ValidatorSignal{NextAction: "content"}, false)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "content")
}

// TestWriteCorrectionFile_EnvNoiseFallback locks RC-3: a findingless bounce whose
// captured output is pure environment/infrastructure noise (the goal-001 DATADOG
// docker-compose warnings) is NEVER surfaced as a raw dump or a code correction —
// it renders a structured finding owned by ops, and the framing header survives.
func TestWriteCorrectionFile_EnvNoiseFallback(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	header := "Implementation completed but failed acceptance criteria."
	noise := `time="2026-06-15T20:21:03+02:00" level=warning msg="The \"DATADOG_SCRIPT_ENABLED\" variable is not set. Defaulting to a blank string."
time="2026-06-15T20:21:06+02:00" level=warning msg="The \"DATADOG_API_KEY\" variable is not set. Defaulting to a blank string."`
	sig := &ValidatorSignal{NextAction: header + "\n\n" + noise}

	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig, true))

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)

	// Framing header survives; the correction is structured and classified ops.
	assert.True(t, strings.HasPrefix(content, header))
	assert.Contains(t, content, "### Finding: validation failed (no structured validator findings)")
	assert.Contains(t, content, "owner=ops")
	assert.Contains(t, content, "do NOT change code")
	// The env-noise is shown as captured output context but never as a code defect.
	assert.NotContains(t, content, "code-defect")
}

// TestWriteCorrectionFile_StructuredPerFinding asserts that each non-pass
// finding is emitted as its own structured ### Finding block with
// Command/Output/Expected/Correction lines, and that pass findings are omitted.
func TestWriteCorrectionFile_StructuredPerFinding(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	sig := &ValidatorSignal{
		Verdict: "fail",
		Findings: []ValidationFinding{
			{
				Rule: "price-calc", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestTotal",
				OutputExcerpt:  "want 1000 got 100",
				ExpectedState:  "total in cents matches the API",
				Correction:     "multiply dollars by 100 before formatting",
			},
			{
				Rule: "currency-format", Status: "fail", FailureClass: "code-defect", Owner: "implementer",
				FailingCommand: "go test ./pricing -run TestLocale",
				OutputExcerpt:  "want 1.000,00 got 1,000.00",
				ExpectedState:  "locale-aware currency formatting",
				Correction:     "use the locale formatter for the active request",
			},
			{Rule: "smoke", Status: "pass"},
		},
		NextAction: "should not appear when structured findings exist",
	}

	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig, true))

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	content := string(data)

	// Both non-pass findings produce a structured block.
	assert.Contains(t, content, "### Finding: price-calc")
	assert.Contains(t, content, "### Finding: currency-format")
	assert.Contains(t, content, "Command: go test ./pricing -run TestTotal")
	assert.Contains(t, content, "Output: want 1000 got 100")
	assert.Contains(t, content, "Expected: total in cents matches the API")
	assert.Contains(t, content, "Correction: multiply dollars by 100 before formatting")
	assert.Contains(t, content, "Command: go test ./pricing -run TestLocale")
	assert.Contains(t, content, "Correction: use the locale formatter for the active request")

	// Pass finding is omitted and the NextAction one-liner is not used.
	assert.NotContains(t, content, "### Finding: smoke")
	assert.NotContains(t, content, "should not appear when structured findings exist")
}

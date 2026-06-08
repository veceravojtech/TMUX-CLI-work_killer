package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCorrectionFile_DoneHeader(t *testing.T) {
	d, _, _ := setupDaemon(t)
	goalDir := filepath.Join(t.TempDir(), "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))

	// No structured findings → fallback writes NextAction verbatim (the call site
	// primes it with the daemon framing header).
	sig := &ValidatorSignal{NextAction: "Implementation completed but failed acceptance criteria.\n\nfix the pricing"}
	err := d.writeCorrectionFile(goalDir, 1, sig)
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
	err := d.writeCorrectionFile(goalDir, 2, sig)
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

	err := d.writeCorrectionFile(goalDir, 1, &ValidatorSignal{NextAction: "content"})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(goalDir, "corrections", "cycle-1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "content")
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

	require.NoError(t, d.writeCorrectionFile(goalDir, 1, sig))

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

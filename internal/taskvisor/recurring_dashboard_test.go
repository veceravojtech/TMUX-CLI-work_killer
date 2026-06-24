package taskvisor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRecurringYAML writes an active .tmux-cli/recurring.yaml fixture as raw YAML
// text. goal-001's RecurringTask type is unavailable in this worktree, so the field
// names mirror its `yaml` tags verbatim — goal-007's LoadRecurring deserializes the
// same file unchanged. dispatched_at is now-90s so formatElapsed yields a
// "1m 30s"-class in-cycle value regardless of sub-second drift.
func writeRecurringYAML(t *testing.T, dir string) {
	t.Helper()
	dispatchedAt := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`task:
  id: recur-1
  prompt: "check things"
  total_cycles: 10
  completed_cycles: 0
  status: active
  current_cycle:
    index: 1
    phase: settling
    dispatched_at: %s
`, dispatchedAt)
	tmuxDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "recurring.yaml"), []byte(body), 0o644))
}

// assertRecurringSection isolates the RECURRING section (from its header to the next
// blank line) and asserts the active task's id, cycle X/N, status, phase, and an
// in-cycle duration. Section-scoping is required so the goals table's own elapsed
// column cannot satisfy the duration regex.
func assertRecurringSection(t *testing.T, out string) {
	t.Helper()
	i := strings.Index(out, "RECURRING")
	sec := ""
	if i >= 0 {
		sec = out[i:]
		if j := strings.Index(sec, "\n\n"); j >= 0 {
			sec = sec[:j]
		}
	}
	assert.Contains(t, sec, "recur-1")
	assert.Contains(t, sec, "1/10")
	assert.Contains(t, sec, "active")
	assert.Contains(t, sec, "settling")
	assert.Regexp(t, `\d+m \d+s|\d+s`, sec)
}

// TestRecurringDashboardSection_RendersActiveTask pins that an active recurring task
// surfaces on the board under both render paths: the daemon-foreground (nil
// executor) path and the standalone (real executor, no live session) path. RED
// phase: collectRecurring/renderRecurringSection are stubs unwired from RenderBoard,
// so the section is absent and assertRecurringSection fails. goal-007 makes it green.
func TestRecurringDashboardSection_RendersActiveTask(t *testing.T) {
	dir := t.TempDir()
	writeRecurringYAML(t, dir)

	// (1) nil executor — daemon-foreground path; census degrades to placeholder.
	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, nil))
	assertRecurringSection(t, buf.String())

	// (2) mock executor, no live session — only FindSessionByEnvironment is reached;
	// collectCensus returns the placeholder before ListWindows, so no ListWindows
	// expectation is set and AssertExpectations is intentionally not called.
	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return("", nil)
	var buf2 bytes.Buffer
	require.NoError(t, RenderBoard(&buf2, dir, mockExec))
	assertRecurringSection(t, buf2.String())
}

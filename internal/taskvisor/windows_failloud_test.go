package taskvisor

import (
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windows_failloud_test.go — P5 fix #1: waitForPromptOrFail is the bounded-retry
// sibling of waitForPrompt that RETURNS its error (mirroring waitClaudeBoot)
// instead of the old log-and-swallow at the call sites. The N=3 re-polls divide
// the caller's existing timeout (split, not multiplied) so the total wall-bound
// is unchanged.
//
// The "never arrives" cases use an auto-advancing clock so the polling loop
// (promptPollInterval=0) crosses each sub-deadline deterministically — no
// wall-clock hang.

// autoClock returns a clock that advances by step on EVERY call. A polling loop
// reading it with promptPollInterval=0 therefore crosses any deadline on its
// next now() check, turning "prompt never arrives" into a bounded, sleep-free
// timeout instead of a spin.
func autoClock(step time.Duration) func() time.Time {
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	var n int64
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * step)
	}
}

func TestWaitForPromptOrFail_PromptNeverArrives_ReturnsError(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession
	d.clock = autoClock(time.Hour) // each now() jumps an hour → every sub-deadline blown

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001"},
	}, nil)
	// Pane never shows the prompt glyph.
	exec.On("CaptureWindowOutput", testSession, "@1").Return("booting...", nil)

	err := d.waitForPromptOrFail("supervisor-001", 30*time.Second)

	require.Error(t, err, "a window that never shows ❯ must return an error, not silently proceed")
	assert.Contains(t, err.Error(), "prompt never arrived",
		"the wrapped error must name the bounded-retry exhaustion")
	assert.Contains(t, err.Error(), "supervisor-001")
}

func TestWaitForPromptOrFail_PromptArrives_ReturnsNil(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession
	// Default clock (time.Now) is fine: the prompt is found on the first capture,
	// so the deadline check is never reached — success path stays byte-identical.

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)

	err := d.waitForPromptOrFail("supervisor-001", 30*time.Second)

	require.NoError(t, err, "a window showing ❯ on the first attempt returns nil")
}

func TestWaitForPromptOrFail_PromptArrivesOnSecondAttempt_ReturnsNil(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession
	d.clock = autoClock(time.Hour) // first sub-wait times out deterministically

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001"},
	}, nil)
	// First attempt sees no prompt (→ sub-deadline timeout); the second sees it.
	exec.On("CaptureWindowOutput", testSession, "@1").Return("booting...", nil).Once()
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)

	err := d.waitForPromptOrFail("supervisor-001", 30*time.Second)

	require.NoError(t, err, "the first sub-wait failure is non-fatal; the retry recovers")
}

// TestWaitForPromptOrFail_SuccessPathByteIdentical guards that the wrapper does
// not alter waitForPrompt's success contract: still routes through the same
// CaptureWindowOutput probe and returns nil on the glyph.
func TestWaitForPromptOrFail_SuccessPathByteIdentical(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@7", Name: "validator-001"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@7").Return(
		strings.Repeat("x", 10)+" ❯ ", nil)

	require.NoError(t, d.waitForPromptOrFail("validator-001", 30*time.Second))
}

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setBootstrapFlags pins the package-level bootstrap flag vars for one test.
func setBootstrapFlags(t *testing.T, resume bool, maxCycles int) {
	t.Helper()
	oldResume, oldMax := e2eResume, e2eMaxCycles
	e2eResume, e2eMaxCycles = resume, maxCycles
	t.Cleanup(func() { e2eResume, e2eMaxCycles = oldResume, oldMax })
}

// ── Fix 1: every bootstrap-side ledger write keeps state.md in lockstep ─────

func TestResolveCycle_FreshWritesStateMD(t *testing.T) {
	repoRoot := t.TempDir()
	mdFile := e2e.StateMDPath(repoRoot, "scn")
	require.NoError(t, os.MkdirAll(filepath.Dir(mdFile), 0o755))
	stale := "Invoke `/tmux:e2e-evaluator resume` — verify task-317"
	require.NoError(t, os.WriteFile(mdFile, []byte(stale), 0o644))
	setBootstrapFlags(t, false, 5)

	cycle, err := resolveCycle(repoRoot, "scn")
	require.NoError(t, err)
	assert.Equal(t, 1, cycle)

	raw, err := os.ReadFile(e2e.StateFilePath(repoRoot, "scn"))
	require.NoError(t, err)
	st, err := e2e.ParseState(raw)
	require.NoError(t, err)
	require.Equal(t, e2e.StatusInProgress, st.Status)

	md, err := os.ReadFile(mdFile)
	require.NoError(t, err, "fresh init must write the state.md rendering")
	assert.Equal(t, e2e.RenderStateMD(st), string(md), "state.md must be the rendering of the on-disk json ledger")
	assert.NotContains(t, string(md), "task-317", "stale prior-run handoff must be gone")
	assert.Contains(t, string(md), "cycle: 1")
}

func TestResolveCycle_ResumeMissingLedgerWritesStateMD(t *testing.T) {
	repoRoot := t.TempDir()
	mdFile := e2e.StateMDPath(repoRoot, "scn")
	require.NoError(t, os.MkdirAll(filepath.Dir(mdFile), 0o755))
	setBootstrapFlags(t, true, 4)

	cycle, err := resolveCycle(repoRoot, "scn")
	require.NoError(t, err)
	assert.Equal(t, 1, cycle)

	raw, err := os.ReadFile(e2e.StateFilePath(repoRoot, "scn"))
	require.NoError(t, err)
	st, err := e2e.ParseState(raw)
	require.NoError(t, err)

	md, err := os.ReadFile(mdFile)
	require.NoError(t, err, "resume-with-missing-ledger init must write state.md")
	assert.Equal(t, e2e.RenderStateMD(st), string(md))
}

func TestResolveCycle_ExhaustedRewritesStateMD(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 3, 2) // budget blown, still in-progress on disk
	mdFile := e2e.StateMDPath(repoRoot, "scn")
	staleMD := "Invoke `/tmux:e2e-evaluator resume` — continue the in-progress run at cycle 3."
	require.NoError(t, os.WriteFile(mdFile, []byte(staleMD), 0o644))
	setBootstrapFlags(t, true, 2)

	_, err := resolveCycle(repoRoot, "scn")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ledger marked exhausted")

	raw, err := os.ReadFile(e2e.StateFilePath(repoRoot, "scn"))
	require.NoError(t, err)
	st, err := e2e.ParseState(raw)
	require.NoError(t, err)
	require.Equal(t, e2e.StatusExhausted, st.Status)

	md, err := os.ReadFile(mdFile)
	require.NoError(t, err)
	assert.Equal(t, e2e.RenderStateMD(st), string(md), "exhausted flip must rewrite state.md from the same state")
	assert.Contains(t, string(md), "nothing to resume")
	assert.NotContains(t, string(md), "continue the in-progress run")
}

// ── Fix 2: fresh bootstrap sweeps only THIS scenario's stale artifacts ───────

func TestClearRunArtifacts_SweepsOnlyOwnScenario(t *testing.T) {
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))
	write := func(p string) string {
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		return p
	}

	// Scenario A ("scn") artifacts in their production shapes: state.md,
	// receipt (<scenario>-<stamp>.receipt), pipe-pane log named after the
	// target session (tmux-cli-tmp-<scenario>-<lowercased stamp>-<started>).
	aMD := write(e2e.StateMDPath(repoRoot, "scn"))
	aReceipt := write(e2e.ReceiptPath(repoRoot, "scn-20260702T080000Z"))
	aLog := write(e2e.LogPath(repoRoot, "tmux-cli-tmp-scn-20260702t080000z-20260702T090000"))

	// Scenario B ("scn-two") shares A's slug as a prefix — must survive.
	bMD := write(e2e.StateMDPath(repoRoot, "scn-two"))
	bReceipt := write(e2e.ReceiptPath(repoRoot, "scn-two-20260702T081500Z"))
	bLog := write(e2e.LogPath(repoRoot, "tmux-cli-tmp-scn-two-20260702t081500z-20260702T091500"))

	// Non-matching bystanders: wrong extension, no run stamp after the slug.
	txt := write(filepath.Join(logsDir, "scn-20260702T080000Z.txt"))
	noStamp := write(filepath.Join(logsDir, "scn-notes.log"))

	clearRunArtifacts(repoRoot, "scn")

	for _, p := range []string{aMD, aReceipt, aLog} {
		_, statErr := os.Stat(p)
		assert.True(t, os.IsNotExist(statErr), "scenario A artifact must be swept: %s", p)
	}
	for _, p := range []string{bMD, bReceipt, bLog, txt, noStamp} {
		_, statErr := os.Stat(p)
		assert.NoError(t, statErr, "must survive the sweep: %s", p)
	}
}

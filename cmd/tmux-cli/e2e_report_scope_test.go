package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedReportDir creates the conductor's owned dir with the given file names.
func seedReportDir(t *testing.T, repoRoot string, names []string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644))
	}
	return dir
}

// TestClearReports_SweepsOnlyOwnScenario: a fresh bootstrap of scenario A must
// remove ONLY A's per-cycle reports (plus legacy unscoped orphans) — scenario
// B's reports survive, and a sibling slug that extends A's ("scn" vs
// "scn-two") is never cross-swept in either direction.
func TestClearReports_SweepsOnlyOwnScenario(t *testing.T) {
	repoRoot := t.TempDir()
	dir := seedReportDir(t, repoRoot, []string{
		"e2e-report-scn-cycle-1.md",     // own — removed
		"e2e-report-scn-cycle-12.md",    // own, multi-digit cycle — removed
		"e2e-report-other-cycle-1.md",   // scenario B — kept
		"e2e-report-scn-two-cycle-1.md", // sibling slug extending ours — kept
		"e2e-report-cycle-1.md",         // legacy unscoped orphan — removed (migration)
		"e2e-report-cycle-3.md",         // legacy unscoped orphan — removed (migration)
		"scn.state.json",                // ledger — untouched
		"scn.state.md",                  // handoff — untouched (clearRunArtifacts owns it)
	})

	clearReports(repoRoot, "scn")

	assert.NoFileExists(t, filepath.Join(dir, "e2e-report-scn-cycle-1.md"))
	assert.NoFileExists(t, filepath.Join(dir, "e2e-report-scn-cycle-12.md"))
	assert.NoFileExists(t, filepath.Join(dir, "e2e-report-cycle-1.md"))
	assert.NoFileExists(t, filepath.Join(dir, "e2e-report-cycle-3.md"))

	assert.FileExists(t, filepath.Join(dir, "e2e-report-other-cycle-1.md"))
	assert.FileExists(t, filepath.Join(dir, "e2e-report-scn-two-cycle-1.md"))
	assert.FileExists(t, filepath.Join(dir, "scn.state.json"))
	assert.FileExists(t, filepath.Join(dir, "scn.state.md"))
}

// TestClearReports_SiblingSlugReverseDirection: clearing the LONGER sibling
// ("scn-two") must not sweep the shorter one's ("scn") reports.
func TestClearReports_SiblingSlugReverseDirection(t *testing.T) {
	repoRoot := t.TempDir()
	dir := seedReportDir(t, repoRoot, []string{
		"e2e-report-scn-cycle-1.md",     // shorter sibling — kept
		"e2e-report-scn-two-cycle-1.md", // own — removed
	})

	clearReports(repoRoot, "scn-two")

	assert.FileExists(t, filepath.Join(dir, "e2e-report-scn-cycle-1.md"))
	assert.NoFileExists(t, filepath.Join(dir, "e2e-report-scn-two-cycle-1.md"))
}

// TestClearReports_MissingDirIsNoop: a repo without the owned dir must not
// panic or create anything.
func TestClearReports_MissingDirIsNoop(t *testing.T) {
	repoRoot := t.TempDir()
	clearReports(repoRoot, "scn")
	assert.NoDirExists(t, filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator"))
}

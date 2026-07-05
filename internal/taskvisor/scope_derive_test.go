package taskvisor

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DeriveScopeFromDeliverables token hygiene (F5) ---------------------------

// TestDeriveScope_StripsDotSlash guards the falsely-disjoint stem bug: a
// './internal/x' token and a bare 'internal/x' token must normalize to ONE
// stem. Raw './'-prefixed stems never path-prefix-match their bare twins in
// globsOverlap, so the gate would co-schedule two goals editing the same tree.
func TestDeriveScope_StripsDotSlash(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Edit ./internal/x/file.go and wire it up",
		"Also touch internal/x/file.go for the same change",
		"Create ./cmd/tool/main.go",
	})
	assert.Equal(t, []string{"internal/x/file.go", "cmd/tool/main.go"}, got,
		"leading ./ must be stripped and the bare/dotted twins deduped to one stem")
}

// TestDeriveScope_StripsDotSlash_SingleSegment: './x' is path-like (the
// leading ./ is what carries the slash) and must survive normalization as the
// bare segment, not be dropped.
func TestDeriveScope_StripsDotSlash_SingleSegment(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{"Update ./Makefile targets"})
	assert.Equal(t, []string{"Makefile"}, got)
}

// TestDeriveScope_DropsPunctuationOnlyTokens: './...' (the go package
// wildcard) and other letterless tokens derive a garbage scope stem ("..."
// path-prefixes nothing real but poisons the set) and must be dropped.
func TestDeriveScope_DropsPunctuationOnlyTokens(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Run go vet over ./... before merging",
		"Keep internal/taskvisor/goals.go green",
	})
	assert.Equal(t, []string{"internal/taskvisor/goals.go"}, got)
}

func TestDeriveScope_DropsLetterlessTokens(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Score 1/2 in pkg/mod and note 3/4 ratio",
	})
	assert.Equal(t, []string{"pkg/mod"}, got, "digit-only path-like tokens carry no file footprint")
}

func TestDeriveScope_NilWhenNothingSurvivesHygiene(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{"Run go test ./... and ship it"})
	assert.Nil(t, got, "a deliverable whose only path-like token is './...' has UNKNOWN scope")
}

func TestDeriveScope_KeepsFirstSeenOrder(t *testing.T) {
	got := DeriveScopeFromDeliverables([]string{
		"Touch b/second.go then a/first.go",
		"Then b/second.go again",
	})
	assert.Equal(t, []string{"b/second.go", "a/first.go"}, got)
}

// TestDeriveScope_AcceptanceOnlySemantics documents the CALLERS' contract:
// CreateGoal derives scope from Acceptance ONLY — validate commands are too
// noisy (runner flags, ./... wildcards, tool paths) to mine for a footprint.
// A goal whose only path tokens live in Validate stays UNKNOWN (serialized).
func TestDeriveScope_AcceptanceOnlySemantics(t *testing.T) {
	dir := t.TempDir()

	_, derived, err := CreateGoal(dir, GoalSpec{
		Description: "Validate-only paths goal",
		Acceptance:  []string{"Behavior is correct end to end"},
		Validate:    []string{"go test ./internal/taskvisor/ -count=1"},
	})
	require.NoError(t, err)
	assert.False(t, derived)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Nil(t, gf.Goals[0].Scope, "validate paths must never leak into derived scope")
}

// --- DeriveScopeWithCompleteness (per-line completeness) ----------------------

// TestDeriveScopeWithCompleteness_AllLinesCovered: every non-empty line names a
// path → complete; scope equals the wrapper's output exactly.
func TestDeriveScopeWithCompleteness_AllLinesCovered(t *testing.T) {
	in := []string{"update cors.go in internal/x/cors.go", "edit internal/x/ratelimit.go"}
	scope, incomplete, uncovered := DeriveScopeWithCompleteness(in)
	assert.Equal(t, []string{"internal/x/cors.go", "internal/x/ratelimit.go"}, scope)
	assert.False(t, incomplete)
	assert.Nil(t, uncovered)
	assert.Equal(t, DeriveScopeFromDeliverables(in), scope, "wrapper must return identical scope")
}

// TestDeriveScopeWithCompleteness_PartialIsIncomplete: one bare line (no path
// token) downgrades the derivation to incomplete and is reported verbatim.
func TestDeriveScopeWithCompleteness_PartialIsIncomplete(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"return request id header",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.True(t, incomplete)
	assert.Equal(t, []string{"return request id header"}, uncovered)
}

// TestDeriveScopeWithCompleteness_NoLineCovered: zero path tokens anywhere →
// nil scope (same observable as today's UNKNOWN) but incomplete with both
// lines reported.
func TestDeriveScopeWithCompleteness_NoLineCovered(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{"do X", "do Y"})
	assert.Nil(t, scope)
	assert.True(t, incomplete)
	assert.Equal(t, []string{"do X", "do Y"}, uncovered)
}

// TestDeriveScopeWithCompleteness_BlankLinesNotCounted: blank/whitespace lines
// are NOT criteria — they never trigger a downgrade.
func TestDeriveScopeWithCompleteness_BlankLinesNotCounted(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"",
		"   ",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.False(t, incomplete)
	assert.Empty(t, uncovered)
}

// TestDeriveScopeWithCompleteness_DuplicatePathStillCoversLine: coverage is
// per-line and decided BEFORE dedup — a line whose only token was already
// emitted by an earlier line is still covered.
func TestDeriveScopeWithCompleteness_DuplicatePathStillCoversLine(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness([]string{
		"edit internal/x/cors.go",
		"also touch internal/x/cors.go",
	})
	assert.Equal(t, []string{"internal/x/cors.go"}, scope)
	assert.False(t, incomplete)
	assert.Empty(t, uncovered)
}

// TestDeriveScopeWithCompleteness_NoAcceptanceIsComplete: an empty input has no
// criteria to cover, so it is NOT incomplete (existing UNKNOWN default branch).
func TestDeriveScopeWithCompleteness_NoAcceptanceIsComplete(t *testing.T) {
	scope, incomplete, uncovered := DeriveScopeWithCompleteness(nil)
	assert.Nil(t, scope)
	assert.False(t, incomplete)
	assert.Nil(t, uncovered)
}

// --- goal-create scope resolution + zero-match guard (task 436) --------------

// initScopeGitRepo builds a real git repo in a temp dir with the given tracked
// files (relpath->content), committed clean. Reuses runGitCmd (autocommit_test.go).
// Skips when git is unavailable. The guard needs a real repo (git ls-files).
func initScopeGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.email", "taskvisor@test.local")
	runGitCmd(t, dir, "config", "user.name", "Taskvisor Test")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")
	return dir
}

// TestResolveScope_SingleFileTargetYieldsFilePathspec: a `<stem>/**` glob for a
// target that is actually a FILE (no such directory) must resolve to the exact
// file pathspec (or a `<stem>*` sibling glob), NEVER `<stem>/**` — so its edit
// is not silently dropped at auto-commit.
func TestResolveScope_SingleFileTargetYieldsFilePathspec(t *testing.T) {
	dir := initScopeGitRepo(t, map[string]string{"a/b/c.xml": "<x/>\n"})
	got := resolveScopeEntries(dir, []string{"a/b/c/**"})
	require.Len(t, got, 1)
	assert.NotEqual(t, "a/b/c/**", got[0], "a single-file target must never persist as a <stem>/** dir glob")
	assert.Contains(t, []string{"a/b/c.xml", "a/b/c*"}, got[0], "exact file or sibling glob")
	assert.Equal(t, []string{"a/b/c.xml"}, gitLsFiles(dir, got[0]), "resolved pathspec must match the tracked file")
}

// TestResolveScope_DirGlobPreserved: a `<stem>/**` glob over a REAL tracked
// directory is kept verbatim (no normalization).
func TestResolveScope_DirGlobPreserved(t *testing.T) {
	dir := initScopeGitRepo(t, map[string]string{"a/b/file.go": "package b\n"})
	got := resolveScopeEntries(dir, []string{"a/b/**"})
	assert.Equal(t, []string{"a/b/**"}, got, "a real directory glob is kept verbatim")
}

// TestValidateScope_ZeroMatchGlobFlagged: the goal-001 fixture — investigate.xml
// tracked, NO investigate/ dir. An un-normalized `.../investigate/**` matches
// zero tracked files and must be loudly WARN-flagged (non-fatal; not a file-stem).
func TestValidateScope_ZeroMatchGlobFlagged(t *testing.T) {
	dir := initScopeGitRepo(t, map[string]string{
		"cmd/tmux-cli/embedded/commands/tmux/investigate.xml":        "<x/>\n",
		"cmd/tmux-cli/embedded/commands/tmux/investigate-worker.xml": "<x/>\n",
	})
	require.Empty(t, gitLsFiles(dir, "cmd/tmux-cli/embedded/commands/tmux/investigate/**"),
		"fixture precondition: the bad glob matches zero tracked files")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	err := validateScopeEntries(dir, []string{"cmd/tmux-cli/embedded/commands/tmux/investigate/**"})
	require.NoError(t, err, "a non-file-stem zero-match is a loud WARN, not a reject")
	assert.Contains(t, buf.String(), "zero tracked files", "zero-match entry must be flagged loudly")
}

// TestValidateScope_FileStemGlobRejected: `<file>/**` where the stem is a tracked
// regular file is unambiguously wrong (a file has no children) — REJECT.
func TestValidateScope_FileStemGlobRejected(t *testing.T) {
	dir := initScopeGitRepo(t, map[string]string{"a/b/c.xml": "<x/>\n"})
	err := validateScopeEntries(dir, []string{"a/b/c.xml/**"})
	require.Error(t, err, "globbing children of a tracked FILE must be rejected")
}

// TestCreateGoal_SingleFileScopeNotDroppedAtAutoCommit: end-to-end goal-001 case.
// CreateGoal with an explicit `<stem>/**` scope for a tracked single file must
// persist a pathspec that MATCHES the file (via the same git :(glob) mechanism
// autoCommitGoal uses), proving the edit would not be silently dropped.
func TestCreateGoal_SingleFileScopeNotDroppedAtAutoCommit(t *testing.T) {
	dir := initScopeGitRepo(t, map[string]string{
		"cmd/tmux-cli/embedded/commands/tmux/investigate.xml": "<x/>\n",
	})
	_, _, err := CreateGoal(dir, GoalSpec{
		Description: "Fix investigate orchestrator",
		Validate:    []string{"make build"},
		Scope:       []string{"cmd/tmux-cli/embedded/commands/tmux/investigate/**"},
	})
	require.NoError(t, err)

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	persisted := gf.Goals[0].Scope
	require.Len(t, persisted, 1)
	assert.NotContains(t, persisted[0], "/**", "single-file target must not persist as a dir glob")
	assert.NotEmpty(t, gitLsFiles(dir, persisted[0]),
		"persisted scope must match the tracked file — the edit would not be dropped at auto-commit")
}

// TestValidateScope_NoGitRepoIsNoop: outside a git repo both helpers no-op
// (return input / nil error) so git-free authoring tests stay green.
func TestValidateScope_NoGitRepoIsNoop(t *testing.T) {
	dir := t.TempDir() // NOT a git repo
	require.NoError(t, validateScopeEntries(dir, []string{"anything/**", "a/b/c.xml/**"}),
		"validate must no-op outside a git repo")
	got := resolveScopeEntries(dir, []string{"a/b/c/**"})
	assert.Equal(t, []string{"a/b/c/**"}, got, "resolve must no-op outside a git repo")
}

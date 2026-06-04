//go:build integration

package taskvisor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runGit runs a real git command in dir, failing the test on error. Used only to
// build the fixture repo; the code under test drives git through the default
// runner (d.gitRunner() with no override).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// TestMergeWorktreeBack_Conflict exercises the real-repo conflict path end to
// end: a peer advances base so the worktree branch cannot rebase cleanly. The
// merge-back must abort, return errMergeConflict with the conflicting path, and
// leave base's HEAD untouched (no partial merge).
//
// Run with: go test -tags=integration ./internal/taskvisor/... -run TestMergeWorktreeBack_Conflict
func TestMergeWorktreeBack_Conflict(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))

	// Fixture repo on branch "main" with one tracked file.
	runGit(t, dir, "init", "-b", "main")
	shared := filepath.Join(dir, "shared.txt")
	require.NoError(t, os.WriteFile(shared, []byte("base line\n"), 0o644))
	runGit(t, dir, "add", "shared.txt")
	runGit(t, dir, "commit", "-m", "initial")

	d := New(dir, nil) // executor unused on this path
	goal := &Goal{ID: "goal-001"}

	// Create the goal's worktree from HEAD, then make a conflicting edit inside it.
	cwd, err := d.ensureWorktree(goal, true)
	require.NoError(t, err)
	wtShared := filepath.Join(cwd, "shared.txt")
	require.NoError(t, os.WriteFile(wtShared, []byte("worktree change\n"), 0o644))

	// A peer advances base with an overlapping change on the SAME line.
	require.NoError(t, os.WriteFile(shared, []byte("peer change\n"), 0o644))
	runGit(t, dir, "add", "shared.txt")
	runGit(t, dir, "commit", "-m", "peer advance")
	baseHeadBefore := runGit(t, dir, "rev-parse", "HEAD")

	// Merge-back must detect the conflict, abort, and surface the path.
	mergeErr := d.mergeWorktreeBack(goal)
	require.Error(t, mergeErr)
	var mc errMergeConflict
	require.True(t, errors.As(mergeErr, &mc), "want errMergeConflict, got %v", mergeErr)
	assert.Contains(t, mc.paths, "shared.txt")

	// Base HEAD is untouched — no partial merge landed.
	assert.Equal(t, baseHeadBefore, runGit(t, dir, "rev-parse", "HEAD"), "base must have no partial merge")

	// Cleanup leaves no worktree or branch behind.
	require.NoError(t, d.discardWorktree(goal))
	wl := runGit(t, dir, "worktree", "list")
	assert.NotContains(t, wl, filepath.Join(".tmux-cli", "worktrees", "goal-001"))
}

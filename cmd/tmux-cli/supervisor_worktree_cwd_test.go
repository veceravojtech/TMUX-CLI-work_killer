package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupervisorXml_Step4ThreadsWorktreeCwd guards the E1 worktree-isolation
// wiring in supervisor.xml's step 4 (spawn protocol): every execute-N
// implementer spawn must pass workingDirectory=<the goal's worktree> so the
// workers that actually EDIT files run inside the per-goal git worktree
// (E1-1a) instead of the base tree. Without this the worktree stays empty and
// mergeWorktreeBack captures nothing — isolation is non-functional. The MCP
// plumbing (WindowsSpawnWorker workingDirectory, E1-1c) already exists; this
// test pins the supervisor-prompt side of the contract.
func TestSupervisorXml_Step4ThreadsWorktreeCwd(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	for _, marker := range []string{
		// The spawn call must thread the worker's cwd.
		"workingDirectory",
		// Resolution sources: the E1-1c marker and the supervisor's own
		// worktree cwd (the daemon -c's the supervisor window there at dispatch).
		"taskvisor-current-worktree",
		".tmux-cli/worktrees/",
		// Resolved value placeholder shared with investigate.xml's E1-1c wiring.
		"WORKTREE_DIR",
	} {
		assert.Contains(t, content, marker,
			"supervisor.xml must carry worktree-cwd spawn wiring marker %q", marker)
	}

	// No-worktree case (MaxGoals=1 / empty marker) must OMIT the field so
	// spawns stay byte-identical to the pre-E1 behavior (session-default cwd).
	assert.Contains(t, content, "OMIT",
		"supervisor.xml must omit workingDirectory when no worktree is active")
}

// TestSupervisorXml_WorktreeWiringConfinedToStep4 asserts the worktree-cwd
// wiring lives ONLY inside step 4 (spawn protocol). audit-3 edits other
// sections of the same file; confining this change to step 4 keeps the two
// edits collision-free, and no other step has a windows-spawn-worker call
// that could consume workingDirectory anyway.
func TestSupervisorXml_WorktreeWiringConfinedToStep4(t *testing.T) {
	content := readEmbeddedCommand(t, "supervisor.xml")

	step4 := strings.Index(content, `<step n="4"`)
	step5 := strings.Index(content, `<step n="5"`)
	require.NotEqual(t, -1, step4, "supervisor.xml must have a step 4")
	require.NotEqual(t, -1, step5, "supervisor.xml must have a step 5")
	require.Less(t, step4, step5, "step 4 must precede step 5")

	for _, marker := range []string{"workingDirectory", "taskvisor-current-worktree", "WORKTREE_DIR"} {
		for idx := strings.Index(content, marker); idx != -1; {
			assert.GreaterOrEqual(t, idx, step4,
				"%q occurrence at offset %d must not appear before step 4", marker, idx)
			assert.Less(t, idx, step5,
				"%q occurrence at offset %d must not appear after step 4", marker, idx)
			next := strings.Index(content[idx+len(marker):], marker)
			if next == -1 {
				break
			}
			idx += len(marker) + next
		}
	}
}

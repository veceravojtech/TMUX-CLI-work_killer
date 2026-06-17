package taskvisor

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitfresh_test.go — fake-runner unit tests for the git-freshness preflight
// (goal-005). Every decision branch (in-sync / behind / diverged / ahead /
// fetch-fail / no-upstream) is asserted via the recording fakeGitRunner, with no
// real repo. The daemon-gate tests exercise the block disposition end-to-end.

// freshnessResponder builds a fakeGitRunner respond func that answers the
// preflight's git calls: rev-parse @{upstream} → upstream (or non-zero when
// upstream==""), fetch → fetchCode, rev-list → "<ahead>\t<behind>", pull →
// pullCode.
func freshnessResponder(upstream string, fetchCode, ahead, behind, pullCode int) func(args []string) (string, int) {
	return func(args []string) (string, int) {
		switch {
		case argsContain(args, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"):
			if upstream == "" {
				return "", 128 // no upstream / not a repo / detached
			}
			return upstream + "\n", 0
		case argsContain(args, "fetch"):
			return "", fetchCode
		case argsContain(args, "rev-list", "--left-right", "--count"):
			return strconv.Itoa(ahead) + "\t" + strconv.Itoa(behind) + "\n", 0
		case argsContain(args, "pull", "--ff-only"):
			return "", pullCode
		default:
			return "", 0
		}
	}
}

func TestPreflightGitFreshness_InSync(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 0, 0, 0)}
	action, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.NoError(t, err)
	assert.Equal(t, FreshnessInSync, action)
	assert.Equal(t, 0, f.count("pull", "--ff-only"), "in-sync must not pull")
	assert.Equal(t, 1, f.count("fetch", "origin"), "must fetch the upstream's remote")
}

func TestPreflightGitFreshness_Behind_FastForwards(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 0, 3, 0)}
	action, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.NoError(t, err)
	assert.Equal(t, FreshnessFastForward, action)
	assert.Equal(t, 1, f.count("pull", "--ff-only"), "strictly-behind must fast-forward")
}

func TestPreflightGitFreshness_Diverged_Refuses(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 2, 1, 0)}
	action, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diverged from origin")
	assert.Contains(t, err.Error(), "(2 ahead, 1 behind)")
	assert.Contains(t, err.Error(), "proj", "refusal must name the project")
	assert.Equal(t, 0, f.count("pull", "--ff-only"), "diverged must NOT pull")
	assert.NotEqual(t, FreshnessFastForward, action, "action must not advance on refusal")
}

func TestPreflightGitFreshness_AheadOnly_Proceeds(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 4, 0, 0)}
	action, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.NoError(t, err, "ahead-only is the normal post-commit state — never refuse")
	assert.Equal(t, FreshnessAhead, action)
	assert.Equal(t, 0, f.count("pull", "--ff-only"))
}

func TestPreflightGitFreshness_FetchFails_Refuses(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 1, 0, 0, 0)}
	_, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proj", "fetch failure must name the project")
	assert.Equal(t, 0, f.count("rev-list", "--left-right", "--count"), "no rev-list after a failed fetch")
	assert.Equal(t, 0, f.count("pull", "--ff-only"))
}

func TestPreflightGitFreshness_NoUpstream_Skips(t *testing.T) {
	f := &fakeGitRunner{respond: freshnessResponder("", 0, 0, 0, 0)}
	action, err := PreflightGitFreshness(context.Background(), f.run, "/work/proj", "proj")
	require.NoError(t, err)
	assert.Equal(t, FreshnessSkipped, action)
	assert.Equal(t, 0, f.count("fetch"), "no fetch when there is no upstream")
}

func TestGitFreshnessGate_Disabled_NoGitCalls(t *testing.T) {
	dir := t.TempDir()
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 2, 1, 0)}
	d := New(dir, new(testutil.MockTmuxExecutor))
	d.SetGitRunnerFunc(f.run)
	d.gitFreshness = false // OFF — gate is a no-op

	goal := &Goal{ID: "goal-005", Status: GoalPending}
	goals := &GoalsFile{Goals: []Goal{*goal}}
	require.NoError(t, d.gitFreshnessGate(goal, goals))
	assert.Equal(t, GoalPending, goal.Status, "disabled gate must not touch goal status")
	assert.Len(t, f.calls, 0, "disabled gate must make ZERO git calls")
}

func TestGitFreshnessGate_Diverged_BlocksGoal(t *testing.T) {
	dir := t.TempDir()
	f := &fakeGitRunner{respond: freshnessResponder("origin/master", 0, 2, 1, 0)}
	d := New(dir, new(testutil.MockTmuxExecutor))
	d.SetGitRunnerFunc(f.run)
	d.gitFreshness = true

	goal := &Goal{ID: "goal-005", Status: GoalPending}
	goals := &GoalsFile{Goals: []Goal{*goal}}

	// A diverged checkout blocks the goal but does NOT return an error (so it is
	// not miscounted as a dispatch crash).
	require.NoError(t, d.gitFreshnessGate(goal, goals))
	assert.Equal(t, GoalBlocked, goal.Status, "diverged checkout must block the goal")
	assert.False(t, goal.BlockedByPrecondition, "git-freshness block leaves auto-resume UNSET")

	// The blocked ValidatorSignal must be on disk.
	raw, err := os.ReadFile(SignalPath(dir, "goal-005"))
	require.NoError(t, err)
	var sig ValidatorSignal
	require.NoError(t, json.Unmarshal(raw, &sig))
	assert.Equal(t, "blocked", sig.Verdict)
	assert.Equal(t, "ops", sig.Owner)
	assert.Contains(t, strings.Join(findingDetails(&sig), " "), "diverged from origin")
}

// findingDetails flattens a signal's finding Detail strings for assertion.
func findingDetails(sig *ValidatorSignal) []string {
	var out []string
	for _, f := range sig.Findings {
		out = append(out, f.Detail)
	}
	return out
}

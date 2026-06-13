package taskvisor

import (
	"testing"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
)

// autopush_test.go — completion-time auto-push (taskvisor.auto_push). Recording-
// fake tests inject SetGitRunnerFunc and assert the exact `git push` argv without
// a real repo, mirroring autocommit_test.go's fakeGitRunner harness.

// autoPushDaemon builds a daemon over dir with the fake runner injected. The
// auto-push gate is left at its zero value (OFF); each test sets d.autoPush
// explicitly, since default-OFF is the contract for this outward-facing step.
func autoPushDaemon(t *testing.T, dir string, fake *fakeGitRunner) *Daemon {
	t.Helper()
	d := New(dir, new(testutil.MockTmuxExecutor))
	d.SetGitRunnerFunc(fake.run)
	return d
}

func TestAutoPush_EnabledPushesExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{}
	d := autoPushDaemon(t, dir, fake)
	d.autoPush = true

	d.autoPushOnCompletion()

	assert.Equal(t, 1, fake.count("push"), "enabled auto-push must emit exactly one git push")
	assert.Len(t, fake.calls, 1, "enabled auto-push must make exactly one git call (plain push, no extra args)")
}

func TestAutoPush_NoOpPushLogsNothingToPush(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			if argsContain(args, "push") {
				return "Everything up-to-date\n", 0
			}
			return "", 0
		},
	}
	d := autoPushDaemon(t, dir, fake)
	d.autoPush = true

	d.autoPushOnCompletion()

	assert.Equal(t, 1, fake.count("push"), "no-op push still emits exactly one git push")
	assert.Len(t, fake.calls, 1, "no-op detection must add no extra git calls (single plain push)")
}

func TestAutoPush_DisabledMakesNoGitCalls(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{}
	d := autoPushDaemon(t, dir, fake)
	d.autoPush = false

	d.autoPushOnCompletion()

	assert.Empty(t, fake.calls, "disabled (default) auto-push must make zero git calls")
}

func TestAutoPush_PushFailureIsWarnOnly(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGitRunner{
		respond: func(args []string) (string, int) {
			if argsContain(args, "push") {
				// e.g. "no configured upstream" — an ordinary warn-only path.
				return "fatal: The current branch has no upstream branch", 1
			}
			return "", 0
		},
	}
	d := autoPushDaemon(t, dir, fake)
	d.autoPush = true

	assert.NotPanics(t, func() { d.autoPushOnCompletion() },
		"a push failure must be warn-only — never panic, never block teardown")
	assert.Equal(t, 1, fake.count("push"), "warn-only path still attempts exactly one push")
}

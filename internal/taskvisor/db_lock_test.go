package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tryDBLockHeld reports whether the cross-process db.lock at <dir>/.tmux-cli/db.lock
// is currently held by SOME open file description in this process. flock locks
// obtained via independent open() calls are treated independently, so a
// non-blocking LOCK_EX on a fresh fd is DENIED (EWOULDBLOCK) while another fd in
// the same process holds it — the exact observation the validate-wrap test needs.
func tryDBLockHeld(t *testing.T, dir string) bool {
	t.Helper()
	f, err := os.OpenFile(DBLockPath(dir), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Could not acquire ⇒ someone else holds it ⇒ held.
		return true
	}
	// Acquired ⇒ not held; release immediately.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func TestWithDBLock_RunsFnUnderLock(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	ran := false
	err := WithDBLock(dir, func() error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, ran, "fn should run under the db lock")

	_, statErr := os.Stat(DBLockPath(dir))
	assert.NoError(t, statErr, "lock file should exist at .tmux-cli/db.lock")
	assert.Equal(t, filepath.Join(dir, ".tmux-cli", "db.lock"), DBLockPath(dir))
}

func TestWithDBLock_CreatesTmuxCliDir(t *testing.T) {
	dir := t.TempDir() // no .tmux-cli inside

	ran := false
	err := WithDBLock(dir, func() error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, ran)

	info, statErr := os.Stat(filepath.Join(dir, ".tmux-cli"))
	require.NoError(t, statErr, ".tmux-cli dir should be created by MkdirAll")
	assert.True(t, info.IsDir())
}

func TestWithDBLock_SerializesConcurrentHolders(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	var concurrent int32
	var maxObserved int32
	iterations := 50

	var wg sync.WaitGroup
	wg.Add(2)
	worker := func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			err := WithDBLock(dir, func() error {
				n := atomic.AddInt32(&concurrent, 1)
				for {
					old := atomic.LoadInt32(&maxObserved)
					if n <= old || atomic.CompareAndSwapInt32(&maxObserved, old, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&concurrent, -1)
				return nil
			})
			require.NoError(t, err)
		}
	}
	go worker()
	go worker()
	wg.Wait()

	assert.Equal(t, int32(1), maxObserved, "critical sections must never overlap (max concurrency 1)")
}

func TestWithDBLock_ReleasesOnError(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	sentinel := assert.AnError
	err := WithDBLock(dir, func() error {
		return sentinel
	})
	require.ErrorIs(t, err, sentinel, "fn error should propagate unchanged")

	// A subsequent acquire must be immediate (lock was released by defer LOCK_UN).
	done := make(chan struct{})
	go func() {
		_ = WithDBLock(dir, func() error { return nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second WithDBLock blocked — lock was not released on error")
	}
}

func TestRunValidateScript_HoldsDBLockDuringExec(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	var heldDuringExec bool
	d.SetScriptRunnerFunc(func(_ context.Context, _ string, _ string, _ []string) (string, string, int, error) {
		heldDuringExec = tryDBLockHeld(t, dir)
		return "", "", 0, nil
	})

	goal := &Goal{ID: "goal-001"}
	_, _, _, err = d.runValidateScript(goal)
	require.NoError(t, err)

	assert.True(t, heldDuringExec, "db.lock must be held during validate.sh exec")
	assert.False(t, tryDBLockHeld(t, dir), "db.lock must be released after runValidateScript returns")
}

func TestRunValidateScript_ResultUnchangedUnderLock(t *testing.T) {
	d, _, dir := setupDaemon(t)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	scriptPath := filepath.Join(goalDir, "validate.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755))

	goal := &Goal{ID: "goal-001"}
	passed, _, stderr, err := d.runValidateScript(goal)

	require.NoError(t, err)
	assert.True(t, passed, "a passing validate.sh still returns passed=true under the db lock")
	assert.Empty(t, stderr)
}

func TestGoal_MigratesFlagRoundTrips(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "migrating", Status: GoalPending, Migrates: true},
		{ID: "goal-002", Description: "plain", Status: GoalPending},
	}}
	require.NoError(t, SaveGoals(dir, gf))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Goals, 2)
	assert.True(t, loaded.Goals[0].Migrates, "migrates: true survives the round-trip")
	assert.False(t, loaded.Goals[1].Migrates, "absent migrates ⇒ false")
}

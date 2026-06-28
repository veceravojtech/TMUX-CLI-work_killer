package taskvisor

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

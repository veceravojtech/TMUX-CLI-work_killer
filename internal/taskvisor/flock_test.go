package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ensureTmuxCliDir(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
}

func TestWithGoalsLock_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)
	exec := new(testutil.MockTmuxExecutor)

	iterations := 50
	var wg sync.WaitGroup
	wg.Add(2)

	incrementGoal := func() {
		defer wg.Done()
		d := New(dir, exec)
		for i := 0; i < iterations; i++ {
			err := d.withGoalsLock(func() error {
				gf, err := LoadGoals(dir)
				if err != nil {
					return err
				}
				if gf == nil {
					gf = &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "counter", Retries: 0}}}
				}
				gf.Goals[0].Retries++
				return SaveGoals(dir, gf)
			})
			require.NoError(t, err)
		}
	}

	writeGoals(t, dir, &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "counter", Retries: 0}}})

	go incrementGoal()
	go incrementGoal()
	wg.Wait()

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, iterations*2, gf.Goals[0].Retries, "all increments should be reflected — no lost updates under flock")
}

func TestWithGoalsLock_BlocksSecondWriter(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)
	exec := new(testutil.MockTmuxExecutor)

	holdDuration := 200 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(2)

	startA := make(chan struct{})
	startB := make(chan struct{})

	var bStart, bEnd time.Time

	// Goroutine A: acquire lock, signal, hold for holdDuration, release
	go func() {
		defer wg.Done()
		d := New(dir, exec)
		err := d.withGoalsLock(func() error {
			close(startA)
			time.Sleep(holdDuration)
			return nil
		})
		require.NoError(t, err)
	}()

	// Goroutine B: wait for A to hold lock, then attempt to acquire
	go func() {
		defer wg.Done()
		<-startA
		time.Sleep(50 * time.Millisecond)
		close(startB)
		bStart = time.Now()
		d := New(dir, exec)
		err := d.withGoalsLock(func() error {
			bEnd = time.Now()
			return nil
		})
		require.NoError(t, err)
	}()

	wg.Wait()

	waited := bEnd.Sub(bStart)
	assert.Greater(t, waited, 100*time.Millisecond, "goroutine B should have waited for A to release the lock")
}

func TestWithGoalsLock_Exported_SameLockFile(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	err := WithGoalsLock(dir, func() error {
		return nil
	})
	require.NoError(t, err)

	lockPath := filepath.Join(dir, ".tmux-cli", "goals.yaml.lock")
	_, statErr := os.Stat(lockPath)
	assert.NoError(t, statErr, "lock file should exist at .tmux-cli/goals.yaml.lock")
}

func TestWithGoalsLock_Exported_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	iterations := 50
	var wg sync.WaitGroup
	wg.Add(2)

	increment := func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			err := WithGoalsLock(dir, func() error {
				gf, err := LoadGoals(dir)
				if err != nil {
					return err
				}
				if gf == nil {
					gf = &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "counter", Retries: 0}}}
				}
				gf.Goals[0].Retries++
				return SaveGoals(dir, gf)
			})
			require.NoError(t, err)
		}
	}

	writeGoals(t, dir, &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "counter", Retries: 0}}})

	go increment()
	go increment()
	wg.Wait()

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, iterations*2, gf.Goals[0].Retries, "all increments should be reflected — no lost updates under exported flock")
}

func TestCLIGoalAdd_ConcurrentWithDaemonPoll(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)
	exec := new(testutil.MockTmuxExecutor)

	writeGoals(t, dir, &GoalsFile{Goals: []Goal{{ID: "goal-001", Description: "counter", Retries: 0}}})

	iterations := 20
	var wg sync.WaitGroup
	wg.Add(2)

	// Simulate daemon using withGoalsLock (unexported, via Daemon)
	go func() {
		defer wg.Done()
		d := New(dir, exec)
		for i := 0; i < iterations; i++ {
			err := d.withGoalsLock(func() error {
				gf, err := LoadGoals(dir)
				if err != nil {
					return err
				}
				if gf == nil {
					return fmt.Errorf("goals nil")
				}
				gf.Goals[0].Retries++
				return SaveGoals(dir, gf)
			})
			require.NoError(t, err)
		}
	}()

	// Simulate CLI using exported WithGoalsLock
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			err := WithGoalsLock(dir, func() error {
				gf, err := LoadGoals(dir)
				if err != nil {
					return err
				}
				if gf == nil {
					return fmt.Errorf("goals nil")
				}
				gf.Goals[0].Retries++
				return SaveGoals(dir, gf)
			})
			require.NoError(t, err)
		}
	}()

	wg.Wait()

	gf, err := LoadGoals(dir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, iterations*2, gf.Goals[0].Retries, "CLI + daemon racing should not lose updates")
}

func TestCrashRecovery_HoldsFlock(t *testing.T) {
	dir := t.TempDir()
	ensureTmuxCliDir(t, dir)

	var wg sync.WaitGroup
	wg.Add(2)

	held := make(chan struct{})
	var bStart, bEnd time.Time

	// goroutine A: hold lock for 200ms (simulates crash recovery holding lock)
	go func() {
		defer wg.Done()
		err := WithGoalsLock(dir, func() error {
			close(held)
			time.Sleep(200 * time.Millisecond)
			return nil
		})
		require.NoError(t, err)
	}()

	// goroutine B: wait for A to hold, then attempt lock
	go func() {
		defer wg.Done()
		<-held
		time.Sleep(50 * time.Millisecond)
		bStart = time.Now()
		err := WithGoalsLock(dir, func() error {
			bEnd = time.Now()
			return nil
		})
		require.NoError(t, err)
	}()

	wg.Wait()

	waited := bEnd.Sub(bStart)
	assert.Greater(t, waited, 100*time.Millisecond, "concurrent lock holder should block until first releases")
}

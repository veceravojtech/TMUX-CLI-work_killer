package taskvisor

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestM01_PreflightPreconditionBlock (alias TestM01_UnsetSecret): a goal with an
// unset env precondition is blocked before any worker spawn — signal.json is
// written with verdict=blocked/class=env-config/owner=ops, no window is created,
// the retry counter is untouched, the goal is marked blocked, and dispatch
// returns nil (handled, not an error).
func TestM01_PreflightPreconditionBlock(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_UNSET_DB_USER"
	os.Unsetenv(envSpec)

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "needs DB_USER",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec + "=..."},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	// No windows exist, so the kill/wait lookups all return empty.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	goal := &gf.Goals[0]
	retriesBefore := goal.Retries
	err = d.dispatch(goal, gf)
	require.NoError(t, err, "block path returns nil, not an error")

	assert.Equal(t, 0, createCount, "no worker window may be created on a block")
	assert.Equal(t, retriesBefore, goal.Retries, "retry counter must be untouched on a block")
	assert.Equal(t, GoalBlocked, goal.Status, "goal must be marked blocked")
	assert.True(t, goal.BlockedByPrecondition, "env/infra precondition block must flag the goal for §5 auto-resume")

	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	vs, ok := sig.(*ValidatorSignal)
	require.True(t, ok, "block signal must be a validator signal")
	assert.Equal(t, "blocked", vs.Verdict)
	assert.Equal(t, "env-config", vs.Class)
	assert.Equal(t, "ops", vs.Owner)
	assert.NotEmpty(t, vs.Remedy, "remedy runbook must be present")
	require.Len(t, vs.Findings, 1)
	assert.Equal(t, envSpec, vs.Findings[0].Rule)
	assert.Equal(t, "blocked", vs.Findings[0].Status)

	// Persisted goal status confirms the re-dispatch loop guard.
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, found := loaded.GoalByID("goal-001")
	require.True(t, found)
	assert.Equal(t, GoalBlocked, g.Status)
	assert.True(t, g.BlockedByPrecondition, "BlockedByPrecondition must persist so the resume loop can re-evaluate")
}

func TestEvaluatePreconditions_ServiceUnreachable(t *testing.T) {
	d, _, _ := setupDaemon(t)

	// Bind then immediately release a port so dialing it reliably fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	goal := &Goal{
		ID: "goal-001",
		Preconditions: []Precondition{
			{Kind: "service", Spec: addr, Remedy: "start the service on " + addr},
		},
	}

	ok, class, remedy := d.evaluatePreconditions(goal)
	assert.False(t, ok, "unreachable service must fail the precondition")
	assert.Equal(t, "infra-flake", class)
	assert.Equal(t, "start the service on "+addr, remedy)
}

func TestEvaluatePreconditions_AllPass(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_SET_VAR"
	t.Setenv(envSpec, "present")

	// A live listener makes the service precondition reachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "all preconds satisfied",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec},
				{Kind: "service", Spec: ln.Addr().String(), Remedy: "start svc"},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err = EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	ok, class, remedy := d.evaluatePreconditions(goal)
	assert.True(t, ok, "all preconditions satisfied → pass")
	assert.Empty(t, class)
	assert.Empty(t, remedy)

	// And dispatch spawns the supervisor exactly as before.
	setupDispatchMocks(exec, testSession, "@0")
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	err = d.dispatch(goal, gf)
	require.NoError(t, err)
	assert.Equal(t, 1, createCount, "supervisor window must be spawned when preconditions pass")
	assert.Equal(t, GoalRunning, goal.Status)
}

// TestDispatchBlockedGoalNotRedispatched: a blocked goal lands in GoalBlocked so
// the next pending-goal selection skips it — the existing mechanism that halts
// the daemon's re-dispatch loop. The block signal is the only artifact written
// and no worker windows are created.
func TestDispatchBlockedGoalNotRedispatched(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	const envSpec = "TV_PRECOND_UNSET_REDISPATCH"
	os.Unsetenv(envSpec)

	gf := &GoalsFile{
		Goals: []Goal{{
			ID:          "goal-001",
			Description: "blocked goal",
			Status:      GoalPending,
			Preconditions: []Precondition{
				{Kind: "env", Spec: envSpec, Remedy: "export " + envSpec},
			},
		}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)
	createCount := 0
	d.SetWindowCreateFunc(countingCreateWindowFn(&createCount, "@0"))

	goal := &gf.Goals[0]
	retriesBefore := goal.Retries
	require.NoError(t, d.dispatch(goal, gf))

	assert.Equal(t, 0, createCount, "no supervisor/execute-* window on a block")
	assert.Equal(t, retriesBefore, goal.Retries, "retries untouched")
	assert.Equal(t, GoalBlocked, goal.Status)

	// signal.json is present (the sole signal artifact).
	sig, err := LoadSignal(dir, "goal-001")
	require.NoError(t, err)
	require.NotNil(t, sig)

	// Reloading and selecting the next pending goal must skip the blocked one,
	// so the poll loop never re-dispatches it.
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	_, hasPending := loaded.NextPendingGoal()
	assert.False(t, hasPending, "blocked goal must not be selected as pending again")
}

// --- C6 convergence circuit-breaker (M03) ---

// TestBackfill_Goal002Preconditions — the goal.md the first-run backfill writes
// carries a ## Preconditions section while preserving the other goal content.
// (The first-run trigger itself is an LLM step in task-plan-generate.xml; this
// pins WriteGoalMD's precondition emission, the section that backfill produces.)
func TestBackfill_Goal002Preconditions(t *testing.T) {
	dir := t.TempDir()
	goalDir, err := EnsureGoalDir(dir, "goal-002")
	require.NoError(t, err)

	require.NoError(t, WriteGoalMD(goalDir, "Scaffold goal-002", "scaffold",
		[]string{"PA-01 scaffolding present"}, []string{"go build ./..."},
		[]Precondition{{Kind: "env", Spec: "DATABASE_URL", Remedy: "export DATABASE_URL"}},
		"context preserved", "out-of-scope preserved", nil))

	data, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	md := string(data)

	assert.Contains(t, md, "## Preconditions", "backfill injects the Preconditions section")
	assert.Contains(t, md, "DATABASE_URL", "precondition spec rendered")
	// Other content preserved.
	assert.Contains(t, md, "# Scaffold goal-002")
	assert.Contains(t, md, "PA-01 scaffolding present")
	assert.Contains(t, md, "go build ./...")
	assert.Contains(t, md, "context preserved")
	assert.Contains(t, md, "out-of-scope preserved")
}

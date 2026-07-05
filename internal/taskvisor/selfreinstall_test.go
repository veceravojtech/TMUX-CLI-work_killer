package taskvisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCliCheckout stamps dir as a tmux-cli source checkout: a Makefile plus a
// go.mod declaring the cli module path — what setup.IsCliSourceCheckout keys on.
func writeCliCheckout(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("install:\n\ttrue\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/console/tmux-cli\n\ngo 1.25.5\n"), 0o644))
}

// reinstallGoal is a running goal whose declared Scope prefix-matches the cli
// source set, so the non-git fallback predicate fires in plain-TempDir tests.
func reinstallGoal(id string) Goal {
	g := routeGoal(id, 2, 2, 1, 0)
	g.Scope = []string{"internal/taskvisor/**"}
	return g
}

// gitIn runs git in dir, failing the test on a non-zero exit.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// writeFileIn writes a file under dir creating parent dirs.
func writeFileIn(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// seedCliRepo initializes a git repo with one seed commit containing a tracked
// internal/ file and a tracked docs/ file, returning the repo dir.
func seedCliRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@test.local")
	gitIn(t, dir, "config", "user.name", "test")
	writeFileIn(t, dir, "internal/taskvisor/a.go", "package taskvisor\n")
	writeFileIn(t, dir, "docs/readme.md", "docs\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "seed")
	return dir
}

// --- goalTouchesCliSource: git-backed predicate -----------------------------

func TestGoalTouchesCliSource_GitDiff(t *testing.T) {
	goal := &Goal{ID: "goal-001"}

	t.Run("uncommitted change under internal/ -> true", func(t *testing.T) {
		dir := seedCliRepo(t)
		writeFileIn(t, dir, "internal/taskvisor/a.go", "package taskvisor\n// fixed\n")
		d := New(dir, nil)
		assert.True(t, d.goalTouchesCliSource(dir, goal))
	})

	t.Run("change only under docs/ -> false", func(t *testing.T) {
		dir := seedCliRepo(t)
		writeFileIn(t, dir, "docs/readme.md", "docs v2\n")
		d := New(dir, nil)
		assert.False(t, d.goalTouchesCliSource(dir, goal))
	})

	t.Run("untracked cmd/x.go -> true", func(t *testing.T) {
		dir := seedCliRepo(t)
		writeFileIn(t, dir, "cmd/x.go", "package main\n")
		d := New(dir, nil)
		assert.True(t, d.goalTouchesCliSource(dir, goal))
	})

	t.Run("committed internal/ change on a worktree branch -> true", func(t *testing.T) {
		dir := seedCliRepo(t)
		wt := filepath.Join(dir, ".tmux-cli", "worktrees", "goal-001")
		gitIn(t, dir, "worktree", "add", "-b", "taskvisor/goal-001", wt)
		writeFileIn(t, wt, "internal/taskvisor/fix.go", "package taskvisor\n")
		gitIn(t, wt, "add", "-A")
		gitIn(t, wt, "commit", "-m", "fix")
		d := New(dir, nil)
		assert.True(t, d.goalTouchesCliSource(wt, goal),
			"clean-status worktree with the fix already committed must be caught by the merge-base diff arm")
	})
}

func TestGoalTouchesCliSource_FallbackToScope(t *testing.T) {
	dir := t.TempDir() // non-git: git enumeration fails -> declared-footprint fallback
	d := New(dir, nil)

	in := &Goal{ID: "goal-001", Scope: []string{"internal/taskvisor/**"}}
	assert.True(t, d.goalTouchesCliSource(dir, in), "Scope prefix inside the cli source set")

	out := &Goal{ID: "goal-001", Scope: []string{"projects/api/**"}}
	assert.False(t, d.goalTouchesCliSource(dir, out), "Scope prefix outside the cli source set")

	da := &Goal{ID: "goal-001", DeliverableArea: "cmd/tmux-cli/"}
	assert.True(t, d.goalTouchesCliSource(dir, da), "DeliverableArea participates in the fallback match")
}

// --- maybeSelfReinstall: hook semantics --------------------------------------

func TestMaybeSelfReinstall_InvokesOncePerCycle(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeCliCheckout(t, dir)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	calls := 0
	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		calls++
		return selfUpdateResult{Restart: "none"}, nil
	}

	d.maybeSelfReinstall(goal, gf)
	d.maybeSelfReinstall(goal, gf)
	assert.Equal(t, 1, calls, "same-cycle re-entry must not rebuild twice")
	assert.Equal(t, CurrentCycle(goal), goal.LastSelfReinstallCycle, "cycle stamp set on the goal")

	reloaded, err := LoadGoals(dir)
	require.NoError(t, err)
	rg, ok := reloaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, CurrentCycle(goal), rg.LastSelfReinstallCycle,
		"stamp persisted to goals.yaml — the one-rebuild-per-cycle guarantee is crash-safe")

	goal.CodeRetries-- // consume budget: a retry cycle bumps CurrentCycle
	d.maybeSelfReinstall(goal, gf)
	assert.Equal(t, 2, calls, "a new goal cycle rebuilds again")
}

func TestMaybeSelfReinstall_BuildFailureNonDestructive(t *testing.T) {
	d, mockExec, dir := setupDaemon(t)
	d.session = testSession
	noWindows(mockExec)
	writeCliCheckout(t, dir)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]
	before := *goal

	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		return selfUpdateResult{Stage: "build"}, fmt.Errorf("make install: exit status 2")
	}

	require.NotPanics(t, func() { d.maybeSelfReinstall(goal, gf) })

	assert.Equal(t, before.Status, goal.Status, "goal status untouched by a failed rebuild")
	assert.Equal(t, before.CodeRetries, goal.CodeRetries, "code budget untouched")
	assert.Equal(t, before.SpecRetries, goal.SpecRetries, "spec budget untouched")
	assert.Equal(t, before.ValidationRetries, goal.ValidationRetries, "validation budget untouched")
	_, err := os.Stat(filepath.Join(dir, ".tmux-cli", "taskvisor-restart"))
	assert.True(t, os.IsNotExist(err), "no restart marker on build failure")
	assert.Equal(t, CurrentCycle(goal), goal.LastSelfReinstallCycle,
		"stamp set on failure too — no rebuild thrash within the cycle")
}

func TestMaybeSelfReinstall_NudgeOnBinaryChanged(t *testing.T) {
	t.Run("binary_changed true zeroes lastStaleCheck", func(t *testing.T) {
		d, _, dir := setupDaemon(t)
		writeCliCheckout(t, dir)
		// The throttle-nudge is now gated behind a stale-binary adoption flag (task
		// 445): with a restart flag ON the original unconditional-nudge behavior is
		// preserved, so this test keeps asserting the zero — it is NOT weakened.
		d.restartOnStaleBinary = true
		gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
		writeGoals(t, dir, gf)
		d.lastStaleCheck = time.Now()

		d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
			return selfUpdateResult{BinaryChanged: true, Restart: "daemon"}, nil
		}
		d.maybeSelfReinstall(&gf.Goals[0], gf)

		assert.True(t, d.lastStaleCheck.IsZero(),
			"binary_changed:true un-throttles checkStaleBinary so adoption fires next tick")
	})

	t.Run("binary_changed false leaves the throttle untouched", func(t *testing.T) {
		d, _, dir := setupDaemon(t)
		writeCliCheckout(t, dir)
		gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
		writeGoals(t, dir, gf)
		stamp := time.Now()
		d.lastStaleCheck = stamp

		d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
			return selfUpdateResult{BinaryChanged: false, Restart: "none"}, nil
		}
		d.maybeSelfReinstall(&gf.Goals[0], gf)

		assert.Equal(t, stamp, d.lastStaleCheck, "a no-op rebuild must not nudge adoption")
	})
}

// TestMaybeSelfReinstall_NoRestartWhenFlagsOff pins route A (task 445): with both
// stale-binary adoption flags OFF and binary_changed:true, the rebuild still runs
// (cycle stamped) but the lastStaleCheck throttle-nudge is SUPPRESSED — so the
// next-tick checkStaleBinary stays gated and no mid-goal exec-replace is armed
// between VerdictPass and the done→auto-commit step.
func TestMaybeSelfReinstall_NoRestartWhenFlagsOff(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeCliCheckout(t, dir)
	require.False(t, d.restartOnStaleBinary, "default: restart_on_stale_binary off")
	require.False(t, d.haltOnStaleBinary, "default: halt_on_stale_binary off")
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	stamp := time.Now()
	d.lastStaleCheck = stamp

	built := false
	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		built = true
		return selfUpdateResult{BinaryChanged: true, Restart: "daemon"}, nil
	}
	d.maybeSelfReinstall(&gf.Goals[0], gf)

	assert.True(t, built, "the rebuild still runs so validators see the fresh binary")
	assert.Equal(t, stamp, d.lastStaleCheck,
		"both flags off ⇒ throttle-nudge suppressed; no mid-goal exec-replace armed")
	assert.Equal(t, CurrentCycle(&gf.Goals[0]), gf.Goals[0].LastSelfReinstallCycle,
		"rebuild guard stamp still set (the stamp guards the rebuild, not the restart)")
}

// TestMaybeSelfReinstall_RestartWhenFlagOn guards against over-gating: with
// restart_on_stale_binary ON and binary_changed:true, the throttle-nudge STILL
// fires (existing behavior preserved) so the operator's opted-in stale-binary
// restart adopts the freshly installed binary next tick.
func TestMaybeSelfReinstall_RestartWhenFlagOn(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeCliCheckout(t, dir)
	d.restartOnStaleBinary = true
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	d.lastStaleCheck = time.Now()

	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		return selfUpdateResult{BinaryChanged: true, Restart: "daemon"}, nil
	}
	d.maybeSelfReinstall(&gf.Goals[0], gf)

	assert.True(t, d.lastStaleCheck.IsZero(),
		"restart_on_stale_binary ON ⇒ nudge fires; adoption exec-replaces next tick")
}

// TestMaybeSelfReinstall_HaltFlagAlsoNudges: the halt_on_stale_binary flag is the
// second opt-in that arms adoption — with it ON (restart still off) the nudge
// fires, since either flag means the operator wants stale-binary handling.
func TestMaybeSelfReinstall_HaltFlagAlsoNudges(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeCliCheckout(t, dir)
	d.haltOnStaleBinary = true
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	d.lastStaleCheck = time.Now()

	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		return selfUpdateResult{BinaryChanged: true, Restart: "daemon"}, nil
	}
	d.maybeSelfReinstall(&gf.Goals[0], gf)

	assert.True(t, d.lastStaleCheck.IsZero(),
		"halt_on_stale_binary ON is also an opt-in ⇒ nudge fires")
}

func TestMaybeSelfReinstall_SkipsNonCliCheckout(t *testing.T) {
	d, _, dir := setupDaemon(t) // plain project dir: no Makefile/go.mod
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)
	goal := &gf.Goals[0]

	calls := 0
	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		calls++
		return selfUpdateResult{}, nil
	}
	d.maybeSelfReinstall(goal, gf)

	assert.Equal(t, 0, calls, "never fires outside a tmux-cli source checkout")
	assert.Equal(t, 0, goal.LastSelfReinstallCycle, "no stamp when the hook does not fire")
}

func TestMaybeSelfReinstall_ProjectIsBaseDir(t *testing.T) {
	d, _, dir := setupDaemon(t)
	wt := t.TempDir()
	writeCliCheckout(t, wt)
	d.runtime("goal-001").WorktreeDir = wt
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
	writeGoals(t, dir, gf)

	gotSource, gotProject := "", ""
	d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
		gotSource, gotProject = sourceDir, projectDir
		return selfUpdateResult{BinaryChanged: true, Restart: "daemon"}, nil
	}
	d.maybeSelfReinstall(&gf.Goals[0], gf)

	assert.Equal(t, wt, gotSource, "--source is the goal's worktree (the fix is not merged to base yet)")
	assert.Equal(t, dir, gotProject,
		"--project is ALWAYS the base project dir — the daemon watches .tmux-cli/taskvisor-restart there, a worktree-placed marker is never seen")
}

// --- statemachine call site ---------------------------------------------------

func TestCheckSupervisingPhase_ReinstallBeforeValidator(t *testing.T) {
	t.Run("inline validator path", func(t *testing.T) {
		d, mockExec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive
		d.validatorSendDelay = 0
		d.runtime("goal-001").phase = phaseSupervising
		writeCliCheckout(t, dir)

		gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{reinstallGoal("goal-001")}}
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)
		require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
			Status: "done", Timestamp: "2026-07-02T10:00:00Z",
		}))

		var events []string
		d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
			events = append(events, "reinstall")
			return selfUpdateResult{Restart: "none"}, nil
		}
		// Supervisor teardown kills (execute prefix + supervisor window).
		mockExec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		mockExec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		setupValidatorMocks(mockExec, testSession, "@5")
		d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
			events = append(events, "validator")
			return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
		})

		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

		assert.Equal(t, []string{"reinstall", "validator"}, events,
			"the rebuild fires after supervisor teardown and BEFORE any validator window exists")
		assert.Equal(t, phaseValidating, d.runtime("goal-001").phase)
	})

	t.Run("deferred-validation branch", func(t *testing.T) {
		d, mockExec, dir := setupDaemon(t)
		d.session = testSession
		d.mode = modeActive
		d.skipValidation = true
		d.autoCommit = false
		d.runtime("goal-001").phase = phaseSupervising
		writeCliCheckout(t, dir)

		goal := routeGoal("goal-001", 2, 2, 1, 0)
		// Empty Scope keeps the inline zero-integration gate out of play;
		// DeliverableArea alone drives the non-git fallback predicate.
		goal.DeliverableArea = "internal/taskvisor/"
		gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{
			goal,
			{ID: "goal-002", Description: "next", Status: GoalPending},
			{ID: "goal-v01", Description: "validate impl", Status: GoalPending,
				Validates: "goal-001", DependsOn: []string{"goal-001"}},
		}}
		writeGoals(t, dir, gf)
		_, err := EnsureGoalDir(dir, "goal-001")
		require.NoError(t, err)
		require.NoError(t, SaveSupervisorSignal(dir, "goal-001", &SupervisorSignal{
			Status: "done", Timestamp: "2026-07-02T10:00:00Z",
		}))

		var events []string
		d.selfUpdateFn = func(sourceDir, projectDir string) (selfUpdateResult, error) {
			events = append(events, "reinstall")
			return selfUpdateResult{Restart: "none"}, nil
		}
		validatorCreated := false
		d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
			validatorCreated = true
			return &CreatedWindow{TmuxWindowID: "@5", Name: name}, nil
		})
		// Only the two teardown kill lookups — NO validator spawn on the defer path.
		mockExec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()
		mockExec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Once()

		require.NoError(t, d.checkSupervisingPhase(&gf.Goals[0], gf))

		assert.Equal(t, []string{"reinstall"}, events,
			"the rebuild fires on the deferred-validation branch too — the hook sits before the fork")
		assert.False(t, validatorCreated, "no inline validator on the defer path")
		assert.Equal(t, GoalDone, gf.Goals[0].Status)
	})
}

// --- goals.yaml stamp lifecycle -----------------------------------------------

func TestResetGoal_ClearsSelfReinstallStamp(t *testing.T) {
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalFailed, LastSelfReinstallCycle: 3},
	}}

	require.True(t, gf.ResetGoal("goal-001"))

	assert.Equal(t, 0, gf.Goals[0].LastSelfReinstallCycle,
		"ResetGoal clears the stamp — a re-pended goal rebuilds on its fresh cycle")
}

// --- default selfUpdateFn JSON parsing (TC-parse) -------------------------------

func TestParseSelfUpdateOutput(t *testing.T) {
	res, err := parseSelfUpdateOutput([]byte(
		"{\"binary_changed\":true,\"source\":\"/src/wt\",\"restart\":\"daemon\"}\n"))
	require.NoError(t, err)
	assert.True(t, res.BinaryChanged)
	assert.Equal(t, "/src/wt", res.Source)
	assert.Equal(t, "daemon", res.Restart)

	_, err = parseSelfUpdateOutput([]byte("make[1]: Entering directory\nnot-json"))
	require.Error(t, err, "garbage stdout is an error — the caller's build-failure path")
}

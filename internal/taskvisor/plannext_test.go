package taskvisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// writeProductCompleteMarker writes the .tmux-cli/taskvisor-product-complete
// marker the incremental generator emits when every deliverable is met.
func writeProductCompleteMarker(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, ".tmux-cli", "taskvisor-product-complete")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("all deliverables verified"), 0o644))
}

// writeDiscoveryEvidence writes a docs/architecture/product-brief.md discovery
// artifact — the product-discovery evidence Option A (task 412) gates incremental
// generation on. Its presence makes hasDiscoveryEvidence() true.
func writeDiscoveryEvidence(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "docs", "architecture", "product-brief.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("# Product Brief\n"), 0o644))
}

// setupPlanNextDispatchMocks programs the ListWindows sequence one
// dispatchPlanNext makes: 2 empty (killWindowByName plan-next + waitWindowsGone),
// then the booted plan-next window (waitClaudeBoot + waitForPrompt + the trailing
// notifySupervisor lookup, which finds no bare "supervisor" and silently skips).
// Returns a pointer to the captured SendMessage commands.
func setupPlanNextDispatchMocks(exec *testutil.MockTmuxExecutor, session, winID string) *[]string {
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{}, nil).Times(2)
	exec.On("ListWindows", session).Return([]tmux.WindowInfo{
		{TmuxWindowID: winID, Name: planNextWindow, CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", session, winID).Return("ready ❯ ", nil)
	sent := &[]string{}
	exec.On("SendMessage", session, winID, mock.Anything).Run(func(args mock.Arguments) {
		*sent = append(*sent, args.Get(2).(string))
	}).Return(nil)
	return sent
}

// TestDispatchCommand_PlanNext pins the exact generator invocation: the seam
// contract with task-plan-generate.xml step 0a is the literal `incremental`
// argument (primary mode signal — robust across setting.yaml rewrites).
func TestDispatchCommand_PlanNext(t *testing.T) {
	got := dispatchCommand(DispatchPlanNext, DispatchArgs{})
	assert.Equal(t, "/tmux:task-plan-generate incremental", got)
	assert.Equal(t, "plan-next", DispatchPlanNext.String())
}

// TestMaxGoals_IncrementalCoercesToOne proves incremental planning coerces the
// in-flight cap to 1 ahead of setting.yaml (no concurrency, ever), while roadmap
// mode keeps the configured multi-goal bound byte-identically.
func TestMaxGoals_IncrementalCoercesToOne(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeSettingsMaxGoals(t, dir, 4)

	assert.Equal(t, 4, d.maxGoals(), "roadmap mode keeps the configured cap")

	d.planningMode = setup.PlanningModeIncremental
	assert.Equal(t, 1, d.maxGoals(), "incremental mode coerces the cap to 1")
}

// TestTrailingConsecutiveFailures covers the K-consecutive-failure guard input:
// only the UNBROKEN failed tail of the ledger counts (a done goal resets it).
func TestTrailingConsecutiveFailures(t *testing.T) {
	tests := []struct {
		name string
		gf   *GoalsFile
		want int
	}{
		{"empty ledger", &GoalsFile{}, 0},
		{"all done", &GoalsFile{Goals: []Goal{{Status: GoalDone}, {Status: GoalDone}}}, 0},
		{"failed tail of two", &GoalsFile{Goals: []Goal{{Status: GoalDone}, {Status: GoalFailed}, {Status: GoalFailed}}}, 2},
		{"done breaks the streak", &GoalsFile{Goals: []Goal{{Status: GoalFailed}, {Status: GoalDone}, {Status: GoalFailed}}}, 1},
		{"all failed", &GoalsFile{Goals: []Goal{{Status: GoalFailed}, {Status: GoalFailed}, {Status: GoalFailed}}}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, trailingConsecutiveFailures(tt.gf))
		})
	}
}

// TestIncrementalShouldGenerate covers the whole next-goal decision: generate
// only when the marker is absent, the cap and failure guards hold, every authored
// goal is terminal (done|failed), AND discovery evidence exists (Option A, task
// 412 — a terminal ledger with no product spec idles). Ordering matters: the
// no-evidence cases are asserted BEFORE writeDiscoveryEvidence, which then
// persists for the remainder of this single-dir test (matching greenfield state).
func TestIncrementalShouldGenerate(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.planningMode = setup.PlanningModeIncremental

	// Option A: with NO discovery artifact, even a terminal/empty ledger idles.
	assert.False(t, d.incrementalShouldGenerate(&GoalsFile{}), "empty ledger, no discovery evidence → idle (Option A)")
	terminalNoEvidence := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	assert.False(t, d.incrementalShouldGenerate(terminalNoEvidence), "terminal ledger, no discovery evidence → idle (Option A)")

	// Introduce discovery evidence — the generator now has a product spec to
	// ground on; the fixture persists for every assertion below.
	writeDiscoveryEvidence(t, dir)

	assert.True(t, d.incrementalShouldGenerate(&GoalsFile{}), "empty ledger + discovery evidence → author goal-001")

	done := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	assert.True(t, d.incrementalShouldGenerate(done), "terminal ledger + discovery evidence → author the next goal")

	pending := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalPending}}}
	assert.False(t, d.incrementalShouldGenerate(pending), "non-terminal goal → never dispatch the generator")

	blocked := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalBlocked}}}
	assert.False(t, d.incrementalShouldGenerate(blocked), "blocked goal is human-owned → no generation")

	failedTail := &GoalsFile{Goals: []Goal{
		{Status: GoalFailed}, {Status: GoalFailed}, {Status: GoalFailed},
	}}
	assert.False(t, d.incrementalShouldGenerate(failedTail), "K consecutive failures → halt")

	var capped GoalsFile
	for i := 0; i < incrementalMaxGoals; i++ {
		capped.Goals = append(capped.Goals, Goal{Status: GoalDone})
	}
	assert.False(t, d.incrementalShouldGenerate(&capped), "cap reached → halt")

	writeProductCompleteMarker(t, dir)
	assert.False(t, d.incrementalShouldGenerate(&GoalsFile{}), "product-complete marker → never generate")
}

// TestTick_Incremental_EmptyLedgerDispatchesGenerator proves activation with no
// pending/running/roadmap goal and no product-complete marker dispatches the
// generator (/tmux:task-plan-generate incremental) instead of tearing down.
func TestTick_Incremental_EmptyLedgerDispatchesGenerator(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeDiscoveryEvidence(t, dir)

	sent := setupPlanNextDispatchMocks(exec, testSession, "@7")
	var createdName string
	d.SetWindowCreateFunc(func(name, command, cwd string) (*CreatedWindow, error) {
		createdName = name
		return &CreatedWindow{TmuxWindowID: "@7", Name: name}, nil
	})

	require.NoError(t, d.tick(context.Background(), &GoalsFile{}))

	assert.Equal(t, planNextWindow, createdName, "the generator window is requested")
	require.Len(t, *sent, 1)
	assert.Equal(t, "/tmux:task-plan-generate incremental", (*sent)[0])
	assert.True(t, d.planNext.inFlight, "an episode is open after dispatch")
	assert.Equal(t, modeActive, d.mode, "the daemon stays active while generating")
}

// TestTick_Incremental_TerminalGoalTriggersNextGeneration proves the loop core:
// once every authored goal is terminal (here: goal-001 done), the next tick
// dispatches the generator again for the next goal instead of deactivating.
func TestTick_Incremental_TerminalGoalTriggersNextGeneration(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental

	writeDiscoveryEvidence(t, dir)
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	sent := setupPlanNextDispatchMocks(exec, testSession, "@7")
	d.SetWindowCreateFunc(mockCreateWindowFn("@7"))

	require.NoError(t, d.tick(context.Background(), gf))

	require.Len(t, *sent, 1)
	assert.Equal(t, "/tmux:task-plan-generate incremental", (*sent)[0])
	assert.True(t, d.planNext.inFlight)
	assert.Equal(t, 1, d.planNext.baselineGoals, "baseline snapshots the ledger size at dispatch")
	assert.Equal(t, modeActive, d.mode)
}

// TestTick_Incremental_GeneratorInFlightBlocksDispatch proves the in-flight
// guard: while a generator episode is open, the tick neither re-dispatches the
// generator nor dispatches a goal — even when the generator has just authored a
// fresh pending goal (that goal dispatches on the NEXT tick, after the episode
// is closed by drivePlanNext).
func TestTick_Incremental_GeneratorInFlightBlocksDispatch(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.planNext = planNextState{inFlight: true, dispatchedAt: now.Add(-time.Minute), baselineGoals: 0}

	// The generator just authored goal-001 (ledger grew past the baseline).
	gf := &GoalsFile{Goals: []Goal{{ID: "goal-001", Status: GoalPending}}}
	writeGoals(t, dir, gf)

	// drivePlanNext kills the finished generator window (1 lookup + KillWindow).
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@7", Name: planNextWindow, CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@7").Return(nil)
	var created int
	d.SetWindowCreateFunc(countingCreateWindowFn(&created, "@9"))

	require.NoError(t, d.tick(context.Background(), gf))

	assert.False(t, d.planNext.inFlight, "the authored goal closes the episode")
	assert.Zero(t, d.planNext.attempts, "a successful episode resets the failure streak")
	assert.Zero(t, created, "no window (goal or generator) is dispatched in the same tick")
	assert.Equal(t, GoalPending, gf.Goals[0].Status, "the new goal awaits the next tick")
}

// TestDrivePlanNext_MarkerDeactivatesAsComplete proves the product-complete
// marker ends the run through the same terminal path as "all goals done" —
// completion report generated, mode idle — and the marker file is KEPT (it is
// the durable product-done signal; the generator's no-op guard keys on it).
func TestDrivePlanNext_MarkerDeactivatesAsComplete(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	d.planNext = planNextState{inFlight: true, dispatchedAt: time.Now()}
	writeProductCompleteMarker(t, dir)
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, modeIdle, d.mode, "product complete → deactivated")
	assert.False(t, d.planNext.inFlight)
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "taskvisor-product-complete"),
		"the marker is kept as the durable product-done signal")
	assert.FileExists(t, filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md"),
		"the same terminal path as all-goals-done runs")
}

// TestDrivePlanNext_CrashRetriesThenHalts proves the runaway guard on the
// generator itself: a vanished generator window (no goal authored, no marker)
// fails the episode; the daemon retries up to planNextAttemptLimit episodes and
// then deactivates with a loud halt reason instead of spinning.
func TestDrivePlanNext_CrashRetriesThenHalts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeGoals(t, dir, &GoalsFile{})

	// The generator window is gone; only the human's window-0 supervisor is live
	// (keeps deactivate()'s ensureWindow0Supervisor a no-op on the final halt).
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)
	exec.On("SendMessageWithDelay", testSession, "@0", mock.Anything).Return(nil).Maybe()

	for i := 1; i < planNextAttemptLimit; i++ {
		d.planNext = planNextState{inFlight: true, dispatchedAt: time.Now(), attempts: i - 1}
		require.NoError(t, d.drivePlanNext(&GoalsFile{}))
		assert.False(t, d.planNext.inFlight, "crash closes the episode")
		assert.Equal(t, i, d.planNext.attempts)
		assert.Equal(t, modeActive, d.mode, "under the limit the daemon stays active to retry")
	}

	d.planNext = planNextState{inFlight: true, dispatchedAt: time.Now(), attempts: planNextAttemptLimit - 1}
	require.NoError(t, d.drivePlanNext(&GoalsFile{}))
	assert.Equal(t, modeIdle, d.mode, "attempt limit reached → deactivate, do not spin")
	assert.Contains(t, d.haltReason, "incremental generator", "the halt is operator-visible")
}

// TestDrivePlanNext_TimeoutFailsEpisode proves a wedged (still-running but
// never-authoring) generator episode is bounded by planNextTimeout: the window
// is killed and the episode counted as a failed attempt.
func TestDrivePlanNext_TimeoutFailsEpisode(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeGoals(t, dir, &GoalsFile{})
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	d.planNext = planNextState{inFlight: true, dispatchedAt: now.Add(-planNextTimeout - time.Minute)}

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@7", Name: planNextWindow, CurrentCommand: "claude"},
	}, nil)
	exec.On("KillWindow", testSession, "@7").Return(nil)

	require.NoError(t, d.drivePlanNext(&GoalsFile{}))

	assert.False(t, d.planNext.inFlight)
	assert.Equal(t, 1, d.planNext.attempts)
	assert.Equal(t, modeActive, d.mode, "first timeout retries rather than halting")
	exec.AssertCalled(t, "KillWindow", testSession, "@7")
}

// TestPlanNextOrComplete_CapDeactivates proves the incrementalMaxGoals runaway
// cap: a full ledger deactivates with a loud halt reason instead of authoring
// goal-041.
func TestPlanNextOrComplete_CapDeactivates(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	gf := &GoalsFile{}
	for i := 0; i < incrementalMaxGoals; i++ {
		gf.Goals = append(gf.Goals, Goal{ID: NextGoalID(gf.Goals), Status: GoalDone})
	}
	gf.CurrentGoal = gf.Goals[0].ID
	writeGoals(t, dir, gf)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, modeIdle, d.mode)
	assert.Contains(t, d.haltReason, "incremental cap")
}

// TestPlanNextOrComplete_ConsecutiveFailuresDeactivate proves the K-consecutive-
// failure guard: three failed goals in a row deactivate the run (a failed goal
// informs the next generation, but an unbroken failure streak means no forward
// progress and must not loop).
func TestPlanNextOrComplete_ConsecutiveFailuresDeactivate(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)

	gf := &GoalsFile{CurrentGoal: "goal-004", Goals: []Goal{
		{ID: "goal-001", Status: GoalDone},
		{ID: "goal-002", Status: GoalFailed},
		{ID: "goal-003", Status: GoalFailed},
		{ID: "goal-004", Status: GoalFailed},
	}}
	writeGoals(t, dir, gf)
	setupDeactivateOnCompletionMocks(exec, testSession)

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, modeIdle, d.mode)
	assert.Contains(t, d.haltReason, "consecutive")
}

// TestTick_Incremental_NoDiscoveryEvidenceIdles proves Option A (task 412): an
// all-terminal ledger under cap/failure guards, with no product-complete marker
// AND no docs/architecture discovery artifact, idles via deactivateOnCompletion
// instead of dispatching the generator — no plan-next window is created and the
// daemon goes to modeIdle (not a repeating failPlanNextEpisode).
func TestTick_Incremental_NoDiscoveryEvidenceIdles(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeSettings(t, dir, true, true)
	writeGuardFile(t, dir)
	// Deliberately NO writeDiscoveryEvidence and NO product-complete marker.

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)
	setupDeactivateOnCompletionMocks(exec, testSession)

	var created int
	d.SetWindowCreateFunc(countingCreateWindowFn(&created, "@9"))

	require.NoError(t, d.tick(context.Background(), gf))

	assert.Equal(t, modeIdle, d.mode, "terminal ledger + no discovery evidence → idle (Option A)")
	assert.False(t, d.planNext.inFlight, "no generator episode is opened")
	assert.Zero(t, created, "no plan-next window is created")
}

// TestAdvanceToNextGoal_IncrementalStaysActive proves the terminal-goal seam:
// with no pending goal left, roadmap mode deactivates but incremental mode stays
// active so the next tick dispatches the generator (review + next goal).
func TestAdvanceToNextGoal_IncrementalStaysActive(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental
	writeDiscoveryEvidence(t, dir)

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{{ID: "goal-001", Status: GoalDone}}}
	writeGoals(t, dir, gf)

	require.NoError(t, d.advanceToNextGoal(gf, "goal-001", true))

	assert.Equal(t, modeActive, d.mode, "incremental keeps the daemon active for the next generation")
}

// TestTick_Incremental_PendingGoalDispatchesNormally proves the incremental arm
// does not hijack normal goal execution: a pending authored goal dispatches
// through the standard plan path, not the generator.
func TestTick_Incremental_PendingGoalDispatchesNormally(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.planningMode = setup.PlanningModeIncremental

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals:       []Goal{{ID: "goal-001", Description: "test", Status: GoalPending}},
	}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	setupDispatchMocks(exec, testSession, "@0")
	d.SetWindowCreateFunc(mockCreateWindowFn("@0"))

	require.NoError(t, d.tick(context.Background(), gf))
	assert.Equal(t, GoalRunning, gf.Goals[0].Status)
	assert.False(t, d.planNext.inFlight, "no generator episode while a goal runs")
}

// TestPoll_Incremental_ActivatesWithoutGoalsFile proves incremental activation
// on a virgin project: a start signal with NO goals.yaml on disk activates the
// daemon (the generator authors goal-001 afterwards) instead of erroring.
// Roadmap mode keeps the "no goals.yaml found" error byte-identically.
func TestPoll_Incremental_ActivatesWithoutGoalsFile(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.planningMode = setup.PlanningModeIncremental
	writeSettings(t, dir, true, true)
	writeStartSignal(t, dir)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.poll(context.Background()))
	assert.Equal(t, modeActive, d.mode)
}

// TestPoll_Roadmap_NoGoalsFileStillErrors pins the roadmap-mode contract the
// incremental branch must not disturb: activation without goals.yaml fails.
func TestPoll_Roadmap_NoGoalsFileStillErrors(t *testing.T) {
	d, _, dir := setupDaemon(t)
	writeStartSignal(t, dir)

	err := d.poll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no goals.yaml")
}

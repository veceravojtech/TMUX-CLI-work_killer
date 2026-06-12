package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// requireLaneOnDisk asserts BOTH persistence surfaces of a lane write: the
// goals.yaml field (via the canonical loader) and the goal.md `## Lane` section
// body (exactly the bare lane string).
func requireLaneOnDisk(t *testing.T, dir, goalID, want string) {
	t.Helper()
	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g, ok := loaded.GoalByID(goalID)
	require.True(t, ok)
	assert.Equal(t, want, g.Lane, "goals.yaml lane for %s", goalID)
	md, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", goalID, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "## Lane\n\n"+want+"\n", "goal.md ## Lane body for %s", goalID)
}

// soloGoal returns a GoalRunning solo-lane goal with the standard per-class
// budgets used by the demotion-site tests.
func soloGoal(id string, validate []string) Goal {
	return Goal{
		ID: id, Description: "test", Status: GoalRunning, Lane: LaneSolo,
		StartedAt: "2026-06-12T10:00:00Z",
		Validate:  validate,
		Retries:   0, MaxRetries: 5,
		CodeRetries: 5, MaxCodeRetries: 5,
		SpecRetries: 3, MaxSpecRetries: 3,
		ValidationRetries: 2, MaxValidationRetries: 2,
		StuckRetries: 3, MaxStuckRetries: 3,
	}
}

func TestSoloLane_LaneOrFull(t *testing.T) {
	cases := []struct {
		lane string
		want string
	}{
		{"", LaneFull},
		{LaneSolo, LaneSolo},
		{LaneFull, LaneFull},
	}
	for _, tc := range cases {
		g := Goal{Lane: tc.lane}
		assert.Equal(t, tc.want, g.LaneOrFull(), "Lane=%q", tc.lane)
	}
}

func TestSoloLane_GoalsYamlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "with marker", Status: GoalPending, Lane: LaneSolo,
			MaxRetries: 5, MaxCodeRetries: 5, MaxSpecRetries: 3, MaxValidationRetries: 2},
		{ID: "goal-002", Description: "without marker", Status: GoalPending,
			MaxRetries: 5, MaxCodeRetries: 5, MaxSpecRetries: 3, MaxValidationRetries: 2},
	}}
	require.NoError(t, SaveGoals(dir, gf))

	// omitempty zero-change guarantee: assert on RAW BYTES — exactly one lane:
	// key (goal-001's), none for the lane-absent goal-002.
	raw, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(raw), "lane:"), "exactly one lane: key in goals.yaml")
	assert.Contains(t, string(raw), "lane: solo")

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	g1, ok := loaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, LaneSolo, g1.Lane, "lane: solo must round-trip")
	// The re-seed guard keys on the four live counters, never on Lane: the solo
	// goal (all live counters zero, non-terminal) is still re-seeded to budget.
	assert.Equal(t, 5, g1.CodeRetries, "re-seed guard must still fire for the solo goal")
	assert.Equal(t, 3, g1.SpecRetries)
	assert.Equal(t, 2, g1.ValidationRetries)

	g2, ok := loaded.GoalByID("goal-002")
	require.True(t, ok)
	assert.Equal(t, "", g2.Lane, "lane-absent goal stays lane-absent")
}

func TestSoloLane_CreateGoalPersistsAndSurfaces(t *testing.T) {
	dir := t.TempDir()
	id, _, err := CreateGoal(dir, GoalSpec{
		Description: "Solo goal",
		Acceptance:  []string{"AC1"},
		Validate:    []string{"go test ./..."},
		Phase:       "domain",
		Lane:        LaneSolo,
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "lane: solo")

	mdData, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", id, "goal.md"))
	require.NoError(t, err)
	md := string(mdData)
	assert.Contains(t, md, "## Lane\n\nsolo\n", "## Lane body is exactly the bare lane string")
	idxPhase := strings.Index(md, "\n## Phase\n")
	idxLane := strings.Index(md, "\n## Lane\n")
	idxAcc := strings.Index(md, "\n## Acceptance Criteria\n")
	require.True(t, idxPhase >= 0 && idxLane >= 0 && idxAcc >= 0)
	assert.True(t, idxPhase < idxLane && idxLane < idxAcc, "## Lane sits between Phase and Acceptance")

	// Invalid lane: rejected BEFORE any side effect — no ID burned, no goal dir.
	dir2 := t.TempDir()
	_, _, err = CreateGoal(dir2, GoalSpec{Description: "Bad", Validate: []string{"true"}, Lane: "fast"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lane")
	_, statErr := os.Stat(GoalsFilePath(dir2))
	assert.True(t, os.IsNotExist(statErr), "no goals.yaml may be created on invalid lane")
	_, statErr = os.Stat(filepath.Join(dir2, ".tmux-cli", "goals"))
	assert.True(t, os.IsNotExist(statErr), "no goal dir may be created on invalid lane")

	// Lane omitted: no ## Lane section (zero-change contract).
	dir3 := t.TempDir()
	id3, _, err := CreateGoal(dir3, GoalSpec{Description: "Plain", Validate: []string{"true"}})
	require.NoError(t, err)
	md3, err := os.ReadFile(filepath.Join(dir3, ".tmux-cli", "goals", id3, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(md3), "## Lane")
	raw3, err := os.ReadFile(GoalsFilePath(dir3))
	require.NoError(t, err)
	assert.NotContains(t, string(raw3), "lane:")
}

func TestSoloLane_SetGoalMDLane(t *testing.T) {
	// Replace in place: every other byte of the file survives.
	dir := t.TempDir()
	require.NoError(t, WriteGoalMD(dir, "Lane goal", "domain", LaneSolo,
		[]string{"AC1"}, []string{"go test ./..."}, nil, "ctx", "not this", nil))
	before, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)

	require.NoError(t, SetGoalMDLane(dir, LaneFull))
	after, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	want := strings.Replace(string(before), "## Lane\n\nsolo\n", "## Lane\n\nfull\n", 1)
	assert.Equal(t, want, string(after), "only the ## Lane body may change")

	// Insert when missing: lands before ## Acceptance Criteria.
	dir2 := t.TempDir()
	require.NoError(t, WriteGoalMD(dir2, "No lane yet", "", "",
		[]string{"AC1"}, []string{"true"}, nil, "", "", nil))
	require.NoError(t, SetGoalMDLane(dir2, LaneFull))
	md2, err := os.ReadFile(filepath.Join(dir2, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md2), "## Lane\n\nfull\n")
	idxLane := strings.Index(string(md2), "\n## Lane\n")
	idxAcc := strings.Index(string(md2), "\n## Acceptance Criteria\n")
	require.True(t, idxLane >= 0 && idxAcc >= 0)
	assert.Less(t, idxLane, idxAcc, "inserted section must precede ## Acceptance Criteria")
}

func TestSoloLane_DemoteOnFailedCycle(t *testing.T) {
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix it", "code-defect"))

	assert.Equal(t, LaneFull, goal.Lane)
	assert.Equal(t, 4, goal.CodeRetries, "existing CodeRetries decrement must be preserved")
	assert.Equal(t, GoalPending, goal.Status)
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_DemoteOnStuckSupervisor(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{soloGoal("goal-001", []string{"true"})}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	// handleStuckSupervisor: 2 kill lookups; dispatch path: 5 kill lookups +
	// collectManagedNames + waitWindowsGone; then waitClaudeBoot/waitForPrompt.
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(9)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckSupervisor(goal, gf))

	assert.Equal(t, LaneFull, goal.Lane)
	assert.Equal(t, 2, goal.StuckRetries, "existing StuckRetries decrement must be preserved")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_DemoteOnStuckValidator(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{soloGoal("goal-001", []string{"go test ./..."})}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"go test ./..."}, nil, "", "", nil))

	// kill old validator (empty list = no-op), then waitClaudeBoot for new validator
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(1)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	goal := &gf.Goals[0]
	require.NoError(t, d.handleStuckValidator(goal, gf))

	assert.Equal(t, LaneFull, goal.Lane)
	assert.Equal(t, 2, goal.StuckRetries, "existing StuckRetries decrement must be preserved")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_DemoteOnRerunValidationOnly(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating
	d.SetWindowCreateFunc(mockCreateWindowFn("@1"))

	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{soloGoal("goal-001", []string{"go test ./..."})}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"go test ./..."}, nil, "", "", nil))

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(1)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "validator-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	exec.On("SendMessage", testSession, mock.Anything, mock.Anything).Return(nil)
	exec.On("SendMessageWithDelay", testSession, mock.Anything, mock.Anything).Return(nil).Maybe()

	goal := &gf.Goals[0]
	require.NoError(t, d.rerunValidationOnly(goal, gf, nil))

	assert.Equal(t, LaneFull, goal.Lane)
	assert.Equal(t, 1, goal.ValidationRetries, "existing ValidationRetries decrement must be preserved")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_DemoteOnDispatchRetry(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	g := soloGoal("goal-001", []string{"true"})
	g.Status = GoalPending
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{g}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	// Capture the persisted lane at spawn time: demotion must be on disk BEFORE
	// the worker is sent its command.
	var laneAtSpawn string
	exec.On("SendMessage", testSession, "@1", mock.Anything).Run(func(args mock.Arguments) {
		if lg, lerr := LoadGoals(dir); lerr == nil {
			if lgg, ok := lg.GoalByID("goal-001"); ok {
				laneAtSpawn = lgg.Lane
			}
		}
	}).Return(nil)
	d.createWindowFn = mockCreateWindowFn("@1")

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	assert.Equal(t, LaneFull, gf.Goals[0].Lane)
	assert.Equal(t, LaneFull, laneAtSpawn, "demotion must be persisted before the worker spawn")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_DemoteOnCrashRecovery(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	writeGuardFile(t, dir)

	g := soloGoal("goal-001", nil)
	g.Retries = 1
	g.MaxRetries = 3
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{g}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	require.NoError(t, d.crashRecovery(false))

	loaded, err := LoadGoals(dir)
	require.NoError(t, err)
	lg, ok := loaded.GoalByID("goal-001")
	require.True(t, ok)
	assert.Equal(t, GoalPending, lg.Status, "goal must be re-pended")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLane_NoOpForFullAndAbsent(t *testing.T) {
	// A lane-absent goal through the busiest demotion sink stays lane-absent
	// and the saved yaml carries no lane: key at all.
	d, _, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive
	d.runtime("goal-001").phase = phaseValidating

	g := soloGoal("goal-001", nil)
	g.Lane = ""
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{g}}
	writeGoals(t, dir, gf)
	_, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)

	goal := &gf.Goals[0]
	require.NoError(t, d.handleFailedCycle(goal, gf, "fix it", "code-defect"))
	assert.Equal(t, "", goal.Lane, "lane-absent goal must stay lane-absent")
	raw, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "lane:", "no lane: key may be emitted for a lane-absent goal")

	// Direct helper calls on full and absent goals are STRICT no-ops: no save,
	// no goal.md write (no goal.md even exists here — an attempted write would
	// error the call).
	d2, _, dir2 := setupDaemon(t)
	gf2 := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Description: "explicit full", Status: GoalRunning, Lane: LaneFull},
		{ID: "goal-002", Description: "absent", Status: GoalRunning},
	}}
	writeGoals(t, dir2, gf2)
	before, err := os.ReadFile(GoalsFilePath(dir2))
	require.NoError(t, err)
	require.NoError(t, d2.demoteSoloLane(&gf2.Goals[0], gf2, "test"))
	require.NoError(t, d2.demoteSoloLane(&gf2.Goals[1], gf2, "test"))
	after, err := os.ReadFile(GoalsFilePath(dir2))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "no-op demotion must not rewrite goals.yaml")
	assert.Equal(t, LaneFull, gf2.Goals[0].Lane)
	assert.Equal(t, "", gf2.Goals[1].Lane)

	// Idempotence: the second demotion of a demoted goal is a pure no-op.
	d3, _, dir3 := setupDaemon(t)
	gf3 := &GoalsFile{Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir3, gf3)
	goalDir3, err := EnsureGoalDir(dir3, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir3, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))
	require.NoError(t, d3.demoteSoloLane(&gf3.Goals[0], gf3, "first"))
	requireLaneOnDisk(t, dir3, "goal-001", LaneFull)
	yamlAfterFirst, err := os.ReadFile(GoalsFilePath(dir3))
	require.NoError(t, err)
	mdAfterFirst, err := os.ReadFile(filepath.Join(goalDir3, "goal.md"))
	require.NoError(t, err)
	require.NoError(t, d3.demoteSoloLane(&gf3.Goals[0], gf3, "second"))
	yamlAfterSecond, err := os.ReadFile(GoalsFilePath(dir3))
	require.NoError(t, err)
	mdAfterSecond, err := os.ReadFile(filepath.Join(goalDir3, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, string(yamlAfterFirst), string(yamlAfterSecond), "second demotion must not rewrite goals.yaml")
	assert.Equal(t, string(mdAfterFirst), string(mdAfterSecond), "second demotion must not rewrite goal.md")
}

// tasksYamlLane parses the per-goal tasks.yaml and returns its top-level lane
// key ("" when absent) — the value supervisor step 3c's resolution sees first.
func tasksYamlLane(t *testing.T, dir, goalID string) string {
	t.Helper()
	data, err := os.ReadFile(tasks.GoalTasksFilePath(dir, goalID))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, yaml.Unmarshal(data, &m))
	lane, _ := m["lane"].(string)
	return lane
}

func TestSoloLaneDemotionTasksYaml_RewritesSoloToFull(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
lane: solo
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: pending
    context: .tmux-cli/research/ctx1.md
`)
	before, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)

	require.NoError(t, d.demoteSoloLane(&gf.Goals[0], gf, "test"))

	after, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	want := strings.Replace(string(before), "lane: solo", "lane: full", 1)
	assert.Equal(t, want, string(after), "only the top-level lane line may change — every other byte preserved")
	assert.Equal(t, LaneFull, tasksYamlLane(t, dir, "goal-001"))
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLaneDemotionTasksYaml_AbsentFileNoOp(t *testing.T) {
	// Pre-plan demotion: no per-goal tasks.yaml exists yet.
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	require.NoError(t, d.demoteSoloLane(&gf.Goals[0], gf, "test"))

	_, statErr := os.Stat(tasks.GoalTasksFilePath(dir, "goal-001"))
	assert.True(t, os.IsNotExist(statErr), "demotion must not create a tasks.yaml")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLaneDemotionTasksYaml_AbsentKeyNoOp(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: pending
    context: .tmux-cli/research/ctx1.md
`)
	before, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)

	require.NoError(t, d.demoteSoloLane(&gf.Goals[0], gf, "test"))

	after, err := os.ReadFile(tasks.GoalTasksFilePath(dir, "goal-001"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "lane-absent tasks.yaml must stay byte-unchanged")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLaneDemotionTasksYaml_RepeatCallStillGuards(t *testing.T) {
	// Crash-window state: goals.yaml + goal.md already demoted but the
	// tasks.yaml splice never landed. The dispatchRetry funnel's repeat call
	// must repair tasks.yaml WITHOUT re-writing the other two surfaces.
	d, _, dir := setupDaemon(t)
	g := soloGoal("goal-001", nil)
	g.Lane = LaneFull
	gf := &GoalsFile{Goals: []Goal{g}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneFull, []string{"AC"}, []string{"true"}, nil, "", "", nil))
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
lane: solo
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	goalsBefore, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	mdBefore, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)

	require.NoError(t, d.demoteSoloLane(&gf.Goals[0], gf, "retry dispatch"))

	assert.Equal(t, LaneFull, tasksYamlLane(t, dir, "goal-001"), "repeat call must still splice tasks.yaml")
	goalsAfter, err := os.ReadFile(GoalsFilePath(dir))
	require.NoError(t, err)
	mdAfter, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.Equal(t, string(goalsBefore), string(goalsAfter), "repeat call must not rewrite goals.yaml")
	assert.Equal(t, string(mdBefore), string(mdAfter), "repeat call must not rewrite goal.md")
}

func TestSoloLaneDemotionTasksYaml_RetryDispatchResolution(t *testing.T) {
	// E2E for the G5 hole: plan wrote lane: solo into the per-goal tasks.yaml,
	// a failure demoted the goal, and dispatchRetry re-dispatches WITHOUT
	// planning. The supervisor-visible resolution (tasks.yaml top-level key,
	// which wins precedence) must already be full when the worker gets its
	// command — and must have survived resetTaskStatuses' yaml round-trip.
	d, exec, dir := setupDaemon(t)
	d.session = testSession
	d.mode = modeActive

	g := soloGoal("goal-001", []string{"true"})
	g.Status = GoalPending
	gf := &GoalsFile{CurrentGoal: "goal-001", Goals: []Goal{g}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))

	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
lane: solo
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: done
    context: .tmux-cli/research/ctx1.md
`)
	writeTaskContext(t, dir, ".tmux-cli/research/ctx1.md", "# Task 1")

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil).Times(7)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "supervisor-001", CurrentCommand: "claude"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@1").Return("ready ❯ ", nil)
	var laneAtSpawn string
	exec.On("SendMessage", testSession, "@1", mock.Anything).Run(func(args mock.Arguments) {
		laneAtSpawn = tasksYamlLane(t, dir, "goal-001")
	}).Return(nil)
	d.createWindowFn = mockCreateWindowFn("@1")

	require.NoError(t, d.dispatchRetry(&gf.Goals[0], gf))

	assert.Equal(t, LaneFull, laneAtSpawn, "tasks.yaml lane must resolve full BEFORE the retry supervisor is commanded")
	assert.Equal(t, LaneFull, gf.Goals[0].Lane)
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

func TestSoloLaneDemotionTasksYaml_QuotedSolo(t *testing.T) {
	d, _, dir := setupDaemon(t)
	gf := &GoalsFile{Goals: []Goal{soloGoal("goal-001", nil)}}
	writeGoals(t, dir, gf)
	goalDir, err := EnsureGoalDir(dir, "goal-001")
	require.NoError(t, err)
	require.NoError(t, WriteGoalMD(goalDir, "test", "", LaneSolo, []string{"AC"}, []string{"true"}, nil, "", "", nil))
	writeGoalTasksYaml(t, dir, "goal-001", `status: ready
lane: "solo"
cycle: 1
tasks:
  - name: "task one"
    wid: execute-1
    status: pending
    context: .tmux-cli/research/ctx1.md
`)

	require.NoError(t, d.demoteSoloLane(&gf.Goals[0], gf, "test"))

	assert.Equal(t, LaneFull, tasksYamlLane(t, dir, "goal-001"), "quoted solo value must be recognized and demoted")
	requireLaneOnDisk(t, dir, "goal-001", LaneFull)
}

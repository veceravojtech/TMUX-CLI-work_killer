package taskvisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboard_FormatElapsed_NoStart(t *testing.T) {
	result := formatElapsed("", "")
	assert.Equal(t, "—", result)
}

func TestDashboard_FormatElapsed_Running(t *testing.T) {
	started := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	result := formatElapsed(started, "")
	assert.NotEqual(t, "—", result)
	assert.True(t, strings.Contains(result, "m") || strings.Contains(result, "s"),
		"expected time format, got: %s", result)
}

func TestDashboard_FormatElapsed_Completed(t *testing.T) {
	start := time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Format(time.RFC3339)
	finish := time.Date(2026, 5, 20, 15, 5, 30, 0, time.UTC).Format(time.RFC3339)
	result := formatElapsed(start, finish)
	assert.Equal(t, "5m 30s", result)
}

func TestDashboard_FormatElapsed_Hours(t *testing.T) {
	start := time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC).Format(time.RFC3339)
	finish := time.Date(2026, 5, 20, 15, 30, 45, 0, time.UTC).Format(time.RFC3339)
	result := formatElapsed(start, finish)
	assert.Equal(t, "1h 30m 45s", result)
}

func TestDashboard_FormatElapsed_SecondsOnly(t *testing.T) {
	start := time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Format(time.RFC3339)
	finish := time.Date(2026, 5, 20, 15, 0, 42, 0, time.UTC).Format(time.RFC3339)
	result := formatElapsed(start, finish)
	assert.Equal(t, "42s", result)
}

func setupDashboardDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	dir := t.TempDir()
	exec := new(testutil.MockTmuxExecutor)
	d := New(dir, exec)
	return d, dir
}

func TestDashboard_Render_IdleMode(t *testing.T) {
	d, _ := setupDashboardDaemon(t)
	d.mode = modeIdle

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "IDLE")
	assert.Contains(t, out, "waiting")
	assert.Contains(t, out, d.pollInterval.String())
}

func TestDashboard_Render_ActiveSupervising(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Fix pricing", Status: GoalRunning,
				StartedAt: time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
				Retries:   1, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ACTIVE")
	assert.Contains(t, out, "SUPERVISING")
	assert.Contains(t, out, "goal-001")
	assert.Contains(t, out, "Fix pricing")
}

func TestDashboard_Render_ActiveValidating(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseValidating

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Test goal", Status: GoalRunning, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ACTIVE")
	assert.Contains(t, out, "VALIDATING")
}

func TestDashboard_Render_GoalColors(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-002"
	d.runtime("goal-002").phase = phaseSupervising

	now := time.Now().UTC().Format(time.RFC3339)
	gf := &GoalsFile{
		CurrentGoal: "goal-002",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone, StartedAt: now, FinishedAt: now},
			{ID: "goal-002", Status: GoalRunning, StartedAt: now},
			{ID: "goal-003", Status: GoalFailed, StartedAt: now, FinishedAt: now},
			{ID: "goal-004", Status: GoalPending},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "\033[32m", "done goal should use green")
	assert.Contains(t, out, "\033[33m", "running goal should use yellow")
	assert.Contains(t, out, "\033[31m", "failed goal should use red")
	assert.Contains(t, out, "\033[2m", "pending goal should use dim")
}

func TestDashboard_Render_PriorityColumnHidden(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First", Status: GoalRunning, MaxRetries: 3},
			{ID: "goal-002", Description: "Second", Status: GoalPending, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	// No Prio column when every goal is at the default priority of 0.
	assert.NotContains(t, out, "Prio", "Prio column must be hidden when all priorities are 0")
	// Byte-identical default header (the off-branch format string verbatim).
	expectedHeader := fmt.Sprintf("%s%-4s  %-12s  %-30s  %-10s  %-8s  %s%s",
		ansiDim, "#", "ID", "Description", "Status", "Retries", "Elapsed", ansiReset)
	assert.Contains(t, out, expectedHeader, "header must match the pre-Prio format byte-for-byte")
}

func TestDashboard_Render_PriorityColumnShown(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First", Status: GoalRunning, Priority: 5, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Prio", "Prio column must be shown when a goal has non-zero priority")
	assert.Contains(t, out, "5", "the goal's priority value must be rendered")
	// Existing columns survive.
	assert.Contains(t, out, "Status")
	assert.Contains(t, out, "Retries")
	assert.Contains(t, out, "Elapsed")
	assert.Contains(t, out, GoalRunning)
}

func TestDashboard_Render_PriorityMixedZeros(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "First", Status: GoalRunning, Priority: 2, MaxRetries: 3},
			{ID: "goal-002", Description: "Second", Status: GoalPending, Priority: 0, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Prio", "Prio column must be shown when any goal has non-zero priority")
	// The zero-priority row renders "0" in the Prio column, not a blank.
	lines := strings.Split(out, "\n")
	var row2 string
	for _, line := range lines {
		if strings.Contains(line, "goal-002") {
			row2 = line
			break
		}
	}
	require.NotEmpty(t, row2, "expected a rendered row for goal-002")
	// %-4d for priority 0 yields "0" followed by padding before the 2-space sep.
	assert.Contains(t, row2, "0", "zero-priority row must render 0 in the Prio column")
}

func TestDashboard_Render_ElapsedRunning(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	started := time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalRunning, StartedAt: started, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "m", "running goal should show minutes in elapsed")
	assert.NotContains(t, out, "—", "running goal should not show dash for elapsed")
}

func TestDashboard_Render_ElapsedDone(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	start := time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Format(time.RFC3339)
	finish := time.Date(2026, 5, 20, 15, 12, 30, 0, time.UTC).Format(time.RFC3339)
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Status: GoalDone, StartedAt: start, FinishedAt: finish, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "12m 30s")
}

func TestDashboard_Render_DescriptionTruncation(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	longDesc := "Implement full payment gateway integration"
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: longDesc, Status: GoalRunning,
				StartedAt: time.Now().UTC().Format(time.RFC3339), MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Implement full payment gate...")
	assert.NotContains(t, out, longDesc)
}

func TestDashboard_Render_DescriptionNoTruncation(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	shortDesc := "Fix pricing for hotels"
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: shortDesc, Status: GoalRunning,
				StartedAt: time.Now().UTC().Format(time.RFC3339), MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, shortDesc)
}

func TestDashboard_Render_ElapsedPending(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Pending goal", Status: GoalPending, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "—")
}

func TestDashboard_Render_NoGoals(t *testing.T) {
	d, _ := setupDashboardDaemon(t)
	d.mode = modeActive
	d.runtime(d.currentGoal).phase = phaseSupervising

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ACTIVE")
	assert.NotContains(t, out, "goal-")
}

func TestDashboardRendersStackGateSkips(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising
	d.stackGateSkips = 2

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Some task", Status: GoalRunning,
				Retries: 0, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	require.NoError(t, d.renderDashboard(&buf))
	out := buf.String()
	assert.Contains(t, out, "stack-gated: 2")
}

func TestDashboardOmitsStackGateSkipsWhenZero(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.currentGoal = "goal-001"
	d.runtime("goal-001").phase = phaseSupervising
	d.stackGateSkips = 0

	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{ID: "goal-001", Description: "Some task", Status: GoalRunning,
				Retries: 0, MaxRetries: 3},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	var buf bytes.Buffer
	require.NoError(t, d.renderDashboard(&buf))
	out := buf.String()
	assert.NotContains(t, out, "stack-gated")
}

// ── Board renderer (RenderBoard/WatchBoard) tests ──────────────────────────────
//
// All five sections are daemon-independent and read-only. Every case below runs
// with NO live tmux server: the census source is an injected MockTmuxExecutor (or
// nil). Tests use t.TempDir() so the queue cache write (the ONLY permitted write)
// is isolated.

// syncBuffer is a mutex-guarded io.Writer so the WatchBoard goroutine and the test
// reader never race on the underlying bytes.Buffer (-race clean).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// writeBoardLog writes content to .tmux-cli/logs/taskvisor.log under dir.
func writeBoardLog(t *testing.T, dir, content string) {
	t.Helper()
	logDir := filepath.Join(dir, ".tmux-cli", "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "taskvisor.log"), []byte(content), 0o644))
}

func TestTaskvisorDashboardRenderBoard_Populated(t *testing.T) {
	dir := t.TempDir()

	start := time.Now().Add(-7 * time.Minute).UTC().Format(time.RFC3339)
	doneStart := time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC).Format(time.RFC3339)
	doneFinish := time.Date(2026, 5, 20, 15, 4, 0, 0, time.UTC).Format(time.RFC3339)
	gf := &GoalsFile{
		CurrentGoal: "goal-025",
		Goals: []Goal{
			{ID: "goal-025", Description: "Implement the taskvisor dashboard board renderer end to end",
				Status: GoalRunning, Phase: "supervising", Lane: LaneSolo, StartedAt: start, MaxRetries: 5},
			{ID: "goal-026", Description: "Done goal", Status: GoalDone,
				StartedAt: doneStart, FinishedAt: doneFinish, MaxRetries: 5},
			{ID: "goal-027", Description: "Pending goal", Status: GoalPending, MaxRetries: 5},
		},
	}
	require.NoError(t, SaveGoals(dir, gf))

	tgf := &TaskGoalsFile{Mappings: []TaskGoalMapping{
		{TaskID: "task-42", GoalID: "goal-025", Title: "Dashboard renderer", ClaimedAt: start},
	}}
	require.NoError(t, SaveTaskGoals(dir, tgf))

	writeBoardLog(t, dir, strings.Join([]string{
		"2026/06/13 10:00:00 daemon started",
		"2026/06/13 10:00:01 COUNTERS goal=goal-025 cycle=2 phase=validating event=transition retries_code=4 retries_spec=3 retries_val=2 inv_spawned=1 inv_reused=0 inv_inlined=0 cycle_wall_s=10 goal_wall_s=20",
		"2026/06/13 10:00:02 goal-025: phase supervising -> validating",
	}, "\n")+"\n")

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return("sess", nil)
	mockExec.On("ListWindows", "sess").Return([]tmux.WindowInfo{
		{Name: "supervisor"},     // bare window-0 — NOT a goal worker
		{Name: "supervisor-025"}, // goal supervisor
		{Name: "execute-025-1"},
		{Name: "validator-025"},
		{Name: "investigator-025-1"},
	}, nil)

	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, mockExec))
	out := buf.String()

	// Section 1 — goals table.
	assert.Contains(t, out, "goal-025")
	assert.Contains(t, out, "goal-026")
	assert.Contains(t, out, "goal-027")
	assert.Contains(t, out, "Implement the", "description prefix should render")
	assert.Contains(t, out, "...", "long description should be truncated")
	assert.Contains(t, out, "supervisor-025", "bound supervisor window column")
	assert.Contains(t, out, "c/s/v", "retries-remaining header")
	// goal-025 (MaxRetries 5) seeds live remaining budget to MigrateRetries(5) =
	// Code 5 / Spec 3 / Val 2 via LoadGoals.
	assert.Contains(t, out, "5/3/2", "goal-025 c/s/v remaining budget")

	// Section 2 — mappings.
	assert.Contains(t, out, "task-42")

	// Section 4 — census (bare supervisor excluded).
	assert.Contains(t, out, "supervisor 1")
	assert.Contains(t, out, "execute 1")
	assert.Contains(t, out, "validator 1")
	assert.Contains(t, out, "investigator 1")

	// Section 5 — log tail (last COUNTERS + last transition).
	assert.Contains(t, out, "COUNTERS goal=goal-025")
	assert.Contains(t, out, "supervising -> validating")

	// Section 3 — api disabled (no setting.yaml).
	assert.Contains(t, out, "api disabled")

	mockExec.AssertExpectations(t)
}

func TestTaskvisorDashboardRenderBoard_DaemonDownMissingGoals(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	require.NotPanics(t, func() {
		require.NoError(t, RenderBoard(&buf, dir, nil))
	})
	out := buf.String()

	// All five section headers present.
	assert.Contains(t, out, "GOALS")
	assert.Contains(t, out, "MAPPINGS")
	assert.Contains(t, out, "QUEUE")
	assert.Contains(t, out, "WORKER WINDOWS")
	assert.Contains(t, out, "RECENT ACTIVITY")

	// Every missing source degrades to its placeholder.
	assert.Contains(t, out, "(no goals.yaml")
	assert.Contains(t, out, "(no in-flight task")
	assert.Contains(t, out, "api disabled")
	assert.Contains(t, out, "(no tmux session")
	assert.Contains(t, out, "(no taskvisor.log")
}

func TestTaskvisorDashboardRenderBoard_EmptyGoals(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveGoals(dir, &GoalsFile{Goals: nil}))

	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, nil))
	out := buf.String()

	assert.Contains(t, out, "GOALS")
	assert.Contains(t, out, "(no goals)")
}

func TestTaskvisorDashboardRenderBoard_APIDisabled(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tmux-cli", "setting.yaml"),
		[]byte("apiEnabled: false\n"), 0o644))

	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, nil))
	out := buf.String()

	assert.Contains(t, out, "BACKEND QUEUE")
	assert.Contains(t, out, "api disabled")
	// No cache file was written for the disabled path.
	_, err := os.Stat(filepath.Join(dir, ".tmux-cli", "dashboard-queue-cache.json"))
	assert.True(t, os.IsNotExist(err), "disabled api must make no network call and write no cache")
}

func TestTaskvisorDashboardWindowCensus_Classification(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"supervisor", ""}, // bare window-0, not a goal worker
		{"supervisor-025", "supervisor"},
		{"execute-025-1", "execute"},
		{"validator", ""}, // bare fallback, not counted
		{"validator-025", "validator"},
		{"investigator-025-2", "investigator"},
		{"plan-audit-025", "plan-audit"},
		{"bash", ""},
		{"claude", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyWindow(tc.name))
		})
	}
}

func TestTaskvisorDashboardLogTail_LastCountersAndTransition(t *testing.T) {
	dir := t.TempDir()
	writeBoardLog(t, dir, strings.Join([]string{
		"COUNTERS goal=goal-025 cycle=1 phase=supervising event=dispatch retries_code=5 retries_spec=3 retries_val=2 inv_spawned=0 inv_reused=0 inv_inlined=0 cycle_wall_s=0 goal_wall_s=0",
		"goal-025: phase supervising -> validating",
		"COUNTERS goal=goal-025 cycle=2 phase=validating event=transition retries_code=4 retries_spec=3 retries_val=2 inv_spawned=2 inv_reused=0 inv_inlined=0 cycle_wall_s=30 goal_wall_s=60",
		"goal-025: phase validating -> supervising",
	}, "\n")+"\n")

	lastTransition, lastCounters, cycleByGoal := tailLog(dir)
	assert.Equal(t, "goal-025: phase validating -> supervising", lastTransition)
	assert.Contains(t, lastCounters, "cycle=2")

	tokens := parseCounters(lastCounters)
	assert.Equal(t, "2", tokens["cycle"])
	assert.Equal(t, "validating", tokens["phase"])
	assert.Equal(t, "2", tokens["inv_spawned"])

	require.NotNil(t, cycleByGoal)
	assert.Equal(t, "2", cycleByGoal["goal-025"])
}

func TestTaskvisorDashboardLogTail_MissingLog(t *testing.T) {
	dir := t.TempDir()

	lastTransition, lastCounters, cycleByGoal := tailLog(dir)
	assert.Equal(t, "", lastTransition)
	assert.Equal(t, "", lastCounters)
	assert.Nil(t, cycleByGoal)

	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, nil))
	assert.Contains(t, buf.String(), "(no taskvisor.log)")
}

func TestTaskvisorDashboardQueueCache_Fallback(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))

	cached := &queueCounts{
		Total:      7,
		ByStatus:   map[string]int{"new": 4, "claimed": 3},
		BySeverity: map[string]int{"high": 2, "low": 5},
		Sampled:    7,
		SampledAt:  time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(cached)
	require.NoError(t, err)
	cachePath := filepath.Join(dir, ".tmux-cli", "dashboard-queue-cache.json")
	require.NoError(t, os.WriteFile(cachePath, data, 0o644))

	// api disabled (no setting.yaml) ⇒ render from cache.
	var buf bytes.Buffer
	require.NoError(t, RenderBoard(&buf, dir, nil))
	out := buf.String()

	assert.Contains(t, out, "(cached", "cache age annotation")
	assert.Contains(t, out, "new 4", "cached by-status counts rendered")
	assert.Contains(t, out, "claimed 3")

	// The disabled path must NOT rewrite the cache.
	after, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, data, after, "cache file must be unchanged on the read-only fallback path")
}

func TestTaskvisorDashboardWatchBoard_CancelStops(t *testing.T) {
	dir := t.TempDir()
	sb := &syncBuffer{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- WatchBoard(ctx, sb, dir, nil, 10*time.Millisecond)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "WatchBoard returns nil on ctx cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("WatchBoard did not return after ctx cancel")
	}

	out := sb.String()
	assert.Contains(t, out, ansiClearScreen, "WatchBoard paints clear+board at least once")
	assert.Contains(t, out, "GOALS", "a section header proves a full board was painted")
}

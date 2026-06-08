package taskvisor

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/testutil"
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

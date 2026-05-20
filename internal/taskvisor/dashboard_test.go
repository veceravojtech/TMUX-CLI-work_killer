package taskvisor

import (
	"bytes"
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
	d.phase = phaseSupervising

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
	d.phase = phaseValidating

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
	d.phase = phaseSupervising

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

func TestDashboard_Render_ElapsedRunning(t *testing.T) {
	d, dir := setupDashboardDaemon(t)
	d.mode = modeActive
	d.phase = phaseSupervising

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
	d.phase = phaseSupervising

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
	d.phase = phaseSupervising

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
	d.phase = phaseSupervising

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
	d.phase = phaseSupervising

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
	d.phase = phaseSupervising

	var buf bytes.Buffer
	err := d.renderDashboard(&buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ACTIVE")
	assert.NotContains(t, out, "goal-")
}

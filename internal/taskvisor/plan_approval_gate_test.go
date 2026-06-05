package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivate_RequirePlanApproval_NoFile_Halts(t *testing.T) {
	d, exec, dir := setupDaemon(t)
	d.session = testSession

	writeSettings(t, dir, true, true)

	settingPath := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	raw, err := os.ReadFile(settingPath)
	require.NoError(t, err)
	raw = append(raw, []byte("  require_plan_approval: true\n")...)
	require.NoError(t, os.WriteFile(settingPath, raw, 0o644))

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	// Gate fires before discoverSession; deactivate needs ListWindows for
	// teardown + ensureWindow0Supervisor.
	setupDeactivateMocks(exec, testSession, "@9")

	err = d.activate(gf)
	require.NoError(t, err)

	assert.Equal(t, modeIdle, d.mode, "daemon must be idle when plan-approval.md is absent")
	assert.Contains(t, d.haltReason, "plan-approval.md", "haltReason must mention plan-approval.md")
}

func TestActivate_RequirePlanApproval_FilePresent_Proceeds(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)

	settingPath := filepath.Join(dir, ".tmux-cli", "setting.yaml")
	raw, err := os.ReadFile(settingPath)
	require.NoError(t, err)
	raw = append(raw, []byte("  require_plan_approval: true\n")...)
	require.NoError(t, os.WriteFile(settingPath, raw, 0o644))

	approvalDir := filepath.Join(dir, "docs", "architecture")
	require.NoError(t, os.MkdirAll(approvalDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(approvalDir, "plan-approval.md"), []byte("approved"), 0o644))

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err = d.activate(gf)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "daemon must proceed when plan-approval.md exists")
	assert.Empty(t, d.haltReason, "no halt reason expected")
}

func TestActivate_RequirePlanApproval_False_NoCheck(t *testing.T) {
	d, exec, dir := setupDaemon(t)

	writeSettings(t, dir, true, true)

	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Description: "test", Status: GoalPending},
		},
	}
	writeGoals(t, dir, gf)

	exec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", dir).Return(testSession, nil)
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.activate(gf)
	require.NoError(t, err)

	assert.Equal(t, modeActive, d.mode, "daemon must proceed when require_plan_approval is false")
	assert.Empty(t, d.haltReason)
}

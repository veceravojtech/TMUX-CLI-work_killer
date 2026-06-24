package main

import (
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetGoalEditFlags clears the goal-edit flag state — both the backing vars and
// each pflag's exported Changed marker — so the cmd.Flags().Changed() tri-state
// detection in runTaskvisorGoalEdit never leaks across executions in this package.
func resetGoalEditFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "goal", "edit"})
	require.NoError(t, err)
	for _, name := range []string{"acceptance", "validate", "scope", "status", "deliverable-area", "phase"} {
		if f := cmd.Flags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
	goalEditAcceptance, goalEditValidate, goalEditScope = nil, nil, nil
	goalEditStatus, goalEditDeliverableArea, goalEditPhase = "", "", ""
	t.Cleanup(func() {
		// rootCmd.SetArgs is sticky across Execute() calls — reset it so a later
		// test (e.g. root_test.go's no-args help) does not re-run this command.
		rootCmd.SetArgs(nil)
		for _, name := range []string{"acceptance", "validate", "scope", "status", "deliverable-area", "phase"} {
			if f := cmd.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
		goalEditAcceptance, goalEditValidate, goalEditScope = nil, nil, nil
		goalEditStatus, goalEditDeliverableArea, goalEditPhase = "", "", ""
	})
}

func TestGoalEditCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "goal", "edit"})
	require.NoError(t, err)
	require.Equal(t, "edit", cmd.Name())
	for _, name := range []string{"acceptance", "validate", "scope", "status", "deliverable-area", "phase"} {
		assert.NotNil(t, cmd.Flags().Lookup(name), "--%s flag should exist on goal edit", name)
	}
}

// TestGoalEdit_AppliesProvidedFlagsViaCommand drives the REAL command so the
// cmd.Flags().Changed() tri-state wiring is exercised: only the flags passed on
// the command line are applied; an omitted flag leaves its field untouched.
func TestGoalEdit_AppliesProvidedFlagsViaCommand(t *testing.T) {
	withTempCwd(t, func(dir string) {
		// Seed a roadmap skeleton carrying an existing phase that must survive an
		// edit that does not name --phase.
		require.NoError(t, taskvisor.SaveGoals(dir, &taskvisor.GoalsFile{Goals: []taskvisor.Goal{
			{ID: "goal-001", Description: "Skeleton", Status: taskvisor.GoalRoadmap, Phase: "domain"},
		}}))

		resetGoalEditFlags(t)
		rootCmd.SetArgs([]string{
			"taskvisor", "goal", "edit", "goal-001",
			"--acceptance", "Returns 200",
			"--validate", "go test ./internal/api/...",
			"--scope", "internal/api/**",
			"--status", "pending",
			"--deliverable-area", "internal/api/v2/",
		})
		captureStdout(t, func() {
			require.NoError(t, rootCmd.Execute())
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		g, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		assert.Equal(t, []string{"Returns 200"}, g.Acceptance)
		assert.Equal(t, []string{"go test ./internal/api/..."}, g.Validate)
		assert.Equal(t, []string{"internal/api/**"}, g.Scope)
		assert.Equal(t, "internal/api/v2/", g.DeliverableArea)
		assert.Equal(t, taskvisor.GoalPending, g.Status)
		assert.Equal(t, "domain", g.Phase, "phase was not passed — must be untouched")
	})
}

// TestGoalEdit_RejectsDaemonOwnedStatusViaCommand proves the status guard
// surfaces through the CLI surface too (converged with the core/MCP guard).
func TestGoalEdit_RejectsDaemonOwnedStatusViaCommand(t *testing.T) {
	withTempCwd(t, func(dir string) {
		require.NoError(t, taskvisor.SaveGoals(dir, &taskvisor.GoalsFile{Goals: []taskvisor.Goal{
			{ID: "goal-001", Description: "Skeleton", Status: taskvisor.GoalRoadmap},
		}}))

		resetGoalEditFlags(t)
		rootCmd.SetArgs([]string{"taskvisor", "goal", "edit", "goal-001", "--status", "done"})
		err := rootCmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not editable")

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		g, _ := gf.GoalByID("goal-001")
		assert.Equal(t, taskvisor.GoalRoadmap, g.Status, "rejected status must persist nothing")
	})
}

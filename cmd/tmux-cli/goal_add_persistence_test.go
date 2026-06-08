package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetGoalAddFlags snapshots every package-level `goal add` flag variable and
// restores it on cleanup, so these tests never leak state into the existing
// taskvisor_test.go tests (which snapshot only the flags they touch).
func resetGoalAddFlags(t *testing.T) {
	t.Helper()
	oldDesc, oldAcc, oldVal := goalDescription, goalAcceptance, goalValidate
	oldCtx, oldNIS, oldPhase := goalContext, goalNotInScope, goalPhase
	oldRetries, oldScope, oldPrio := goalMaxRetries, goalScope, goalPriority
	t.Cleanup(func() {
		goalDescription, goalAcceptance, goalValidate = oldDesc, oldAcc, oldVal
		goalContext, goalNotInScope, goalPhase = oldCtx, oldNIS, oldPhase
		goalMaxRetries, goalScope, goalPriority = oldRetries, oldScope, oldPrio
	})
	goalDescription, goalAcceptance, goalValidate = "", nil, nil
	goalContext, goalNotInScope, goalPhase = "", "", ""
	goalMaxRetries, goalScope, goalPriority = 5, nil, 0
}

func TestGoalAddCmd_ScopeFlagRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "goal", "add"})
	require.NoError(t, err)

	flag := cmd.Flags().Lookup("scope")
	require.NotNil(t, flag, "--scope flag should exist on goal add")
	assert.Equal(t, "stringArray", flag.Value.Type())
}

// TestGoalAdd_PersistsStructuredFields is the RC-A CLI fix: --acceptance and
// --validate must land in goals.yaml as structured Goal fields (the daemon
// reads them from there), not be dropped to goal.md prose only. Context and
// not-in-scope stay goal.md prose — the Goal struct has no fields for them
// and goals.go is sibling-owned this wave.
func TestGoalAdd_PersistsStructuredFields(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Build API endpoint"
		goalAcceptance = []string{"Returns 200 on success", "Validates input"}
		goalValidate = []string{"go test ./...", "curl http://localhost/api"}
		goalContext = "Legacy code needs cleanup"
		goalNotInScope = "Performance tuning"
		goalMaxRetries = 3

		captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.NotNil(t, gf)
		require.Len(t, gf.Goals, 1)
		g := gf.Goals[0]
		assert.Equal(t, []string{"Returns 200 on success", "Validates input"}, g.Acceptance)
		assert.Equal(t, []string{"go test ./...", "curl http://localhost/api"}, g.Validate)
		assert.Equal(t, taskvisor.GoalPending, g.Status)
		assert.Equal(t, 3, g.MaxRetries)

		md, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "goal-001", "goal.md"))
		require.NoError(t, err)
		assert.Contains(t, string(md), "Legacy code needs cleanup")
		assert.Contains(t, string(md), "Performance tuning")
	})
}

func TestGoalAdd_ScopeFlagPersisted(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Scoped goal"
		goalValidate = []string{"check"}
		goalScope = []string{"internal/x/**", `App\Billing`}

		output := captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		assert.Contains(t, output, `scope: [internal/x/**, App\Billing]`)
		assert.NotContains(t, output, "derived from acceptance")
		assert.NotContains(t, output, "scope: unknown")

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, gf.Goals, 1)
		assert.Equal(t, []string{"internal/x/**", `App\Billing`}, gf.Goals[0].Scope)
	})
}

func TestGoalAdd_ScopeDerivedFromAcceptance(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Derived scope goal"
		goalAcceptance = []string{"Create `internal/x/file.go` with the gate", "Update internal/mcp/server.go"}
		goalValidate = []string{"go test ./..."}

		output := captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		assert.Contains(t, output, "scope: [internal/x/file.go, internal/mcp/server.go] (derived from acceptance)")
		assert.NotContains(t, output, "scope: unknown")

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, gf.Goals, 1)
		assert.Equal(t, []string{"internal/x/file.go", "internal/mcp/server.go"}, gf.Goals[0].Scope)
	})
}

func TestGoalAdd_ScopeUnknownWarns(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "Prose-only goal"
		goalAcceptance = []string{"Make the daemon faster"}
		goalValidate = []string{"benchmarks improve"}

		output := captureStdout(t, func() {
			require.NoError(t, runTaskvisorGoalAdd(nil, nil))
		})

		assert.Contains(t, output, "⚠ scope: unknown — goal will serialize against all concurrent goals")
		assert.NotContains(t, output, "derived from acceptance")

		gf, err := taskvisor.LoadGoals(dir)
		require.NoError(t, err)
		require.Len(t, gf.Goals, 1)
		assert.Nil(t, gf.Goals[0].Scope)
	})
}

// TestGoalAdd_RequiresValidate pins the core-owned rule surfacing through the
// CLI: a goal without at least one --validate rule is rejected before any
// filesystem side effect.
func TestGoalAdd_RequiresValidate(t *testing.T) {
	withTempCwd(t, func(dir string) {
		resetGoalAddFlags(t)
		goalDescription = "No validate"

		err := runTaskvisorGoalAdd(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validation rule")

		_, statErr := os.Stat(filepath.Join(dir, ".tmux-cli", "goals.yaml"))
		assert.True(t, os.IsNotExist(statErr))
	})
}

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
)

func TestGoalCreate_FirstGoal(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Fix prices", []string{"Price matches API"}, []string{"Check price"}, "", "", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "Fix prices", gf.Goals[0].Description)
	assert.Equal(t, "pending", gf.Goals[0].Status)
	assert.Equal(t, 0, gf.Goals[0].Retries)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
	// Inverted per supervisor AMEND (F5/RC-A): acceptance/validate are now
	// persisted as structured Goal fields — the daemon reads them from
	// goals.yaml (EnsureInvestigationConfig, own-suite derivation).
	assert.Equal(t, []string{"Price matches API"}, gf.Goals[0].Acceptance, "acceptance must persist to goals.yaml")
	assert.Equal(t, []string{"Check price"}, gf.Goals[0].Validate, "validate must persist to goals.yaml")

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	_, statErr := os.Stat(goalDir)
	assert.NoError(t, statErr)

	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "# Fix prices")
	assert.Contains(t, mdContent, "- Price matches API")
	assert.Contains(t, mdContent, "- Check price")
}

func TestGoalCreate_SequentialID(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: done
- id: goal-002
  description: Second
  status: running
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Third", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)
}

func TestGoalCreate_ExplicitMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Custom retries", nil, []string{"check"}, "", "", "", 5, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

func TestGoalCreate_DefaultMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Default retries", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 5, gf.Goals[0].MaxRetries)
}

// TestGoalCreateDefaultMaxRetriesIsFive: a fresh MCP goal-create with
// max_retries omitted (0) persists max_retries=5, which LoadGoals migrates
// into per-class budgets Code 5 / Spec 3 / Val 2 / Block 0.

func TestGoalCreateDefaultMaxRetriesIsFive(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Default budgets", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	// Persisted single counter is the new default.
	raw, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, raw.Goals, 1)
	assert.Equal(t, 5, raw.Goals[0].MaxRetries)

	// The migrating loader derives the per-class budgets Code 5 / Spec 3 / Val 2.
	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	g := gf.Goals[0]
	assert.Equal(t, 5, g.MaxCodeRetries)
	assert.Equal(t, 3, g.MaxSpecRetries)
	assert.Equal(t, 2, g.MaxValidationRetries)
	assert.Equal(t, 0, g.MaxBlockRetries)
}

func TestGoalCreate_EmptyDescription(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("", nil, nil, "", "", "", 0, nil, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "description cannot be empty")
}

func TestGoalCreate_DescriptionTooLong(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	longDesc := strings.Repeat("a", 121)
	_, err := server.GoalCreate(longDesc, nil, nil, "", "", "", 0, nil, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "120")
	assert.Contains(t, err.Error(), "--acceptance")
}

func TestGoalCreate_DescriptionExactlyAtLimit(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	exactDesc := strings.Repeat("b", 120)
	output, err := server.GoalCreate(exactDesc, nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, exactDesc, gf.Goals[0].Description)
}

func TestGoalCreate_AppendsToExisting(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: First
  status: pending
  max_retries: 3
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "goal-001", gf.Goals[0].ID)
	assert.Equal(t, "First", gf.Goals[0].Description)
	assert.Equal(t, "goal-002", gf.Goals[1].ID)
	assert.Equal(t, "Second", gf.Goals[1].Description)
}

func TestGoalCreate_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Test atomic", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	tmpFile := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.tmp")
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr), "temp file should not remain after atomic write")
}

func TestGoalCreate_WithContext(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Refactor auth", []string{"Tests pass"}, []string{"check"}, "Legacy code", "Performance", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Context")
	assert.Contains(t, mdContent, "Legacy code")
	assert.Contains(t, mdContent, "## Not In Scope")
	assert.Contains(t, mdContent, "Performance")
}

func TestGoalCreate_WithPhase(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "infrastructure", gf.Goals[0].Phase)
}

func TestGoalCreate_NoAcceptance(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple task", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001")
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Acceptance Criteria")
	assert.Contains(t, mdContent, "## Validation Rules")
	assert.NotContains(t, mdContent, "## Context")
	assert.NotContains(t, mdContent, "## Not In Scope")
}

func TestGoalCreate_EmptyValidate(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Valid desc", nil, nil, "", "", "", 0, nil, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "validation rule")
}

func TestGoalCreate_EmptyValidateSlice(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Valid desc", nil, []string{}, "", "", "", 0, nil, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "validation rule")
}

func TestGoalCreate_InvalidPhase(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Bad phase goal", nil, []string{"check"}, "", "", "nonexistent", 0, nil, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "invalid phase")
}

func TestGoalCreate_WithDependsOn(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("First goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, []string{"goal-001"}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-002", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, []string{"goal-001"}, gf.Goals[1].DependsOn)
}

func TestGoalCreate_DependsOnNonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Orphan goal", nil, []string{"check"}, "", "", "", 0, []string{"goal-999"}, nil, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	assert.Contains(t, err.Error(), "non-existent goal")
}

func TestGoalCreate_WithPhaseAndDependsOn(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Prereq goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Domain goal", nil, []string{"check"}, "", "", "domain", 0, []string{"goal-001"}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-002", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "domain", gf.Goals[1].Phase)
	assert.Equal(t, []string{"goal-001"}, gf.Goals[1].DependsOn)
}

func TestGoalCreate_DependsOnEmptySlice(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("No deps goal", nil, []string{"check"}, "", "", "", 0, []string{}, nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)
}

func TestGoalCreate_DependsOnMultiple(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Goal A", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = server.GoalCreate("Goal B", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	output, err := server.GoalCreate("Goal C", nil, []string{"check"}, "", "", "", 0, []string{"goal-001", "goal-002"}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-003", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 3)
	assert.Equal(t, []string{"goal-001", "goal-002"}, gf.Goals[2].DependsOn)
}

// --- GoalValidationDone tests ---

func TestGoalCreate_LockFileCreated(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Lock test", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	lockPath := filepath.Join(tmpDir, ".tmux-cli", "goals.yaml.lock")
	_, statErr := os.Stat(lockPath)
	assert.NoError(t, statErr, "lock file should exist after GoalCreate")
}

func TestGoalCreate_Concurrent_AllSucceed(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make([]error, goroutines)
	ids := make([]string, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			output, err := server.GoalCreate(
				fmt.Sprintf("Goal %d", idx),
				[]string{fmt.Sprintf("criterion-%d", idx)},
				[]string{"check"}, "", "", "",
				0, nil, nil, nil, nil,
			)
			errs[idx] = err
			if output != nil {
				ids[idx] = output.ID
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < goroutines; i++ {
		assert.NoError(t, errs[i], "goroutine %d should succeed", i)
	}

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err, "goals.yaml must be valid YAML (no corruption)")
	require.NotNil(t, gf)
	assert.Equal(t, goroutines, len(gf.Goals), "all %d goals should be persisted", goroutines)

	idSet := make(map[string]bool, goroutines)
	for _, id := range ids {
		if id != "" {
			idSet[id] = true
		}
	}
	assert.Equal(t, goroutines, len(idSet), "all goal IDs should be unique")
}

func TestGoalCreate_LockCoversLoadSaveSpan(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error
	go func() {
		defer wg.Done()
		_, err1 = server.GoalCreate("First concurrent", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	}()
	go func() {
		defer wg.Done()
		_, err2 = server.GoalCreate("Second concurrent", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	}()

	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 2, len(gf.Goals), "both goals must appear — no lost writes")
}

func TestGoalCreate_PreservesGlobalMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `global_max_retries: 7
goals:
- id: goal-001
  description: First
  status: done
  max_retries: 3
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 7, gf.GlobalMaxRetries, "global_max_retries must survive GoalCreate round-trip")
	require.Len(t, gf.Goals, 2)
}

func TestGoalCreate_PreservesGoalTimingFields(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Running goal
  status: running
  max_retries: 3
  started_at: "2026-05-29T10:00:00Z"
`)

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	_, err := server.GoalCreate("Second goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 2)
	assert.Equal(t, "2026-05-29T10:00:00Z", gf.Goals[0].StartedAt,
		"started_at must survive GoalCreate round-trip")
}

func TestGoalCreate_WritesPhaseToGoalMD(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Domain goal", nil, []string{"check"}, "", "", "domain", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)

	assert.Contains(t, mdContent, "## Phase")
	assert.Contains(t, mdContent, "domain")
}

func TestGoalCreate_NoPhaseOmitsSection(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)

	assert.NotContains(t, string(mdData), "## Phase")
}

// TestGoalValidationDone_WritesResultsJSON: when per-finding re-validation
// inputs are supplied, the orchestrator-owned results.json ledger is written
// alongside signal.json with a stable input fingerprint per finding, keyed by
// finding id (the rule). Out-of-scope changes do not alter the fingerprint.

func TestGoalCreate_WithPreconditions_PersistsToGoalsYaml(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN=postgres://..."},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	require.Len(t, gf.Goals[0].Preconditions, 2)
	assert.Equal(t, taskvisor.Precondition{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN=postgres://..."}, gf.Goals[0].Preconditions[0])
	assert.Equal(t, taskvisor.Precondition{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"}, gf.Goals[0].Preconditions[1])
}

func TestGoalCreate_PersistsScope(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	scope := []string{"internal/x/**", `App\Billing`}
	output, err := server.GoalCreate("Scoped goal", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, scope)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, scope, gf.Goals[0].Scope)
}

func TestGoalCreate_WithPreconditions_PersistsToGoalMD(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Preconditions")
	assert.Contains(t, mdContent, "- [env] DB_DSN — export DB_DSN")
}

func TestGoalCreate_PreconditionsRoundTripToEvaluate(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "env", Spec: "DB_DSN", Remedy: "export DB_DSN"},
		{Kind: "service", Spec: "localhost:5432", Remedy: "start postgres"},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil, nil)
	require.NoError(t, err)

	gf, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	g, ok := gf.GoalByID(output.ID)
	require.True(t, ok)
	require.Len(t, g.Preconditions, 2)
	assert.Equal(t, preconds[0], g.Preconditions[0])
	assert.Equal(t, preconds[1], g.Preconditions[1])
}

func TestGoalCreate_NoPreconditions_OmitsYamlKeyAndSection(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	output, err := server.GoalCreate("Simple task", nil, []string{"check"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	rawYaml, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(rawYaml), "preconditions:")

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(mdData), "## Preconditions")
}

func TestGoalCreate_PreconditionEmptyRemedy_RendersSpecOnly(t *testing.T) {
	tmpDir := t.TempDir()

	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)
	preconds := []taskvisor.Precondition{
		{Kind: "service", Spec: "localhost:5432", Remedy: ""},
	}
	output, err := server.GoalCreate("Setup DB", nil, []string{"check"}, "", "", "infrastructure", 0, nil, preconds, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "- [service] localhost:5432")
	assert.NotContains(t, mdContent, "localhost:5432 —")
}

// --- GoalCreate Investigation Config (M2) tests ---

// validInvestigatorSet returns n fully-valid investigators for the happy-path
// and boundary tests. Names are distinctive ("Custom Investigator N") so a test
// can prove the explicit config — not deriveInvestigators — reached goal.md.

func TestGoalCreate_AcceptsValidInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.GoalCreate("Valid investigators", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(2), nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Investigation Config")
	// Distinctive names prove the explicit config (not the derived fallback)
	// reached WriteGoalMD.
	assert.Contains(t, mdContent, "Custom Investigator 1")
	assert.Contains(t, mdContent, "Custom Investigator 2")
}

func TestGoalCreate_AcceptsFourInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	output, err := server.GoalCreate("Four investigators", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(4), nil)
	require.NoError(t, err)
	assert.Equal(t, "goal-001", output.ID)
}

func TestGoalCreate_RejectsTooFewInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Too few", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(1), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "2–4")
	// Rejection happens before the goals-file lock: no goal dir is created.
	_, statErr := os.Stat(filepath.Join(tmpDir, ".tmux-cli", "goals", "goal-001"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestGoalCreate_RejectsTooManyInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	_, err := server.GoalCreate("Too many", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(5), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "2–4")
}

func TestGoalCreate_RejectsEmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[1].Name = ""
	_, err := server.GoalCreate("Empty name", nil, []string{"check"}, "", "", "", 0, nil, nil, invs, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "name")
	require.ErrorContains(t, err, "investigator[2]") // 1-based index
}

func TestGoalCreate_RejectsBadType(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Type = "bogus"
	_, err := server.GoalCreate("Bad type", nil, []string{"check"}, "", "", "", 0, nil, nil, invs, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "invalid type")
}

// TestGoalCreate_AcceptsPlannerEmittedTypes proves the M4 superset fix: each
// investigator type the planner (task-plan-generate.xml) emits but that M2's
// enum originally lacked must now be accepted. Each newly-added type is paired
// with a known-good type ("static-analysis") to satisfy the 2–4 floor.

func TestGoalCreate_AcceptsPlannerEmittedTypes(t *testing.T) {
	newTypes := []string{
		"command",
		"environment-check",
		"file-check",
		"implementation-check",
		"integration-check",
	}

	for _, plannerType := range newTypes {
		t.Run(plannerType, func(t *testing.T) {
			tmpDir := t.TempDir()
			server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

			invs := []taskvisor.Investigator{
				{
					Name:     "Planner Investigator",
					Type:     plannerType,
					Commands: []string{"make check"},
					Pass:     "exit 0",
				},
				{
					Name:     "Known Good Investigator",
					Type:     "static-analysis",
					Commands: []string{"make analyse"},
					Pass:     "exit 0",
				},
			}

			output, err := server.GoalCreate("Planner type "+plannerType, nil, []string{"check"}, "", "", "", 0, nil, nil, invs, nil)
			require.NoError(t, err)

			goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
			mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
			require.NoError(t, err)
			mdContent := string(mdData)
			assert.Contains(t, mdContent, "## Investigation Config")
			assert.Contains(t, mdContent, "Planner Investigator")
		})
	}
}

func TestGoalCreate_RejectsMissingCommand(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Commands = nil
	_, err := server.GoalCreate("Missing command", nil, []string{"check"}, "", "", "", 0, nil, nil, invs, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "command")
}

func TestGoalCreate_RejectsEmptyPass(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	invs := validInvestigatorSet(2)
	invs[0].Pass = ""
	_, err := server.GoalCreate("Empty pass", nil, []string{"check"}, "", "", "", 0, nil, nil, invs, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
	require.ErrorContains(t, err, "pass")
}

func TestGoalCreate_FallbackWhenNoInvestigators(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// nil investigators → M1's deriveInvestigators fallback must still render a
	// valid 2–4 section.
	output, err := server.GoalCreate("Fallback", nil, []string{"PHPStan level 9 passes", "Unit tests pass"}, "", "", "", 0, nil, nil, nil, nil)
	require.NoError(t, err)

	goalDir := filepath.Join(tmpDir, ".tmux-cli", "goals", output.ID)
	mdData, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	mdContent := string(mdData)
	assert.Contains(t, mdContent, "## Investigation Config")
	assert.GreaterOrEqual(t, strings.Count(mdContent, "### Investigator "), 2)
	// The distinctive explicit-config names never appear via the fallback path,
	// confirming nil (not an explicit set) was threaded through.
	assert.NotContains(t, mdContent, "Custom Investigator")
}

func TestGoalCreate_UnchangedGuardsStillFire(t *testing.T) {
	tmpDir := t.TempDir()
	server := newTestServer(new(testutil.MockTmuxExecutor), tmpDir)

	// Empty description still rejects even with a valid investigator set — the
	// new trailing param did not reorder the existing guards.
	_, err := server.GoalCreate("", nil, []string{"check"}, "", "", "", 0, nil, nil, validInvestigatorSet(2), nil)
	assert.ErrorIs(t, err, ErrInvalidInput)

	// Empty validate still rejects.
	_, err = server.GoalCreate("Valid desc", nil, nil, "", "", "", 0, nil, nil, validInvestigatorSet(2), nil)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// --- GoalAddPrerequisite tests ---

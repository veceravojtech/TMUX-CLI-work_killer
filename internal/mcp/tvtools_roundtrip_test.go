package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
)

func TestTvGoalsFile_GlobalMaxRetries_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `global_max_retries: 10
goals:
- id: goal-001
  description: Test
  status: pending
  max_retries: 3
`)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	assert.Equal(t, 10, gf.GlobalMaxRetries)

	require.NoError(t, tvSaveGoals(tmpDir, gf))

	reloaded, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, 10, reloaded.GlobalMaxRetries)
}

func TestTvGoal_TimingFields_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Test
  status: running
  max_retries: 3
  started_at: "2026-05-29T10:00:00Z"
  finished_at: "2026-05-29T11:30:00Z"
`)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 1)
	assert.Equal(t, "2026-05-29T10:00:00Z", gf.Goals[0].StartedAt)
	assert.Equal(t, "2026-05-29T11:30:00Z", gf.Goals[0].FinishedAt)

	require.NoError(t, tvSaveGoals(tmpDir, gf))

	reloaded, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Len(t, reloaded.Goals, 1)
	assert.Equal(t, "2026-05-29T10:00:00Z", reloaded.Goals[0].StartedAt)
	assert.Equal(t, "2026-05-29T11:30:00Z", reloaded.Goals[0].FinishedAt)
}

func TestTvGoal_MigratesRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, `goals:
- id: goal-001
  description: Add bookings table migration
  status: pending
  max_retries: 3
  migrates: true
- id: goal-002
  description: Non-migrating goal
  status: pending
  max_retries: 3
`)

	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.Len(t, gf.Goals, 2)
	assert.True(t, gf.Goals[0].Migrates, "migrates: true must load into tvGoal")
	assert.False(t, gf.Goals[1].Migrates)

	require.NoError(t, tvSaveGoals(tmpDir, gf))

	// Re-read via the CANONICAL taskvisor loader — proves the MCP resave did not
	// erase the flag the daemon's DisjointReadySet exclusion depends on.
	canonical, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.Len(t, canonical.Goals, 2)
	assert.True(t, canonical.Goals[0].Migrates, "migrates: true must survive the MCP load-resave round-trip")
	assert.False(t, canonical.Goals[1].Migrates)
}

// TestGoalAddPrerequisite_PreservesMigratesFlag: regression for the erase bug —
// wiring a prerequisite onto goal A load-resaves the whole goals file via
// tvGoal, which must not strip migrates: true from ANY goal in the file.

func TestTvGoal_AllDurableFieldsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoalsYaml(t, tmpDir, fullyLoadedGoalYaml)

	// Baseline: what the daemon sees BEFORE any MCP touch.
	baseline, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, baseline)
	require.Len(t, baseline.Goals, 3)

	// MCP load-resave round-trip (the erase path under test).
	gf, err := tvLoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, gf)
	require.NoError(t, tvSaveGoals(tmpDir, gf))

	// Re-read via the CANONICAL loader: every durable field must be intact.
	after, err := taskvisor.LoadGoals(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.Equal(t, baseline, after,
		"MCP tvLoadGoals→tvSaveGoals round-trip must preserve every durable taskvisor.Goal field")

	// Spot-check the 13 audit-6 fields explicitly so a failure names the field.
	g := after.Goals[0]
	assert.Equal(t, 3, g.CodeRetries, "code_retries erased")
	assert.Equal(t, 2, g.SpecRetries, "spec_retries erased")
	assert.Equal(t, 1, g.ValidationRetries, "validation_retries erased")
	assert.Equal(t, 1, g.BlockRetries, "block_retries erased")
	assert.Equal(t, 5, g.MaxCodeRetries, "max_code_retries erased")
	assert.Equal(t, 3, g.MaxSpecRetries, "max_spec_retries erased")
	assert.Equal(t, 2, g.MaxValidationRetries, "max_validation_retries erased")
	assert.Equal(t, 1, g.MaxBlockRetries, "max_block_retries erased")
	assert.Equal(t, []string{"code-sig-a", "code-sig-b"}, g.ConvergenceSignatures, "convergence_signatures erased")
	assert.Equal(t, 2, g.ConvergenceStreak, "convergence_streak erased")
	assert.Equal(t, []string{"spec-sig-a"}, g.SpecConvergenceSignatures, "spec_convergence_signatures erased")
	assert.Equal(t, 1, g.SpecConvergenceStreak, "spec_convergence_streak erased")
	assert.True(t, g.BlockedByPrecondition, "blocked_by_precondition erased")
	assert.Equal(t, "validation-timeout", g.FailedBy, "failed_by erased")
}

// TestGoalAddPrerequisite_PreservesAllDurableFields: wiring a prerequisite
// load-resaves the WHOLE goals file via tvGoal; no durable state on any goal
// may be erased by that rewrite (regression for the audit-6 erase hazard).

func TestGoalTvGoalYamlTagParity(t *testing.T) {
	// Deliberately MCP-absent fields would be allowlisted here, keyed by yaml
	// key, with a justification string each. Currently EMPTY by design: the MCP
	// tools load-resave the daemon-owned .tmux-cli/goals.yaml IN FULL, so every
	// persisted Goal field is durable state the resave must carry.
	allowlist := map[string]string{}

	goalType := reflect.TypeOf(taskvisor.Goal{})
	tvType := reflect.TypeOf(tvGoal{})

	tvByKey := make(map[string]reflect.StructField, tvType.NumField())
	for i := 0; i < tvType.NumField(); i++ {
		f := tvType.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		tvByKey[strings.Split(tag, ",")[0]] = f
	}

	for i := 0; i < goalType.NumField(); i++ {
		gf := goalType.Field(i)
		tag := gf.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if reason, ok := allowlist[key]; ok {
			t.Logf("allowlisted (deliberately MCP-absent): %s — %s", key, reason)
			continue
		}
		tvField, ok := tvByKey[key]
		if !ok {
			t.Errorf("taskvisor.Goal.%s (yaml %q) has NO mirror on mcp.tvGoal — every MCP load-resave will silently erase it from goals.yaml", gf.Name, key)
			continue
		}
		assert.Equal(t, tag, tvField.Tag.Get("yaml"),
			"yaml tag mismatch for %s: Goal=%q tvGoal=%q (key and omitempty must match)", gf.Name, tag, tvField.Tag.Get("yaml"))
		assert.Equal(t, gf.Type, tvField.Type,
			"type mismatch for yaml key %q: Goal.%s is %s, tvGoal.%s is %s", key, gf.Name, gf.Type, tvField.Name, tvField.Type)
	}
}

// TestGoalsFileTvGoalsFileYamlTagParity: container-level sibling of
// TestGoalTvGoalYamlTagParity. The same dual-struct silent-erase hazard exists
// one level up: taskvisor.GoalsFile (canonical owner of .tmux-cli/goals.yaml)
// vs mcp.tvGoalsFile (the wrapper tvLoadGoals/tvSaveGoals (de)serialize
// through on every MCP resave). Every yaml-persisted field on GoalsFile must
// exist on tvGoalsFile with the SAME yaml tag (key + omitempty); otherwise a
// future GoalsFile field would be silently dropped from goals.yaml by the
// next MCP load-resave.
//
// Exception: for the `goals` field the Go types differ BY DESIGN
// ([]taskvisor.Goal vs []tvGoal), so this test asserts yaml-KEY parity only
// and defers element-level field parity to TestGoalTvGoalYamlTagParity.

func TestGoalsFileTvGoalsFileYamlTagParity(t *testing.T) {
	// Deliberately MCP-absent fields would be allowlisted here, keyed by yaml
	// key, with a justification string each. Currently EMPTY by design: the
	// MCP tools load-resave goals.yaml IN FULL, so every persisted GoalsFile
	// field is durable state the resave must carry.
	allowlist := map[string]string{}

	goalsFileType := reflect.TypeOf(taskvisor.GoalsFile{})
	tvType := reflect.TypeOf(tvGoalsFile{})

	tvByKey := make(map[string]reflect.StructField, tvType.NumField())
	for i := 0; i < tvType.NumField(); i++ {
		f := tvType.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		tvByKey[strings.Split(tag, ",")[0]] = f
	}

	for i := 0; i < goalsFileType.NumField(); i++ {
		gf := goalsFileType.Field(i)
		tag := gf.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if reason, ok := allowlist[key]; ok {
			t.Logf("allowlisted (deliberately MCP-absent): %s — %s", key, reason)
			continue
		}
		tvField, ok := tvByKey[key]
		if !ok {
			t.Errorf("taskvisor.GoalsFile.%s (yaml %q) has NO mirror on mcp.tvGoalsFile — every MCP load-resave will silently erase it from goals.yaml", gf.Name, key)
			continue
		}
		assert.Equal(t, tag, tvField.Tag.Get("yaml"),
			"yaml tag mismatch for %s: GoalsFile=%q tvGoalsFile=%q (key and omitempty must match)", gf.Name, tag, tvField.Tag.Get("yaml"))
		// Skip Go-type equality for the goals slice: []Goal vs []tvGoal differ
		// by design; element parity is covered by TestGoalTvGoalYamlTagParity.
		if key == "goals" {
			continue
		}
		assert.Equal(t, gf.Type, tvField.Type,
			"type mismatch for yaml key %q: GoalsFile.%s is %s, tvGoalsFile.%s is %s", key, gf.Name, gf.Type, tvField.Name, tvField.Type)
	}
}

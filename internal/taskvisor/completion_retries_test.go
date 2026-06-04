package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retriesLine arithmetic: consumed = Max… − live (live counters hold the
// REMAINING budget). All four classes with non-zero budgets render.
func TestRetriesLine_PerClassConsumed(t *testing.T) {
	g := &Goal{
		MaxCodeRetries: 2, CodeRetries: 1,
		MaxSpecRetries: 2, SpecRetries: 2,
		MaxValidationRetries: 1, ValidationRetries: 0,
		MaxBlockRetries: 3, BlockRetries: 3,
	}
	assert.Equal(t, "code 1/2 · spec 0/2 · validation 1/1 · block 0/3", retriesLine(g))
}

// A class whose Max… budget is 0 was never granted budget and is omitted.
// MaxBlockRetries is 0 for every migrated goal (MigrateRetries: "blocked
// never gets budget"), so the typical line has only code/spec/validation.
func TestRetriesLine_ZeroBudgetClassOmitted(t *testing.T) {
	g := &Goal{
		MaxCodeRetries: 5, CodeRetries: 3,
		MaxSpecRetries: 3, SpecRetries: 3,
		MaxValidationRetries: 2, ValidationRetries: 1,
	}
	got := retriesLine(g)
	assert.Equal(t, "code 2/5 · spec 0/3 · validation 1/2", got)
	assert.NotContains(t, got, "block")
}

// live > Max can occur on a hand-edited goals.yaml: consumed clamps to 0,
// never negative.
func TestRetriesLine_NegativeConsumedClampsToZero(t *testing.T) {
	g := &Goal{
		MaxCodeRetries: 2, CodeRetries: 5,
		MaxSpecRetries: 2, SpecRetries: 1,
	}
	assert.Equal(t, "code 0/2 · spec 1/2", retriesLine(g))
}

// True pre-migration goal (all four Max… zero AND legacy MaxRetries > 0 —
// only possible for an in-memory GoalsFile that bypassed LoadGoals, which
// always seeds the Max… budgets) falls back to the legacy line.
func TestRetriesLine_PreMigrationLegacyFallback(t *testing.T) {
	g := &Goal{Retries: 1, MaxRetries: 3}
	assert.Equal(t, "1/3", retriesLine(g))
}

// No budgets anywhere (all Max… zero, legacy MaxRetries zero): "none".
func TestRetriesLine_NoBudgetsRendersNone(t *testing.T) {
	assert.Equal(t, "none", retriesLine(&Goal{}))
}

// End-to-end: generateCompletionReport emits the per-class line for a
// migrated goal and no legacy 0/0 noise.
func TestGenerateCompletionReport_PerClassRetriesLine(t *testing.T) {
	d, _, dir := setupDaemon(t)

	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID: "goal-001", Description: "migrated goal", Status: GoalDone,
				MaxCodeRetries: 2, CodeRetries: 1,
				MaxSpecRetries: 2, SpecRetries: 2,
				MaxValidationRetries: 1, ValidationRetries: 0,
				MaxBlockRetries: 3, BlockRetries: 3,
			},
		},
	}

	require.NoError(t, d.generateCompletionReport(gf))

	data, err := os.ReadFile(filepath.Join(dir, ".tmux-cli", "goals", "completion-report.md"))
	require.NoError(t, err)
	report := string(data)
	assert.Contains(t, report, "- **Retries:** code 1/2 · spec 0/2 · validation 1/1 · block 0/3")
	assert.NotContains(t, report, "- **Retries:** 0/0")
}

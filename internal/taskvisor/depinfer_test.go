package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestInferMissingDeps_ConsumerWithoutEdge(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:    "goal-002",
				Scope: []string{"src/SomeOther.php"},
			},
			{
				ID:    "goal-003",
				Scope: []string{"src/Entity/User.php"},
			},
			{
				ID:         "goal-006",
				Acceptance: []string{"src/Entity/User.php must have correct User entity"},
				DependsOn:  []string{"goal-002"},
			},
		},
	}

	findings := InferMissingDeps(gf)

	require.Len(t, findings, 1)
	assert.Equal(t, "goal-006", findings[0].Consumer)
	assert.Equal(t, "goal-003", findings[0].Producer)
	assert.Equal(t, "src/Entity/User.php", findings[0].Stem)
	assert.Equal(t, "acceptance", findings[0].Evidence)
}

func TestInferMissingDeps_TransitiveEdgePresent(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:    "goal-A",
				Scope: []string{"internal/foo/service.go"},
			},
			{
				ID:        "goal-C",
				DependsOn: []string{"goal-A"},
			},
			{
				ID:         "goal-B",
				Acceptance: []string{"internal/foo/service.go must be implemented"},
				DependsOn:  []string{"goal-C"},
			},
		},
	}

	findings := InferMissingDeps(gf)
	assert.Empty(t, findings)
}

func TestInferMissingDeps_BareWordIgnored_DotSlashUnifies(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:    "goal-001",
				Scope: []string{"./internal/foo/bar.go"},
			},
			{
				ID:       "goal-002",
				Validate: []string{"internal/foo/bar.go must compile", "Entity should exist"},
			},
		},
	}

	findings := InferMissingDeps(gf)

	require.Len(t, findings, 1)
	assert.Equal(t, "goal-002", findings[0].Consumer)
	assert.Equal(t, "goal-001", findings[0].Producer)
	assert.Equal(t, "internal/foo/bar.go", findings[0].Stem)
	assert.Equal(t, "validate", findings[0].Evidence)
}

func TestInferMissingDeps_ReadOnly_DAGUntouched(t *testing.T) {
	gf := &GoalsFile{
		CurrentGoal: "goal-001",
		Goals: []Goal{
			{
				ID:         "goal-001",
				Scope:      []string{"src/Entity/User.php"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
			{
				ID:         "goal-002",
				Acceptance: []string{"src/Entity/User.php exists"},
				DependsOn:  []string{"goal-001"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
		},
	}

	before, err := yaml.Marshal(gf)
	require.NoError(t, err)

	_ = InferMissingDeps(gf)

	after, err := yaml.Marshal(gf)
	require.NoError(t, err)

	assert.Equal(t, string(before), string(after), "GoalsFile must be byte-identical after InferMissingDeps")
}

func TestInferMissingDeps_EmptyGoals(t *testing.T) {
	findings := InferMissingDeps(nil)
	require.NotNil(t, findings)
	assert.Empty(t, findings)

	findings = InferMissingDeps(&GoalsFile{Goals: nil})
	require.NotNil(t, findings)
	assert.Empty(t, findings)

	findings = InferMissingDeps(&GoalsFile{Goals: []Goal{}})
	require.NotNil(t, findings)
	assert.Empty(t, findings)
}

func TestOverlappingGoalsSerializeNotConcurrent(t *testing.T) {
	// Two pending goals both editing src/Entity/ProjectBinding.php, no depends_on.
	newOverlapping := func() *GoalsFile {
		return &GoalsFile{
			Goals: []Goal{
				{
					ID:         "goal-001",
					Scope:      []string{"src/Entity/ProjectBinding.php"},
					Acceptance: []string{"src/Entity/ProjectBinding.php has the binding entity"},
					Status:     GoalPending,
					MaxRetries: 5,
				},
				{
					ID:         "goal-002",
					Scope:      []string{"src/Entity/ProjectBinding.php"},
					Acceptance: []string{"src/Entity/ProjectBinding.php gains a relation"},
					Status:     GoalPending,
					MaxRetries: 5,
				},
			},
		}
	}

	t.Run("serialize", func(t *testing.T) {
		gf := newOverlapping()
		edges := EnforceFileOverlapDeps(gf)

		require.Len(t, edges, 1)
		assert.Equal(t, "goal-002", edges[0].From)
		assert.Equal(t, "goal-001", edges[0].To)
		assert.NotEmpty(t, edges[0].Stem)

		g1, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		assert.Empty(t, g1.DependsOn, "lower-id producer gains no edge")

		g2, ok := gf.GoalByID("goal-002")
		require.True(t, ok)
		assert.Equal(t, []string{"goal-001"}, g2.DependsOn, "higher-id dependent depends_on lower-id")
	})

	t.Run("DispatchExcludesDependent", func(t *testing.T) {
		gf := newOverlapping()
		EnforceFileOverlapDeps(gf)

		ready := gf.DisjointReadySet(2)
		require.Len(t, ready, 1, "dependent is dep-gated, never co-dispatched")
		assert.Equal(t, "goal-001", ready[0].ID)

		// Producer completes → dependent becomes runnable (serialized after, not deadlocked).
		g1, ok := gf.GoalByID("goal-001")
		require.True(t, ok)
		g1.Status = GoalDone

		ready = gf.DisjointReadySet(2)
		require.Len(t, ready, 1)
		assert.Equal(t, "goal-002", ready[0].ID)
	})

	t.Run("idempotent", func(t *testing.T) {
		gf := newOverlapping()
		first := EnforceFileOverlapDeps(gf)
		require.Len(t, first, 1)

		second := EnforceFileOverlapDeps(gf)
		assert.Empty(t, second, "second call adds zero edges")

		g2, ok := gf.GoalByID("goal-002")
		require.True(t, ok)
		assert.Equal(t, []string{"goal-001"}, g2.DependsOn, "DependsOn unchanged on re-run")
	})

	t.Run("no-overlap-untouched", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-001", Scope: []string{"src/Entity/Alpha.php"}, Status: GoalPending},
				{ID: "goal-002", Scope: []string{"src/Entity/Beta.php"}, Status: GoalPending},
			},
		}
		edges := EnforceFileOverlapDeps(gf)
		assert.Empty(t, edges)

		g1, _ := gf.GoalByID("goal-001")
		g2, _ := gf.GoalByID("goal-002")
		assert.Empty(t, g1.DependsOn)
		assert.Empty(t, g2.DependsOn)
	})

	t.Run("already-ordered-no-cycle", func(t *testing.T) {
		// goal-001 already depends_on goal-002, overlapping stems: adding goal-002→goal-001
		// would create a 2-cycle. The reverse-path guard must skip it.
		gf := &GoalsFile{
			Goals: []Goal{
				{
					ID:        "goal-001",
					Scope:     []string{"src/Entity/ProjectBinding.php"},
					DependsOn: []string{"goal-002"},
					Status:    GoalPending,
				},
				{
					ID:     "goal-002",
					Scope:  []string{"src/Entity/ProjectBinding.php"},
					Status: GoalPending,
				},
			},
		}
		edges := EnforceFileOverlapDeps(gf)
		assert.Empty(t, edges, "reverse edge skipped — no cycle")

		g2, _ := gf.GoalByID("goal-002")
		assert.Empty(t, g2.DependsOn, "no edge added to producer")
	})

	t.Run("nil-and-empty-goals", func(t *testing.T) {
		edges := EnforceFileOverlapDeps(nil)
		require.NotNil(t, edges)
		assert.Empty(t, edges)

		edges = EnforceFileOverlapDeps(&GoalsFile{})
		require.NotNil(t, edges)
		assert.Empty(t, edges)
	})
}

func TestEnforceFileOverlap_ExcludesToolBinaries(t *testing.T) {
	// Two pending goals with DISJOINT scopes whose only shared validate tokens are
	// read-only tool binaries (vendor/bin/phpunit + vendor/bin/phpstan). Before the
	// fix these shared stems forced a false depends_on edge, serializing the plan.
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Scope:      []string{"contexts/identity/**"},
				Validate:   []string{"vendor/bin/phpunit", "vendor/bin/phpstan"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
			{
				ID:         "goal-002",
				Scope:      []string{"contexts/dashboard/**"},
				Validate:   []string{"vendor/bin/phpunit", "vendor/bin/phpstan"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
		},
	}

	edges := EnforceFileOverlapDeps(gf)
	assert.Empty(t, edges, "tool-binary-only overlap must not produce an enforced edge")

	g1, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Empty(t, g1.DependsOn, "goal-001 gains no depends_on from a shared tool binary")

	g2, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Empty(t, g2.DependsOn, "goal-002 gains no depends_on from a shared tool binary")

	// Disjoint-scope goals co-schedule under MaxGoals>1 instead of serializing 1x.
	ready := gf.DisjointReadySet(2)
	assert.Len(t, ready, 2, "disjoint-scope goals co-schedule once the false edge is gone")
}

func TestInferMissingDeps_SelfReference(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Scope:      []string{"internal/foo/handler.go"},
				Acceptance: []string{"internal/foo/handler.go handles requests correctly"},
			},
		},
	}

	findings := InferMissingDeps(gf)
	assert.Empty(t, findings)
}

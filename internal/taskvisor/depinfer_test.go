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

func TestEnforceFileOverlapDeps_CoarseAreaNoEdge(t *testing.T) {
	// Two pending same-BC siblings whose ONLY shared stem is a coarse directory
	// area (path-like, no source extension). A shared coarse-dir stem is NOT a
	// produce/consume signal, so it must not force a serializing depends_on edge.
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Scope:      []string{"contexts/Foo/app/src/Http/Controller/"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
			{
				ID:         "goal-002",
				Scope:      []string{"contexts/Foo/app/src/Http/Controller/"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
		},
	}

	edges := EnforceFileOverlapDeps(gf)
	assert.Empty(t, edges, "shared coarse-directory stem alone must not force an edge")

	g1, ok := gf.GoalByID("goal-001")
	require.True(t, ok)
	assert.Empty(t, g1.DependsOn, "producer gains no edge from a coarse-dir overlap")
	g2, ok := gf.GoalByID("goal-002")
	require.True(t, ok)
	assert.Empty(t, g2.DependsOn, "sibling stays runnable in parallel — no false serialization")
}

func TestEnforceFileOverlapDeps_ConcreteFileStillEdges(t *testing.T) {
	// A shared concrete source-file stem IS the directional produce/consume proxy
	// (one goal edits a file the other references) — the legitimate edge stays.
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Scope: []string{"src/Entity/User.php"}, Status: GoalPending, MaxRetries: 5},
			{ID: "goal-002", Scope: []string{"src/Entity/User.php"}, Status: GoalPending, MaxRetries: 5},
		},
	}

	edges := EnforceFileOverlapDeps(gf)
	require.Len(t, edges, 1)
	assert.Equal(t, "goal-002", edges[0].From, "higher-id goal depends on lower-id")
	assert.Equal(t, "goal-001", edges[0].To)
	assert.Equal(t, "src/Entity/User.php", edges[0].Stem)
	assert.True(t, hasKnownExtension(edges[0].Stem), "trigger stem is the concrete .php file")
}

func TestEnforceFileOverlapDeps_MixedPrefersConcreteStem(t *testing.T) {
	// The pair shares BOTH a coarse dir (listed first in slice order) AND a
	// concrete file. The coarse dir must be skipped and the concrete file becomes
	// the trigger stem — exactly one edge, its Stem a known-extension file.
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:         "goal-001",
				Scope:      []string{"contexts/Foo/app/src/Entity/", "contexts/Foo/app/src/Entity/User.php"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
			{
				ID:         "goal-002",
				Scope:      []string{"contexts/Foo/app/src/Entity/", "contexts/Foo/app/src/Entity/User.php"},
				Status:     GoalPending,
				MaxRetries: 5,
			},
		},
	}

	edges := EnforceFileOverlapDeps(gf)
	require.Len(t, edges, 1, "exactly one edge despite two shared stems")
	assert.Equal(t, "contexts/Foo/app/src/Entity/User.php", edges[0].Stem)
	assert.True(t, hasKnownExtension(edges[0].Stem), "the concrete file wins as the trigger stem, not the coarse dir")
}

func TestEnforceFileOverlapDeps_CoarseAreaIdempotentAndAcyclic(t *testing.T) {
	newCoarse := func() *GoalsFile {
		return &GoalsFile{
			Goals: []Goal{
				{ID: "goal-001", Scope: []string{"contexts/Foo/app/src/Http/Controller/"}, Status: GoalPending, MaxRetries: 5},
				{ID: "goal-002", Scope: []string{"contexts/Foo/app/src/Http/Controller/"}, Status: GoalPending, MaxRetries: 5},
			},
		}
	}

	gf := newCoarse()
	before, err := yaml.Marshal(gf)
	require.NoError(t, err)

	first := EnforceFileOverlapDeps(gf)
	assert.Empty(t, first, "coarse-only pair yields no edge")
	second := EnforceFileOverlapDeps(gf)
	assert.Empty(t, second, "second call adds zero edges (idempotent)")

	after, err := yaml.Marshal(gf)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "coarse-only pair leaves the DAG byte-identical and acyclic")
}

func TestInferMissingDeps_CoarseAreaNoFinding(t *testing.T) {
	// The consumer's only overlap with the producer is a coarse directory stem.
	// The read-only inference must stay consistent with the enforcer: no finding.
	gf := &GoalsFile{
		Goals: []Goal{
			{
				ID:    "goal-001",
				Scope: []string{"contexts/Foo/app/src/Http/Controller/"},
			},
			{
				ID:         "goal-002",
				Acceptance: []string{"contexts/Foo/app/src/Http/Controller/ must contain the controller"},
			},
		},
	}

	findings := InferMissingDeps(gf)
	assert.Empty(t, findings, "a coarse-directory overlap yields no inferred dependency finding")
}

func TestInferMissingDeps_ConcreteProducerConsumerStillFound(t *testing.T) {
	gf := &GoalsFile{
		Goals: []Goal{
			{ID: "goal-001", Scope: []string{"src/Entity/User.php"}},
			{ID: "goal-002", Acceptance: []string{"src/Entity/User.php must expose the user fields"}},
		},
	}

	findings := InferMissingDeps(gf)
	require.Len(t, findings, 1, "a concrete-file produce/consume overlap is still reported")
	assert.Equal(t, "goal-002", findings[0].Consumer)
	assert.Equal(t, "goal-001", findings[0].Producer)
	assert.Equal(t, "src/Entity/User.php", findings[0].Stem)
	assert.Equal(t, "acceptance", findings[0].Evidence)
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

func TestDetectOverSerialized(t *testing.T) {
	t.Run("near-linear chain triggers", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-002", Status: GoalPending},
				{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
				{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-002", "goal-003"}},
				{ID: "goal-005", Status: GoalPending, DependsOn: []string{"goal-002", "goal-003", "goal-004"}},
			},
		}

		sf := DetectOverSerialized(gf)

		require.NotNil(t, sf)
		assert.Equal(t, 4, sf.PendingCount)
		assert.GreaterOrEqual(t, sf.CriticalPath, 3)
		assert.Equal(t, 1, sf.RunnableCount)
		assert.NotEmpty(t, sf.Reason)
	})

	t.Run("genuine parallel branches no trigger", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-002", Status: GoalPending},
				{ID: "goal-003", Status: GoalPending},
				{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-002"}},
				{ID: "goal-005", Status: GoalPending, DependsOn: []string{"goal-002"}},
			},
		}

		sf := DetectOverSerialized(gf)

		assert.Nil(t, sf)
	})

	t.Run("done-set leaves one runnable triggers", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-001", Status: GoalDone},
				{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-001"}},
				{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-001", "goal-002"}},
				{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-001", "goal-002", "goal-003"}},
			},
		}

		sf := DetectOverSerialized(gf)

		require.NotNil(t, sf)
		assert.Equal(t, 3, sf.PendingCount)
		assert.Equal(t, 1, sf.RunnableCount)
		assert.NotEmpty(t, sf.Reason)
	})

	t.Run("below floor no trigger", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-002", Status: GoalPending},
				{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
			},
		}

		sf := DetectOverSerialized(gf)

		assert.Nil(t, sf)
	})

	t.Run("nil and empty", func(t *testing.T) {
		assert.Nil(t, DetectOverSerialized(nil))
		assert.Nil(t, DetectOverSerialized(&GoalsFile{}))
	})

	t.Run("cycle terminates", func(t *testing.T) {
		// Mutual depends_on among pending goals: the longest-path DFS must
		// terminate via its visiting guard, never recurse forever.
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-002", Status: GoalPending, DependsOn: []string{"goal-003"}},
				{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
				{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-002"}},
			},
		}

		// Must return (not hang); a cyclic single-runnable graph is over-serialized.
		sf := DetectOverSerialized(gf)
		assert.NotNil(t, sf)
	})

	t.Run("read-only no mutation", func(t *testing.T) {
		gf := &GoalsFile{
			Goals: []Goal{
				{ID: "goal-002", Status: GoalPending},
				{ID: "goal-003", Status: GoalPending, DependsOn: []string{"goal-002"}},
				{ID: "goal-004", Status: GoalPending, DependsOn: []string{"goal-002", "goal-003"}},
				{ID: "goal-005", Status: GoalPending, DependsOn: []string{"goal-002", "goal-003", "goal-004"}},
			},
		}

		before := make([]int, len(gf.Goals))
		for i := range gf.Goals {
			before[i] = len(gf.Goals[i].DependsOn)
		}

		_ = DetectOverSerialized(gf)

		for i := range gf.Goals {
			assert.Equal(t, before[i], len(gf.Goals[i].DependsOn),
				"DependsOn of %s must be unchanged", gf.Goals[i].ID)
		}
	})
}

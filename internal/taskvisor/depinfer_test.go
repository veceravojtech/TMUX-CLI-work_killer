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

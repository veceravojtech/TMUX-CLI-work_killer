package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalMDDrift_CorruptedRepaired(t *testing.T) {
	dir := t.TempDir()

	// Write a goal.md with a corrupted phpunit token in Validation Rules.
	corruptedMD := `# Fix identity domain

## Acceptance Criteria

- Identity domain works correctly

## Validation Rules

- vendor/bin/phpunit\Domain
- bin/console lint:container

## Context

Legacy code needs careful handling
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "goal.md"), []byte(corruptedMD), 0o644))

	goal := &Goal{
		ID:          "goal-001",
		Description: "Fix identity domain",
		Acceptance:  []string{"Identity domain works correctly"},
		Validate:    []string{`vendor/bin/phpunit --filter=IdentityAccess\\Domain`, "bin/console lint:container"},
	}

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.NotEmpty(t, drifted, "should detect drift between corrupted goal.md and goals.yaml")

	// Now repair by splicing the correct validation rules.
	require.NoError(t, repairValidationRules(dir, goal))

	// Re-read and verify the repaired content.
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `- vendor/bin/phpunit --filter=IdentityAccess\\Domain`)
	assert.Contains(t, content, "- bin/console lint:container")
	assert.NotContains(t, content, `phpunit\Domain`)
	// Context section should be preserved.
	assert.Contains(t, content, "## Context")
	assert.Contains(t, content, "Legacy code needs careful handling")

	// Verify no drift after repair.
	drifted2, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.Empty(t, drifted2, "should detect no drift after repair")
}

func TestGoalMDDrift_Identical(t *testing.T) {
	dir := t.TempDir()

	validate := []string{"go test ./...", "curl -f http://localhost:8080/health"}
	require.NoError(t, WriteGoalMD(dir, "Build API", "", []string{"Returns 200"}, validate, nil, "", "", nil))

	goal := &Goal{
		ID:          "goal-002",
		Description: "Build API",
		Acceptance:  []string{"Returns 200"},
		Validate:    validate,
	}

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.Empty(t, drifted, "identical goal.md and goals.yaml should produce no drift")
}

func TestGoalMDDrift_UnwritableGoalDir(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, WriteGoalMD(dir, "Test goal", "", []string{"AC1"}, []string{"original cmd"}, nil, "", "", nil))

	goal := &Goal{
		ID:       "goal-003",
		Validate: []string{"different cmd"},
	}

	// Detect drift first (should work even on read-only dir).
	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.NotEmpty(t, drifted)

	// Make directory read-only so atomicWrite can't create temp file.
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	// Repair should fail.
	err = repairValidationRules(dir, goal)
	assert.Error(t, err, "repair should fail on unwritable goal dir")
}

func TestGoalMDDrift_RetriesUnchanged(t *testing.T) {
	dir := t.TempDir()

	// Write goal.md with wrong validate.
	corruptedMD := `# Test retries

## Acceptance Criteria

- Works

## Validation Rules

- wrong command
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "goal.md"), []byte(corruptedMD), 0o644))

	goal := &Goal{
		ID:                "goal-004",
		Description:       "Test retries",
		Acceptance:        []string{"Works"},
		Validate:          []string{"correct command"},
		CodeRetries:       3,
		SpecRetries:       2,
		ValidationRetries: 1,
		BlockRetries:      4,
	}

	// Snapshot counters before.
	codeBefore := goal.CodeRetries
	specBefore := goal.SpecRetries
	valBefore := goal.ValidationRetries
	blockBefore := goal.BlockRetries

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.NotEmpty(t, drifted)

	require.NoError(t, repairValidationRules(dir, goal))

	// Assert all four counters unchanged.
	assert.Equal(t, codeBefore, goal.CodeRetries, "CodeRetries must be unchanged after repair")
	assert.Equal(t, specBefore, goal.SpecRetries, "SpecRetries must be unchanged after repair")
	assert.Equal(t, valBefore, goal.ValidationRetries, "ValidationRetries must be unchanged after repair")
	assert.Equal(t, blockBefore, goal.BlockRetries, "BlockRetries must be unchanged after repair")
}

func TestGoalMDDrift_EmptyValidate(t *testing.T) {
	dir := t.TempDir()

	// WriteGoalMD with empty validate produces "(none)".
	require.NoError(t, WriteGoalMD(dir, "Empty goal", "", []string{"AC1"}, nil, nil, "", "", nil))

	goal := &Goal{
		ID:         "goal-005",
		Validate:   nil,
		Acceptance: []string{"AC1"},
	}

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.Empty(t, drifted, "empty validate + (none) in goal.md = no drift")
}

func TestGoalMDDrift_ExtraCommandsInGoalMD(t *testing.T) {
	dir := t.TempDir()

	// goal.md has extra commands not in goals.yaml.
	md := `# Extra commands

## Acceptance Criteria

- AC1

## Validation Rules

- cmd-a
- cmd-b
- cmd-extra
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "goal.md"), []byte(md), 0o644))

	goal := &Goal{
		ID:       "goal-006",
		Validate: []string{"cmd-a", "cmd-b"},
	}

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err)
	assert.NotEmpty(t, drifted, "extra commands in goal.md should register as drift")
}

func TestGoalMDDrift_NoGoalMDFile(t *testing.T) {
	dir := t.TempDir()

	goal := &Goal{
		ID:       "goal-007",
		Validate: []string{"cmd"},
	}

	drifted, err := goalMDDrift(dir, goal)
	require.NoError(t, err, "missing goal.md is a no-op, not an error")
	assert.Empty(t, drifted)
}

func TestExtractValidationRules(t *testing.T) {
	tests := []struct {
		name     string
		md       string
		expected []string
	}{
		{
			name: "standard section",
			md: `## Validation Rules

- go test ./...
- bin/console lint:container

## Investigation Config
`,
			expected: []string{"go test ./...", "bin/console lint:container"},
		},
		{
			name: "none placeholder",
			md: `## Validation Rules

(none)

## Investigation Config
`,
			expected: nil,
		},
		{
			name: "missing section",
			md: `## Acceptance Criteria

- AC1
`,
			expected: nil,
		},
		{
			name: "trailing whitespace on commands",
			md: `## Validation Rules

- go test ./...
- curl check

## Context
`,
			expected: []string{"go test ./...", "curl check"},
		},
		{
			name: "section at EOF without next heading",
			md: `## Validation Rules

- final check
`,
			expected: []string{"final check"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractValidationRules(tt.md)
			assert.Equal(t, tt.expected, got)
		})
	}
}

package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dockerGoalDir creates <root>/.tmux-cli/goals/goal-001 (the canonical shape
// ownSuiteFSRoot climbs) with a docker-mode test-environment.md at root.
func dockerGoalDir(t *testing.T, runTarget string) (root, goalDir string) {
	t.Helper()
	root = t.TempDir()
	writeTestEnvMD(t, root, "**Run Target:** "+runTarget+"\n**Playwright Status:** not applicable\n")
	goalDir = filepath.Join(root, ".tmux-cli", "goals", "goal-001")
	require.NoError(t, os.MkdirAll(goalDir, 0o755))
	return root, goalDir
}

func TestWriteGoalMD_DockerWrapsInvestigatorCommands(t *testing.T) {
	_, goalDir := dockerGoalDir(t, "docker")
	invs := []Investigator{
		{Name: "Quality", Type: "quality-gate", Commands: []string{"vendor/bin/phpstan analyse"}, Pass: "p", Fail: "f"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"vendor/bin/phpunit"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(goalDir, "G", "", "", []string{"AC"}, []string{"v"}, nil, "", "", invs))
	out, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "docker compose exec -T app sh -c 'vendor/bin/phpstan analyse'")
	assert.Contains(t, string(out), "docker compose exec -T app sh -c 'vendor/bin/phpunit'")
}

func TestWriteGoalMD_LocalLeavesCommandsBare(t *testing.T) {
	_, goalDir := dockerGoalDir(t, "local")
	invs := []Investigator{
		{Name: "Quality", Type: "quality-gate", Commands: []string{"vendor/bin/phpstan analyse"}, Pass: "p", Fail: "f"},
		{Name: "Tests", Type: "test-execution", Commands: []string{"vendor/bin/phpunit"}, Pass: "p", Fail: "f"},
	}
	require.NoError(t, WriteGoalMD(goalDir, "G", "", "", []string{"AC"}, []string{"v"}, nil, "", "", invs))
	out, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "docker compose exec")
	assert.Contains(t, string(out), "- command: vendor/bin/phpstan analyse")
}

func TestEnsureInvestigationConfig_DockerWrapsInvestigatorCommands(t *testing.T) {
	root, goalDir := dockerGoalDir(t, "docker")
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"),
		[]byte("# G\n\n## Validation Rules\n\n- vendor/bin/phpstan analyse --level=9\n"), 0o644))

	repaired, err := EnsureInvestigationConfig(root, goalDir, []string{"vendor/bin/phpstan analyse --level=9", "vendor/bin/phpunit"})
	require.NoError(t, err)
	assert.True(t, repaired)

	out, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "docker compose exec -T app sh -c",
		"docker-mode investigator commands must be wrapped to run in the app container")
}

func TestEnsureInvestigationConfig_LocalLeavesCommandsBare(t *testing.T) {
	root, goalDir := dockerGoalDir(t, "local")
	require.NoError(t, os.WriteFile(filepath.Join(goalDir, "goal.md"),
		[]byte("# G\n\n## Validation Rules\n\n- vendor/bin/phpstan analyse\n"), 0o644))

	_, err := EnsureInvestigationConfig(root, goalDir, []string{"vendor/bin/phpstan analyse", "vendor/bin/phpunit"})
	require.NoError(t, err)
	out, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "docker compose exec",
		"local mode must leave commands bare (no-op regression)")
}

package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTasksFile_ThreeParallelTasks(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: implement BC-Pricing
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
  - name: implement BC-Display
    wid: execute-2
    status: pending
    context: .tmux-cli/research/2026-05-28-14/display.md
  - name: implement BC-Logging
    wid: execute-3
    status: pending
    context: .tmux-cli/research/2026-05-28-14/logging.md
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "three parallel tasks should validate without errors")
}

func TestValidateTasksFile_ThreeTasksWithDependency(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.yaml")

	yamlContent := `status: ready
tasks:
  - name: scaffold shared types
    wid: execute-1
    status: pending
    context: .tmux-cli/research/2026-05-28-14/scaffold.md
  - name: implement BC-Pricing
    wid: execute-2
    status: pending
    context: .tmux-cli/research/2026-05-28-14/pricing.md
    depends_on:
      - execute-1
  - name: implement BC-Display
    wid: execute-3
    status: pending
    context: .tmux-cli/research/2026-05-28-14/display.md
    depends_on:
      - execute-1
`
	require.NoError(t, os.WriteFile(tasksPath, []byte(yamlContent), 0o644))

	errs := tasks.ValidateTasksFile(tasksPath)
	assert.Empty(t, errs, "three tasks with dependency fan-out should validate without errors")
}

func TestGoalTemplates_ContainTestRequirements(t *testing.T) {
	qualityGatesPath := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "quality-gates.md")
	testStrategyPath := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "test-strategy.md")

	qgData, err := os.ReadFile(qualityGatesPath)
	require.NoError(t, err, "quality-gates.md must exist")
	qgContent := strings.ToLower(string(qgData))

	tsData, err := os.ReadFile(testStrategyPath)
	require.NoError(t, err, "test-strategy.md must exist")
	tsContent := strings.ToLower(string(tsData))

	testKeywords := []string{"test", "unit test", "integration test", "e2e"}

	hasTestRef := false
	for _, kw := range testKeywords {
		if strings.Contains(qgContent, kw) || strings.Contains(tsContent, kw) {
			hasTestRef = true
			break
		}
	}
	assert.True(t, hasTestRef, "templates must contain test requirement patterns")

	assert.Contains(t, tsContent, "unit test", "test-strategy.md must define unit test layer")
	assert.Contains(t, tsContent, "integration test", "test-strategy.md must define integration test layer")
	assert.Contains(t, tsContent, "e2e test", "test-strategy.md must define e2e test layer")
}

func TestWaitForPrompt_PanicPropagates(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("CaptureWindowOutput", testSession, "@0").Panic("simulated nil pointer dereference")

	assert.Panics(t, func() {
		_ = d.waitForPrompt("supervisor", 5*time.Second)
	}, "waitForPrompt must not swallow panics from CaptureWindowOutput")
}

// --- C4: validate-timeout clamp (derived from worker budget) ---

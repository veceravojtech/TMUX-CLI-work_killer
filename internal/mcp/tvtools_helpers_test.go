package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/testutil"
	"github.com/console/tmux-cli/internal/tmux"
)

func writeTestGoalsYaml(t *testing.T, dir string, content string) {
	t.Helper()
	goalsDir := filepath.Join(dir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(goalsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goalsDir, "goals.yaml"), []byte(content), 0o644))
}

// --- TaskvisorStart tests ---

func newSpecDefectTestServer(t *testing.T, tmpDir, goalID string) *Server {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".tmux-cli", "goals", goalID), 0o755))

	mockExec := new(testutil.MockTmuxExecutor)
	mockExec.On("FindSessionByEnvironment", "TMUX_CLI_PROJECT_PATH", tmpDir).Return("test-session", nil)
	mockExec.On("ListWindows", "test-session").Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
		{TmuxWindowID: "@1", Name: "validator"},
	}, nil)
	mockExec.On("GetWindowOption", "test-session", "@1", "window-uuid").Return("uuid-val-1", nil)
	t.Setenv("TMUX_WINDOW_UUID", "uuid-val-1")

	return newTestServer(mockExec, tmpDir)
}

// readSignalFindings reads back signal.json and returns its verdict plus the
// findings array decoded as generic maps (so we assert against the persisted
// JSON keys: rule, status, failure_class, owner, ...).

func readSignalFindings(t *testing.T, tmpDir, goalID string) (verdict string, findings []map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmpDir, ".tmux-cli", "goals", goalID, "signal.json"))
	require.NoError(t, err)
	var sig map[string]any
	require.NoError(t, json.Unmarshal(data, &sig))
	verdict, _ = sig["verdict"].(string)
	raw, _ := sig["findings"].([]any)
	for _, f := range raw {
		if m, ok := f.(map[string]any); ok {
			findings = append(findings, m)
		}
	}
	return verdict, findings
}

// TestM06_SpecDefectDetection: a Config that references composer.json under a
// greenfield binding produces a blocked / spec-defect / planner finding. The
// load-bearing assertion is owner==planner (and NOT implementer/ops) — it proves
// C1's owner-priority resolved the contradiction to the planner so the C2 daemon
// switch bounces it to goal-generation and decrements spec_retries, never charging
// the implementer with an unwinnable code retry.

func validInvestigatorSet(n int) []taskvisor.Investigator {
	types := []string{"static-analysis", "quality-gate", "test-execution", "architecture-check"}
	invs := make([]taskvisor.Investigator, n)
	for i := 0; i < n; i++ {
		invs[i] = taskvisor.Investigator{
			Name:     fmt.Sprintf("Custom Investigator %d", i+1),
			Type:     types[i%len(types)],
			Commands: []string{fmt.Sprintf("make check-%d", i+1)},
			Pass:     "exit 0",
		}
	}
	return invs
}

const fullyLoadedGoalYaml = `current_goal: goal-001
goals:
- id: goal-001
  description: Fully loaded durable goal
  acceptance:
  - builds green
  validate:
  - go test ./...
  preconditions:
  - kind: command
    spec: which go
    remedy: install go
  status: in_progress
  retries: 1
  max_retries: 5
  code_retries: 3
  spec_retries: 2
  validation_retries: 1
  block_retries: 1
  max_code_retries: 5
  max_spec_retries: 3
  max_validation_retries: 2
  max_block_retries: 1
  convergence_signatures:
  - code-sig-a
  - code-sig-b
  convergence_streak: 2
  spec_convergence_signatures:
  - spec-sig-a
  spec_convergence_streak: 1
  phase: implement
  depends_on:
  - goal-002
  escalation_count: 1
  scope:
  - internal/mcp/**
  migrates: true
  failed_by: validation-timeout
  blocked_by: goal-002
  blocked_by_precondition: true
  started_at: "2026-06-03T10:00:00Z"
- id: goal-002
  description: Prerequisite P
  status: pending
  retries: 0
  max_retries: 3
  code_retries: 3
  spec_retries: 2
  validation_retries: 2
  max_code_retries: 3
  max_spec_retries: 2
  max_validation_retries: 2
- id: goal-003
  description: Independent goal
  status: pending
  retries: 0
  max_retries: 3
  code_retries: 3
  spec_retries: 2
  validation_retries: 2
  max_code_retries: 3
  max_spec_retries: 2
  max_validation_retries: 2
`

// TestTvGoal_AllDurableFieldsRoundTrip: a goal carrying every durable field
// must survive the MCP tvLoadGoals → tvSaveGoals round-trip and re-read
// IDENTICALLY via the canonical taskvisor.LoadGoals. Without full tvGoal field
// parity, any MCP load-resave (goal-create, goal-add-prerequisite) silently
// erases per-class retry counters/budgets (corrupting the LoadGoals zero +
// re-seed guard), resets the C6/spec convergence breakers, and drops the
// blocked_by_precondition auto-resume key.

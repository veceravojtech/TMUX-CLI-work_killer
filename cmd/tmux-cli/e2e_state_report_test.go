package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/console/tmux-cli/internal/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCmdReport is the happy-path FAIL report the cmd-layer tests use.
func validCmdReport(scenario string, cycle int) e2e.CycleReport {
	return e2e.CycleReport{
		Scenario:        scenario,
		Cycle:           cycle,
		DrivenSummary:   "drove discovery → roadmap → taskvisor",
		FailurePoint:    "taskvisor phase, goal-002 wedge",
		DefectSignature: "dispatch/hang/goal",
		FiledTask:       "task-281 / new",
		TimingTable:     "implement p90 300s; mean in-flight 1.2",
		Verdict:         e2e.VerdictFail,
		VerdictReason:   "goal-002 never reached validate",
		AppUp:           false,
	}
}

func TestE2EStateReport_WritesScenarioScopedReport(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 3, 10)

	path, err := e2eStateReport(repoRoot, validCmdReport("scn", 3))
	require.NoError(t, err)
	assert.Equal(t, e2e.ReportFilePath(repoRoot, "scn", 3), path,
		"the report path comes ONLY from e2e.ReportFilePath")
	assert.True(t, e2e.IsScenarioReport(filepath.Base(path), "scn"),
		"the written name must satisfy the scenario-scoped matcher")

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, e2e.RenderCycleReport(validCmdReport("scn", 3)), string(b),
		"the file carries exactly the pure rendering")
}

func TestE2EStateReport_ResultJSONCarriesPath(t *testing.T) {
	res := e2eStateResult{Ok: true, Scenario: "scn", Cycle: 3, Path: "/x/e2e-report-scn-cycle-3.md"}
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.JSON()), &got))
	assert.Equal(t, true, got["ok"])
	assert.Equal(t, "scn", got["scenario"])
	assert.Equal(t, float64(3), got["cycle"])
	assert.Equal(t, "/x/e2e-report-scn-cycle-3.md", got["path"])

	// record/mark stdout stays byte-identical: an empty Path is omitted.
	assert.NotContains(t, e2eStateResult{Ok: true, Scenario: "scn", Cycle: 3, Status: "in-progress"}.JSON(), "path",
		"Path is omitempty so record/mark-self-update outputs are unchanged")
}

func TestE2EStateReport_RefusesMissingLedger(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := e2eStateReport(repoRoot, validCmdReport("scn", 1))
	require.Error(t, err, "report never initializes a ledger — that's e2e-bootstrap's job")
	assert.Contains(t, err.Error(), "e2e-bootstrap")
	assert.NoFileExists(t, e2e.ReportFilePath(repoRoot, "scn", 1),
		"no report file is created on refusal")
}

func TestE2EStateReport_RefusesTerminalAndMismatch(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 4, 10)

	// Cycle mismatch: ledger at 4, report for 3.
	_, err := e2eStateReport(repoRoot, validCmdReport("scn", 3))
	require.Error(t, err, "the report is for the ledger's current in-progress cycle only")
	assert.NoFileExists(t, e2e.ReportFilePath(repoRoot, "scn", 3))

	// Terminal ledger: passed refuses even a matching cycle.
	st := e2e.NewState("scn", 10)
	st.Cycle = 4
	st.Status = e2e.StatusPassed
	require.NoError(t, writeStateAtomic(stateFile, st))
	_, err = e2eStateReport(repoRoot, validCmdReport("scn", 4))
	require.Error(t, err, "terminal-ledger discipline mirrors record's, with no PASS exception")
	assert.NoFileExists(t, e2e.ReportFilePath(repoRoot, "scn", 4))
}

func TestE2EStateReport_NeverMutatesLedger(t *testing.T) {
	repoRoot := t.TempDir()
	stateFile := seedLedger(t, repoRoot, "scn", 2, 10)
	// A state.md is present too (bootstrap renders it) — it must also survive.
	mdFile := e2e.StateMDPath(repoRoot, "scn")
	require.NoError(t, os.WriteFile(mdFile, []byte("md-before"), 0o644))

	jsonBefore, err := os.ReadFile(stateFile)
	require.NoError(t, err)

	_, err = e2eStateReport(repoRoot, validCmdReport("scn", 2))
	require.NoError(t, err)

	jsonAfter, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	assert.Equal(t, string(jsonBefore), string(jsonAfter), "the ledger JSON is byte-identical after a report")

	mdAfter, err := os.ReadFile(mdFile)
	require.NoError(t, err)
	assert.Equal(t, "md-before", string(mdAfter), "the state.md rendering is untouched by a report")
}

func TestE2EStateReport_AtomicWriteNoTmpLeft(t *testing.T) {
	repoRoot := t.TempDir()
	seedLedger(t, repoRoot, "scn", 2, 10)

	path, err := e2eStateReport(repoRoot, validCmdReport("scn", 2))
	require.NoError(t, err)
	_, statErr := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "no .tmp leftover after the atomic rename")

	// Re-run same cycle: atomic overwrite, last-writer-wins, no exists-check.
	second := validCmdReport("scn", 2)
	second.VerdictReason = "re-emitted after a conductor retry"
	path2, err := e2eStateReport(repoRoot, second)
	require.NoError(t, err)
	assert.Equal(t, path, path2)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(b), "re-emitted after a conductor retry")
}

// ── flag surface: shape-only strict parsing (intrinsic checks live in e2e) ──

func TestParseE2EReportFlags_Valid(t *testing.T) {
	r, err := parseE2EReportFlags("scn", "3", "drove", "wedge", "dispatch/hang/goal",
		"task-281 / new", "p90 300s", "FAIL", "goal-002 wedged", "false")
	require.NoError(t, err)
	assert.Equal(t, "scn", r.Scenario)
	assert.Equal(t, 3, r.Cycle)
	assert.Equal(t, e2e.VerdictFail, r.Verdict)
	assert.False(t, r.AppUp)
}

func TestParseE2EReportFlags_RejectsBadAppUpAndCycle(t *testing.T) {
	_, err := parseE2EReportFlags("scn", "3", "a", "b", "c", "d", "e", "FAIL", "f", "yes")
	require.Error(t, err, "--app-up is a strict true|false enum")
	assert.Contains(t, err.Error(), "--app-up")

	for _, cycle := range []string{"x", "0", "-1", ""} {
		_, err := parseE2EReportFlags("scn", cycle, "a", "b", "c", "d", "e", "FAIL", "f", "true")
		require.Error(t, err, "--cycle %q must be refused (positive integer required)", cycle)
		assert.Contains(t, err.Error(), "--cycle")
	}
}

func TestParseE2EReportFlags_RequiresScenario(t *testing.T) {
	_, err := parseE2EReportFlags("  ", "3", "a", "b", "c", "d", "e", "FAIL", "f", "true")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scenario")
}

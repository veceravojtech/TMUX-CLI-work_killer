package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/e2e"
	"github.com/spf13/cobra"
)

// errE2EStateFailed is the sentinel returned after an e2e-state operation
// (record / mark-self-update) fails: the ok:false JSON is already printed to
// stdout (mirroring e2e-bootstrap's failure surface), so the command just
// needs a non-zero exit.
var errE2EStateFailed = errors.New("e2e-state command failed")

// e2e-state record is the deterministic state-ledger writer for the
// /tmux:e2e-evaluator conductor (steps 8–9): it appends the just-finished
// cycle's typed history entry and advances cycle/status per the step-8
// semantics, replacing the conductor's hand-authored JSON. It never
// initializes the ledger — that is e2e-bootstrap's job.

var (
	e2eStateScenario        string
	e2eStateOutcome         string
	e2eStateSignature       string
	e2eStateTaskID          string
	e2eStateTaskStatus      string
	e2eStateAppUp           string
	e2eStateGitAfter        string
	e2eStateDurations       string
	e2eStateVerifySignature string
	e2eStateVerifyTaskID    string

	e2eMarkScenario string
	e2eMarkTaskID   string
	e2eMarkAt       string

	e2eReportScenario        string
	e2eReportCycle           string
	e2eReportDrivenSummary   string
	e2eReportFailurePoint    string
	e2eReportDefectSignature string
	e2eReportFiledTask       string
	e2eReportTimingTable     string
	e2eReportVerdict         string
	e2eReportVerdictReason   string
	e2eReportAppUp           string
)

var e2eStateCmd = &cobra.Command{
	Use:   "e2e-state",
	Short: "Deterministic e2e-evaluator run-state ledger operations",
}

var e2eStateRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "Append the finished cycle's history entry and advance the run-state ledger",
	Long: `Record one finished e2e-evaluator cycle into the scenario's durable ledger
(.tmux-cli/e2e-evaluator/<scenario>.state.json) deterministically — the
conductor never hand-writes the JSON:

  --outcome failed  appends the entry and bumps cycle (or sets status:exhausted
                    when the bump would exceed max_cycles)
  --outcome passed  appends the entry and sets status:passed without a bump

The ledger MUST already exist (e2e-bootstrap initializes it); a missing or
corrupt ledger is refused. The write is atomic (temp + rename). Prints exactly
one JSON line {ok, scenario, cycle, status} on stdout; on failure
{ok:false, error} with a non-zero exit.`,
	RunE:          runE2EStateRecord,
	SilenceErrors: true, // ok:false JSON is the error surface; just exit non-zero
	SilenceUsage:  true,
}

var e2eStateMarkSelfUpdateCmd = &cobra.Command{
	Use:   "mark-self-update",
	Short: "Stamp the managed session restart performed for a resolved task (restart-loop guard)",
	Long: `Stamp last_self_update = {task_id, at} into the scenario's ledger — the
one-restart-per-resolved-task loop guard of the e2e-evaluator self-update
handoff (design §6). A repeat task-id is REFUSED ({ok:false}, non-zero exit):
the conductor consults the refusal to SKIP a second session restart and go
straight to the verification cycle. --at is RFC3339; empty defaults to now.`,
	RunE:          runE2EStateMarkSelfUpdate,
	SilenceErrors: true, // ok:false JSON is the error surface; just exit non-zero
	SilenceUsage:  true,
}

var e2eStateReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Write the finished cycle's markdown report deterministically (fixed section order, atomic)",
	Long: `Write the scenario's per-cycle report
(.tmux-cli/e2e-evaluator/e2e-report-<scenario>-cycle-<n>.md) deterministically —
the conductor authors the section PROSE (the flag values) but never the file:
naming (e2e.ReportFilePath), the fixed section order (Driven Summary / Failure
Point / Defect Signature / Filed Task / Timing Table / Verdict), and the atomic
temp+rename write are the command's.

The ledger is cross-checked READ-ONLY and never mutated: it MUST already exist
(e2e-bootstrap initializes it), still be in-progress (report precedes record),
and sit at --cycle (the just-finished cycle). --verdict PASS requires
--app-up true (the false-pass guard); --verdict EXHAUSTED only at the ledger's
max_cycles boundary. Prints exactly one JSON line {ok, scenario, cycle, path}
on stdout; on refusal {ok:false, error} with a non-zero exit.`,
	RunE:          runE2EStateReport,
	SilenceErrors: true, // ok:false JSON is the error surface; just exit non-zero
	SilenceUsage:  true,
}

func init() {
	f := e2eStateRecordCmd.Flags()
	f.StringVar(&e2eStateScenario, "scenario", "", "Scenario slug whose ledger to record into (required)")
	f.StringVar(&e2eStateOutcome, "outcome", "", "Cycle outcome: failed|passed (required)")
	f.StringVar(&e2eStateSignature, "signature", "", `Normalized defect signature "<phase/reason-class/area>" or the literal "none" (required)`)
	f.StringVar(&e2eStateTaskID, "task-id", "", "Filed /tmux:task-report id (empty when none filed)")
	f.StringVar(&e2eStateTaskStatus, "task-status", "", "Filed task's backend status (empty when none filed)")
	f.StringVar(&e2eStateAppUp, "app-up", "", "Whether the app-up probe passed: true|false (required)")
	f.StringVar(&e2eStateGitAfter, "git-after", "", "Target repo HEAD sha after the cycle")
	f.StringVar(&e2eStateDurations, "durations-json", "", "Per-phase durations as a JSON object (default {})")
	f.StringVar(&e2eStateVerifySignature, "verify-signature", "", "Flag the NEXT cycle as a fix-verification for this defect signature (requires --verify-task-id)")
	f.StringVar(&e2eStateVerifyTaskID, "verify-task-id", "", "The resolved task the pending verification confirms (requires --verify-signature)")

	m := e2eStateMarkSelfUpdateCmd.Flags()
	m.StringVar(&e2eMarkScenario, "scenario", "", "Scenario slug whose ledger to stamp (required)")
	m.StringVar(&e2eMarkTaskID, "task-id", "", "Resolved task the session restart is performed for (required)")
	m.StringVar(&e2eMarkAt, "at", "", "RFC3339 stamp of the restart (default: now UTC)")

	r := e2eStateReportCmd.Flags()
	r.StringVar(&e2eReportScenario, "scenario", "", "Scenario slug whose report to write (required)")
	r.StringVar(&e2eReportCycle, "cycle", "", "The just-finished cycle number — must equal the ledger's current in-progress cycle (required)")
	r.StringVar(&e2eReportDrivenSummary, "driven-summary", "", "Driven Summary prose: what the cycle drove (required)")
	r.StringVar(&e2eReportFailurePoint, "failure-point", "", `Failure Point prose: phase+milestone where the cycle failed, or "none — passed" (required)`)
	r.StringVar(&e2eReportDefectSignature, "defect-signature", "", `Defect Signature prose: normalized {phase, reason-class, area} tuple or "none" (required)`)
	r.StringVar(&e2eReportFiledTask, "filed-task", "", `Filed Task prose: task id + backend status, or "none filed (…)" (required)`)
	r.StringVar(&e2eReportTimingTable, "timing-table", "", "Timing Table prose: per-phase p90 + mean in-flight goals from MEASURE (required)")
	r.StringVar(&e2eReportVerdict, "verdict", "", "Verdict enum: PASS|FAIL|EXHAUSTED (required)")
	r.StringVar(&e2eReportVerdictReason, "verdict-reason", "", "One-line verdict reason, rendered as <VERDICT> — <reason> (required)")
	r.StringVar(&e2eReportAppUp, "app-up", "", "Whether the app-up probe passed: true|false (required; PASS requires true)")

	e2eStateCmd.AddCommand(e2eStateRecordCmd)
	e2eStateCmd.AddCommand(e2eStateMarkSelfUpdateCmd)
	e2eStateCmd.AddCommand(e2eStateReportCmd)
	rootCmd.AddCommand(e2eStateCmd)
}

// e2eStateResult is the single JSON line printed on stdout.
type e2eStateResult struct {
	Ok       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Scenario string `json:"scenario,omitempty"`
	Cycle    int    `json:"cycle,omitempty"`
	Status   string `json:"status,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (r e2eStateResult) JSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		// Marshal of this flat struct cannot realistically fail; degrade safely.
		return fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error())
	}
	return string(b)
}

func failE2EState(msg string) error {
	fmt.Println(e2eStateResult{Ok: false, Error: msg}.JSON())
	return errE2EStateFailed
}

func runE2EStateRecord(cmd *cobra.Command, args []string) error {
	scenario := strings.TrimSpace(e2eStateScenario)
	if scenario == "" {
		return failE2EState("--scenario is required")
	}
	entry, err := parseE2EStateFlags(e2eStateOutcome, e2eStateSignature, e2eStateAppUp,
		e2eStateTaskID, e2eStateTaskStatus, e2eStateGitAfter, e2eStateDurations)
	if err != nil {
		return failE2EState(err.Error())
	}
	verify, err := parseE2EVerifyFlags(e2eStateVerifySignature, e2eStateVerifyTaskID)
	if err != nil {
		return failE2EState(err.Error())
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return failE2EState(fmt.Sprintf("resolve cwd: %v", err))
	}
	st, err := e2eStateRecord(repoRoot, scenario, entry, verify)
	if err != nil {
		return failE2EState(err.Error())
	}
	fmt.Println(e2eStateResult{Ok: true, Scenario: st.Scenario, Cycle: st.Cycle, Status: st.Status}.JSON())
	return nil
}

func runE2EStateMarkSelfUpdate(cmd *cobra.Command, args []string) error {
	scenario := strings.TrimSpace(e2eMarkScenario)
	if scenario == "" {
		return failE2EState("--scenario is required")
	}
	if strings.TrimSpace(e2eMarkTaskID) == "" {
		return failE2EState("--task-id is required")
	}
	at, err := resolveMarkAt(e2eMarkAt)
	if err != nil {
		return failE2EState(err.Error())
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return failE2EState(fmt.Sprintf("resolve cwd: %v", err))
	}
	st, err := e2eStateMarkSelfUpdate(repoRoot, scenario, strings.TrimSpace(e2eMarkTaskID), at)
	if err != nil {
		return failE2EState(err.Error())
	}
	fmt.Println(e2eStateResult{Ok: true, Scenario: st.Scenario, Cycle: st.Cycle, Status: st.Status}.JSON())
	return nil
}

// resolveMarkAt validates an explicit --at as RFC3339, defaulting an empty
// flag to the CLI-layer clock (internal/e2e stays clock-free).
func resolveMarkAt(at string) (string, error) {
	at = strings.TrimSpace(at)
	if at == "" {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		return "", fmt.Errorf("--at must be RFC3339 (e.g. 2026-07-02T09:00:00Z): %v", err)
	}
	return at, nil
}

// parseE2EVerifyFlags pairs the verify flag surface: both set → a pending
// VerifyState, neither → nil (which clears the ledger field on record), one
// without the other → refused.
func parseE2EVerifyFlags(signature, taskID string) (*e2e.VerifyState, error) {
	signature, taskID = strings.TrimSpace(signature), strings.TrimSpace(taskID)
	switch {
	case signature == "" && taskID == "":
		return nil, nil
	case signature == "" || taskID == "":
		return nil, fmt.Errorf("--verify-signature and --verify-task-id must be given together")
	}
	return &e2e.VerifyState{Signature: signature, TaskID: taskID}, nil
}

// parseE2EStateFlags validates the record flag surface into a typed entry:
// outcome and app-up are strict enums (no silent bool default), the signature
// is required (the literal "none" marks a defect-free cycle), and
// durations-json must be a JSON object when given (defaulting to {}).
func parseE2EStateFlags(outcome, signature, appUp, taskID, taskStatus, gitAfter, durationsJSON string) (e2e.HistoryEntry, error) {
	if outcome != e2e.OutcomeFailed && outcome != e2e.OutcomePassed {
		return e2e.HistoryEntry{}, fmt.Errorf("--outcome must be %s|%s, got %q", e2e.OutcomeFailed, e2e.OutcomePassed, outcome)
	}
	if strings.TrimSpace(signature) == "" {
		return e2e.HistoryEntry{}, fmt.Errorf(`--signature is required (use the literal "none" when no defect)`)
	}
	var up bool
	switch appUp {
	case "true":
		up = true
	case "false":
		up = false
	default:
		return e2e.HistoryEntry{}, fmt.Errorf("--app-up must be true|false, got %q", appUp)
	}
	durations := json.RawMessage(`{}`)
	if strings.TrimSpace(durationsJSON) != "" {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(durationsJSON), &obj); err != nil {
			return e2e.HistoryEntry{}, fmt.Errorf("--durations-json must be a JSON object: %v", err)
		}
		durations = json.RawMessage(durationsJSON)
	}
	return e2e.HistoryEntry{
		Outcome:         outcome,
		DefectSignature: strings.TrimSpace(signature),
		TaskReported:    taskID,
		TaskStatus:      taskStatus,
		GitAfter:        gitAfter,
		AppUp:           up,
		Durations:       durations,
	}, nil
}

// e2eStateRecord reads the scenario's ledger (refusing a missing or corrupt
// file — record never initializes), applies the pure step-8 transition (verify
// sets/clears the pending fix-verification), and rewrites the ledger + its
// state.md rendering atomically.
func e2eStateRecord(repoRoot, scenario string, entry e2e.HistoryEntry, verify *e2e.VerifyState) (e2e.State, error) {
	st, err := readE2ELedger(repoRoot, scenario)
	if err != nil {
		return e2e.State{}, err
	}
	next, err := e2e.RecordCycleOutcome(st, entry, verify)
	if err != nil {
		return e2e.State{}, err
	}
	return next, writeE2ELedger(repoRoot, scenario, next)
}

// e2eStateMarkSelfUpdate applies the restart-loop guard: stamp last_self_update
// for a resolved task, refusing a repeat task-id, and rewrite ledger + md.
func e2eStateMarkSelfUpdate(repoRoot, scenario, taskID, at string) (e2e.State, error) {
	st, err := readE2ELedger(repoRoot, scenario)
	if err != nil {
		return e2e.State{}, err
	}
	next, err := e2e.MarkSelfUpdate(st, taskID, at)
	if err != nil {
		return e2e.State{}, err
	}
	return next, writeE2ELedger(repoRoot, scenario, next)
}

func runE2EStateReport(cmd *cobra.Command, args []string) error {
	r, err := parseE2EReportFlags(e2eReportScenario, e2eReportCycle, e2eReportDrivenSummary,
		e2eReportFailurePoint, e2eReportDefectSignature, e2eReportFiledTask,
		e2eReportTimingTable, e2eReportVerdict, e2eReportVerdictReason, e2eReportAppUp)
	if err != nil {
		return failE2EState(err.Error())
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return failE2EState(fmt.Sprintf("resolve cwd: %v", err))
	}
	path, err := e2eStateReport(repoRoot, r)
	if err != nil {
		return failE2EState(err.Error())
	}
	fmt.Println(e2eStateResult{Ok: true, Scenario: r.Scenario, Cycle: r.Cycle, Path: path}.JSON())
	return nil
}

// parseE2EReportFlags converts the report flag surface into a typed
// CycleReport — SHAPE ONLY (scenario present, cycle a positive integer, app-up
// a strict true|false with no silent bool default). The intrinsic
// section/verdict validation lives solely in e2e.ValidateCycleReport (single
// source of truth — a deliberate divergence from record's duplicated enum check).
func parseE2EReportFlags(scenario, cycle, drivenSummary, failurePoint, defectSignature,
	filedTask, timingTable, verdict, verdictReason, appUp string) (e2e.CycleReport, error) {
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		return e2e.CycleReport{}, fmt.Errorf("--scenario is required")
	}
	n, err := strconv.Atoi(strings.TrimSpace(cycle))
	if err != nil || n < 1 {
		return e2e.CycleReport{}, fmt.Errorf("--cycle must be a positive integer (the just-finished cycle), got %q", cycle)
	}
	var up bool
	switch appUp {
	case "true":
		up = true
	case "false":
		up = false
	default:
		return e2e.CycleReport{}, fmt.Errorf("--app-up must be true|false, got %q", appUp)
	}
	return e2e.CycleReport{
		Scenario:        scenario,
		Cycle:           n,
		DrivenSummary:   drivenSummary,
		FailurePoint:    failurePoint,
		DefectSignature: defectSignature,
		FiledTask:       filedTask,
		TimingTable:     timingTable,
		Verdict:         verdict,
		VerdictReason:   verdictReason,
		AppUp:           up,
	}, nil
}

// e2eStateReport cross-checks the scenario's ledger READ-ONLY (it must exist —
// report never initializes it — still be in-progress, and sit at the report's
// cycle: report precedes record) and writes the rendered report atomically at
// e2e.ReportFilePath. It NEVER calls writeE2ELedger: the ledger and its
// state.md are byte-identical after. A re-run of the same cycle overwrites
// through the same temp+rename (last-writer-wins, no exists-check).
func e2eStateReport(repoRoot string, r e2e.CycleReport) (string, error) {
	st, err := readE2ELedger(repoRoot, r.Scenario)
	if err != nil {
		return "", err
	}
	if err := e2e.ValidateReportForState(r, st); err != nil {
		return "", err
	}
	path := e2e.ReportFilePath(repoRoot, r.Scenario, r.Cycle)
	if err := writeFileAtomic(path, []byte(e2e.RenderCycleReport(r))); err != nil {
		return "", fmt.Errorf("write report %s: %w", path, err)
	}
	return path, nil
}

// readE2ELedger loads + strictly parses the scenario ledger; a missing file is
// refused with the e2e-bootstrap pointer (this layer never initializes).
func readE2ELedger(repoRoot, scenario string) (e2e.State, error) {
	stateFile := e2e.StateFilePath(repoRoot, scenario)
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return e2e.State{}, fmt.Errorf("read ledger %s: %v — e2e-state never initializes it (run tmux-cli e2e-bootstrap first)", stateFile, err)
	}
	st, err := e2e.ParseState(raw)
	if err != nil {
		return e2e.State{}, fmt.Errorf("corrupt ledger %s: %w", stateFile, err)
	}
	return st, nil
}

// writeE2ELedger rewrites the JSON ledger and its <scenario>.state.md
// rendering, each atomically (temp + rename), on every mutation.
func writeE2ELedger(repoRoot, scenario string, st e2e.State) error {
	stateFile := e2e.StateFilePath(repoRoot, scenario)
	if err := writeStateAtomic(stateFile, st); err != nil {
		return fmt.Errorf("rewrite ledger %s: %w", stateFile, err)
	}
	mdFile := e2e.StateMDPath(repoRoot, scenario)
	if err := writeFileAtomic(mdFile, []byte(e2e.RenderStateMD(st))); err != nil {
		return fmt.Errorf("rewrite state md %s: %w", mdFile, err)
	}
	return nil
}

// writeFileAtomic is the temp+rename write shared by the md rendering.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
